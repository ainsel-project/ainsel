package mcp

// parity_test.go is the MCP half of the route-to-tool parity gate from issue
// #326 (Decision 7). The spec (openapi/hub.yaml) annotates every operation
// with either `x-mcp-tool: <tool_name>` (or a list of names, e.g. pause and
// resume over one subresource) or `x-mcp-not-exposed: "<reason>"`; the hub
// side (services/hub/internal/api/openapi_parity_test.go) fails any operation
// that declares neither. This test closes the loop from the tool side:
//
//   - every tool named in the spec must be registered by New — an annotation
//     may not advertise a tool the MCP server does not serve;
//   - every tool New registers must be named in the spec or appear in
//     builtinAllowlist — a composite tool that joins several routes is the
//     only legitimate reason a tool has no single spec operation.
//
// Method coverage note, stated plainly: the hub registers most routes as
// path-only patterns and dispatches the method inside the handler, so method
// coverage cannot be derived from the router. It is asserted from the spec —
// the same limitation the hub's route-table parity check records.
//
// The spec is read with a line parser, not a YAML library: services/mcp must
// not gain a module dependency for a test fixture (go.sum in this module does
// not even carry gopkg.in/yaml.v3 as a direct require). The parser relies on
// the spec's committed formatting (path items at 2-space indent, operations at
// 4, operation keys at 6). It fails loudly rather than silently passing if
// that shape drifts: finding no operations, or an operation without exactly
// one exposure key, is a test failure. The hub test parses the same file with
// a real YAML parser, so a spec that parses there but confuses this parser
// surfaces as a failure here, never as a green skip.

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// builtinAllowlist holds registered built-in tools that legitimately have no
// single spec operation because they join several routes. Keep this list
// small and deliberate; a new tool that is not in the spec and not here fails
// the parity check, which is the point.
var builtinAllowlist = map[string]bool{
	// summarize_workflows joins GET /api/v1/agents and GET /api/v1/triggers.
	"summarize_workflows": true,
	// summarize_agent_activity joins agents, invocations and the queue.
	"summarize_agent_activity": true,
}

// specOperation is one operation's MCP exposure as read by the line parser.
type specOperation struct {
	path   string // e.g. "/api/v1/agents/{name}"
	method string // e.g. "PUT"
	tools  []string
	reason string // non-empty when x-mcp-not-exposed is set
}

// readHubSpecBytes returns the repository's OpenAPI document, found by
// walking up from the working directory — the same strategy the hub's
// openapi_parity_test.go uses. A missing spec is a hard failure: this test
// must never skip, or the parity gate silently disappears when the file moves.
func readHubSpecBytes(t *testing.T) []byte {
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
			t.Fatalf("openapi/hub.yaml not found above %s — the parity gate must not skip", dir)
		}
		dir = parent
	}
	// #nosec G304 -- the path is always the repo's openapi/hub.yaml, found
	// by walking up from the test working directory; it is not user input.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}

