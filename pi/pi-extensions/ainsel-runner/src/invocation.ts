/**
 * Process-environment scoping for the task currently running. Both of these
 * mutate process globals, which is why they are a module of their own: the
 * runner is single-task-per-process by design (see README "Sequential only").
 */

import { eventSubject, trackingID, type EventContext } from "./event-context.ts";



export function setAinselEventEnv(ctx: EventContext, agentName: string) {
	const subject = eventSubject(ctx);
	process.env.AINSEL_EVENT_ORG = subject.org;
	process.env.AINSEL_EVENT_REPO = subject.repo;
	process.env.AINSEL_EVENT_AGENT = agentName;
	process.env.AINSEL_EVENT_TYPE = subject.eventType;
	process.env.AINSEL_EVENT_ID = ctx.id ?? "";
	process.env.AINSEL_EVENT_TRACKING_ID =
		ctx.type === "chat.message" ? ctx.chatSessionId ?? "" : trackingID(ctx);
}

/**
 * Sets AINSEL_INVOCATION_ID for the duration of fn and restores the previous
 * value afterwards — including when fn throws or rejects — so the in-process
 * MCP catalog can attribute plugin proxy calls to the running task. An empty
 * invocationId clears the variable for the duration of fn.
 */
export async function withInvocationEnv<T>(
	invocationId: string,
	fn: () => Promise<T>,
): Promise<T> {
	const previous = process.env.AINSEL_INVOCATION_ID;
	if (invocationId) {
		process.env.AINSEL_INVOCATION_ID = invocationId;
	} else {
		delete process.env.AINSEL_INVOCATION_ID;
	}
	try {
		return await fn();
	} finally {
		if (previous === undefined) {
			delete process.env.AINSEL_INVOCATION_ID;
		} else {
			process.env.AINSEL_INVOCATION_ID = previous;
		}
	}
}
