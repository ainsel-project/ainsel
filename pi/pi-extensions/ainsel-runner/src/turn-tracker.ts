/**
 * Turn state: which generation owns the current turn, what it captured, and
 * the incremental post context that streams the transcript to the hub while
 * the task is still running.
 */

import type { SerializeMeta } from "./conversation.ts";
import { reportConversation } from "./hub-client.ts";
import type { PiMessage } from "./pi-types.ts";



/**
 * TurnTracker correlates agent_settled / message_end events with the specific
 * task run that started them. A monotonically increasing generation counter
 * prevents stale events from a previous run from resolving the current
 * run's promise or setting its state.
 *
 * Key pi lifecycle distinction:
 *   - `turn_end` fires after EACH LLM response (+ its tool calls). A single
 *     user message can produce many turns (tool-use loop).
 *   - `agent_settled` fires once, when the entire agent run is complete and
 *     pi will not continue automatically (no more retries, compaction, or
 *     follow-ups).
 *
 * The runner must wait for `agent_settled` — not `turn_end` — to know the
 * LLM has finished processing a task. Resolving on `turn_end` causes the
 * runner to ACK the task after the first LLM response while pi is still
 * executing the tool-use loop, leading to the next task's sendUserMessage
 * being injected into the still-running previous agent run.
 */
export interface TurnTracker {
	/** Incremented each time a new task turn begins. */
	generation: number;
	/** The generation of the currently active turn (0 = no active turn). */
	activeGeneration: number;
	/** Resolves the active turn's promise; null when no turn is active. */
	resolve: (() => void) | null;
	/** Per-turn error state, reset when a new turn starts. */
	errorMessage: string | null;
	assistantMessageCompleted: boolean;
	/** Messages captured during the active turn, reset on beginTurn. Each
	 *  entry is tagged with the generation active at capture time so a turn
	 *  only serializes the messages it actually captured. */
	capturedMessages: { gen: number; message: PiMessage }[];
	/** Whether the agent has settled (no more events will fire) since the
	 *  last beginTurn. Starts true (agent is idle at startup), set false on
	 *  beginTurn, set true again by markSettled (agent_settled event). */
	settled: boolean;
	/** Resolves a pending waitForSettle promise; null when nobody is waiting. */
	settleResolve: (() => void) | null;
	/** Post context for the task currently being processed, or null between
	 *  tasks. When set, turn_end flushes newly captured messages to the hub
	 *  incrementally (live conversation tracking). See TaskPostContext. */
	postCtx: TaskPostContext | null;
}

export function createTurnTracker(): TurnTracker {
	return {
		generation: 0,
		activeGeneration: 0,
		resolve: null,
		errorMessage: null,
		assistantMessageCompleted: false,
		capturedMessages: [],
		settled: true,
		settleResolve: null,
		postCtx: null,
	};
}

/** Start a new turn: bump generation, reset per-turn state, return a promise
 *  that resolves when agent_settled fires (entire agent run complete). */
export function beginTurn(tracker: TurnTracker): Promise<void> {
	tracker.generation++;
	tracker.activeGeneration = tracker.generation;
	tracker.errorMessage = null;
	tracker.assistantMessageCompleted = false;
	tracker.capturedMessages = [];
	tracker.settled = false;
	return new Promise<void>((resolve) => {
		tracker.resolve = resolve;
	});
}

/** Called by the agent_settled event handler. Only resolves if a turn is
 *  currently active (ignores stale/duplicate events). */
export function endTurn(tracker: TurnTracker) {
	if (tracker.activeGeneration === 0) return; // no active turn
	const r = tracker.resolve;
	tracker.resolve = null;
	tracker.activeGeneration = 0;
	r?.();
}

/** Called by the message_end event handler. Only records state if the
 *  generation matches the active turn. */
export function recordAssistantEnd(tracker: TurnTracker, errorMessage: string | null) {
	if (tracker.activeGeneration === 0) return; // stale event
	tracker.assistantMessageCompleted = true;
	if (errorMessage) {
		tracker.errorMessage = errorMessage;
	}
}

/** Called by the agent_settled event handler. Marks the agent as settled
 *  and resolves any pending waitForSettle promise. */
export function markSettled(tracker: TurnTracker) {
	tracker.settled = true;
	const r = tracker.settleResolve;
	tracker.settleResolve = null;
	r?.();
}

/**
 * Wait for the abandoned turn to fully settle before allowing the next task
 * to begin. Checks ctx.isIdle() first (covers the race where the turn
 * finished between the timeout firing and the abort call). If not idle,
 * awaits the settle promise with a bounded timeout so the run can
 * never hang forever.
 *
 * Returns true if the agent settled, false if the backstop timeout expired.
 */
export async function waitForSettle(
	tracker: TurnTracker,
	ctx: { isIdle(): boolean },
	timeoutMs: number,
): Promise<boolean> {
	// Already settled — nothing to wait for.
	if (tracker.settled || ctx.isIdle()) return true;

	return new Promise<boolean>((resolve) => {
		// Re-check inside the executor to close the window where markSettled
		// fires between the fast-path check above and promise creation.
		if (tracker.settled || ctx.isIdle()) {
			resolve(true);
			return;
		}
		let timer: ReturnType<typeof setTimeout> | undefined;
		tracker.settleResolve = () => {
			clearTimeout(timer);
			resolve(true);
		};
		timer = setTimeout(() => {
			tracker.settleResolve = null;
			resolve(false);
		}, timeoutMs);
	});
}

