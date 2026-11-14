import { describe, it } from "node:test";

import assert from "node:assert/strict";

import { toConversationPayloads } from "./conversation.ts";

import { resetRedactionCache } from "./redaction.ts";

import { summaryConversationMessage } from "./usage.ts";

describe("toConversationPayloads", () => {
	const meta = {
		agentName: "olli",
		invocationId: "inv-1",
		correlationId: "corr-1",
		repo: "ainsel-project/ainsel",
		org: "ainsel-project",
		eventType: "issue_comment.created",
		issueNumber: 410,
	};

	it("serializes a user message", () => {
		const messages = [
			{
				role: "user",
				content: [
					{ type: "text", text: "Hello" },
					{ type: "image", url: "http://x/y.png" },
				],
			},
		];

		const out = toConversationPayloads(messages, meta);

		assert.equal(out.length, 1);
		assert.equal(out[0].agentName, "olli");
		assert.equal(out[0].invocationId, "inv-1");
		assert.equal(out[0].correlationId, "corr-1");
		// Subject labels travel on every payload, not just assistant rows:
		// a row with no subject is still consumption (issue #410).
		assert.equal(out[0].repo, "ainsel-project/ainsel");
		assert.equal(out[0].org, "ainsel-project");
		assert.equal(out[0].eventType, "issue_comment.created");
		assert.equal(out[0].issueNumber, 410);
		assert.equal(out[0].role, "user");
		assert.equal(out[0].inputTokens, 0);
		assert.equal(out[0].outputTokens, 0);
		// A user row carries no usage: the cache/cost fields must be absent,
		// not defaulted to 0. Defaults here would make the hub's presence
		// flags read "reported" for rows that never had a figure (issue #75).
		assert.equal("cacheReadTokens" in out[0], false);
		assert.equal("cacheWriteTokens" in out[0], false);
		assert.equal("costUsd" in out[0], false);
		assert.equal(out[0].model, "");
		assert.equal(out[0].stopReason, "");
		assert.deepEqual(JSON.parse(out[0].content), [
			{ type: "text", text: "Hello" },
			{ type: "image" },
		]);
	});

	it("serializes an assistant message with thinking, text and toolCall blocks plus usage", () => {
		const messages = [
			{
				role: "assistant",
				model: "gpt-x",
				usage: { input: 10, output: 20, cacheRead: 6000, cacheWrite: 300, cost: { input: 0.001, output: 0.002, cacheRead: 0.003, cacheWrite: 0.004, total: 0.01 } },
				stopReason: "end_turn",
				content: [
					{ type: "thinking", thinking: "hmm" },
					{ type: "text", text: "answer" },
					{ type: "toolCall", id: "tc-1", name: "bash", arguments: { cmd: "ls" } },
					{ type: "weird" },
				],
			},
		];

		const out = toConversationPayloads(messages, meta);

		assert.equal(out.length, 1);
		assert.equal(out[0].role, "assistant");
		assert.equal(out[0].model, "gpt-x");
		assert.equal(out[0].inputTokens, 10);
		assert.equal(out[0].outputTokens, 20);
		assert.equal(out[0].cacheReadTokens, 6000);
		assert.equal(out[0].cacheWriteTokens, 300);
		assert.equal(out[0].costUsd, 0.01);
		assert.equal(out[0].stopReason, "end_turn");
		assert.deepEqual(JSON.parse(out[0].content), [
			{ type: "thinking", text: "hmm" },
			{ type: "text", text: "answer" },
			{ type: "toolCall", id: "tc-1", name: "bash", arguments: { cmd: "ls" } },
			{ type: "weird" },
		]);
	});

	it("serializes a toolResult message", () => {
		const messages = [
			{
				role: "toolResult",
				toolCallId: "tc-1",
				isError: true,
				content: "boom",
			},
		];

		const out = toConversationPayloads(messages, meta);

		assert.equal(out.length, 1);
		assert.equal(out[0].role, "toolResult");
		assert.deepEqual(JSON.parse(out[0].content), {
			toolCallId: "tc-1",
			isError: true,
			content: "boom",
		});
	});

	it("omits cache components when usage omits them", () => {
		// A provider without prompt caching reports no cacheRead/cacheWrite.
		// The payload must omit the fields rather than default them to 0: an
		// omitted field is stored as NULL — "not reported" — while a 0 would
		// claim a measured zero (issue #75).
		const messages = [
			{ role: "assistant", model: "m", usage: { input: 5, output: 5, totalTokens: 10 }, content: [] },
		];
		const out = toConversationPayloads(messages, meta);
		assert.equal("cacheReadTokens" in out[0], false);
		assert.equal("cacheWriteTokens" in out[0], false);
		assert.equal("costUsd" in out[0], false);
	});

	it("omits cache components when usage is absent entirely", () => {
		const messages = [{ role: "assistant", content: [{ type: "text", text: "x" }] }];
		const out = toConversationPayloads(messages, meta);
		assert.equal(out[0].inputTokens, 0);
		assert.equal(out[0].outputTokens, 0);
		assert.equal("cacheReadTokens" in out[0], false);
		assert.equal("cacheWriteTokens" in out[0], false);
		assert.equal("costUsd" in out[0], false);
	});

	it("keeps reported zeros as 0 rather than omitting them", () => {
		// Issue #75, other direction: a provider reporting cached_tokens: 0 has
		// a measured zero hit rate, and a priced model on a free run reports
		// cost 0. Both must be sent as 0 so the hub stores a measurement.
		const messages = [
			{
				role: "assistant",
				model: "m",
				usage: { input: 5, output: 5, cacheRead: 0, cacheWrite: 0, cost: { total: 0 }, totalTokens: 10 },
				content: [],
			},
		];
		const out = toConversationPayloads(messages, meta);
		assert.equal(out[0].cacheReadTokens, 0);
		assert.equal(out[0].cacheWriteTokens, 0);
		assert.equal(out[0].costUsd, 0);
	});

	it("skips unknown roles", () => {
		const messages = [{ role: "system", content: [{ type: "text", text: "hi" }] }];

		const out = toConversationPayloads(messages, meta);

		assert.equal(out.length, 0);
	});

	it("serializes string user-content to a single text block", () => {
		const messages = [{ role: "user", content: "plain string body" }];

		const out = toConversationPayloads(messages, meta);

		assert.equal(out.length, 1);
		assert.equal(out[0].role, "user");
		assert.deepEqual(JSON.parse(out[0].content), [
			{ type: "text", text: "plain string body" },
		]);
	});

	it("returns [] for undefined and null messages", () => {
		assert.deepEqual(toConversationPayloads(undefined as any, meta), []);
		assert.deepEqual(toConversationPayloads(null as any, meta), []);
	});

	it("preserves order and count across multiple messages", () => {
		const messages = [
			{ role: "user", content: [{ type: "text", text: "one" }] },
			{ role: "assistant", content: [{ type: "text", text: "two" }] },
			{ role: "toolResult", toolCallId: "tc-1", content: "three" },
		];

		const out = toConversationPayloads(messages, meta);

		assert.equal(out.length, 3);
		assert.deepEqual(out.map((p) => p.role), ["user", "assistant", "toolResult"]);
	});

	it("defaults assistant fields when usage/model/stopReason are absent", () => {
		const messages = [{ role: "assistant", content: [{ type: "text", text: "hi" }] }];

		const out = toConversationPayloads(messages, meta);

		assert.equal(out.length, 1);
		assert.equal(out[0].model, "");
		assert.equal(out[0].inputTokens, 0);
		assert.equal(out[0].outputTokens, 0);
		assert.equal(out[0].stopReason, "");
	});

	it("serializes an empty content array to \"[]\"", () => {
		const messages = [{ role: "user", content: [] }];

		const out = toConversationPayloads(messages, meta);

		assert.equal(out.length, 1);
		assert.equal(out[0].content, "[]");
	});

	it("skips an unserializable message without discarding the rest", () => {
		const circular: any = { a: 1 };
		circular.self = circular;
		const messages = [
			{ role: "user", content: [{ type: "text", text: "before" }] },
			{ role: "toolResult", toolCallId: "tc-bad", isError: false, content: circular },
			{ role: "assistant", content: [{ type: "text", text: "after" }] },
		];

		const out = toConversationPayloads(messages, meta);

		// The circular message is skipped; the other two survive.
		assert.equal(out.length, 2);
		assert.deepEqual(out.map((p) => p.role), ["user", "assistant"]);
	});
});

