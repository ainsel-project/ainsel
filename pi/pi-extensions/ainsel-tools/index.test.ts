import { describe, it, afterEach } from "node:test";
import assert from "node:assert/strict";
import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import ainselToolsExtension, {
	parseAllowlist,
	allowsToolName,
	selectTools,
	blockedShellCommand,
	listCatalogTools,
} from "./index.ts";

// ---------------------------------------------------------------------------
// Pure helpers
// ---------------------------------------------------------------------------

describe("parseAllowlist", () => {
	it("treats unset, empty and blank-only values as no restriction", () => {
		assert.deepEqual(parseAllowlist(undefined), []);
		assert.deepEqual(parseAllowlist(""), []);
		assert.deepEqual(parseAllowlist(" , ,, "), []);
	});

	it("trims entries and drops duplicates", () => {
		assert.deepEqual(parseAllowlist(" read_file , forgejo,read_file "), ["read_file", "forgejo"]);
	});
});

describe("allowsToolName", () => {
	it("matches a registered tool exactly", () => {
		assert.equal(allowsToolName(["read_file"], "read_file"), true);
		assert.equal(allowsToolName(["git"], "git-ops"), false);
		assert.equal(allowsToolName(["read"], "read_file"), false);
	});

	it("grants every tool of an MCP server by bare name", () => {
		assert.equal(allowsToolName(["github"], "mcp__github__list_issues"), true);
		assert.equal(allowsToolName(["github"], "mcp__gitlab__merge"), false);
	});

	it("grants every tool of an MCP server by its mcp__ name", () => {
		assert.equal(allowsToolName(["mcp__github"], "mcp__github__list_issues"), true);
		assert.equal(allowsToolName(["mcp__github__"], "mcp__github__list_issues"), true);
		assert.equal(allowsToolName(["mcp__github"], "mcp__githubx__y"), false);
	});

	it("still matches an MCP tool by its full name", () => {
		assert.equal(allowsToolName(["mcp__github__list_issues"], "mcp__github__list_issues"), true);
	});
});

describe("selectTools", () => {
	it("keeps registration order and only selected tools", () => {
		assert.deepEqual(
			selectTools(["forgejo", "github"], ["read", "bash", "forgejo", "mcp__github__x", "mcp__other__y"]),
			["forgejo", "mcp__github__x"],
		);
	});
});

describe("blockedShellCommand", () => {
	const blocked = new Set(["forgejo", "git-ops"]);

	it("blocks a withheld tool run by name", () => {
		assert.equal(blockedShellCommand("forgejo issue list", blocked), "forgejo");
	});

	it("blocks a withheld tool run by path", () => {
		assert.equal(
			blockedShellCommand("/usr/local/bin/tools/forgejo issue list", blocked),
			"forgejo",
		);
	});

	it("blocks a withheld tool in any command position", () => {
		assert.equal(blockedShellCommand("git status && forgejo issue list", blocked), "forgejo");
		assert.equal(blockedShellCommand("echo hi | forgejo issue list", blocked), "forgejo");
		assert.equal(blockedShellCommand("$(forgejo issue list)", blocked), "forgejo");
		assert.equal(blockedShellCommand("sudo forgejo issue list", blocked), "forgejo");
		assert.equal(blockedShellCommand("DRY_RUN=1 forgejo issue list", blocked), "forgejo");
	});

	it("leaves the tool named in a non-command position alone", () => {
		assert.equal(blockedShellCommand("echo forgejo", blocked), null);
		assert.equal(blockedShellCommand("cat notes/forgejo.md", blocked), null);
	});

	it("allows commands that touch no withheld tool", () => {
		assert.equal(blockedShellCommand("git status", blocked), null);
		assert.equal(blockedShellCommand("", blocked), null);
	});

	it("blocks nothing when nothing is withheld", () => {
		assert.equal(blockedShellCommand("forgejo issue list", new Set()), null);
	});
});

describe("listCatalogTools", () => {
	it("lists only executable files", () => {
		const dir = fs.mkdtempSync(path.join(os.tmpdir(), "ainsel-tools-"));
		try {
			fs.writeFileSync(path.join(dir, "forgejo"), "#!/bin/sh\n", { mode: 0o755 });
			fs.writeFileSync(path.join(dir, "README.md"), "docs", { mode: 0o644 });
			fs.mkdirSync(path.join(dir, "nested"));
			fs.chmodSync(path.join(dir, "nested"), 0o755);
			assert.deepEqual(listCatalogTools(dir), ["forgejo"]);
		} finally {
			fs.rmSync(dir, { recursive: true, force: true });
		}
	});

	it("returns nothing for a missing tools dir", () => {
		assert.deepEqual(listCatalogTools("/nonexistent/tools/dir"), []);
	});
});

// ---------------------------------------------------------------------------
// Extension wiring
// ---------------------------------------------------------------------------

type Handler = (event: any, ctx?: any) => unknown;

