package api

// openapi_mcp_marker_test.go is the MCP-surface half of the spec gate.
//
// Every operation in `openapi/hub.yaml` must state, in the spec itself, whether
// the MCP server exposes it: exactly one of
//
//   - `x-mcp-tool: <name>` (or a list of names, for a route several tools share —
//     an aggregate tool and the plain list tool over the same endpoint), or
//   - `x-mcp-not-exposed: "<reason>"`.
//
// That is the parity mechanic issue #326 decided on and issue #329 lands: a new
// hub route cannot merge as an undocumented gap, because the mux-vs-spec check in
// openapi_parity_test.go will find the route and this check will demand a marker
// for it. The reason string is where a deliberate non-exposure is recorded — a
// deferred surface says which issue picks it up.
//
// An aggregate tool reads several routes, so it may be named by several rows;
// what must stay unique is the marker key on a row, not the name across rows.
//
// The other half lives in services/mcp/internal/mcp/parity_test.go, which checks
// the marked names against the tools the MCP server actually registers.

import (
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// mcpMarkerMethods are the OpenAPI operation keys. A path item's other keys
// (summary, parameters, servers) are not operations.
var mcpMarkerMethods = []string{"get", "put", "post", "delete", "patch", "head", "options", "trace"}

// mcpToolNamePattern is the MCP tool naming convention: snake_case, verb first
// (list_/get_/create_/update_/delete_ and the few named actions).
var mcpToolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

type mcpMarkerDoc struct {
	Paths map[string]map[string]any `yaml:"paths"`
}

func loadMCPMarkerSpec(t *testing.T) mcpMarkerDoc {
	t.Helper()

	var doc mcpMarkerDoc
	if err := yaml.Unmarshal(readOpenAPISpec(t), &doc); err != nil {
		t.Fatalf("parse openapi/hub.yaml: %v", err)
	}
	if len(doc.Paths) == 0 {
		t.Fatal("openapi/hub.yaml declares no paths")
	}
	return doc
}

// TestEveryOperationDeclaresItsMCPSurface fails when an operation carries
// neither marker, both markers, or a marker whose value is empty or malformed.
func TestEveryOperationDeclaresItsMCPSurface(t *testing.T) {
	doc := loadMCPMarkerSpec(t)

	operations := 0
	for path, item := range doc.Paths {
		for _, method := range mcpMarkerMethods {
			raw, ok := item[method]
			if !ok {
				continue
			}
			op, ok := raw.(map[string]any)
			if !ok {
				t.Errorf("%s %s: operation is not a mapping", strings.ToUpper(method), path)
				continue
			}
			operations++
			label := strings.ToUpper(method) + " " + path

			tool, hasTool := op["x-mcp-tool"]
			reason, hasReason := op["x-mcp-not-exposed"]
			switch {
			case hasTool && hasReason:
				t.Errorf("%s: declares both x-mcp-tool and x-mcp-not-exposed", label)
			case !hasTool && !hasReason:
				t.Errorf("%s: declares neither x-mcp-tool nor x-mcp-not-exposed — every operation is either an MCP tool or a recorded non-exposure", label)
			case hasTool:
				for _, name := range markerStringList(t, label, "x-mcp-tool", tool) {
					if !mcpToolNamePattern.MatchString(name) {
						t.Errorf("%s: x-mcp-tool %q is not a snake_case tool name", label, name)
					}
				}
			default:
				s, ok := reason.(string)
				if !ok || strings.TrimSpace(s) == "" {
					t.Errorf("%s: x-mcp-not-exposed must be a non-empty string stating why", label)
				}
			}
		}
	}
	if operations < 100 {
		t.Fatalf("only %d operations found — the spec parse is not reading paths correctly", operations)
	}
}

// markerStringList reads an x-mcp-tool value, which is either a single tool name
// or a list of them.
func markerStringList(t *testing.T, label, key string, v any) []string {
	t.Helper()

	switch val := v.(type) {
	case string:
		if strings.TrimSpace(val) == "" {
			t.Errorf("%s: %s is empty", label, key)
			return nil
		}
		return []string{val}
	case []any:
		if len(val) == 0 {
			t.Errorf("%s: %s list is empty", label, key)
			return nil
		}
		out := make([]string, 0, len(val))
		for _, item := range val {
			s, ok := item.(string)
			if !ok || strings.TrimSpace(s) == "" {
				t.Errorf("%s: %s entry is not a non-empty string", label, key)
				continue
			}
			out = append(out, s)
		}
		return out
	default:
		t.Errorf("%s: %s must be a string or a list of strings", label, key)
		return nil
	}
}
