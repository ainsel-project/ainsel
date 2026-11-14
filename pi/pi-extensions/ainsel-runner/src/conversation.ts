/**
 * Serialisation of pi's captured messages into the hub's
 * `task_conversations` payload shape.
 *
 * Import direction: conversation → caps → redaction, and conversation →
 * usage for the presence rules the rows share with the counters. Nothing
 * under `usage`/`caps`/`redaction` may import this module.
 */

import { capToolResultContent, truncateWithMarker } from "./caps.ts";
import { resolveToolResultMaxChars } from "./config.ts";
import { redactSecrets } from "./redaction.ts";
import { finiteNumberOrUndefined, hasRecordableUsage } from "./usage.ts";
import { logError } from "./logging.ts";
import type { PiContentBlock, PiMessage } from "./pi-types.ts";



// ConversationPayload is the camelCase JSON shape the hub ingest endpoint
// decodes into. `content` is a JSON-serialized string of normalized blocks.
// The four token fields mirror pi's usage components (input excludes
// prompt-cache traffic; totalTokens is the sum of all four) so hub-side
// totals match the agent_tokens_used_total counter series and the by-event
// view stops under-counting cached prompts (issue #281).
//
// cacheReadTokens/cacheWriteTokens/costUsd are optional and omitted when pi
// reports no figure (issue #75): the hub stores an omitted field as NULL —
// "not reported" — while a sent 0 is stored as a measured zero. They must
// never be defaulted to 0 on rows that carry no usage at all (user and
// toolResult rows): a window of such rows would otherwise read as reported
// zeros and flip the hub's presence flags to a lie.
export interface ConversationPayload {
	agentName: string;
	invocationId: string;
	correlationId: string;
	role: string;
	content: string; // JSON-serialized content blocks
	model: string;
	inputTokens: number;
	outputTokens: number;
	cacheReadTokens?: number;
	cacheWriteTokens?: number;
	// costUsd mirrors pi's usage.cost.total for the message, priced from the
	// model's cost block in models.json (spec.llm.cost). The hub sums it into
	// the by-event and summary cost figures (issue #281) and uses its presence
	// to tell an unpriced model from a free run (issue #75).
	costUsd?: number;
	stopReason: string;
	// Subject labels the token tables group by (issue #410): the same
	// normalised values the agent_tokens_used_total counter carries, sent to
	// a second destination so the counter and the row cannot disagree.
	repo: string;
	org: string;
	eventType: string;
	issueNumber: number;
}

export interface SerializeMeta {
	agentName: string;
	invocationId: string;
	correlationId: string;
	// Subject labels (issue #410), filled from eventSubject() by processTask.
	// Optional so partial callers (tests, future report paths) still produce a
	// complete payload — the subject just decodes as "unknown" on the hub.
	repo?: string;
	org?: string;
	eventType?: string;
	issueNumber?: number;
}

// toBlockArray normalizes message content to a block array. UserMessage.content
// can be a plain string or a (TextContent|ImageContent)[] array; this mirrors
// the guard used in the `context` event handler.
function toBlockArray(content: unknown): PiContentBlock[] {
	if (typeof content === "string") return [{ type: "text", text: content }];
	return (content ?? []) as PiContentBlock[];
}

/** Converts pi AgentMessage[] into hub ConversationMessage payloads.
 *  Pure and exported for unit testing. Unknown roles are skipped. */
