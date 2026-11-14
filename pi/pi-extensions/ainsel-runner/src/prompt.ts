/**
 * The user-message body the runner hands to `pi.sendUserMessage`: the
 * `<event>…</event>` block plus the nudge, and the raw payload capped by
 * `EVENT_DATA_MAX_CHARS`.
 */

import { resolveEventDataMaxChars } from "./config.ts";
import { repoFullName, type EventContext } from "./event-context.ts";
import { truncateWithMarker } from "./caps.ts";



export function buildUserMessage(ctx: EventContext, payload?: unknown): string {
	if (ctx.type === "chat.message") {
		const lines = [
			"<event>",
			"  type: chat.message",
			`  session_id: ${ctx.chatSessionId ?? ""}`,
			"</event>",
			"",
			"A user sent you a chat message:",
			"",
			ctx.chatMessage ?? "",
			"",
			"Respond to the user by calling the mcp__chat__send_reply tool with",
			"the session_id above and your reply as the content. Do not post a",
			"Forgejo comment — chat replies go through the chat MCP tool.",
		];
		return lines.join("\n");
	}

	if (ctx.type === "cron") {
		const lines = [
			"<event>",
			"  type: cron",
			`  cron_trigger: ${ctx.cronTrigger ?? ""}`,
			"</event>",
			"",
			ctx.prompt ?? "",
		];
		return lines.join("\n");
	}

	const repo = repoFullName(ctx);
	const ref = ctx.number
		? ctx.kind === "pull_request"
			? `${repo}!${ctx.number}`
			: `${repo}#${ctx.number}`
		: (repo || "(unknown target)");
	const lines = [
		"<event>",
		`  type: ${ctx.type ?? ""}`,
		`  org: ${ctx.owner ?? ""}`,
		`  repo: ${repo}`,
	];
	if (ctx.number) {
		if (ctx.kind === "pull_request") {
			lines.push(`  pr: ${ctx.number}`);
		} else {
			lines.push(`  issue: ${ctx.number}`);
		}
	}
	if (ctx.actor) lines.push(`  actor: ${ctx.actor}`);
	if (ctx.id) lines.push(`  event_id: ${ctx.id}`);
	lines.push("</event>");
	lines.push("");
	lines.push(`Handle the ${ctx.type ?? "event"} on ${ref}.`);
	if (payload !== undefined && payload !== null) {
		lines.push("");
		lines.push("The full raw event payload:");
		lines.push("");
		lines.push("<event-data>");
		lines.push(truncateWithMarker(JSON.stringify(payload, null, 2), resolveEventDataMaxChars()));
		lines.push("</event-data>");
	}
	return lines.join("\n");
}
