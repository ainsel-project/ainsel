// Package mcpservers formats the MCP server definitions an agent connects to
// into the runtime env values the agent container expects.
package mcpservers

import (
	"fmt"
	"strings"

	ainselv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

// MissingEnvEntry pairs an MCP server name with the env-var name it
// references via tokenFromEnv when that env var is not defined on the
// AgentImage. The controller uses this to emit a Warning Event and set
// a Degraded condition on the Agent status.
type MissingEnvEntry struct {
	ServerName string
	EnvVarName string
}

// EnvValue formats the discovered entries into the MCP_SERVERS env value
// format the agent runtime expects: comma-separated "name=url".
func EnvValue(entries []string) string {
	return strings.Join(entries, ",")
}

// DedupeEntries removes duplicate "name=url" entries, keeping the first
// occurrence of each server name. The controller builds MCP_SERVERS from
// several sources (the agent's or its profile's MCP servers, sidecar
// declarations, and the injected chat sidecar); an MCP declared twice would
// otherwise reach the runtime twice and be connected/registered twice.
func DedupeEntries(entries []string) []string {
	seen := make(map[string]bool, len(entries))
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e
		if i := strings.Index(e, "="); i > 0 {
			name = e[:i]
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, e)
	}
	return out
}

// TokenEnvValue builds the MCP_SERVER_TOKENS env-var value as a
// comma-separated "name=$(VAR)" string. Each server whose TokenFromEnv is
// set yields an entry that Kubernetes resolves at container start by
// substituting the env var named TokenFromEnv. Callers must guarantee
// that any referenced env var is defined earlier in the same container's
// env: list (envFrom-loaded vars are not eligible for $(VAR) substitution).
// envNames is the set of env-var names already present on the container; any
// server whose TokenFromEnv is not in that set is skipped and its details
// are returned in `missingEnv` so the caller can surface a Degraded
// condition and emit a Warning Event.
func TokenEnvValue(servers []ainselv1alpha1.AgentImageMCPServer, envNames map[string]bool) (value string, missingEnv []MissingEnvEntry) {
	var parts []string
	for _, s := range servers {
		if s.TokenFromEnv == "" {
			continue
		}
		if !envNames[s.TokenFromEnv] {
			missingEnv = append(missingEnv, MissingEnvEntry{
				ServerName: s.Name,
				EnvVarName: s.TokenFromEnv,
			})
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=$(%s)", s.Name, s.TokenFromEnv))
	}
	return strings.Join(parts, ","), missingEnv
}

// PluginTokenEnv is the platform-injected env var that plugin MCP servers in
// the hub registry use as their bearer: the hub's agent endpoint for plugins
// authenticates the runner with it.
const PluginTokenEnv = "HUB_INTERNAL_VALIDATE_SECRET" // #nosec G101 -- an env var name, not a credential

// IsHubPluginURL reports whether url is one of the hub's internal plugin
// endpoints (<hubURL>/api/internal/plugins/...). Only such servers may
// reference PluginTokenEnv: handing the platform secret to any other URL
// would let a user-registered server collect it.
func IsHubPluginURL(url, hubURL string) bool {
	hubURL = strings.TrimSuffix(hubURL, "/")
	if hubURL == "" {
		return false
	}
	return strings.HasPrefix(url, hubURL+"/api/internal/plugins/")
}

// TokenEnvValueWithPlatform is TokenEnvValue plus one platform exception:
// a server pointing at the hub's internal plugin endpoints may use
// PluginTokenEnv even though it is not an AgentImage env var (the operator
// injects it on every agent container).
func TokenEnvValueWithPlatform(servers []ainselv1alpha1.AgentImageMCPServer, envNames map[string]bool, hubURL string) (string, []MissingEnvEntry) {
	var parts []string
	var missing []MissingEnvEntry
	for _, s := range servers {
		if s.TokenFromEnv == PluginTokenEnv && IsHubPluginURL(s.URL, hubURL) {
			parts = append(parts, fmt.Sprintf("%s=$(%s)", s.Name, s.TokenFromEnv))
			continue
		}
		v, m := TokenEnvValue([]ainselv1alpha1.AgentImageMCPServer{s}, envNames)
		if v != "" {
			parts = append(parts, v)
		}
		missing = append(missing, m...)
	}
	return strings.Join(parts, ","), missing
}
