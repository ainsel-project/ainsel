import { describe, it } from "node:test";

import assert from "node:assert/strict";

import {
	beginTurn,
	captureMessage,
	createTurnTracker,
	evaluateTurnOutcome,
	endTurn,
	flushTurnMessages,
	markSettled,
	queueConversationFlush,
	recordAssistantEnd,
	waitForSettle,
	type TurnTracker,
} from "./turn-tracker.ts";

describe("TurnTracker", () => {
	it("resolves the turn promise on endTurn", async () => {
		const tracker = createTurnTracker();
		const turnDone = beginTurn(tracker);

		recordAssistantEnd(tracker, null);
		endTurn(tracker);

		await turnDone; // should resolve without hanging
		assert.equal(tracker.activeGeneration, 0);
		assert.equal(tracker.assistantMessageCompleted, true);
		assert.equal(tracker.errorMessage, null);
	});

	it("ignores a stale/duplicate endTurn", async () => {
		const tracker = createTurnTracker();
		const turnDone = beginTurn(tracker);

		endTurn(tracker);
		await turnDone;

		// Second endTurn is a no-op — activeGeneration is already 0
		endTurn(tracker);
		assert.equal(tracker.activeGeneration, 0);
		assert.equal(tracker.resolve, null);
	});

	it("ignores recordAssistantEnd after endTurn (stale message_end)", async () => {
		const tracker = createTurnTracker();
		const turnDone = beginTurn(tracker);

		endTurn(tracker);
		await turnDone;

		// Stale message_end arrives after turn already ended
		recordAssistantEnd(tracker, "late error");
		assert.equal(tracker.assistantMessageCompleted, false);
		assert.equal(tracker.errorMessage, null);
	});

	it("records error from recordAssistantEnd", async () => {
		const tracker = createTurnTracker();
		const turnDone = beginTurn(tracker);

		recordAssistantEnd(tracker, "LLM exploded");
		endTurn(tracker);
		await turnDone;

		assert.equal(tracker.errorMessage, "LLM exploded");
		assert.equal(tracker.assistantMessageCompleted, true);
	});

	it("bumps generation on each beginTurn", () => {
		const tracker = createTurnTracker();

		beginTurn(tracker);
		assert.equal(tracker.generation, 1);
		assert.equal(tracker.activeGeneration, 1);

		endTurn(tracker);
		beginTurn(tracker);
		assert.equal(tracker.generation, 2);
		assert.equal(tracker.activeGeneration, 2);
	});

	it("resets per-turn state on beginTurn", async () => {
		const tracker = createTurnTracker();

		const turn1 = beginTurn(tracker);
		recordAssistantEnd(tracker, "error from turn 1");
		endTurn(tracker);
		await turn1;

		// New turn resets state
		beginTurn(tracker);
		assert.equal(tracker.errorMessage, null);
		assert.equal(tracker.assistantMessageCompleted, false);
	});

	it("post-turn check: activeGeneration === 0 after normal endTurn (blocker fix)", async () => {
		const tracker = createTurnTracker();
		const turnDone = beginTurn(tracker);
		const thisGeneration = tracker.activeGeneration;

		recordAssistantEnd(tracker, null);
		endTurn(tracker);
		await turnDone;

		// This is the exact check from processTask — after a normal endTurn,
		// activeGeneration is 0, which must be treated as "turn completed
		// normally", not as a generation mismatch.
		const stateIsTrusted =
			tracker.activeGeneration === 0 || tracker.activeGeneration === thisGeneration;
		assert.equal(stateIsTrusted, true, "normal endTurn must be trusted");
	});

	it("post-turn check: detects genuine generation mismatch", () => {
		const tracker = createTurnTracker();
		beginTurn(tracker);
		const thisGeneration = tracker.activeGeneration; // 1

		// Simulate re-entrancy: a new turn starts before the old one ends
		beginTurn(tracker);
		// activeGeneration is now 2, thisGeneration is 1

		const stateIsTrusted =
			tracker.activeGeneration === 0 || tracker.activeGeneration === thisGeneration;
		assert.equal(stateIsTrusted, false, "different non-zero generation is a mismatch");
	});
});

