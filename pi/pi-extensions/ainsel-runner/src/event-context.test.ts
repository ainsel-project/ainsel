import { describe, it } from "node:test";

import assert from "node:assert/strict";

import { eventSubject, extractEventContext, type HubEvent } from "./event-context.ts";

import { setAinselEventEnv } from "./invocation.ts";

describe("extractEventContext", () => {
	it("parses a raw Forgejo issues webhook (regression: PR #569)", () => {
		// This is the exact data flow that was broken: the hub sends
		// the raw webhook body as task.payload and the original headers
		// (plus the canonical "type") as task.headers.
		const event: HubEvent = {
			id: "evt-forgejo-123",
			headers: {
				"type": "issues",
				"X-Trigger-Name": "develop-on-assign",
				"X-Invocation-ID": "inv-abc",
				"X-Forgejo-Event": "issues",
				"content-type": "application/json",
			},
			data: {
				action: "assigned",
				sender: { login: "dpinsel" },
				repository: {
					name: "warhammer-maui",
					full_name: "apps/warhammer-maui",
					owner: { login: "apps" },
				},
				issue: { number: 77, title: "Fix the thing" },
			},
		};

		const ctx = extractEventContext(event);

		assert.equal(ctx.type, "issues.assigned");
		assert.equal(ctx.action, "assigned");
		assert.equal(ctx.actor, "dpinsel");
		assert.equal(ctx.owner, "apps");
		assert.equal(ctx.repo, "warhammer-maui");
		assert.equal(ctx.number, 77);
		assert.equal(ctx.kind, "issue");
		assert.equal(ctx.id, "evt-forgejo-123");
	});

	it("parses a pull_request webhook", () => {
		const event: HubEvent = {
			id: "evt-456",
			headers: { type: "pull_request" },
			data: {
				action: "opened",
				sender: { login: "alice" },
				repository: {
					name: "my-repo",
					owner: { login: "org" },
				},
				pull_request: { number: 15 },
			},
		};

		const ctx = extractEventContext(event);

		assert.equal(ctx.type, "pull_request.opened");
		assert.equal(ctx.owner, "org");
		assert.equal(ctx.repo, "my-repo");
		assert.equal(ctx.number, 15);
		assert.equal(ctx.kind, "pull_request");
		assert.equal(ctx.actor, "alice");
	});

	it("parses a push event (no action)", () => {
		const event: HubEvent = {
			id: "evt-789",
			headers: { type: "push" },
			data: {
				ref: "refs/heads/main",
				sender: { login: "bob" },
				repository: {
					name: "my-repo",
					owner: { login: "org" },
				},
			},
		};

		const ctx = extractEventContext(event);

		assert.equal(ctx.type, "push");
		assert.equal(ctx.owner, "org");
		assert.equal(ctx.repo, "my-repo");
		assert.equal(ctx.number, undefined);
	});

	it("detects chat events via type header", () => {
		const event: HubEvent = {
			id: "evt-chat-1",
			headers: {
				type: "chat.message",
				"X-Trigger-Name": "chat",
			},
			data: {
				session_id: "sess-42",
				message: "Hello agent!",
			},
		};

		const ctx = extractEventContext(event);

		assert.equal(ctx.type, "chat.message");
		assert.equal(ctx.chatSessionId, "sess-42");
		assert.equal(ctx.chatMessage, "Hello agent!");
	});

	it("detects cron events via type header", () => {
		const event: HubEvent = {
			id: "evt-cron-1",
			headers: {
				type: "cron",
				"X-Trigger-Name": "daily-report",
			},
			data: {
				cronTrigger: "daily-report",
				prompt: "Generate the daily summary.",
			},
		};

		const ctx = extractEventContext(event);

		assert.equal(ctx.type, "cron");
		assert.equal(ctx.prompt, "Generate the daily summary.");
		assert.equal(ctx.cronTrigger, "daily-report");
	});

	it("parses a structured HubEvent with subject/actor", () => {
		const event: HubEvent = {
			id: "evt-structured",
			type: "issue.opened",
			action: "opened",
			subject: {
				kind: "issue",
				owner: "org",
				repo: "my-repo",
				number: 42,
			},
			actor: { login: "carol" },
			headers: {},
			data: {},
		};

		const ctx = extractEventContext(event);

		assert.equal(ctx.type, "issue.opened");
		assert.equal(ctx.action, "opened");
		assert.equal(ctx.actor, "carol");
		assert.equal(ctx.owner, "org");
		assert.equal(ctx.repo, "my-repo");
		assert.equal(ctx.number, 42);
		assert.equal(ctx.kind, "issue");
	});

	it("handles empty data gracefully", () => {
		const event: HubEvent = {
			id: "evt-empty",
			headers: {},
			data: {},
		};

		const ctx = extractEventContext(event);

		assert.equal(ctx.id, "evt-empty");
		assert.equal(ctx.owner, undefined);
		assert.equal(ctx.repo, undefined);
		assert.equal(ctx.number, undefined);
	});

	it("uses type header without provider-specific headers", () => {
		// The runner must not depend on X-Forgejo-Event etc.
		const event: HubEvent = {
			id: "evt-generic",
			headers: { type: "issues" },
			data: {
				action: "closed",
				sender: { login: "dave" },
				repository: {
					name: "repo",
					owner: { login: "org" },
				},
				issue: { number: 5 },
			},
		};

		const ctx = extractEventContext(event);

		assert.equal(ctx.type, "issues.closed");
		assert.equal(ctx.owner, "org");
		assert.equal(ctx.number, 5);
	});
});

describe("eventSubject", () => {
	it("uses the full repo name, org, type and issue number for a repo event", () => {
		const s = eventSubject({ type: "issue_comment.created", owner: "ainsel-project", repo: "ainsel", number: 410 });
		assert.deepEqual(s, {
			repo: "ainsel-project/ainsel",
			org: "ainsel-project",
			eventType: "issue_comment.created",
			issueNumber: 410,
		});
	});

	it("falls back to the bare repo when no owner is present", () => {
		const s = eventSubject({ type: "issues", repo: "standalone", number: 3 });
		assert.equal(s.repo, "standalone");
		assert.equal(s.org, "");
	});

	it("reports issueNumber 0 when the event carries no number", () => {
		const s = eventSubject({ type: "push", owner: "o", repo: "r" });
		assert.equal(s.issueNumber, 0);
		assert.equal(s.repo, "o/r");
	});

	it("blanks repo and org for chat.message, like the counter labels do", () => {
		// setAinselEventEnv deliberately reports no repo/org for chat events;
		// the conversation rows must report the same subject so the two
		// destinations of the same fact cannot disagree.
		const s = eventSubject({ type: "chat.message", owner: "ainsel-project", repo: "ainsel", number: 7, chatSessionId: "s-1" });
		assert.deepEqual(s, { repo: "", org: "", eventType: "chat.message", issueNumber: 0 });
	});

	it("defaults type to the empty string when absent", () => {
		const s = eventSubject({ owner: "o", repo: "r" });
		assert.equal(s.eventType, "");
	});
});
