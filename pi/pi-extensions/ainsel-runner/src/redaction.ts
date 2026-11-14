// ---------------------------------------------------------------------------
// Secret redaction — defense-in-depth control that replaces every env value
// that could be a secret with "***" before transcript content is sent to the
// hub. Operates on known env values opaquely (no parsing/guessing token
// formats), so it fails closed: a new secret var is automatically covered.
// ---------------------------------------------------------------------------

/** Env var names that are known non-sensitive and must never be redacted. */
const REDACTION_DENYLIST = new Set([
	"AGENT_NAME",
	"PI_PROVIDER",
	"AINSEL_LLM_MODEL",
	"OLLAMA_CLOUD_MODEL",
	"AGENT_PERSONA_PATH",
	"HUB_URL",
	"HUB_ENABLED",
	"OLLAMA_CLOUD_MAX_TURNS",
	"AGENT_TOOLS",
	"MCP_SERVERS",
	"PATH",
	"HOME",
	"HOSTNAME",
	"PWD",
	"NAK_DELAY_MS",
	"TOOL_RESULT_MAX_CHARS",
	"EVENT_DATA_MAX_CHARS",
	"POST_TIMEOUT_MS",
	"TURN_TIMEOUT_MS",
	"TURN_SETTLE_TIMEOUT_MS",
	"NODE_ENV",
	"SHLVL",
	"TERM",
	"LANG",
	"LC_ALL",
]);

/** Env var name prefixes that are known non-sensitive (set per-task). */
const REDACTION_DENYLIST_PREFIXES = ["AINSEL_EVENT_"];

/** Minimum length for a value to be considered a potential secret. */
const MIN_SECRET_LENGTH = 8;

/** The memoized set of secret values to redact. Null until first use. */
let cachedSecrets: string[] | null = null;

/** Build the list of secret values from process.env (called once, memoized). */
function buildSecretList(env: Record<string, string | undefined>): string[] {
	const secrets: string[] = [];
	for (const [key, value] of Object.entries(env)) {
		if (value === undefined || value.length < MIN_SECRET_LENGTH) continue;
		if (REDACTION_DENYLIST.has(key)) continue;
		if (REDACTION_DENYLIST_PREFIXES.some((p) => key.startsWith(p))) continue;
		secrets.push(value);
	}
	// Deduplicate (multiple vars may share the same value).
	return [...new Set(secrets)];
}

/**
 * Replace all occurrences of known env secret values in `input` with `***`.
 * Handles both the raw value and its JSON-escaped form (e.g. a secret
 * containing `"` appears as `\"` inside a JSON string).
 *
 * Exported for unit testing. The secret set is memoized on first call;
 * use `resetRedactionCache()` in tests to re-derive after mutating env.
 */
export function redactSecrets(input: string): string {
	if (cachedSecrets === null) {
		cachedSecrets = buildSecretList(process.env as Record<string, string | undefined>);
	}
	let out = input;
	for (const secret of cachedSecrets) {
		out = out.replaceAll(secret, "***");
		// Also redact the JSON-escaped form (covers secrets with ", \, /, etc.)
		const escaped = JSON.stringify(secret).slice(1, -1);
		if (escaped !== secret) {
			out = out.replaceAll(escaped, "***");
		}
	}
	return out;
}

/** Reset the memoized secret cache. For tests only. */
export function resetRedactionCache(): void {
	cachedSecrets = null;
}