describe("captureMessage", () => {
	it("captures messages during an active turn", () => {
		const tracker = createTurnTracker();
		beginTurn(tracker);

		captureMessage(tracker, { role: "user", content: "hi" });
		captureMessage(tracker, { role: "assistant", content: "hello" });

		assert.equal(tracker.capturedMessages.length, 2);
	});

	it("resets the buffer on a new beginTurn", () => {
		const tracker = createTurnTracker();
		beginTurn(tracker);
		captureMessage(tracker, { role: "user", content: "turn 1" });
		endTurn(tracker);

		beginTurn(tracker);
		assert.equal(tracker.capturedMessages.length, 0);
	});

	it("ignores captures when no turn is active", () => {
		const tracker = createTurnTracker();

		captureMessage(tracker, { role: "user", content: "stale" });

		assert.equal(tracker.capturedMessages.length, 0);
	});

	it("tags each captured message with the active generation", () => {
		const tracker = createTurnTracker();
		beginTurn(tracker); // generation 1
		const gen = tracker.activeGeneration;

		captureMessage(tracker, { role: "user", content: "hi" });

		assert.equal(tracker.capturedMessages.length, 1);
		assert.equal(tracker.capturedMessages[0].gen, gen);
		assert.deepEqual(tracker.capturedMessages[0].message, { role: "user", content: "hi" });
	});

	it("drops orphaned messages after timeout drain (activeGeneration === 0)", () => {
		const tracker = createTurnTracker();
		beginTurn(tracker); // generation 1
		captureMessage(tracker, { role: "user", content: "legit" });

		// Simulate timeout: abandon the turn
		tracker.resolve = null;
		tracker.activeGeneration = 0;

		// Orphaned message_end from the abandoned turn arrives during drain
		captureMessage(tracker, { role: "assistant", content: "orphaned tail" });

		// Only the legit message should be captured
		assert.equal(tracker.capturedMessages.length, 1);
		assert.deepEqual(tracker.capturedMessages[0].message, { role: "user", content: "legit" });
	});
});

