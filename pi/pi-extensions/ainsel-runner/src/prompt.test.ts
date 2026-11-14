import { describe, it } from "node:test";

import assert from "node:assert/strict";

import { buildUserMessage } from "./prompt.ts";

describe("buildUserMessage", () => {
	const webhookCtx = {
		type: "issue_comment.created",
		actor: "alice",
		owner: "acme",
		repo: "web",
		number: 83,
		kind: "issue",
		id: "evt-1",
	};

	it("inlines the raw payload in an event-data block", () => {
		const payload = {
			action: "created",
			comment: { body: "@review-agent wdyt?" },
		};
		const msg = buildUserMessage(webhookCtx, payload);

		assert.ok(msg.includes("type: issue_comment.created"));
		assert.ok(msg.includes("issue: 83"));
		assert.ok(msg.includes("<event-data>"));
		assert.ok(msg.includes("</event-data>"));
		assert.ok(msg.includes("@review-agent wdyt?"));
		assert.ok(msg.includes('"action": "created"'));
	});

	it("omits the event-data block when no payload is given", () => {
		const msg = buildUserMessage(webhookCtx);
		assert.ok(!msg.includes("<event-data>"));
		assert.ok(msg.includes("Handle the issue_comment.created"));
	});

	it("omits the event-data block for null payloads", () => {
		const msg = buildUserMessage(webhookCtx, null);
		assert.ok(!msg.includes("<event-data>"));
	});

	it("truncates oversized payloads with a marker", () => {
		const prev = process.env.EVENT_DATA_MAX_CHARS;
		process.env.EVENT_DATA_MAX_CHARS = "200";
		try {
			const payload = { blob: "x".repeat(5000) };
			const msg = buildUserMessage(webhookCtx, payload);
			const start = msg.indexOf("<event-data>") + "<event-data>\n".length;
			const end = msg.lastIndexOf("</event-data>");
			const body = msg.slice(start, end);
			assert.ok(body.includes("[truncated"), "payload must carry a truncation marker");
			assert.ok(body.length < 500, "payload must be capped near the limit");
		} finally {
			if (prev === undefined) delete process.env.EVENT_DATA_MAX_CHARS;
			else process.env.EVENT_DATA_MAX_CHARS = prev;
		}
	});

	it("does not inline payload for chat.message events", () => {
		const msg = buildUserMessage({ type: "chat.message", chatSessionId: "s1", chatMessage: "hi" });
		assert.ok(!msg.includes("<event-data>"));
		assert.ok(msg.includes("hi"));
	});
});