/** Minimal ExtensionAPI: records handlers, reports a fixed tool registry. */
function makePi(registered: string[]) {
	const handlers = new Map<string, Handler[]>();
	const state = { active: null as string[] | null };
	const pi = {
		getAllTools: () => registered.map((name) => ({ name } as any)),
		getActiveTools: () => state.active ?? registered,
		setActiveTools: (names: string[]) => {
			state.active = names;
		},
		on: (event: string, handler: Handler) => {
			const list = handlers.get(event) ?? [];
			list.push(handler);
			handlers.set(event, list);
		},
	};
	return {
		pi: pi as any,
		state,
		async fire(event: string, arg: unknown) {
			const results = [];
			for (const h of handlers.get(event) ?? []) results.push(await h(arg, {}));
			return results;
		},
	};
}

const savedEnv: Record<string, string | undefined> = {};

async function withEnv(
	vars: Record<string, string | undefined>,
	fn: () => Promise<void>,
): Promise<void> {
	for (const [key, value] of Object.entries(vars)) {
		savedEnv[key] = process.env[key];
		if (value === undefined) delete process.env[key];
		else process.env[key] = value;
	}
	try {
		await fn();
	} finally {
		for (const key of Object.keys(vars)) process.env[key] = savedEnv[key];
	}
}

describe("ainsel-tools extension", () => {
	const registered = ["read", "bash", "forgejo", "git-ops", "mcp__github__list", "mcp__linear__search"];

	afterEach(() => {
		delete process.env.AGENT_TOOLS;
		delete process.env.AINSEL_TOOLS_DIR;
	});

	it("does nothing when the agent has no tool selection", async () => {
		await withEnv({ AGENT_TOOLS: "" }, async () => {
			const h = makePi(registered);
			ainselToolsExtension(h.pi);
			assert.deepEqual(await h.fire("session_start", {}), []);
			assert.equal(h.state.active, null);
			const results = await h.fire("tool_call", { toolName: "git-ops", input: {} });
			assert.deepEqual(results, []);
		});
	});

	it("narrows the declared tools to the selection on session start", async () => {
		await withEnv({ AGENT_TOOLS: "bash, forgejo, linear" }, async () => {
			const h = makePi(registered);
			ainselToolsExtension(h.pi);
			await h.fire("session_start", {});
			assert.deepEqual(h.state.active, ["bash", "forgejo", "mcp__linear__search"]);
		});
	});

	it("re-applies the selection before each run, so a late activation cannot widen it", async () => {
		await withEnv({ AGENT_TOOLS: "read" }, async () => {
			const h = makePi(registered);
			ainselToolsExtension(h.pi);
			await h.fire("session_start", {});
			assert.deepEqual(h.state.active, ["read"]);
			// Something else widened the set; the next run narrows it again.
			h.pi.setActiveTools(registered);
			await h.fire("before_agent_start", {});
			assert.deepEqual(h.state.active, ["read"]);
		});
	});

	it("blocks a call to a tool outside the selection", async () => {
		await withEnv({ AGENT_TOOLS: "read" }, async () => {
			const h = makePi(registered);
			ainselToolsExtension(h.pi);
			const [result] = await h.fire("tool_call", { toolName: "bash", input: { command: "ls" } });
			assert.equal(result.block, true);
			assert.match(result.reason, /not in this agent's tool selection/);
		});
	});

	it("withholds image catalog tools from the shell", async () => {
		const dir = fs.mkdtempSync(path.join(os.tmpdir(), "ainsel-tools-env-"));
		try {
			fs.writeFileSync(path.join(dir, "forgejo"), "#!/bin/sh\n", { mode: 0o755 });
			fs.writeFileSync(path.join(dir, "git-ops"), "#!/bin/sh\n", { mode: 0o755 });
			await withEnv({ AGENT_TOOLS: "bash,forgejo", AINSEL_TOOLS_DIR: dir }, async () => {
				const h = makePi(registered);
				ainselToolsExtension(h.pi);

				const allowed = await h.fire("tool_call", {
					toolName: "bash",
					input: { command: "forgejo issue list" },
				});
				assert.deepEqual(allowed, [undefined]);

				const blocked = await h.fire("tool_call", {
					toolName: "bash",
					input: { command: "git-ops deploy" },
				});
				assert.equal(blocked[0].block, true);
				assert.match(blocked[0].reason, /"git-ops"/);
			});
		} finally {
			fs.rmSync(dir, { recursive: true, force: true });
		}
	});

	it("leaves the shell alone when the image ships no catalog tools", async () => {
		await withEnv(
			{ AGENT_TOOLS: "bash", AINSEL_TOOLS_DIR: "/nonexistent/tools/dir" },
			async () => {
				const h = makePi(registered);
				ainselToolsExtension(h.pi);
				const results = await h.fire("tool_call", {
					toolName: "bash",
					input: { command: "git-ops deploy" },
				});
				assert.deepEqual(results, [undefined]);
			},
		);
	});
});