describe("toConversationPayloads — toolResult capping", () => {
	const meta = { agentName: "olli", invocationId: "inv-1", correlationId: "corr-1" };

	it("caps oversized string toolResult content with marker", () => {
		const big = "O".repeat(20_000);
		const messages = [{ role: "toolResult", toolCallId: "tc-1", isError: false, content: big }];
		const out = toConversationPayloads(messages, meta);
		assert.equal(out.length, 1);
		const parsed = JSON.parse(out[0].content);
		assert.ok(parsed.content.includes("[truncated"), "must contain truncation marker");
		assert.ok(parsed.content.length < big.length, "must be shorter than original");
	});

	it("small toolResult content is byte-identical to pre-capping behavior", () => {
		const messages = [{ role: "toolResult", toolCallId: "tc-1", isError: true, content: "boom" }];
		const out = toConversationPayloads(messages, meta);
		assert.deepEqual(JSON.parse(out[0].content), {
			toolCallId: "tc-1",
			isError: true,
			content: "boom",
		});
	});

	it("caps oversized text blocks in array content and reduces non-text blocks", () => {
		const content = [
			{ type: "text", text: "T".repeat(20_000) },
			{ type: "image", url: "http://x/y.png" },
		];
		const messages = [{ role: "toolResult", toolCallId: "tc-2", isError: false, content }];
		const out = toConversationPayloads(messages, meta);
		const parsed = JSON.parse(out[0].content);
		assert.ok(parsed.content[0].text.includes("[truncated"), "text block must be capped");
		assert.deepEqual(parsed.content[1], { type: "image" }, "image block must be reduced");
	});

	it("circular toolResult content is still skipped", () => {
		const circular: any = { a: 1 };
		circular.self = circular;
		const messages = [
			{ role: "user", content: [{ type: "text", text: "before" }] },
			{ role: "toolResult", toolCallId: "tc-bad", isError: false, content: circular },
			{ role: "assistant", content: [{ type: "text", text: "after" }] },
		];
		const out = toConversationPayloads(messages, meta);
		assert.equal(out.length, 2);
		assert.deepEqual(out.map((p) => p.role), ["user", "assistant"]);
	});

	it("redaction runs before truncation so secrets are never split", () => {
		const savedEnv = { ...process.env };
		// Clear test vars
		for (const key of Object.keys(process.env)) {
			if (key.startsWith("TEST_")) delete process.env[key];
		}
		const secret = "straddle-secret-value-99999";
		process.env.TEST_STRADDLE_SECRET = secret;
		resetRedactionCache();
		try {
			// Place the secret right at the 75% head boundary of a 100-char cap.
			// head = 75 chars, so put the secret starting at char 70.
			const prefix = "A".repeat(70);
			const suffix = "B".repeat(200);
			const input = prefix + secret + suffix;
			const messages = [{ role: "toolResult", toolCallId: "tc-1", isError: false, content: input }];
			const out = toConversationPayloads(messages, meta);
			const parsed = JSON.parse(out[0].content);
			assert.ok(!parsed.content.includes(secret), "secret must be fully redacted");
			assert.ok(parsed.content.includes("***"), "redaction marker must be present");
		} finally {
			for (const key of Object.keys(process.env)) {
				if (key.startsWith("TEST_")) delete process.env[key];
			}
			Object.assign(process.env, savedEnv);
			resetRedactionCache();
		}
	});

	it("per-message safety net caps pathological many-block content", () => {
		// Create many blocks each just under the per-block cap so the total
		// serialized string exceeds the message-level cap.
		const saved = process.env.TOOL_RESULT_MAX_CHARS;
		try {
			process.env.TOOL_RESULT_MAX_CHARS = "100";
			// messageCap = max(128_000, 8 * 100) = 128_000
			// Create 2000 blocks of 99 chars each → ~200k serialized
			const blocks = Array.from({ length: 2000 }, (_, i) => ({
				type: "text",
				text: `block${i}-` + "X".repeat(90),
			}));
			const messages = [{ role: "toolResult", toolCallId: "tc-1", isError: false, content: blocks }];
			const out = toConversationPayloads(messages, meta);
			assert.equal(out.length, 1);
			// The serialized content string must be bounded by the message cap + marker overhead
			assert.ok(out[0].content.length <= 128_000 + 100, "message-level cap must bound the output");
			assert.ok(out[0].content.includes("[truncated"), "must contain truncation marker");
		} finally {
			if (saved === undefined) delete process.env.TOOL_RESULT_MAX_CHARS;
			else process.env.TOOL_RESULT_MAX_CHARS = saved;
		}
	});
});

