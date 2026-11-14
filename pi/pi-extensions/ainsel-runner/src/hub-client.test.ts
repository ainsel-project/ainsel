import { describe, it } from "node:test";

import assert from "node:assert/strict";

import { type ConversationPayload } from "./conversation.ts";

import { postTaskMessages, reportConversation } from "./hub-client.ts";

describe("reportConversation", () => {
	const meta = { agentName: "olli", invocationId: "inv-1", correlationId: "task-7" };

	it("swallows a serialization failure (circular content) and still resolves", async () => {
		const originalFetch = globalThis.fetch;
		let posted = 0;
		globalThis.fetch = (async () => {
			posted++;
			return new Response(null, { status: 204 });
		}) as any;

		try {
			const circular: any = { a: 1 };
			circular.self = circular;
			const messages = [{ role: "toolResult", toolCallId: "tc-1", isError: false, content: circular }];

			// Regression: if the try/catch in reportConversation were removed,
			// JSON.stringify on the circular content would throw here and
			// propagate out — skipping ack/nack and crashing the poll loop.
			await assert.doesNotReject(
				reportConversation("http://hub", "tok", messages, meta, 7),
			);
			// Serialization failed before posting, so nothing was sent.
			assert.equal(posted, 0);
		} finally {
			globalThis.fetch = originalFetch;
		}
	});

	it("posts payloads for serializable messages", async () => {
		const originalFetch = globalThis.fetch;
		let posted = 0;
		globalThis.fetch = (async () => {
			posted++;
			return new Response(null, { status: 204 });
		}) as any;

		try {
			const messages = [{ role: "user", content: "hi" }];
			await reportConversation("http://hub", "tok", messages, meta, 7);
			assert.equal(posted, 1);
		} finally {
			globalThis.fetch = originalFetch;
		}
	});
});

describe("postTaskMessages", () => {
	it("posts one camelCase request per payload to the task-messages endpoint", async () => {
		const originalFetch = globalThis.fetch;
		const calls: Array<{ url: string; init: RequestInit }> = [];
		globalThis.fetch = (async (url: any, init: any) => {
			calls.push({ url: String(url), init });
			return new Response(null, { status: 204 });
		}) as any;

		try {
			const payloads: ConversationPayload[] = [
				{
					agentName: "olli",
					invocationId: "inv-1",
					correlationId: "task-7",
					role: "assistant",
					content: "[]",
					model: "gpt-x",
					inputTokens: 10,
					outputTokens: 20,
					stopReason: "end_turn",
				},
				{
					agentName: "olli",
					invocationId: "inv-1",
					correlationId: "task-7",
					role: "user",
					content: "[]",
					model: "",
					inputTokens: 0,
					outputTokens: 0,
					stopReason: "",
				},
			];

			await postTaskMessages("http://hub", "tok", payloads);

			assert.equal(calls.length, 2);
			for (const c of calls) {
				assert.equal(c.url, "http://hub/api/internal/task-messages");
				assert.equal(c.init.method, "POST");
				const headers = c.init.headers as Record<string, string>;
				assert.equal(headers["X-Internal-Token"], "tok");
				assert.equal(headers["Content-Type"], "application/json");
			}

			const body = JSON.parse(calls[0].init.body as string);
			assert.equal(body.invocationId, "inv-1");
			assert.equal(body.inputTokens, 10);
			assert.equal(body.agentName, "olli");
			// Ensure no snake_case keys leaked through.
			assert.equal(body.invocation_id, undefined);
			assert.equal(body.input_tokens, undefined);
			assert.equal(body.agent_name, undefined);
		} finally {
			globalThis.fetch = originalFetch;
		}
	});

	it("swallows per-request failures and keeps posting", async () => {
		const originalFetch = globalThis.fetch;
		let count = 0;
		globalThis.fetch = (async () => {
			count++;
			throw new Error("network down");
		}) as any;

		try {
			const payloads: ConversationPayload[] = [
				{
					agentName: "olli",
					invocationId: "inv-1",
					correlationId: "task-7",
					role: "user",
					content: "[]",
					model: "",
					inputTokens: 0,
					outputTokens: 0,
					stopReason: "",
				},
				{
					agentName: "olli",
					invocationId: "inv-1",
					correlationId: "task-7",
					role: "assistant",
					content: "[]",
					model: "",
					inputTokens: 0,
					outputTokens: 0,
					stopReason: "",
				},
			];

			// Must not throw despite every request failing.
			await postTaskMessages("http://hub", "tok", payloads);
			assert.equal(count, 2);
		} finally {
			globalThis.fetch = originalFetch;
		}
	});
});
