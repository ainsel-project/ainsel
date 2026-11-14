/**
 * The normalized view of an event, and the labels derived from it. Pure: no
 * env reads, no I/O, no pi types beyond the structural ones in `pi-types`.
 */



// EventContext is the normalized view of an event.
export interface EventContext {
	type?: string;
	action?: string;
	actor?: string;
	owner?: string;
	repo?: string;
	number?: number;
	kind?: string;
	id?: string;
	prompt?: string;
	cronTrigger?: string;
	chatSessionId?: string;
	chatMessage?: string;
}

export interface HubEvent {
	type?: string;
	id?: string;
	subject?: {
		kind?: string;
		owner?: string;
		repo?: string;
		number?: number;
	};
	actor?: { login?: string };
	action?: string;
	headers?: Record<string, string>;
	data?: Record<string, unknown>;
	[k: string]: unknown;
}


export function extractEventContext(event: HubEvent): EventContext {
	const data = event.data ?? {};
	const headers = event.headers ?? {};

	const eventType =
		event.type ??
		headers["type"] ??
		(typeof data.type === "string" ? data.type : undefined);
	if (eventType === "chat.message") {
		const sessionId =
			typeof data.session_id === "string" ? data.session_id : undefined;
		const message =
			typeof data.message === "string" ? data.message : "";
		return {
			type: "chat.message",
			id: event.id,
			chatSessionId: sessionId,
			chatMessage: message,
		};
	}

	if (headers["type"] === "cron") {
		const prompt = typeof data.prompt === "string" ? data.prompt : "";
		return {
			type: "cron",
			id: event.id,
			prompt,
			cronTrigger: typeof data.cronTrigger === "string" ? data.cronTrigger : undefined,
		};
	}

	if (event.type || event.subject || event.actor || event.action) {
		return {
			type: event.type,
			action: event.action,
			actor: event.actor?.login,
			owner: event.subject?.owner,
			repo: event.subject?.repo,
			number: event.subject?.number,
			kind: event.subject?.kind,
			id: event.id,
		};
	}

	const action = typeof data.action === "string" ? data.action : undefined;
	const sender = data.sender as Record<string, unknown> | undefined;
	const actor =
		typeof sender?.login === "string" ? sender.login : undefined;

	const repository = data.repository as Record<string, unknown> | undefined;
	const ownerLogin = (repository?.owner as Record<string, unknown> | undefined)?.login;
	const owner = typeof ownerLogin === "string" ? ownerLogin : undefined;
	const repoName =
		typeof repository?.name === "string" ? repository.name : undefined;
	const fullName =
		typeof repository?.full_name === "string"
			? (repository.full_name as string)
			: undefined;
	const repo = repoName ?? fullName;

	let number: number | undefined;
	let kind: string | undefined;
	const issue = data.issue as Record<string, unknown> | undefined;
	const pr = data.pull_request as Record<string, unknown> | undefined;
	if (typeof issue?.number === "number") {
		number = issue.number;
		kind = "issue";
	} else if (typeof pr?.number === "number") {
		number = pr.number;
		kind = "pull_request";
	}

	// The hub sets a canonical "type" header (e.g. "issues", "push")
	// derived from the webhook event-type header in a platform-independent
	// way. Combine it with the action to produce a compound type like
	// "issues.opened".
	const headerType = headers["type"];
	const type =
		action && headerType
			? `${headerType}.${action}`
			: (headerType ?? action);

	return {
		type,
		action,
		actor,
		owner,
		repo,
		number,
		kind,
		id: event.id,
	};
}

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

export function repoFullName(ctx: EventContext): string {
	if (ctx.owner && ctx.repo) return `${ctx.owner}/${ctx.repo}`;
	return ctx.repo ?? "";
}


/** The subject labels the token views group by (repo / org / event type /
 *  issue). Single source of truth shared by setAinselEventEnv — which feeds
 *  the agent_tokens_used_total counter labels — and the conversation-row
 *  payload, so the counter and the hub row report the same subject for a
 *  turn (issue #410). Pure and exported for unit testing. */
export function eventSubject(ctx: EventContext): {
	repo: string;
	org: string;
	eventType: string;
	issueNumber: number;
} {
	if (ctx.type === "chat.message") {
		return { repo: "", org: "", eventType: "chat.message", issueNumber: 0 };
	}
	return {
		repo: repoFullName(ctx),
		org: ctx.owner ?? "",
		eventType: ctx.type ?? "",
		issueNumber: ctx.number ?? 0,
	};
}


export function trackingID(ctx: EventContext): string {
	if (ctx.type === "chat.message" && ctx.chatSessionId) {
		return `chat/${ctx.chatSessionId}`;
	}
	const repo = repoFullName(ctx);
	if (!repo || !ctx.number) return repo;
	const kind = ctx.kind === "pull_request" ? "pulls" : "issues";
	return `${repo}/${kind}/${ctx.number}`;
}


export function titleFor(ctx: EventContext): string {
	if (ctx.type === "chat.message") {
		return ctx.chatSessionId
			? `chat ${ctx.chatSessionId} · chat.message`
			: "chat · chat.message";
	}
	if (ctx.type === "cron") {
		const name = ctx.cronTrigger ?? "cron";
		return `${name} · cron`;
	}
	const repo = repoFullName(ctx);
	const ref = ctx.number
		? ctx.kind === "pull_request"
			? `${repo}!${ctx.number}`
			: `${repo}#${ctx.number}`
		: repo;
	const type = ctx.type ?? "event";
	return ref ? `${ref} · ${type}` : type;
}
