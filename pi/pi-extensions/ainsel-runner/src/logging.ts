/**
 * The runner's structured log lines. JSON on stdout for `logInfo`, and an
 * `error_event` envelope on stderr for `logError` that the hub's observability
 * feed can parse. Leaf module: everything else logs through these two.
 */



export function logInfo(msg: string, extra: Record<string, unknown> = {}) {
	console.log(JSON.stringify({ level: "info", msg, ...extra }));
}
export function logError(msg: string, extra: Record<string, unknown> = {}) {
	console.error(
		JSON.stringify({
			log_type: "error_event",
			severity: "error",
			source: "agent",
			error_message: msg,
			...extra,
		}),
	);
}