describe("markSettled / waitForSettle", () => {
	it("markSettled sets the settled flag", () => {
		const tracker = createTurnTracker();
		beginTurn(tracker);
		assert.equal(tracker.settled, false);

		markSettled(tracker);
		assert.equal(tracker.settled, true);
	});

	it("beginTurn resets settled to false", () => {
		const tracker = createTurnTracker();
		assert.equal(tracker.settled, true); // idle at startup

		beginTurn(tracker);
		assert.equal(tracker.settled, false);

		markSettled(tracker);
		assert.equal(tracker.settled, true);

		beginTurn(tracker);
		assert.equal(tracker.settled, false);
	});

	it("waitForSettle returns immediately when already settled", async () => {
		const tracker = createTurnTracker();
		// settled is true by default (agent idle at startup)
		const ctx = { isIdle: () => false };

		const result = await waitForSettle(tracker, ctx, 5000);
		assert.equal(result, true);
	});

	it("waitForSettle returns immediately when ctx.isIdle() is true", async () => {
		const tracker = createTurnTracker();
		beginTurn(tracker); // sets settled = false
		const ctx = { isIdle: () => true };

		const result = await waitForSettle(tracker, ctx, 5000);
		assert.equal(result, true);
	});

	it("waitForSettle resolves when markSettled fires", async () => {
		const tracker = createTurnTracker();
		beginTurn(tracker); // sets settled = false
		const ctx = { isIdle: () => false };

		// Fire markSettled after a short delay
		setTimeout(() => markSettled(tracker), 10);

		const result = await waitForSettle(tracker, ctx, 5000);
		assert.equal(result, true);
		assert.equal(tracker.settled, true);
		assert.equal(tracker.settleResolve, null);
	});

	it("waitForSettle returns false on backstop timeout", async () => {
		const tracker = createTurnTracker();
		beginTurn(tracker); // sets settled = false
		const ctx = { isIdle: () => false };

		// Never call markSettled — the backstop should fire
		const result = await waitForSettle(tracker, ctx, 50);
		assert.equal(result, false);
		assert.equal(tracker.settleResolve, null);
	});

	it("waitForSettle re-checks inside the executor to close the race window", async () => {
		const tracker = createTurnTracker();
		beginTurn(tracker); // sets settled = false

		// isIdle returns false on the fast-path check, then true inside the
		// executor — simulating markSettled firing between the two checks.
		let calls = 0;
		const ctx = { isIdle: () => ++calls > 1 };

		const result = await waitForSettle(tracker, ctx, 5000);
		assert.equal(result, true);
		// The backstop timer must not have been needed.
		assert.equal(tracker.settleResolve, null);
	});

	it("orphaned turn_end after timeout does not resolve the next turn", async () => {
		const tracker = createTurnTracker();

		// Turn 1 starts and times out
		beginTurn(tracker); // generation 1
		tracker.resolve = null;
		tracker.activeGeneration = 0;

		// Turn 2 starts
		const turn2Done = beginTurn(tracker); // generation 2
		assert.equal(tracker.activeGeneration, 2);

		// Orphaned turn_end from turn 1 arrives — but activeGeneration is 2,
		// so endTurn WILL resolve it (this is the second bug). However, with
		// the cancel+drain fix, the abandoned turn settles BEFORE beginTurn
		// is called for turn 2, so this scenario cannot happen in practice.
		// This test documents that endTurn still resolves for any non-zero
		// activeGeneration — the fix prevents reaching this state.
		endTurn(tracker);

		// turn2Done resolved (because endTurn doesn't check generation)
		await turn2Done;
		assert.equal(tracker.activeGeneration, 0);
	});

	it("full timeout-drain cycle: orphaned events are harmless", async () => {
		const tracker = createTurnTracker();

		// Turn 1 starts
		beginTurn(tracker); // generation 1
		captureMessage(tracker, { role: "user", content: "task A" });

		// Timeout: abandon turn 1
		tracker.resolve = null;
		tracker.activeGeneration = 0;

		// During drain: orphaned events arrive
		captureMessage(tracker, { role: "assistant", content: "orphaned" });
		endTurn(tracker); // no-op: activeGeneration is 0

		// Drain completes (markSettled)
		markSettled(tracker);
		assert.equal(tracker.settled, true);

		// Turn 2 starts — clean slate
		const turn2Done = beginTurn(tracker); // generation 2
		assert.equal(tracker.settled, false);
		assert.equal(tracker.capturedMessages.length, 0);

		// Turn 2 proceeds normally
		captureMessage(tracker, { role: "user", content: "task B" });
		recordAssistantEnd(tracker, null);
		endTurn(tracker);
		await turn2Done;

		// Only task B's message is captured under generation 2
		assert.equal(tracker.capturedMessages.length, 1);
		assert.equal(tracker.capturedMessages[0].gen, 2);
		assert.deepEqual(tracker.capturedMessages[0].message, { role: "user", content: "task B" });
	});
});