// parseSpecOperations extracts the x-mcp-* exposure of every operation from
// the spec's indentation structure. It understands exactly the committed
// layout of openapi/hub.yaml:
//
//	"  /api/v1/agents:"          path item
//	"    put:"                   operation
//	"      x-mcp-tool: name"     single tool
//	"      x-mcp-tool:"          followed by "        - name" entries
//	"      x-mcp-not-exposed: \"reason\""
//
// Any other line is irrelevant to this check and ignored.
func parseSpecOperations(t *testing.T, raw []byte) []specOperation {
	t.Helper()

	var ops []specOperation
	path := ""
	inPaths := false // between the top-level `paths:` and the next top-level key
	var cur *specOperation
	// pendingList is set when the last operation key seen was `x-mcp-tool:` with
	// no inline value; the following `- name` entries at indent 8 are its list.
	pendingList := false
	flush := func() {
		if cur != nil {
			ops = append(ops, *cur)
			cur = nil
		}
		pendingList = false
	}

	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		switch {
		case indent == 0 && strings.HasSuffix(trimmed, ":"):
			flush()
			inPaths = strings.TrimSuffix(trimmed, ":") == "paths"
		case indent == 2 && inPaths && strings.HasSuffix(trimmed, ":") && strings.HasPrefix(trimmed, "/"):
			flush()
			path = strings.TrimSuffix(trimmed, ":")
		case indent == 4 && inPaths && strings.HasSuffix(trimmed, ":"):
			flush()
			method := strings.ToUpper(strings.TrimSuffix(trimmed, ":"))
			switch method {
			case "GET", "PUT", "POST", "DELETE", "PATCH", "HEAD", "OPTIONS", "TRACE":
				cur = &specOperation{path: path, method: method}
			}
		case indent == 6 && cur != nil && strings.HasPrefix(trimmed, "x-mcp-tool:"):
			pendingList = false
			if rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "x-mcp-tool:")); rest != "" {
				cur.tools = append(cur.tools, strings.Trim(rest, `"`))
			} else {
				pendingList = true
			}
		case indent == 6 && cur != nil && strings.HasPrefix(trimmed, "x-mcp-not-exposed:"):
			pendingList = false
			cur.reason = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "x-mcp-not-exposed:")), `"`)
		case indent == 8 && cur != nil && pendingList && strings.HasPrefix(trimmed, "- "):
			cur.tools = append(cur.tools, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
		case indent == 6 && cur != nil:
			// Any other operation-level key ends a pending x-mcp-tool list, so
			// list items under `parameters:` etc. are never read as tool names.
			pendingList = false
		case indent < 4:
			flush()
		}
	}
	flush()
	return ops
}

func TestMCPToolParityWithOpenAPISpec(t *testing.T) {
	ops := parseSpecOperations(t, readHubSpecBytes(t))
	if len(ops) == 0 {
		t.Fatal("parsed 0 operations from openapi/hub.yaml — the spec layout drifted and the parity gate would pass vacuously")
	}

	// Every operation must declare exactly one exposure. The hub test enforces
	// this too; repeating it here keeps this file's two assertions meaningful
	// on their own and catches a spec that names a tool *and* a reason.
	var misdeclared []string
	named := map[string]bool{}
	for _, op := range ops {
		switch {
		case len(op.tools) > 0 && op.reason != "":
			misdeclared = append(misdeclared, op.method+" "+op.path+" (both x-mcp-tool and x-mcp-not-exposed)")
		case len(op.tools) == 0 && op.reason == "":
			misdeclared = append(misdeclared, op.method+" "+op.path+" (neither x-mcp-tool nor x-mcp-not-exposed)")
		}
		for _, name := range op.tools {
			if name == "" {
				misdeclared = append(misdeclared, op.method+" "+op.path+" (empty x-mcp-tool entry)")
				continue
			}
			named[name] = true
		}
	}
	if len(misdeclared) > 0 {
		t.Fatalf("%d operation(s) in openapi/hub.yaml do not declare exactly one of x-mcp-tool / x-mcp-not-exposed:\n  %s",
			len(misdeclared), strings.Join(misdeclared, "\n  "))
	}

	registered := New(slog.New(slog.NewTextHandler(io.Discard, nil)), Backends{HubURL: "http://hub.invalid"}).ListTools()

	// --- spec -> server: a named tool must exist ------------------------------
	var missing []string
	for name := range named {
		if _, ok := registered[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d tool(s) named by x-mcp-tool in openapi/hub.yaml are not registered by mcp.New — land the tool or move the row back to x-mcp-not-exposed:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}

	// --- server -> spec: a registered tool must be named or allowlisted -------
	var unnamed []string
	for name := range registered {
		if !named[name] && !builtinAllowlist[name] {
			unnamed = append(unnamed, name)
		}
	}
	if len(unnamed) > 0 {
		t.Errorf("%d built-in tool(s) registered by mcp.New appear in no x-mcp-tool annotation and are not in builtinAllowlist — annotate the operation they serve:\n  %s",
			len(unnamed), strings.Join(unnamed, "\n  "))
	}
}
