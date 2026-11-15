/**
 * Attribution headers for calls to AInsel's own agent-MCP endpoints.
 *
 * The hub proxies `/api/internal/plugins/<id>/agent-mcp` to the plugin's own
 * MCP server, and serves the platform's read-only tool plane at
 * `/api/internal/agent-mcp`. Both attribute the call by agent name (required)
 * and invocation id (optional). These headers must only ever be sent to those
 * endpoints — never to third-party MCP servers — so they are derived from the
 * URL path and the runner-managed env vars, and are empty for every other URL.
 */

const PLUGIN_PATH_PREFIX = "/api/internal/plugins/";

/**
 * The platform read plane, matched exactly rather than by prefix: the hub
 * serves one path there, and a prefix match would attribute a call to a path
 * the hub does not answer.
 */
const PLATFORM_AGENT_PATH = "/api/internal/agent-mcp";

export function pluginHeaders(
	url: string,
	env: NodeJS.ProcessEnv,
): Record<string, string> {
	let pathname: string;
	try {
		pathname = new URL(url).pathname;
	} catch {
		return {};
	}
	if (!pathname.startsWith(PLUGIN_PATH_PREFIX) && pathname !== PLATFORM_AGENT_PATH) {
		return {};
	}
	const headers: Record<string, string> = {};
	const agent = env.AGENT_NAME;
	if (agent) {
		headers["X-Ainsel-Agent"] = agent;
	}
	const invocationId = env.AINSEL_INVOCATION_ID;
	if (invocationId) {
		headers["X-Ainsel-Invocation-Id"] = invocationId;
	}
	return headers;
}
