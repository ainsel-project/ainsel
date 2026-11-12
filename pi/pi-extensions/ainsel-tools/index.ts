/**
 * Ainsel Tools Extension for Pi.
 *
 * The Agent CR carries the agent's own tool selection (`spec.enabledTools`).
 * The operator passes it to the pod as `AGENT_TOOLS`; the runtime image only
 * declares what it ships (its capability catalog). This extension is where the
 * selection takes effect — without it the field is decorative, which is exactly
 * what issue #87 complained about.
 *
 * Unset or empty `AGENT_TOOLS` means unrestricted: the agent keeps every tool
 * its image and MCP servers provide, and this extension does nothing.
 *
 * When a selection is in effect, three things happen:
 *
 *   1. The active tool set — what the model is offered — is narrowed to the
 *      selection. Names match a registered pi tool exactly, or an MCP server
 *      (bare `github` or `mcp__github`) to grant every tool of that server.
 *   2. A call to anything else is blocked in `tool_call`, so a tool that some
 *      other extension activates later still cannot run.
 *   3. The image's catalog tools — executables in `AINSEL_TOOLS_DIR` that the
 *      model invokes through the shell — are withheld too: a shell command
 *      whose command position names a catalog binary outside the selection is
 *      blocked. This is capability hygiene, not a sandbox: a determined caller
 *      can copy a binary. It stops the model from reaching a tool the operator
 *      did not give this agent, which is the case that matters.
 *
 * A selection naming nothing registered is applied anyway (the agent ends up
 * with no tools) and logged loudly: silently falling back to "everything" would
 * turn a typo in the Agent CR into a wider tool set than was asked for.
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { readdirSync, statSync } from "node:fs";
import * as path from "node:path";

const MCP_PREFIX = "mcp__";

/** Shell keywords/introduceurs that can precede the real command in a segment. */
const COMMAND_INTRODUCEURS = new Set([
	"sudo",
	"env",
	"command",
	"exec",
	"time",
	"nohup",
	"nice",
	"then",
	"do",
	"if",
]);

/** Split points between commands in one shell line (POSIX-ish, good enough here). */
const SEGMENT_SPLIT = /[;\n|&(){}]+/;

/** Split a shell line into command-position tokens of each segment. */
function commandPositions(command: string): string[] {
	const out: string[] = [];
	for (const segment of command.split(SEGMENT_SPLIT)) {
		const tokens = segment.trim().split(/\s+/).filter(Boolean);
		let i = 0;
		// Skip leading `FOO=bar` assignments and introduceurs like `sudo`.
		while (i < tokens.length) {
			const t = tokens[i];
			if (/^[A-Za-z_][A-Za-z0-9_]*=/.test(t)) {
				i++;
				continue;
			}
			if (COMMAND_INTRODUCEURS.has(t)) {
				i++;
				continue;
			}
			break;
		}
		const cmd = tokens[i];
		if (cmd) out.push(cmd);
	}
	return out;
}

/**
 * The image's catalog tools: executables in the tools dir (the same listing
 * `agent --list-tools` publishes). Missing or unreadable dirs yield [] — the
 * shell guard then simply has nothing to withhold.
 */
export function listCatalogTools(toolsDir: string): string[] {
	let entries: string[];
	try {
		entries = readdirSync(toolsDir);
	} catch {
		return [];
	}
	const out: string[] = [];
	for (const entry of entries) {
		try {
			const st = statSync(path.join(toolsDir, entry));
			if (st.isFile() && (st.mode & 0o111) !== 0) out.push(entry);
		} catch {
			// Race with the image build or a dangling symlink: not a tool.
		}
	}
	return out.sort();
}

/**
 * Parse `AGENT_TOOLS`: comma-separated names, whitespace tolerated, empties and
 * duplicates dropped. Empty result means "no restriction".
 */
export function parseAllowlist(env: string | undefined): string[] {
	const seen = new Set<string>();
	for (const raw of (env ?? "").split(",")) {
		const name = raw.trim();
		if (name !== "") seen.add(name);
	}
	return [...seen];
}

