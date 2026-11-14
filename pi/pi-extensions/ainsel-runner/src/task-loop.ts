/**
 * The task lifecycle: fetch the task the pod exists for, drive one pi turn,
 * report the transcript, and ack or nack. This is the only module that exits
 * the process, and the exit codes are part of the contract with the
 * Kubernetes Job (see `runOneShot`).
 */

import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

import { buildUserMessage } from "./prompt.ts";
import { extractEventContext, eventSubject, trackingID, type HubEvent } from "./event-context.ts";
import {
	resolveNakDelayMs,
	resolveTurnSettleTimeoutMs,
	resolveTurnTimeoutMs,
} from "./config.ts";
import { logError, logInfo } from "./logging.ts";
import {
	ackTask,
	nackTask,
	postTaskLog,
	type Task,
} from "./hub-client.ts";
import {
	beginTurn,
	captureMessage,
	evaluateTurnOutcome,
	queueConversationFlush,
	recordAssistantEnd,
	waitForSettle,
	type TurnTracker,
} from "./turn-tracker.ts";
import { withInvocationEnv, setAinselEventEnv } from "./invocation.ts";
import {
	recordUsage,
	sessionModel,
	summaryConversationMessage,
} from "./usage.ts";
import type { PiUsage } from "./pi-types.ts";

async function processTask(
	pi: ExtensionAPI,
	ctx: ExtensionContext,
	task: Task,
	agentName: string,
	model: string,
	hubUrl: string,
	token: string,
	tracker: TurnTracker,
): Promise<boolean> {
	// The task payload is the raw event data (e.g. the Forgejo webhook
	// body). The hub sends it as-is — not wrapped in a HubEvent envelope.
	// Construct a HubEvent so extractEventContext can parse it via the
	// fallback path (data + headers).
	const event: HubEvent = {
		id: task.event_id,
		headers: task.headers ?? {},
		data: task.payload as Record<string, unknown>,
	};
	const evCtx = extractEventContext(event);
	const invocationId = task.invocation_id ?? "";
	const start = Date.now();

	logInfo("event received", {
		event_type: evCtx.type,
		tracking_id: trackingID(evCtx),
		task_id: task.id,
	});

	// Post a task.log entry so the hub can track execution.
	const correlationId = `task-${task.id}`;
	await postTaskLog(hubUrl, token, agentName, "info", `Processing ${evCtx.type ?? "event"}`, correlationId, invocationId, { event_type: evCtx.type, task_id: task.id });

	setAinselEventEnv(evCtx, agentName);

	// Expose the task's invocation id on the process environment for the
	// whole task — the in-process MCP catalog reads it per request to tag
	// plugin agent-MCP calls. withInvocationEnv restores/clears it on every
	// exit path: success, error, or timeout.
	return withInvocationEnv(invocationId, async () => {
		let succeeded = false;
		let errMsg = "";

		// Begin a new turn — bumps the generation counter so stale agent_settled /
		// message_end events from a previous task cannot resolve this turn.
		const turnDone = beginTurn(tracker);
		const thisGeneration = tracker.activeGeneration;

		// Install the incremental post context. From here on every turn_end
		// flushes newly captured messages to the hub (live conversation
		// tracking); the final report below posts whatever is left and awaits
		// the whole chain before ack/nack.
		tracker.postCtx = {
			hubUrl,
			token,
			meta: { agentName, invocationId, correlationId, ...eventSubject(evCtx) },
			gen: thisGeneration,
			taskId: task.id,
			postedCount: 0,
			postChain: Promise.resolve(),
		};

		try {
			await pi.sendUserMessage(buildUserMessage(evCtx, task.payload));

			// Wait for the turn to end, with a timeout to prevent hanging forever.
			// Read once per task so the timeout, its message and its log line
			// cannot disagree if the env is reparsed.
			const turnTimeoutMs = resolveTurnTimeoutMs();
			const settleTimeoutMs = resolveTurnSettleTimeoutMs();
			let turnTimer: ReturnType<typeof setTimeout> | undefined;
			const timeout = new Promise<"timeout">((resolve) => {
				turnTimer = setTimeout(() => resolve("timeout"), turnTimeoutMs);
			});
			const result = await Promise.race([turnDone, timeout]);
			clearTimeout(turnTimer);

			if (result === "timeout") {
				// Clean up tracker so a late agent_settled doesn't resolve an
				// already-abandoned promise. Keep activeGeneration at 0
				// throughout the drain so orphaned events are dropped.
				tracker.resolve = null;
				tracker.activeGeneration = 0;
				succeeded = false;
				errMsg = `Turn timed out after ${turnTimeoutMs}ms`;
				logError("turn timed out, nacking for retry", {
					event_type: evCtx.type,
					agent: agentName,
					timeout_ms: turnTimeoutMs,
				});

				// Cancel the abandoned turn and wait for it to settle so its
				// tail events cannot leak into the next task's turn.
				ctx.abort();
				const didSettle = await waitForSettle(tracker, ctx, settleTimeoutMs);
				if (!didSettle) {
					logError("abandoned turn did not settle within backstop timeout", {
						event_type: evCtx.type,
						agent: agentName,
						settle_timeout_ms: settleTimeoutMs,
					});
				}
			} else {
				succeeded = true;
			}
		} catch (err) {
			errMsg = err instanceof Error ? err.message : String(err);
			logError("pi turn failed", {
				error_message: errMsg,
				agent: agentName,
				event_type: evCtx.type,
				tracking_id: trackingID(evCtx),
				invocation_id: invocationId,
			});
		}

		// The verdict on a turn that reported success. Pure logic over tracker
		// state, extracted to evaluateTurnOutcome() so it is unit-testable
		// without driving a pi turn; each kind keeps its own log line.
		const outcome = evaluateTurnOutcome(tracker, thisGeneration, succeeded);
		if (outcome.kind === "llm-error") {
			succeeded = false;
			errMsg = outcome.reason;
			logError("LLM returned error, nacking for retry", {
				event_type: evCtx.type,
				error_message: errMsg,
				agent: agentName,
			});
		}

		if (outcome.kind === "no-assistant-message") {
			succeeded = false;
			errMsg = outcome.reason;
			logError("turn ended without assistant message, nacking for retry", {
				event_type: evCtx.type,
				agent: agentName,
			});
		}

		if (outcome.kind === "generation-mismatch") {
			// Generation mismatch — the turn state is stale/unreliable.
			succeeded = false;
			errMsg = outcome.reason;
			logError("turn generation mismatch, nacking for retry", {
				event_type: evCtx.type,
				agent: agentName,
				expected_generation: thisGeneration,
				actual_generation: tracker.activeGeneration,
			});
		}

		const durationMs = Date.now() - start;

		// Report the conversation transcript. Messages already posted by the
		// incremental turn_end flushes are skipped (queueConversationFlush slices
		// past ctx.postedCount); awaiting ctx.postChain covers both the queued
		// incremental batches and this final one. reportConversation is
		// guaranteed not to throw, so a serialize/post failure can never skip
		// ack/nack below or abort the run.
		const postCtx = tracker.postCtx;
		tracker.postCtx = null;
		if (postCtx !== null) {
			queueConversationFlush(tracker, postCtx);
			await postCtx.postChain;
		}

		if (succeeded) {
			await ackTask(hubUrl, token, agentName, task.id);
			logInfo("event processed", {
				event_type: evCtx.type,
				duration_ms: durationMs,
				task_id: task.id,
			});
			return true;
		} else {
			await nackTask(hubUrl, token, agentName, task.id, errMsg);
			logError("event failed, nacked with delay", {
				event_type: evCtx.type,
				duration_ms: durationMs,
				nak_base_delay_ms: resolveNakDelayMs(),
				task_id: task.id,
			});
			// Report the failure to the hub as an error-level task log so it
			// appears in the observability errors feed. Console logError output
			// never leaves the pod; without this the errors view stays empty.
			await postTaskLog(hubUrl, token, agentName, "error", `Event failed: ${errMsg}`, correlationId, invocationId, { event_type: evCtx.type, task_id: task.id, duration_ms: durationMs });
			return false;
		}
	});
}


