package api

import (
	"slices"
	"testing"

	"github.com/ainsel-project/ainsel/shared/api/plugin"
)

// TestRoutePermission_Mappings tests the route to permission mappings.
func TestRoutePermission_Mappings(t *testing.T) {
	tests := []struct {
		method      string
		path        string
		expectPerm  string
		expectAlway bool
		expectOK    bool
	}{
		// plugin-context: always allowed
		{"GET", "/api/v1/plugin-context", "", true, true},
		{"POST", "/api/v1/plugin-context/visible", "", true, true},

		// agents
		{"GET", "/api/v1/agents", "agents:read", false, true},
		{"POST", "/api/v1/agents", "agents:write", false, true},
		{"DELETE", "/api/v1/agents/x", "agents:write", false, true},
		{"GET", "/api/v1/agents/x", "agents:read", false, true},

		// agent-images
		{"GET", "/api/v1/agent-images", "agent-images:read", false, true},
		{"POST", "/api/v1/agent-images", "agent-images:write", false, true},

		// connectors
		{"GET", "/api/v1/connectors", "connectors:read", false, true},
		{"POST", "/api/v1/connectors", "connectors:write", false, true},

		// triggers (both regular and cron)
		{"GET", "/api/v1/triggers", "triggers:read", false, true},
		{"POST", "/api/v1/triggers", "triggers:write", false, true},
		{"GET", "/api/v1/cron-triggers", "triggers:read", false, true},
		{"POST", "/api/v1/cron-triggers", "triggers:write", false, true},

		// mcp-servers
		{"GET", "/api/v1/mcp-servers", "mcp-servers:read", false, true},
		{"POST", "/api/v1/mcp-servers", "mcp-servers:write", false, true},

		// personas
		{"GET", "/api/v1/personas", "personas:read", false, true},
		{"POST", "/api/v1/personas", "personas:write", false, true},

		// skills
		{"GET", "/api/v1/skills", "skills:read", false, true},
		{"POST", "/api/v1/skills", "skills:write", false, true},

		// invocations (read-only)
		{"GET", "/api/v1/invocations", "invocations:read", false, true},
		{"POST", "/api/v1/invocations", "", false, false},

		// events (read-only)
		{"GET", "/api/v1/events", "invocations:read", false, true},
		{"POST", "/api/v1/events", "", false, false},

		// errors (read-only)
		{"GET", "/api/v1/errors", "invocations:read", false, true},
		{"POST", "/api/v1/errors", "", false, false},

		// observability (read-only)
		{"GET", "/api/v1/observability", "observability:read", false, true},
		{"POST", "/api/v1/observability", "", false, false},

		// stats (read-only)
		{"GET", "/api/v1/stats", "observability:read", false, true},
		{"POST", "/api/v1/stats", "", false, false},

		// queue (read-only)
		{"GET", "/api/v1/queue", "observability:read", false, true},
		{"POST", "/api/v1/queue", "", false, false},

		// Denied routes
		{"GET", "/api/v1/plugins", "", false, false},
		{"GET", "/api/v1/users", "", false, false},
		{"GET", "/api/v1/groups", "", false, false},
		{"GET", "/api/v1/auth", "", false, false},
		{"GET", "/api/v1/tokens", "", false, false},
		{"GET", "/api/v1/user-tokens", "", false, false},
		{"GET", "/api/v1/chat", "", false, false},
		{"GET", "/api/v1/ws", "", false, false},
		{"GET", "/api/v1/me", "", false, false},
		{"GET", "/api/v1/audit", "", false, false},
		{"GET", "/api/v1/channels", "", false, false},
		{"GET", "/api/v1/platform", "", false, false},

		// Plugin tool calls are the operator surface: a plugin credential must
		// not be able to call tools back into the hub.
		{"POST", "/api/v1/plugin-tools/call", "", false, false},

		// Path traversal attempts - cleaned path differs
		{"GET", "/api/v1/agents/../users", "", false, false},
		{"GET", "/api/v1/agentsX", "", false, false}, // Not exact or with /

		// POST to plugin-context but not /visible
		{"POST", "/api/v1/plugin-context/other", "", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			perm, always, ok := RoutePermission(tt.method, tt.path)
			if perm != tt.expectPerm {
				t.Errorf("perm: got %s, want %s", perm, tt.expectPerm)
			}
			if always != tt.expectAlway {
				t.Errorf("always: got %v, want %v", always, tt.expectAlway)
			}
			if ok != tt.expectOK {
				t.Errorf("ok: got %v, want %v", ok, tt.expectOK)
			}
		})
	}
}