describe("flushTurnMessages", () => {
	const meta = { agentName: "olli", invocationId: "inv-1", correlationId: "task-7" };

	/** newPostCtx builds a post context for the tracker's active generation. */
	function newPostCtx(tracker: any, opts: { gen?: number; taskId?: number } = {}) {
		return {
			hubUrl: "http://hub",
			token: "tok",
			meta,
			gen: opts.gen ?? tracker.activeGeneration,
			taskId: opts.taskId ?? 7,
			postedCount: 0,
			postChain: Promise.resolve(),
		};
	}

	/** fetchRecorder replaces globalThis.fetch and records the role of each
	 *  task-messages POST body, in call order. */
	function fetchRecorder(): { posted: string[]; calls: () => number } {
		const posted: string[] = [];
		globalThis.fetch = (async (_url: any, init?: any) => {
			const body = JSON.parse(init?.body);
			posted.push(body.role);
			return new Response(null, { status: 204 });
		}) as any;
		return { posted, calls: () => posted.length };
	}

	it("posts newly captured messages and never posts one twice", async () => {
		const originalFetch = globalThis.fetch;
		try {
			const tracker = createTurnTracker();
			beginTurn(tracker);
			const ctx = newPostCtx(tracker);
			tracker.postCtx = ctx;

			const { posted } = fetchRecorder();
			captureMessage(tracker, { role: "user", content: "hi" });
			captureMessage(tracker, { role: "assistant", content: [{ type: "text", text: "hello" }], usage: { input: 1, output: 2, totalTokens: 3 } });
			flushTurnMessages(tracker);
			await ctx.postChain;
			assert.deepEqual(posted, ["user", "assistant"]);

			// Second turn: only the newly captured pair is posted.
			captureMessage(tracker, { role: "assistant", content: [{ type: "text", text: "more" }], usage: { input: 1, output: 1, totalTokens: 2 } });
			captureMessage(tracker, { role: "toolResult", toolCallId: "tc-1", isError: false, content: "ok" });
			flushTurnMessages(tracker);
			await ctx.postChain;
			assert.deepEqual(posted, ["user", "assistant", "assistant", "toolResult"]);
		} finally {
			globalThis.fetch = originalFetch;
		}
	});

	it("is a no-op without an active task post context", () => {
		const tracker = createTurnTracker();
		beginTurn(tracker);
		captureMessage(tracker, { role: "user", content: "hi" });
		// No postCtx installed — must not throw and must not post.
		flushTurnMessages(tracker);
	});

	it("is a no-op while an aborted turn is draining (generation mismatch)", async () => {
		const originalFetch = globalThis.fetch;
		try {
			const tracker = createTurnTracker();
			beginTurn(tracker);
			const ctx = newPostCtx(tracker, { gen: 99 });
			tracker.postCtx = ctx;
			const { posted } = fetchRecorder();
			captureMessage(tracker, { role: "user", content: "hi" });
			flushTurnMessages(tracker);
			await new Promise((r) => setTimeout(r, 10));
			assert.equal(posted.length, 0);
		} finally {
			globalThis.fetch = originalFetch;
		}
	});

	it("is a no-op after the turn settled (activeGeneration 0)", async () => {
		const originalFetch = globalThis.fetch;
		try {
			const tracker = createTurnTracker();
			beginTurn(tracker);
			const ctx = newPostCtx(tracker);
			tracker.postCtx = ctx;
			const { posted } = fetchRecorder();
			captureMessage(tracker, { role: "user", content: "hi" });
			endTurn(tracker); // agent_settled
			flushTurnMessages(tracker);
			await new Promise((r) => setTimeout(r, 10));
			assert.equal(posted.length, 0);
		} finally {
			globalThis.fetch = originalFetch;
		}
	});

	it("chains batches so a later batch cannot overtake an in-flight earlier one", async () => {
		const originalFetch = globalThis.fetch;
		try {
			const tracker = createTurnTracker();
			beginTurn(tracker);
			const ctx = newPostCtx(tracker);
			tracker.postCtx = ctx;

			const posted: string[] = [];
			let calls = 0;
			let releaseHold: (() => void) | null = null;
			const hold = new Promise<void>((resolve) => { releaseHold = resolve; });
			globalThis.fetch = (async (_url: any, init?: any) => {
				calls++;
				const body = JSON.parse(init?.body);
				posted.push(body.role);
				if (calls === 1) await hold; // first POST stalls until released
				return new Response(null, { status: 204 });
			}) as any;

			captureMessage(tracker, { role: "user", content: "turn one" });
			flushTurnMessages(tracker); // batch 1 — stalls on the held POST
			captureMessage(tracker, { role: "assistant", content: [{ type: "text", text: "turn two" }], usage: { input: 1, output: 1, totalTokens: 2 } });
			flushTurnMessages(tracker); // batch 2 — must queue behind batch 1

			// Give batch 2 ample opportunity to (wrongly) overtake batch 1.
			await new Promise((r) => setTimeout(r, 25));
			assert.deepEqual(posted, ["user"]); // batch 2 must not have started

			releaseHold!();
			await ctx.postChain;
			assert.deepEqual(posted, ["user", "assistant"]);
		} finally {
			globalThis.fetch = originalFetch;
		}
	});

	it("final flush via queueConversationFlush posts only the remainder, once", async () => {
		const originalFetch = globalThis.fetch;
		try {
			const tracker = createTurnTracker();
			beginTurn(tracker);
			const ctx = newPostCtx(tracker);
			tracker.postCtx = ctx;
			const { posted } = fetchRecorder();

			captureMessage(tracker, { role: "user", content: "hi" });
			captureMessage(tracker, { role: "assistant", content: [{ type: "text", text: "hello" }], usage: { input: 1, output: 2, totalTokens: 3 } });
			captureMessage(tracker, { role: "toolResult", toolCallId: "tc-1", isError: false, content: "ok" });

			// Incremental flush posts the first two; then the turn settles.
			ctx.postedCount = 2; // simulate an earlier flush of user+assistant
			flushTurnMessages(tracker);
			assert.equal(posted.length, 0); // nothing new captured since the flush

			// The task ends: final report posts the remaining toolResult only.
			tracker.postCtx = null;
			queueConversationFlush(tracker, ctx);
			await ctx.postChain;
			assert.deepEqual(posted, ["toolResult"]);

			// Repeated final flushes must not duplicate the post.
			queueConversationFlush(tracker, ctx);
			await ctx.postChain;
			assert.deepEqual(posted, ["toolResult"]);
		} finally {
			globalThis.fetch = originalFetch;
		}
	});
});