// runOneShot is the runner entrypoint. The operator built this pod for
// exactly one task (AINSEL_TASK_ID names it); the container exits when the task
// resolves. There is no claim step: the task row stays 'pending' in the hub
// until processTask acks it, so a pod that dies mid-task strands nothing —
// the Job starts a fresh pod for the same row, and that is
// the entire recovery story. No claim, nothing to reap.
export async function runOneShot(
	pi: ExtensionAPI,
	ctx: ExtensionContext,
	agentName: string,
	model: string,
	hubUrl: string,
	token: string,
	tracker: TurnTracker,
	taskId: number,
): Promise<void> {
	if (!Number.isInteger(taskId) || taskId <= 0) {
		logError("AINSEL_TASK_ID is not a task id", { value: String(taskId) });
		process.exit(1);
	}
	logInfo("one-shot mode: fetching task", { task_id: taskId, agent: agentName });

	// SIGTERM here is a node drain or eviction. Exit nonzero without acking:
	// the row is still 'pending' (the contract of this model), and the Job
	// restarts it in a fresh pod within seconds — a stranded claim, the old
	// model's failure mode, cannot exist when nothing is ever claimed.
	process.on("SIGTERM", () => {
		logInfo("SIGTERM in one-shot mode, exiting without ack", { task_id: taskId });
		process.exit(143);
	});

	let resp: Response;
	try {
		resp = await fetch(`${hubUrl}/api/internal/agents/${agentName}/tasks/${taskId}`, {
			headers: { "X-Internal-Token": token },
		});
	} catch (err) {
		logError("task fetch request failed", {
			err: err instanceof Error ? err.message : String(err),
		});
		process.exit(1);
		return;
	}

	if (resp.status === 404) {
		// Task pruned, or never existed for this agent. Nothing to run; exit
		// success so the Job completes instead of burning retry attempts.
		logInfo("task not found, nothing to run", { task_id: taskId });
		process.exit(0);
		return;
	}
	if (resp.status === 409) {
		// The hub's nack backoff is still running for this task. The Job's own
		// retry schedule would otherwise ignore that pacing and could burn the
		// whole attempt budget in seconds, so stand down: exit success, and the
		// hub re-lists the task as due when the backoff elapses — which makes
		// the operator retire this finished Job and dispatch a fresh one.
		logInfo("task in retry backoff, standing down", { task_id: taskId });
		process.exit(0);
		return;
	}
	if (!resp.ok) {
		logError("task fetch returned unexpected status", { status: resp.status });
		process.exit(1);
		return;
	}

	let task: Task;
	try {
		task = (await resp.json()) as Task;
	} catch (err) {
		logError("failed to parse task JSON", {
			err: err instanceof Error ? err.message : String(err),
		});
		process.exit(1);
		return;
	}

	if (task.status === "completed" || task.status === "failed") {
		// A previous attempt resolved it and this pod raced the ack (or the hub
		// exhausted attempts while this pod was fetching). This run has nothing
		// to contribute; succeeding the Job lets the TTL collector clean it up.
		logInfo("task already terminal, exiting", { task_id: taskId, status: task.status });
		process.exit(0);
		return;
	}

	const ok = await processTask(pi, ctx, task, agentName, model, hubUrl, token, tracker);
	// processTask acked or nacked. The exit code feeds the Job's failure count;
	// a nacked row stays retryable in the hub either way.
	process.exit(ok ? 0 : 1);
}

