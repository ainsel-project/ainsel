import { describe, it } from "node:test";

import assert from "node:assert/strict";

import { toConversationPayloads } from "./conversation.ts";

import { registerUsageEvents } from "./task-loop.ts";

import { beginTurn, captureMessage, createTurnTracker } from "./turn-tracker.ts";

import { counterValue } from "./usage.ts";

// The counter labels come from the env the runner sets per task, so a counter
// assertion has to pin them and put them back afterwards.
function withEventEnv(labels: Record<string, string>, fn: () => Promise<void> | void): Promise<void> {
	const keys = [
		"AINSEL_EVENT_AGENT",
		"AINSEL_EVENT_REPO",
		"AINSEL_EVENT_ORG",
		"AINSEL_EVENT_TYPE",
		"AINSEL_LLM_MODEL",
		"OLLAMA_CLOUD_MODEL",
	];
	const saved: Record<string, string | undefined> = {};
	for (const key of keys) {
		saved[key] = process.env[key];
		delete process.env[key];
	}
	for (const [key, value] of Object.entries(labels)) process.env[key] = value;
	const restore = () => {
		for (const key of keys) {
			if (saved[key] === undefined) delete process.env[key];
			else process.env[key] = saved[key] as string;
		}
	};
	try {
		return Promise.resolve(fn()).then(restore, (err) => {
			restore();
			throw err;
		});
	} catch (err) {
		restore();
		throw err;
	}
}

describe("registerUsageEvents", () => {
	// A minimal pi stand-in: registerUsageEvents only ever calls on().
	function fakePi(): { handlers: Record<string, (event: any) => void>; pi: any } {
		const handlers: Record<string, (event: any) => void> = {};
		return { handlers, pi: { on: (event: string, handler: any) => { handlers[event] = handler; } } };
	}

	it("books a compaction completion once and captures its row", async () => {
		const { handlers, pi } = fakePi();
		const tracker = createTurnTracker();
		registerUsageEvents(pi, tracker);
		const model = "wiring-model";
		return withEventEnv({ AINSEL_EVENT_AGENT: "olli", OLLAMA_CLOUD_MODEL: model }, async () => {
			beginTurn(tracker);
			handlers.message_end({
				message: {
					role: "assistant",
					model,
					stopReason: "end_turn",
					usage: { input: 100, output: 20, cacheRead: 0, cacheWrite: 0, totalTokens: 120, cost: { total: 0.001 } },
					content: [{ type: "text", text: "working" }],
				},
			});
			handlers.session_compact({
				compactionEntry: {
					type: "compaction",
					summary: "everything so far",
					usage: { input: 40_000, output: 500, cacheRead: 1_024, cacheWrite: 0, totalTokens: 41_524, cost: { total: 0.05 } },
				},
				fromExtension: false,
				reason: "threshold",
				willRetry: false,
			});

			// Each LLM call counted once: the summary's tokens are its own, not
			// already carried by the assistant message next to it.
			assert.equal(await counterValue("agent_tokens_used_total", { model, token_type: "input" }), 40_100);
			assert.equal(await counterValue("agent_tokens_used_total", { model, token_type: "cache_read" }), 1_024);

			const rows = toConversationPayloads(tracker.capturedMessages.map((e) => e.message), {
				agentName: "olli",
				invocationId: "inv-1",
				correlationId: "corr-1",
			});
			assert.equal(rows.length, 2);
			assert.equal(rows[0].role, "assistant");
			assert.equal(rows[0].inputTokens, 100);
			assert.equal(rows[1].role, "system");
			assert.equal(rows[1].stopReason, "compaction");
			assert.equal(rows[1].model, model);
			assert.equal(rows[1].inputTokens, 40_000);
			assert.equal(rows[1].costUsd, 0.05);
		});
	});

	it("books a branch summary from session_tree", async () => {
		const { handlers, pi } = fakePi();
		const tracker = createTurnTracker();
		registerUsageEvents(pi, tracker);
		const model = "tree-model";
		return withEventEnv({ AINSEL_EVENT_AGENT: "olli", OLLAMA_CLOUD_MODEL: model }, async () => {
			beginTurn(tracker);
			handlers.session_tree({
				newLeafId: "e2",
				oldLeafId: "e1",
				summaryEntry: {
					type: "branch_summary",
					summary: "abandoned branch",
					usage: { input: 7_000, output: 200, cacheRead: 0, cacheWrite: 0, totalTokens: 7_200, cost: { total: 0.01 } },
				},
			});
			assert.equal(await counterValue("agent_tokens_used_total", { model, token_type: "input" }), 7_000);
			assert.equal(tracker.capturedMessages.length, 1);
			assert.equal(tracker.capturedMessages[0].message.stopReason, "branch_summary");
		});
	});

	it("records nothing for a navigation that summarised nothing", async () => {
		const { handlers, pi } = fakePi();
		const tracker = createTurnTracker();
		registerUsageEvents(pi, tracker);
		const model = "empty-tree-model";
		return withEventEnv({ AINSEL_EVENT_AGENT: "olli", OLLAMA_CLOUD_MODEL: model }, async () => {
			beginTurn(tracker);
			handlers.session_tree({ newLeafId: "e2", oldLeafId: "e1" });
			handlers.session_tree({
				newLeafId: "e3",
				oldLeafId: "e2",
				summaryEntry: { type: "branch_summary", summary: "", usage: { input: 0, output: 0, cost: { total: 0 } } },
			});
			handlers.session_compact({ compactionEntry: { type: "compaction", summary: "no usage" } });
			assert.equal(tracker.capturedMessages.length, 0);
			assert.equal(await counterValue("agent_tokens_used_total", { model, token_type: "input" }), 0);
		});
	});

	it("books usage a non-assistant message reports", async () => {
		// pi types usage on tool results too; the role guard used to drop it.
		const { handlers, pi } = fakePi();
		const tracker = createTurnTracker();
		registerUsageEvents(pi, tracker);
		const model = "tool-model";
		return withEventEnv({ AINSEL_EVENT_AGENT: "olli", OLLAMA_CLOUD_MODEL: model }, async () => {
			beginTurn(tracker);
			handlers.message_end({
				message: {
					role: "toolResult",
					toolCallId: "tc-1",
					model,
					usage: { input: 300, output: 40, cacheRead: 0, cacheWrite: 0, totalTokens: 340 },
					content: [{ type: "text", text: "ok" }],
				},
			});
			assert.equal(await counterValue("agent_tokens_used_total", { model, token_type: "input" }), 300);
			// A tool result is not an assistant message: it must not mark the
			// turn's assistant message as completed.
			assert.equal(tracker.assistantMessageCompleted, false);
		});
	});

	it("still books the tokens when no turn is active to hold the row", async () => {
		// Idle compaction between tasks: captureMessage drops the row, the
		// counter must not also lose the consumption.
		const { handlers, pi } = fakePi();
		const tracker = createTurnTracker();
		registerUsageEvents(pi, tracker);
		const model = "idle-model";
		return withEventEnv({ AINSEL_EVENT_AGENT: "olli", OLLAMA_CLOUD_MODEL: model }, async () => {
			handlers.session_compact({
				compactionEntry: { type: "compaction", summary: "s", usage: { input: 12_000, output: 100, cost: { total: 0.02 } } },
			});
			assert.equal(tracker.capturedMessages.length, 0);
			assert.equal(await counterValue("agent_tokens_used_total", { model, token_type: "input" }), 12_000);
		});
	});
});
