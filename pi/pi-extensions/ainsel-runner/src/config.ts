/**
 * Every environment tunable the runner reads, in one place.
 *
 * All of them are read lazily at call time, never snapshotted at import:
 * `TOOL_RESULT_MAX_CHARS` and `EVENT_DATA_MAX_CHARS` are documented as lazy in
 * the extension README, and the same laziness keeps every accessor testable by
 * mutating `process.env` between cases.
 */

import { logError } from "./logging.ts";



/** Default per-text-block cap for toolResult content (chars). */
const TOOL_RESULT_MAX_CHARS_DEFAULT = 16_000;

const EVENT_DATA_MAX_CHARS_DEFAULT = 16_000;

/**
 * Resolve the raw-event-payload cap from the `EVENT_DATA_MAX_CHARS` env var,
 * falling back to EVENT_DATA_MAX_CHARS_DEFAULT for unset/invalid values.
 */
export function resolveEventDataMaxChars(): number {
	const env = process.env.EVENT_DATA_MAX_CHARS;
	if (env) {
		const n = parseInt(env, 10);
		if (!Number.isNaN(n) && n > 0) return n;
	}
	return EVENT_DATA_MAX_CHARS_DEFAULT;
}

/**
 * Resolve the per-text-block cap from the `TOOL_RESULT_MAX_CHARS` env var,
 * read lazily at call time so tests can override it without module reload.
 * Falls back to 16 000 when unset or invalid.
 */
export function resolveToolResultMaxChars(): number {
	const env = process.env.TOOL_RESULT_MAX_CHARS;
	if (env) {
		const n = parseInt(env, 10);
		if (!Number.isNaN(n) && n > 0) return n;
	}
	return TOOL_RESULT_MAX_CHARS_DEFAULT;
}


/**
 * Read one integer tunable with the semantics the six duplicated IIFEs had:
 * `parseInt` (so "12abc" is 12), and the raw value is used only when it is a
 * finite number greater than 0 — anything else (unset, "", "abc", "0", "-5")
 * falls back to the default.
 */
export function envInt(name: string, fallback: number): number {
	const env = process.env[name];
	if (env) {
		const n = parseInt(env, 10);
		if (!Number.isNaN(n) && n > 0) return n;
	}
	return fallback;
}

/** Base NACK backoff (ms) sent to the hub, which doubles it per attempt. */
export function resolveNakDelayMs(): number {
	return envInt("NAK_DELAY_MS", 60 * 1000);
}

/** Per-request timeout (ms) for task-messages POSTs. Node fetch has no default
 *  timeout; without this a stalled hub connection could delay ack indefinitely. */
export function resolvePostTimeoutMs(): number {
	return envInt("POST_TIMEOUT_MS", 10_000); // 10 seconds default
}

/** Maximum time (ms) to wait for a single LLM turn before giving up.
 *  Prevents tasks from hanging forever if the LLM stalls. */
export function resolveTurnTimeoutMs(): number {
	return envInt("TURN_TIMEOUT_MS", 10 * 60 * 1000); // 10 minutes default
}

/** Maximum time (ms) to wait for an aborted turn to settle before proceeding.
 *  Backstop only — after ctx.abort() the run should settle in milliseconds. */
export function resolveTurnSettleTimeoutMs(): number {
	return envInt("TURN_SETTLE_TIMEOUT_MS", 10 * 1000); // 10 seconds default
}


/** Resolve the configured LLM model from the environment: the canonical
 *  provider-neutral AINSEL_LLM_MODEL wins; the legacy OLLAMA_CLOUD_MODEL
 *  alias is accepted while the dual-write window is open (issue #96). */
export function resolveModelEnv(env: NodeJS.ProcessEnv = process.env): string {
	return env.AINSEL_LLM_MODEL?.trim() || env.OLLAMA_CLOUD_MODEL?.trim() || "";
}




export function requiredEnv(name: string): string {
	const v = process.env[name];
	if (!v) {
		logError(`${name} is required; ainsel-runner cannot start`);
		throw new Error(`${name} is required`);
	}
	return v;
}

/** Like requiredEnv(), but for the model contract: resolves AINSEL_LLM_MODEL
 *  with the legacy OLLAMA_CLOUD_MODEL alias (issue #96) and fails loudly
 *  when neither is set — there is no default model. */
export function requiredModel(): string {
	const v = resolveModelEnv();
	if (!v) {
		logError("AINSEL_LLM_MODEL is required; ainsel-runner cannot start");
		throw new Error("AINSEL_LLM_MODEL is required");
	}
	return v;
}

/**
 * resolveInternalToken returns the token sent as X-Internal-Token to the
 * hub's internal endpoints (task fetch, ack, nack). It is the platform-managed
 * HUB_INTERNAL_VALIDATE_SECRET, injected by the agent-operator from the
 * chart's shared auth.internalValidateSecret. The legacy AGENT_TOKEN name
 * was removed: it carried the same shared secret under a misleading
 * per-agent-sounding name and a mis-set value broke the claim path
 * (incident 2026-08-07).
 */
export function resolveInternalToken(env: NodeJS.ProcessEnv = process.env): string {
	const token = env.HUB_INTERNAL_VALIDATE_SECRET;
	if (!token) {
		throw new Error("HUB_INTERNAL_VALIDATE_SECRET is not set");
	}
	return token;
}