describe("evaluateTurnOutcome", () => {
	// The ack/nack verdict. Pure over tracker state, so every branch is
	// reachable here without driving a pi turn — which is the point: this is the
	// decision that decides whether the Job sees a failure.
	function trackerWith(state: {
		activeGeneration?: number;
		errorMessage?: string | null;
		assistantMessageCompleted?: boolean;
	}) {
		const tracker = createTurnTracker();
		tracker.activeGeneration = state.activeGeneration ?? 0;
		tracker.errorMessage = state.errorMessage ?? null;
		tracker.assistantMessageCompleted = state.assistantMessageCompleted ?? true;
		return tracker;
	}

	it("passes a completed turn with a settled tracker", () => {
		const outcome = evaluateTurnOutcome(trackerWith({}), 3, true);
		assert.deepStrictEqual(outcome, { ok: true, reason: "", kind: "ok" });
	});

	it("accepts the still-active generation as the expected one", () => {
		// agent_settled can resolve the turn without endTurn() having cleared
		// activeGeneration; the turn's own generation is not a mismatch.
		const outcome = evaluateTurnOutcome(trackerWith({ activeGeneration: 3 }), 3, true);
		assert.equal(outcome.ok, true);
	});

	it("reports the tracker's error message as the nack reason", () => {
		const outcome = evaluateTurnOutcome(
			trackerWith({ errorMessage: "provider returned 500" }),
			3,
			true,
		);
		assert.deepStrictEqual(outcome, {
			ok: false,
			reason: "provider returned 500",
			kind: "llm-error",
		});
	});

	it("prefers the LLM error over the missing assistant message", () => {
		// Both are true here; the error is the more specific of the two, and it
		// is what the old nested check reported.
		const outcome = evaluateTurnOutcome(
			trackerWith({ errorMessage: "boom", assistantMessageCompleted: false }),
			3,
			true,
		);
		assert.equal(outcome.kind, "llm-error");
	});

	it("fails a turn that settled without an assistant message", () => {
		const outcome = evaluateTurnOutcome(
			trackerWith({ assistantMessageCompleted: false }),
			3,
			true,
		);
		assert.deepStrictEqual(outcome, {
			ok: false,
			reason: "Turn ended without producing an assistant message",
			kind: "no-assistant-message",
		});
	});

	it("fails a turn whose tracker belongs to a newer generation", () => {
		const outcome = evaluateTurnOutcome(trackerWith({ activeGeneration: 4 }), 3, true);
		assert.deepStrictEqual(outcome, {
			ok: false,
			reason: "Turn state corrupted (generation mismatch); nacking for retry",
			kind: "generation-mismatch",
		});
	});

	it("leaves an already-failed turn alone", () => {
		// A timeout or a sendUserMessage throw already set the reason; tracker
		// state must not overwrite it.
		const outcome = evaluateTurnOutcome(
			trackerWith({ assistantMessageCompleted: false, errorMessage: "stale" }),
			3,
			false,
		);
		assert.deepStrictEqual(outcome, { ok: false, reason: "", kind: "already-failed" });
	});

	it("does not mutate the tracker", () => {
		const tracker = trackerWith({ activeGeneration: 4 });
		evaluateTurnOutcome(tracker, 3, true);
		assert.equal(tracker.activeGeneration, 4);
		assert.equal(tracker.errorMessage, null);
		assert.equal(tracker.assistantMessageCompleted, true);
	});
});