/** Register the handlers that book LLM usage and mirror it into the
 *  conversation transcript.
 *
 *  Three sources, because pi reports an LLM call in three places:
 *
 *    - `message_end` — the agent loop's messages. Booked on the presence of
 *      usage, not on the role: pi types usage on non-assistant messages too
 *      (`ToolResultEventBase.usage`), and the old `role !== "assistant"` guard
 *      dropped whatever those reported (issue #76).
 *    - `session_compact` — the summary of the whole conversation, produced by a
 *      direct one-shot completion that never appears as a message. It is the
 *      largest single prompt a run makes, and until now it counted as neither
 *      input, output, nor cache.
 *    - `session_tree` — the same one-shot completion behind a branch summary.
 *
 *  The last two also capture a synthetic `role: "system"` message so the hub
 *  gets a `task_conversations` row: the hub prefers its Postgres rows over
 *  these counters whenever it has them, so a counter-only fix would stay
 *  invisible in the console. A summary that fires between tasks has no
 *  conversation to attach a row to and captureMessage drops it, but its tokens
 *  are still booked. Exported for unit testing with a real tracker. */
export function registerUsageEvents(pi: ExtensionAPI, tracker: TurnTracker): void {
	pi.on("message_end", (event) => {
		const msg = event.message;
		captureMessage(tracker, msg);
		if (msg.role === "assistant") {
			const errMsg = msg.stopReason === "error"
				? (msg.errorMessage ?? "LLM returned stopReason=error with no message")
				: null;
			if (errMsg) {
				logError("assistant message ended with error", {
					stopReason: msg.stopReason,
					errorMessage: errMsg,
				});
			}
			recordAssistantEnd(tracker, errMsg);
		}
		// Booked on the presence of usage rather than on the role. Read
		// structurally because pi's AgentMessage union declares usage/model only
		// on the members that report them.
		const carrier = msg as { usage?: PiUsage; model?: string };
		recordUsage(carrier.usage, carrier.model ?? "");
	});

	// Compaction summarises the conversation with a direct one-shot completion
	// (completeSimple in pi's compaction module), so it never produces a
	// message_end — the tokens of the largest prompt in the session simply went
	// unrecorded. The usage pi writes to the session is the source of truth
	// here; the synthetic system row is what makes it visible in the hub UI,
	// which reads Postgres in preference to these counters.
	pi.on("session_compact", (event) => {
		const entry = event.compactionEntry as { usage?: PiUsage; summary?: string };
		// A compaction entry stores usage but no model: pi summarises with the
		// model the run is configured for.
		const model = sessionModel();
		if (!recordUsage(entry?.usage, model, "compaction")) return;
		const row = summaryConversationMessage(entry, "compaction", model);
		if (row) captureMessage(tracker, row);
	});

	// Branch/tree summaries are the same gap: pi summarises a discarded branch
	// with a one-shot completion and reports it only as SessionTreeEvent. The
	// event also fires for a navigation that summarised nothing, which has no
	// usage and records nothing.
	pi.on("session_tree", (event) => {
		const entry = event.summaryEntry as { usage?: PiUsage; summary?: string } | undefined;
		if (!entry) return;
		const model = sessionModel();
		if (!recordUsage(entry.usage, model, "branch_summary")) return;
		const row = summaryConversationMessage(entry, "branch_summary", model);
		if (row) captureMessage(tracker, row);
	});
}