/** Whether `allow` names `toolName`, including the MCP server-level forms. */
export function allowsToolName(allow: string[], toolName: string): boolean {
	for (const a of allow) {
		if (a === toolName) return true;
		// `mcp__github` (or `mcp__github__`) grants the whole server.
		if (a.startsWith(MCP_PREFIX)) {
			const prefix = a.endsWith("__") ? a : `${a}__`;
			if (toolName.startsWith(prefix)) return true;
			continue;
		}
		// A bare server name grants its tools too: `github` → `mcp__github__*`.
		if (toolName.startsWith(`${MCP_PREFIX}${a}__`)) return true;
	}
	return false;
}

/** The registered tools an allowlist selects, in registration order. */
export function selectTools(allow: string[], registered: string[]): string[] {
	return registered.filter((t) => allowsToolName(allow, t));
}

/**
 * The shell command in `command` that reaches a withheld catalog tool, or null
 * when the command touches none of them. Only command positions are checked, so
 * `echo forgejo` is not blocked while `forgejo issue list` is.
 */
export function blockedShellCommand(command: string, blocked: Set<string>): string | null {
	if (blocked.size === 0 || command === "") return null;
	for (const cmd of commandPositions(command)) {
		const base = path.basename(cmd);
		if (blocked.has(base)) return base;
	}
	return null;
}

/** Pi's shell tools; the catalog guard inspects their command argument. */
const SHELL_TOOLS = new Set(["bash", "powershell"]);

function shellCommandOf(input: unknown): string | null {
	if (!input || typeof input !== "object") return null;
	const cmd = (input as { command?: unknown }).command;
	return typeof cmd === "string" ? cmd : null;
}

export default function ainselToolsExtension(pi: ExtensionAPI): void {
	const allow = parseAllowlist(process.env.AGENT_TOOLS);
	if (allow.length === 0) {
		// Unrestricted: the agent keeps everything its image provides.
		return;
	}

	const toolsDir = process.env.AINSEL_TOOLS_DIR ?? "/usr/local/bin/tools";
	// Catalog tools the selection withholds from the shell. Computed once: the
	// tools dir is baked into the image and does not change during a run.
	const blockedCatalog = new Set(
		listCatalogTools(toolsDir).filter((name) => !allowsToolName(allow, name)),
	);

	let warned = false;

	/** Narrow the declared tool set to the agent's selection. */
	function applySelection(source: string): void {
		const registered = pi.getAllTools().map((t) => t.name);
		const selected = selectTools(allow, registered);
		pi.setActiveTools(selected);

		if (!warned) {
			warned = true;
			const catalog = new Set(listCatalogTools(toolsDir));
			const unknown = allow.filter(
				(name) => !registered.some((t) => allowsToolName([name], t)) && !catalog.has(name),
			);
			if (unknown.length > 0) {
				console.error(
					`ainsel-tools: AGENT_TOOLS names nothing registered: ${unknown.join(", ")} (from ${source}); those tools stay unavailable`,
				);
			}
			if (selected.length === 0) {
				console.error(
					"ainsel-tools: the agent's tool selection matches no registered tool; the agent runs with no tools",
				);
			}
		}
	}

	// The selection is applied before every run, not once at session start:
	// registering a tool activates it, so an extension that registers late (or a
	// loader tool that activates names) must not widen the agent's tool set.
	pi.on("session_start", () => {
		applySelection("session_start");
	});
	pi.on("before_agent_start", () => {
		applySelection("before_agent_start");
	});

	pi.on("tool_call", (event) => {
		const name = event.toolName;
		if (!allowsToolName(allow, name)) {
			return {
				block: true,
				reason: `ainsel-tools: "${name}" is not in this agent's tool selection`,
			};
		}
		if (blockedCatalog.size > 0 && SHELL_TOOLS.has(name)) {
			const command = shellCommandOf(event.input);
			if (command !== null) {
				const hit = blockedShellCommand(command, blockedCatalog);
				if (hit !== null) {
					return {
						block: true,
						reason: `ainsel-tools: "${hit}" is not in this agent's tool selection`,
					};
				}
			}
		}
		return undefined;
	});
}