describe("toConversationPayloads — compaction and branch-summary rows", () => {
	const meta = {
		agentName: "olli",
		invocationId: "inv-1",
		correlationId: "corr-1",
		repo: "ainsel-project/ainsel",
		org: "ainsel-project",
		eventType: "issues.opened",
		issueNumber: 76,
	};

	it("reads usage off a system row so the hub's Postgres totals include it", () => {
		// The hub prefers task_conversations over the counters whenever
		// Postgres is ready, so a compaction that only reached the counters
		// would still be missing from the UI.
		const row = summaryConversationMessage(
			{ summary: "summary of 40k tokens", usage: { input: 40_000, output: 800, cacheRead: 512, cacheWrite: 64, cost: { total: 0.2 } } },
			"compaction",
			"gpt-x",
		);
		const out = toConversationPayloads([row], meta);
		assert.equal(out.length, 1);
		assert.equal(out[0].role, "system");
		assert.equal(out[0].stopReason, "compaction");
		assert.equal(out[0].model, "gpt-x");
		assert.equal(out[0].inputTokens, 40_000);
		assert.equal(out[0].outputTokens, 800);
		assert.equal(out[0].cacheReadTokens, 512);
		assert.equal(out[0].cacheWriteTokens, 64);
		assert.equal(out[0].costUsd, 0.2);
		assert.equal(out[0].repo, "ainsel-project/ainsel");
		assert.equal(out[0].issueNumber, 76);
		assert.deepEqual(JSON.parse(out[0].content), [{ type: "text", text: "summary of 40k tokens" }]);
	});

	it("keeps a branch summary row distinct from the assistant row beside it", () => {
		// Each row carries its own usage: the summary's tokens are not part of
		// the assistant message that preceded it, and neither is counted twice.
		const messages = [
			{
				role: "assistant",
				model: "gpt-x",
				usage: { input: 10, output: 20, cost: { total: 0.001 } },
				stopReason: "end_turn",
				content: [{ type: "text", text: "done" }],
			},
			summaryConversationMessage(
				{ summary: "branch", usage: { input: 5_000, output: 300, cost: { total: 0.02 } } },
				"branch_summary",
				"gpt-x",
			),
		];
		const out = toConversationPayloads(messages, meta);
		assert.equal(out.length, 2);
		assert.equal(out[0].role, "assistant");
		assert.equal(out[0].inputTokens, 10);
		assert.equal(out[1].role, "system");
		assert.equal(out[1].stopReason, "branch_summary");
		assert.equal(out[1].inputTokens, 5_000);
		assert.equal(out[1].outputTokens, 300);
	});

	it("skips a system message that carries no usage", () => {
		// pi's own prompt-section patches are system messages with no usage;
		// they are not consumption and must not become rows.
		const out = toConversationPayloads(
			[{ role: "system", content: "", sections: { tools: "a b c" }, timestamp: 1 }],
			meta,
		);
		assert.deepEqual(out, []);
	});

	it("redacts secrets in a summary row", () => {
		process.env.TEST_SECRET_76 = "sup3r-s3cret-value";
		resetRedactionCache();
		try {
			const row = summaryConversationMessage(
				{ summary: "token was sup3r-s3cret-value", usage: { input: 5, output: 1 } },
				"compaction",
				"m",
			);
			const out = toConversationPayloads([row], meta);
			assert.ok(!out[0].content.includes("sup3r-s3cret-value"));
			assert.ok(out[0].content.includes("***"));
		} finally {
			delete process.env.TEST_SECRET_76;
			resetRedactionCache();
		}
	});
});