export function toConversationPayloads(messages: PiMessage[] | null | undefined, meta: SerializeMeta): ConversationPayload[] {
	const out: ConversationPayload[] = [];
	const base = {
		agentName: meta.agentName,
		invocationId: meta.invocationId,
		correlationId: meta.correlationId,
		repo: meta.repo ?? "",
		org: meta.org ?? "",
		eventType: meta.eventType ?? "",
		issueNumber: meta.issueNumber ?? 0,
		model: "",
		inputTokens: 0,
		outputTokens: 0,
		// cacheReadTokens/cacheWriteTokens/costUsd are deliberately absent
		// here: only assistant rows carry usage, and defaulting them to 0 on
		// every row would make the hub's COUNT(...)-based presence flags read
		// "reported" for rows that never had a figure (issue #75).
		stopReason: "",
	};
	for (const m of messages ?? []) {
		try {
			if (m.role === "user") {
				const blocks = toBlockArray(m.content).map((c) =>
					c.type === "text" ? { type: "text", text: c.text } : { type: c.type },
				);
				out.push({ ...base, role: "user", content: redactSecrets(JSON.stringify(blocks)) });
			} else if (m.role === "assistant") {
				const blocks = toBlockArray(m.content).map((c) => {
					if (c.type === "text") return { type: "text", text: c.text };
					if (c.type === "thinking") return { type: "thinking", text: c.thinking ?? c.text ?? "" };
					if (c.type === "toolCall") return { type: "toolCall", id: c.id, name: c.name, arguments: c.arguments };
					return { type: c.type };
				});
				// Reported zeros stay 0; absent figures are omitted entirely — the
				// key is left off the object, so the hub stores NULL, "not
				// reported" (issue #75).
				const cacheRead = finiteNumberOrUndefined(m.usage?.cacheRead);
				const cacheWrite = finiteNumberOrUndefined(m.usage?.cacheWrite);
				const costUsd = finiteNumberOrUndefined(m.usage?.cost?.total);
				out.push({
					...base,
					role: "assistant",
					content: redactSecrets(JSON.stringify(blocks)),
					model: m.model ?? "",
					inputTokens: m.usage?.input ?? 0,
					outputTokens: m.usage?.output ?? 0,
					...(cacheRead !== undefined && { cacheReadTokens: cacheRead }),
					...(cacheWrite !== undefined && { cacheWriteTokens: cacheWrite }),
					...(costUsd !== undefined && { costUsd }),
					stopReason: m.stopReason ?? "",
				});
			} else if (m.role === "system") {
				// Synthetic rows for LLM calls pi never emits as messages: the
				// compaction and branch-summary completions (see
				// summaryConversationMessage). Their usage is the whole point of
				// the row, so a system message without any — pi's own prompt-
				// section patches arrive this way — stays out of the transcript.
				if (!hasRecordableUsage(m.usage)) continue;
				const blocks = toBlockArray(m.content).map((c) =>
					c.type === "text" ? { type: "text", text: c.text } : { type: c.type },
				);
				let serialized = redactSecrets(JSON.stringify(blocks));
				// Same safety net as toolResult rows: a summary is bounded in
				// practice, but a pathological one must not produce an unbounded row.
				const systemCap = Math.max(128_000, 8 * resolveToolResultMaxChars());
				if (serialized.length > systemCap) {
					serialized = truncateWithMarker(serialized, systemCap);
				}
				// Same presence semantics as assistant rows (issue #75): a
				// reported 0 stays 0, an absent figure is omitted so the hub
				// stores NULL — "not reported".
				const sysCacheRead = finiteNumberOrUndefined(m.usage?.cacheRead);
				const sysCacheWrite = finiteNumberOrUndefined(m.usage?.cacheWrite);
				const sysCostUsd = finiteNumberOrUndefined(m.usage?.cost?.total);
				out.push({
					...base,
					role: "system",
					content: serialized,
					model: m.model ?? "",
					inputTokens: m.usage?.input ?? 0,
					outputTokens: m.usage?.output ?? 0,
					...(sysCacheRead !== undefined && { cacheReadTokens: sysCacheRead }),
					...(sysCacheWrite !== undefined && { cacheWriteTokens: sysCacheWrite }),
					...(sysCostUsd !== undefined && { costUsd: sysCostUsd }),
					stopReason: m.stopReason ?? "",
				});
			} else if (m.role === "toolResult") {
				const capped = capToolResultContent(m.content);
				let serialized = redactSecrets(JSON.stringify({ toolCallId: m.toolCallId, isError: !!m.isError, content: capped }));
				// Per-message safety net: cap the final serialized string so
				// pathological many-block or non-text-object content cannot
				// produce an unbounded row.
				const blockCap = resolveToolResultMaxChars();
				const messageCap = Math.max(128_000, 8 * blockCap);
				if (serialized.length > messageCap) {
					serialized = truncateWithMarker(serialized, messageCap);
				}
				out.push({
					...base,
					role: "toolResult",
					content: serialized,
				});
			}
		} catch (err) {
			// Skip messages that fail serialization (e.g. circular toolResult content)
			// rather than discarding the entire transcript.
			logError("skipping unserializable conversation message", { role: m?.role, err: String(err) });
		}
	}
	return out;
}
