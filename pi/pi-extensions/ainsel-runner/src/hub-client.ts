/**
 * The hub REST calls the runner makes. Every one of them is best-effort by
 * contract: none may throw, because they all sit on the path to ack/nack and a
 * reporting failure must never cost the task its ack.
 *
 * TODO(#272 follow-up): only `postTaskMessages` is timeout-bounded —
 * `ackTask`/`nackTask`/`postTaskLog` are timeout-less today. Unifying them
 * behind one hubPost would change ack behaviour, so it is deliberately left
 * out of this refactor.
 */

import { resolveNakDelayMs, resolvePostTimeoutMs } from "./config.ts";
import { logError } from "./logging.ts";
import type { PiMessage } from "./pi-types.ts";
import { toConversationPayloads, type ConversationPayload, type SerializeMeta } from "./conversation.ts";



// Task is the shape returned by the per-task fetch,
// GET /api/internal/agents/{name}/tasks/{id}. status is only ever present
// there: a per-task Job must be able to see whether an earlier attempt
// already resolved the row before it burns another run on it.
export interface Task {
	id: number;
	event_id: string;
	agent_name: string;
	trigger_name: string;
	invocation_id: string;
	headers: Record<string, string>;
	payload: unknown;
	attempts: number;
	status?: string;
}


export async function ackTask(hubUrl: string, token: string, agentName: string, taskId: number) {
	try {
		await fetch(`${hubUrl}/api/internal/agents/${agentName}/tasks/${taskId}/ack`, {
			method: "POST",
			headers: { "X-Internal-Token": token },
		});
	} catch (err) {
		logError("ack failed", { task_id: taskId, err: String(err) });
	}
}

export async function nackTask(hubUrl: string, token: string, agentName: string, taskId: number, error: string) {
	try {
		await fetch(`${hubUrl}/api/internal/agents/${agentName}/tasks/${taskId}/nack`, {
			method: "POST",
			headers: { "X-Internal-Token": token, "Content-Type": "application/json" },
			body: JSON.stringify({ error, delay_ms: resolveNakDelayMs() }),
		});
	} catch (err) {
		logError("nack failed", { task_id: taskId, err: String(err) });
	}
}

export async function postTaskLog(hubUrl: string, token: string, agentName: string, level: string, message: string, correlationId: string, invocationId: string, fields: Record<string, unknown> = {}) {
	try {
		await fetch(`${hubUrl}/api/internal/task-logs`, {
			method: "POST",
			headers: { "X-Internal-Token": token, "Content-Type": "application/json" },
			body: JSON.stringify({ correlationId, invocationId, agentName, level, message, fields }),
		});
	} catch { /* best-effort */ }
}


/** Post conversation payloads to the hub ingest endpoint. Best-effort:
 *  per-request failures are logged and swallowed so reporting never throws. */
export async function postTaskMessages(hubUrl: string, token: string, payloads: ConversationPayload[]): Promise<void> {
	for (const p of payloads) {
		try {
			await fetch(`${hubUrl}/api/internal/task-messages`, {
				method: "POST",
				headers: { "X-Internal-Token": token, "Content-Type": "application/json" },
				body: JSON.stringify(p),
				signal: AbortSignal.timeout(resolvePostTimeoutMs()),
			});
		} catch (err) {
			logError("task-messages post failed", { role: p.role, err: String(err) });
		}
	}
}

/**
 * Serialize captured messages into conversation payloads and post them to the
 * hub. Best-effort and guaranteed not to throw: a serialization failure (e.g.
 * JSON.stringify on circular tool-result content) or a network error is logged
 * and swallowed so it can never block ack/nack or abort the run.
 */
export async function reportConversation(
	hubUrl: string,
	token: string,
	messages: PiMessage[],
	meta: SerializeMeta,
	taskId: number,
): Promise<void> {
	try {
		const payloads = toConversationPayloads(messages, meta);
		if (payloads.length > 0) {
			await postTaskMessages(hubUrl, token, payloads);
		}
	} catch (err) {
		logError("conversation report failed", { task_id: taskId, err: String(err) });
	}
}