// TestRoutePermission_CoversEveryPermission ensures all permissions in the
// plugin vocabulary are returned by some route.
func TestRoutePermission_CoversEveryPermission(t *testing.T) {
	coveredPerms := make(map[string]bool)

	// Iterate through common methods and paths to find all permissions
	methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH"}
	paths := []string{
		"/api/v1/agents",
		"/api/v1/agent-images",
		"/api/v1/connectors",
		"/api/v1/triggers",
		"/api/v1/cron-triggers",
		"/api/v1/mcp-servers",
		"/api/v1/personas",
		"/api/v1/skills",
		"/api/v1/invocations",
		"/api/v1/events",
		"/api/v1/errors",
		"/api/v1/observability",
		"/api/v1/stats",
		"/api/v1/queue",
	}

	for _, method := range methods {
		for _, path := range paths {
			perm, always, ok := RoutePermission(method, path)
			if !ok {
				continue
			}
			if always || perm != "" {
				coveredPerms[perm] = true
			}
		}
	}

	// Check that all permissions are covered
	for _, p := range plugin.Permissions {
		if !coveredPerms[p] {
			t.Errorf("permission %s not covered by any route", p)
		}
	}

	// Check that no unknown permissions are returned
	knownPerms := make(map[string]bool)
	for _, p := range plugin.Permissions {
		knownPerms[p] = true
	}
	for p := range coveredPerms {
		if !knownPerms[p] && p != "" {
			t.Errorf("unknown permission returned: %s", p)
		}
	}
}

// TestRoutePermission_ValidateCoverage verifies that all known permissions
// are actually available in some route (comprehensive check).
func TestRoutePermission_ValidateCoverage(t *testing.T) {
	// Map of permission -> (method, path) pairs that should return it
	expectedRoutes := map[string][]struct {
		method string
		path   string
	}{
		"agents:read": {
			{"GET", "/api/v1/agents"},
			{"HEAD", "/api/v1/agents"},
		},
		"agents:write": {
			{"POST", "/api/v1/agents"},
			{"PUT", "/api/v1/agents/x"},
		},
		"agent-images:read": {
			{"GET", "/api/v1/agent-images"},
		},
		"agent-images:write": {
			{"POST", "/api/v1/agent-images"},
		},
		"connectors:read": {
			{"GET", "/api/v1/connectors"},
		},
		"connectors:write": {
			{"POST", "/api/v1/connectors"},
		},
		"triggers:read": {
			{"GET", "/api/v1/triggers"},
			{"GET", "/api/v1/cron-triggers"},
		},
		"triggers:write": {
			{"POST", "/api/v1/triggers"},
			{"POST", "/api/v1/cron-triggers"},
		},
		"mcp-servers:read": {
			{"GET", "/api/v1/mcp-servers"},
		},
		"mcp-servers:write": {
			{"POST", "/api/v1/mcp-servers"},
		},
		"personas:read": {
			{"GET", "/api/v1/personas"},
		},
		"personas:write": {
			{"POST", "/api/v1/personas"},
		},
		"skills:read": {
			{"GET", "/api/v1/skills"},
		},
		"skills:write": {
			{"POST", "/api/v1/skills"},
		},
		"invocations:read": {
			{"GET", "/api/v1/invocations"},
			{"GET", "/api/v1/events"},
			{"GET", "/api/v1/errors"},
		},
		"observability:read": {
			{"GET", "/api/v1/observability"},
			{"GET", "/api/v1/stats"},
			{"GET", "/api/v1/queue"},
		},
	}

	for perm, routes := range expectedRoutes {
		for _, route := range routes {
			returnedPerm, always, ok := RoutePermission(route.method, route.path)
			if !ok {
				t.Errorf("route %s %s not available", route.method, route.path)
				continue
			}
			if returnedPerm != perm {
				t.Errorf("route %s %s: got permission %s, want %s", route.method, route.path, returnedPerm, perm)
			}
			if always {
				t.Errorf("route %s %s should not be always-allow", route.method, route.path)
			}
		}
	}

	// Verify all plugin.Permissions are covered
	allCovered := make(map[string]bool)
	for _, p := range expectedRoutes {
		for _, route := range p {
			perm, _, ok := RoutePermission(route.method, route.path)
			if ok && perm != "" {
				allCovered[perm] = true
			}
		}
	}

	for _, perm := range plugin.Permissions {
		if !allCovered[perm] && !slices.Contains([]string{
			// These are write-only permissions that don't have dedicated routes
			// or are covered by implication
		}, perm) {
			// Every permission should be reachable by some route
			// If not, we might be missing a mapping
			_, found := expectedRoutes[perm]
			if !found {
				t.Logf("permission %s not explicitly tested", perm)
			}
		}
	}
}

func TestRoutePermission_PluginContextMethods(t *testing.T) {
	cases := []struct {
		method, path string
		always, ok   bool
	}{
		{"GET", "/api/v1/plugin-context", true, true},
		{"POST", "/api/v1/plugin-context/visible", true, true},
		{"DELETE", "/api/v1/plugin-context", false, false},
		{"PUT", "/api/v1/plugin-context/visible", false, false},
		{"POST", "/api/v1/plugin-context", false, false},
	}
	for _, c := range cases {
		_, always, ok := RoutePermission(c.method, c.path)
		if always != c.always || ok != c.ok {
			t.Errorf("%s %s: always=%v ok=%v, want %v %v", c.method, c.path, always, ok, c.always, c.ok)
		}
	}
	if perm, _, ok := RoutePermission("GET", "/api/v1/agents/"); !ok || perm != "agents:read" {
		t.Errorf("trailing slash: perm=%q ok=%v", perm, ok)
	}
	if _, _, ok := RoutePermission("GET", "/api/v1/agents//x"); ok {
		t.Error("double slash should be denied")
	}
}
