package mcp

// parity_test.go keeps `openapi/hub.yaml`'s MCP markers and the tool set this
// package registers from drifting apart, in both directions:
//
//   - every operation the spec marks `x-mcp-tool` names a tool New registers, so
//     a marker cannot advertise a tool the MCP server does not serve;
//   - every tool New registers appears in at least one `x-mcp-tool` marker, so a
//     tool cannot ship without a spec row that says which route it is the typed
//     surface for.
//
// The marker may name one tool or several: an aggregate tool (summarize_workflows
// over agents + triggers, get_agent_logs and query_logs over the log endpoint)
// shares its route's row with the plain list tool.
//
// The companion check in services/hub/internal/api/openapi_mcp_marker_test.go
// pins the other half of the gate: that every operation carries exactly one of
// `x-mcp-tool` and `x-mcp-not-exposed`. Together they mean a new hub route cannot
// merge without an explicit decision about its MCP surface.

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// specOperationsByPath parses the spec's paths into path → method → operation.
// Path items also carry non-operation keys (summary, parameters, servers); those
// are ignored by the method allowlist below.
func specOperationsByPath(t *testing.T) map[string]map[string]map[string]any {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	var path string
	for {
		candidate := filepath.Join(dir, "openapi", "hub.yaml")
		if _, err := os.Stat(candidate); err == nil {
			path = candidate
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("openapi/hub.yaml not found above %s", dir)
		}
		dir = parent
	}
	// #nosec G304 -- the path is always the repo's openapi/hub.yaml, found by
	// walking up from the test working directory; it is not user input.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse openapi/hub.yaml: %v", err)
	}
	if len(doc.Paths) == 0 {
		t.Fatal("openapi/hub.yaml declares no paths")
	}
	out := make(map[string]map[string]map[string]any, len(doc.Paths))
	for p, item := range doc.Paths {
		ops := map[string]map[string]any{}
		for method, op := range item {
			if !isHTTPMethod(method) {
				continue
			}
			fields, ok := op.(map[string]any)
			if !ok {
				t.Fatalf("%s %s: operation is not a mapping", strings.ToUpper(method), p)
			}
			ops[method] = fields
		}
		out[p] = ops
	}
	return out
}

func isHTTPMethod(key string) bool {
	switch key {
	case "get", "put", "post", "delete", "patch", "head", "options", "trace":
		return true
	}
	return false
}

// specMCPTools returns every tool name the spec marks, plus the "METHOD /path"
// rows that name it, so a failure can point at the row to fix.
func specMCPTools(t *testing.T) (names map[string]bool, rows map[string]string) {
	names = map[string]bool{}
	rows = map[string]string{}
	for p, ops := range specOperationsByPath(t) {
		for method, op := range ops {
			switch v := op["x-mcp-tool"].(type) {
			case string:
				if v == "" {
					t.Fatalf("%s %s: x-mcp-tool is empty", strings.ToUpper(method), p)
				}
				names[v] = true
				rows[v] = method + " " + p
			case []any:
				if len(v) == 0 {
					t.Fatalf("%s %s: x-mcp-tool list is empty", strings.ToUpper(method), p)
				}
				for _, item := range v {
					s, ok := item.(string)
					if !ok || s == "" {
						t.Fatalf("%s %s: x-mcp-tool list entry is not a non-empty string", strings.ToUpper(method), p)
					}
					names[s] = true
					rows[s] = method + " " + p
				}
			}
		}
	}
	return names, rows
}

func registeredToolNames(t *testing.T) []string {
	t.Helper()
	s := New(slog.New(slog.NewTextHandler(io.Discard, nil)), Backends{HubURL: "http://hub.invalid"})
	out := make([]string, 0, len(s.ListTools()))
	for name := range s.ListTools() {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func TestEverySpecMarkedToolIsRegistered(t *testing.T) {
	marked, rows := specMCPTools(t)
	if len(marked) == 0 {
		t.Fatal("openapi/hub.yaml marks no x-mcp-tool operations")
	}
	registered := map[string]bool{}
	for _, name := range registeredToolNames(t) {
		registered[name] = true
	}
	var missing []string
	for name := range marked {
		if !registered[name] {
			missing = append(missing, name+" (marked on "+rows[name]+")")
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("spec marks tools the MCP server does not register:\n  %s", strings.Join(missing, "\n  "))
	}
}

func TestEveryRegisteredToolIsMarkedInTheSpec(t *testing.T) {
	marked, _ := specMCPTools(t)
	var orphans []string
	for _, name := range registeredToolNames(t) {
		if !marked[name] {
			orphans = append(orphans, name)
		}
	}
	if len(orphans) > 0 {
		t.Fatalf("registered tools with no x-mcp-tool row in openapi/hub.yaml:\n  %s", strings.Join(orphans, "\n  "))
	}
}