/**
 * Capture a message into the active turn's buffer (ignores events when no
 * turn is active). Each entry is tagged with the generation that was active
 * at capture time so processTask can serialize only the messages that belong
 * to its own turn.
 *
 * Guarantee (normal path): the generation tag prevents attributing messages
 * to a turn that did not capture them — processTask filters on
 * `gen === thisGeneration`, so messages captured under a different generation
 * are never serialized into the wrong task's transcript.
 *
 * Timeout path (cancel + drain): when a turn times out, processTask calls
 * ctx.abort() to cancel the abandoned turn, then awaits waitForSettle (with
 * a bounded TURN_SETTLE_TIMEOUT_MS backstop) before returning. While the
 * drain is in progress, activeGeneration stays 0, so orphaned message_end
 * events are dropped here and the agent_settled event no-ops in endTurn.
 * This prevents misattribution of the abandoned turn's tail into the next
 * task's transcript. A vanishingly small residual window remains only if
 * abort() fails to terminate the run and the backstop timeout expires;
 * that case is logged loudly for observability.
 */
export function captureMessage(tracker: TurnTracker, message: PiMessage): void {
	if (tracker.activeGeneration === 0) return; // no active turn
	tracker.capturedMessages.push({ gen: tracker.activeGeneration, message });
}

/** Post context for the task currently being processed. Installed by
 *  processTask after beginTurn; while it is set, flushTurnMessages posts
 *  newly captured messages to the hub on every turn_end so the conversation
 *  view shows progress while the task is still running. Cleared when the
 *  final transcript report has been queued. */
export interface TaskPostContext {
	hubUrl: string
	token: string
	/** Conversation metadata (agent, invocation, task correlation). */
	meta: SerializeMeta
	/** Generation of the owning task's turn; flushes only run while it
	 *  matches the tracker's activeGeneration. */
	gen: number
	taskId: number
	/** Number of the task's captured messages already queued for posting.
	 *  Each flush slices past this so a message is never posted twice. */
	postedCount: number
	/** Tail of the serialized post chain. Batches are chained (never fired
	 *  concurrently) so they always reach the hub in conversation order. */
	postChain: Promise<void>
}

/** Queue any not-yet-posted messages of the task that owns ctx. Slicing past
 *  ctx.postedCount keeps a message from being posted twice across successive
 *  flushes; each batch is chained onto ctx.postChain so batches cannot
 *  interleave and reach the hub out of order. */
export function queueConversationFlush(tracker: TurnTracker, ctx: TaskPostContext): void {
	const pending = tracker.capturedMessages.filter((e) => e.gen === ctx.gen);
	const fresh = pending.slice(ctx.postedCount);
	if (fresh.length === 0) return;
	ctx.postedCount = pending.length;
	ctx.postChain = ctx.postChain.then(() =>
		reportConversation(ctx.hubUrl, ctx.token, fresh.map((e) => e.message), ctx.meta, ctx.taskId),
	);
}

/** Post newly captured messages of the active task to the hub. Called from
 *  the turn_end handler for live conversation tracking. No-op when no task
 *  is being processed or the turn belongs to a different generation (e.g.
 *  while an aborted turn is draining). Fire-and-forget: reportConversation
 *  is best-effort and never throws. */
export function flushTurnMessages(tracker: TurnTracker): void {
	const ctx = tracker.postCtx;
	if (ctx === null) return;
	if (tracker.activeGeneration === 0 || tracker.activeGeneration !== ctx.gen) return;
	queueConversationFlush(tracker, ctx);
}

/** How a completed turn's tracker state maps to the task verdict.
 *  - "ok": the turn succeeded and the tracker agrees — ack the task.
 *  - "llm-error": the assistant message ended with an error.
 *  - "no-assistant-message": the run settled without completing one.
 *  - "generation-mismatch": a newer turn owns the tracker, so its state is
 *    stale and cannot be trusted for this task.
 *  - "already-failed": the caller's turn had already failed (timeout or a
 *    throw from `sendUserMessage`); the tracker adds nothing and the caller
 *    keeps the error message it set itself. */
export type TurnOutcomeKind =
	| "ok"
	| "llm-error"
	| "no-assistant-message"
	| "generation-mismatch"
	| "already-failed";

export interface TurnOutcome {
	ok: boolean;
	/** Nack reason; empty for "ok" and "already-failed", where the caller
	 *  already has the message to report. */
	reason: string;
	kind: TurnOutcomeKind;
}

/** Decide whether a turn that reported success really can be acked, from the
 *  tracker state the `agent_settled` / `message_end` handlers left behind.
 *
 *  Pure — no awaits, no I/O — so the verdict that decides ack-vs-nack is
 *  testable on its own. `processTask` maps each kind to its own log line and
 *  nack reason; the ordering matters: an LLM error also means no assistant
 *  message completed, and the error is the more specific of the two.
 *
 *  After a normal `endTurn()` (via agent_settled), `activeGeneration` is 0 —
 *  that is the expected "run completed" state. A genuine mismatch is when a
 *  *different non-zero* generation is active (i.e. a newer run started). */
export function evaluateTurnOutcome(
	tracker: TurnTracker,
	thisGeneration: number,
	succeeded: boolean,
): TurnOutcome {
	if (!succeeded) return { ok: false, reason: "", kind: "already-failed" };
	if (tracker.activeGeneration !== 0 && tracker.activeGeneration !== thisGeneration) {
		return {
			ok: false,
			reason: "Turn state corrupted (generation mismatch); nacking for retry",
			kind: "generation-mismatch",
		};
	}
	if (tracker.errorMessage !== null) {
		return { ok: false, reason: tracker.errorMessage, kind: "llm-error" };
	}
	if (!tracker.assistantMessageCompleted) {
		return {
			ok: false,
			reason: "Turn ended without producing an assistant message",
			kind: "no-assistant-message",
		};
	}
	return { ok: true, reason: "", kind: "ok" };
}
