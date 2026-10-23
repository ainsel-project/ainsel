package api

import (
	"path"
	"strings"
)

// routeRule defines a route pattern and its required permissions.
type routeRule struct {
	prefix      string // path prefix to match
	readPerm    string // permission required for GET/HEAD
	writePerm   string // permission required for write methods
	alwaysAllow bool   // if true, no permission needed
}

// routeRules is an ordered list of route rules. First match wins.
var routeRules = []routeRule{
	// Plugin context: always allowed
	{prefix: "/api/v1/plugin-context", readPerm: "", writePerm: "", alwaysAllow: true},

	// Agents
	{prefix: "/api/v1/agents", readPerm: "agents:read", writePerm: "agents:write"},

	// Agent images
	{prefix: "/api/v1/agent-images", readPerm: "agent-images:read", writePerm: "agent-images:write"},

	// Connectors
	{prefix: "/api/v1/connectors", readPerm: "connectors:read", writePerm: "connectors:write"},

	// Triggers (both regular and cron)
	{prefix: "/api/v1/triggers", readPerm: "triggers:read", writePerm: "triggers:write"},
	{prefix: "/api/v1/cron-triggers", readPerm: "triggers:read", writePerm: "triggers:write"},

	// MCP servers
	{prefix: "/api/v1/mcp-servers", readPerm: "mcp-servers:read", writePerm: "mcp-servers:write"},

	// Personas
	{prefix: "/api/v1/personas", readPerm: "personas:read", writePerm: "personas:write"},

	// Skills
	{prefix: "/api/v1/skills", readPerm: "skills:read", writePerm: "skills:write"},

	// Invocations, events, errors (read-only)
	{prefix: "/api/v1/invocations", readPerm: "invocations:read", writePerm: ""},
	{prefix: "/api/v1/events", readPerm: "invocations:read", writePerm: ""},
	{prefix: "/api/v1/errors", readPerm: "invocations:read", writePerm: ""},

	// Observability, stats, queue (read-only)
	{prefix: "/api/v1/observability", readPerm: "observability:read", writePerm: ""},
	{prefix: "/api/v1/stats", readPerm: "observability:read", writePerm: ""},
	{prefix: "/api/v1/queue", readPerm: "observability:read", writePerm: ""},
}

// RoutePermission returns the permission a plugin credential needs for this request.
// - perm is the required permission (empty if always allowed)
// - always is true if any plugin credential may call it (no permission check)
// - ok is false if the route is not available to plugin credentials
func RoutePermission(method, rawPath string) (perm string, always bool, ok bool) {
	// Reject anything path.Clean would rewrite ("..", ".", "//"); a single
	// trailing slash is fine.
	trimmed := strings.TrimSuffix(rawPath, "/")
	cleanPath := path.Clean(rawPath)
	if cleanPath != trimmed || !strings.HasPrefix(cleanPath, "/api/v1/") {
		return "", false, false
	}

	// Check if this is a read or write operation
	isRead := method == "GET" || method == "HEAD"

	// Find the first matching rule
	for _, rule := range routeRules {
		// Match: exact prefix or prefix + /
		if cleanPath == rule.prefix || strings.HasPrefix(cleanPath, rule.prefix+"/") {
			if rule.alwaysAllow {
				// plugin-context: reads, plus POST /visible only.
				if isRead || (method == "POST" && cleanPath == rule.prefix+"/visible") {
					return "", true, true
				}
				return "", false, false
			}

			if isRead {
				if rule.readPerm == "" {
					return "", false, false
				}
				return rule.readPerm, false, true
			}

			if rule.writePerm == "" {
				return "", false, false
			}
			return rule.writePerm, false, true
		}
	}

	// No matching rule means denied
	return "", false, false
}
