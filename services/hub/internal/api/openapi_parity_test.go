package api

// openapi_parity_test.go keeps `openapi/hub.yaml` and the router from drifting
// apart in either direction.
//
// The spec is a second, hand-maintained description of the same route surface the
// mux serves, and a description nobody checks is a description that rots. Two
// directions are pinned:
//
//   - every pattern the server registers has a path item in the spec, so a new
//     route cannot ship undocumented;
//   - every path item in the spec is served by a registered pattern, so the spec
//     cannot advertise a route the hub does not have;
//   - every operation declares its MCP exposure (`x-mcp-tool` or
//     `x-mcp-not-exposed`), so a new hub route cannot silently miss its MCP
//     tool — see TestOpenAPIOperationsDeclareMCPExposure.
//
// The comparison is on paths, not operations. The mux registers most routes as
// trailing-slash prefixes and dispatches the method inside the handler, so a
// prefix pattern is satisfied by any spec path under it, and a spec path template
// (`/api/v1/agents/{name}`) is served by the prefix pattern (`/api/v1/agents/`).
// Where a pattern does carry a method — the end state issue #265 converts the
// groups to — that method must appear on the matching path item.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// specDoc is the slice of the OpenAPI document the parity check reads. The rest
// (components, responses, security schemes) is prose for humans and clients; a
// schema error there is not a route-surface error.
type specDoc struct {
	Paths map[string]specPathItem `yaml:"paths"`
}

// specPathItem is one path item's operation keys. OpenAPI method keys are
// lowercase; anything else in a path item is not an operation and is ignored.
type specPathItem struct {
	Get     interface{} `yaml:"get"`
	Put     interface{} `yaml:"put"`
	Post    interface{} `yaml:"post"`
	Delete  interface{} `yaml:"delete"`
	Patch   interface{} `yaml:"patch"`
	Head    interface{} `yaml:"head"`
	Options interface{} `yaml:"options"`
	Trace   interface{} `yaml:"trace"`
}

func (p specPathItem) methods() []string {
	out := make([]string, 0, 8)
	for _, m := range []struct {
		name string
		set  interface{}
	}{
		{"GET", p.Get}, {"PUT", p.Put}, {"POST", p.Post}, {"DELETE", p.Delete},
		{"PATCH", p.Patch}, {"HEAD", p.Head}, {"OPTIONS", p.Options}, {"TRACE", p.Trace},
	} {
		if m.set != nil {
			out = append(out, m.name)
		}
	}
	return out
}

// readOpenAPISpec returns the raw bytes of the repository's OpenAPI document. The
// test runs from services/hub/internal/api, so the spec is not a fixed relative
// path — walk up to find it rather than assume a working directory.
func readOpenAPISpec(t *testing.T) []byte {
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
	// #nosec G304 -- the path is always the repo's openapi/hub.yaml, found
	// by walking up from the test working directory; it is not user input.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}

// loadOpenAPISpec reads and parses the repository's OpenAPI document.
func loadOpenAPISpec(t *testing.T) specDoc {
	t.Helper()

	var doc specDoc
	if err := yaml.Unmarshal(readOpenAPISpec(t), &doc); err != nil {
		t.Fatalf("parse openapi/hub.yaml: %v", err)
	}
	if len(doc.Paths) == 0 {
		t.Fatal("openapi/hub.yaml declares no paths")
	}
	return doc
}

// splitPattern splits a registered mux pattern into its method and path. A
// methodless pattern (still the majority, and the shape the route inventory
// snapshot records) has an empty method.
func splitPattern(pattern string) (method, path string) {
	if i := strings.IndexByte(pattern, ' '); i >= 0 {
		return strings.ToUpper(pattern[:i]), pattern[i+1:]
	}
	return "", pattern
}

// isPrefixPattern reports whether a mux path matches by prefix. In ServeMux a
// trailing slash means "this subtree", which is how every route group that
// dispatches by hand is mounted.
func isPrefixPattern(path string) bool {
	return strings.HasSuffix(path, "/")
}

// specCoversPrefix reports whether any spec path item lives under a mux prefix
// pattern. The comparison is literal: the prefix `/api/internal/agents/` is
// covered by the template `/api/internal/agents/{name}/tasks/{id}`, because that
// template only ever expands to a path under the prefix.
func specCoversPrefix(paths map[string]specPathItem, prefix string) (string, bool) {
	for p := range paths {
		if strings.HasPrefix(p, prefix) {
			return p, true
		}
	}
	return "", false
}

// patternServesSpecPath reports whether a registered pattern serves a concrete
// path the spec names. An exact pattern must name the same path; a prefix pattern
// serves any path under it, so a spec template's `{param}` segments are treated
// as one path segment each.
func patternServesSpecPath(pattern, specPath string) bool {
	_, path := splitPattern(pattern)
	if isPrefixPattern(path) {
		return strings.HasPrefix(specPath, path)
	}
	return path == specPath
}

// TestOpenAPIParityWithRegisteredRoutes is the drift check in both directions.
//
// It uses the fully-wired server from the route inventory test — New(...) plus
// the persona group and the channel routes that SetChannelService mounts — so the
// compared set is the whole route surface, including the groups a bare New(...)
// leaves off when its service is nil.
func TestOpenAPIParityWithRegisteredRoutes(t *testing.T) {
	spec := loadOpenAPISpec(t)
	patterns := fullRouteServer(t).routePatterns()
	if len(patterns) == 0 {
		t.Fatal("no registered patterns to compare — the recorder is not wired")
	}

	// --- registered routes must be documented ---------------------------------
	var undocumented []string
	for _, pattern := range patterns {
		_, path := splitPattern(pattern)
		if isPrefixPattern(path) {
			if _, ok := specCoversPrefix(spec.Paths, path); !ok {
				undocumented = append(undocumented, pattern+" (no spec path under this prefix)")
			}
			continue
		}
		if _, ok := spec.Paths[path]; !ok {
			undocumented = append(undocumented, pattern)
		}
	}
	if len(undocumented) > 0 {
		sort.Strings(undocumented)
		t.Errorf("%d registered route(s) are missing from openapi/hub.yaml — add the path item, or remove the route:\n  %s",
			len(undocumented), strings.Join(undocumented, "\n  "))
	}

	// --- documented routes must be registered ---------------------------------
	var unserved []string
	for specPath := range spec.Paths {
		if !strings.HasPrefix(specPath, "/") {
			t.Errorf("spec path %q is not root-relative", specPath)
			continue
		}
		served := false
		for _, pattern := range patterns {
			if patternServesSpecPath(pattern, specPath) {
				served = true
				break
			}
		}
		if !served {
			unserved = append(unserved, specPath)
		}
	}
	if len(unserved) > 0 {
		sort.Strings(unserved)
		t.Errorf("%d spec path(s) match no registered route — the spec advertises routes the hub does not serve:\n  %s",
			len(unserved), strings.Join(unserved, "\n  "))
	}

	// --- methods, where the router declares them ------------------------------
	//
	// While a route group is still methodless the mux answers every method and
	// the handler dispatches, so there is nothing to compare. Once a pattern
	// names a method, that method must be an operation on the path item the
	// pattern maps to.
	var missingOps []string
	for _, pattern := range patterns {
		method, path := splitPattern(pattern)
		if method == "" || isPrefixPattern(path) {
			continue
		}
		item, ok := spec.Paths[path]
		if !ok {
			continue // already reported as undocumented
		}
		found := false
		for _, m := range item.methods() {
			if m == method {
				found = true
				break
			}
		}
		if !found {
			missingOps = append(missingOps, pattern)
		}
	}
	if len(missingOps) > 0 {
		sort.Strings(missingOps)
		t.Errorf("%d method-scoped route(s) have no matching operation in openapi/hub.yaml:\n  %s",
			len(missingOps), strings.Join(missingOps, "\n  "))
	}
}

// TestOpenAPISpecPathsAreWellFormed guards the shape the parity comparison
// assumes. A path item whose parameter segment is malformed would silently match
// no pattern, and the check above would report it as an unserved route with no
// hint as to why.
func TestOpenAPISpecPathsAreWellFormed(t *testing.T) {
	spec := loadOpenAPISpec(t)
	segment := regexp.MustCompile(`^\{[A-Za-z][A-Za-z0-9_]*\}$`)

	for path := range spec.Paths {
		if !strings.HasPrefix(path, "/") {
			t.Errorf("spec path %q must start with /", path)
		}
		if strings.HasSuffix(path, "/") && path != "/" {
			t.Errorf("spec path %q has a trailing slash; a path item names one path, not a subtree", path)
		}
		if strings.Contains(path, "//") {
			t.Errorf("spec path %q contains an empty segment", path)
		}
		for _, seg := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
			if len(seg) > 0 && seg[0] == '{' && !segment.MatchString(seg) {
				t.Errorf("spec path %q has a malformed parameter segment %q", path, seg)
			}
		}
		if len(spec.Paths[path].methods()) == 0 {
			t.Errorf("spec path %q declares no operations", path)
		}
	}
}

// TestOpenAPISpecRefsResolve keeps the document internally consistent. A `$ref` to
// a schema nobody defines is not a documentation nit — a generated client emits a
// type that does not exist, and a hand-written one silently reads nothing. The
// reverse direction is pinned too: a schema no operation reaches is a schema
// nobody maintains, and the next reader cannot tell it from a live one.
func TestOpenAPISpecRefsResolve(t *testing.T) {
	raw := readOpenAPISpec(t)
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	if len(root.Content) != 1 {
		t.Fatalf("spec has %d document nodes, want 1", len(root.Content))
	}
	doc := root.Content[0]

	var refs, defined []string
	var walk func(n *yaml.Node, path string)
	walk = func(n *yaml.Node, path string) {
		if n.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				if k.Value == "$ref" && v.Kind == yaml.ScalarNode {
					refs = append(refs, v.Value)
				}
				walk(v, path+"/"+k.Value)
			}
			return
		}
		for _, c := range n.Content {
			walk(c, path)
		}
	}
	walk(doc, "")

	// Every name defined under components/<section> is a valid ref target.
	// securitySchemes are named by `security:` requirements rather than by `$ref`,
	// so they are checked as targets but never as orphans.
	for _, section := range []string{"schemas", "responses", "parameters", "securitySchemes", "requestBodies"} {
		node := doc
		for _, seg := range []string{"components", section} {
			node = mappingValue(node, seg)
			if node == nil {
				break
			}
		}
		if node == nil {
			continue
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			defined = append(defined, "#/components/"+section+"/"+node.Content[i].Value)
		}
	}

	isRef := map[string]bool{}
	for _, r := range defined {
		isRef[r] = true
	}

	var dangling []string
	for _, r := range refs {
		if !strings.HasPrefix(r, "#/") {
			dangling = append(dangling, r+" (not a local reference)")
			continue
		}
		if !isRef[r] {
			dangling = append(dangling, r)
		}
	}
	sort.Strings(dangling)
	if len(dangling) > 0 {
		t.Errorf("spec has %d unresolvable $ref(s) — define the target or drop the reference:\n  %s",
			len(dangling), strings.Join(dangling, "\n  "))
	}

	// Reachability: a component is reachable if some other node references it.
	referenced := map[string]bool{}
	for _, r := range refs {
		referenced[r] = true
	}
	var orphaned []string
	for _, d := range defined {
		if strings.HasPrefix(d, "#/components/securitySchemes/") {
			continue
		}
		if !referenced[d] {
			orphaned = append(orphaned, d)
		}
	}
	sort.Strings(orphaned)
	if len(orphaned) > 0 {
		t.Errorf("spec defines %d component(s) no operation references — wire them up or delete them:\n  %s",
			len(orphaned), strings.Join(orphaned, "\n  "))
	}
}

// mappingValue returns the value node for key in a mapping node, or nil.
func mappingValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// mcpExposureDoc is the slice of the spec the MCP-exposure check reads: the
// two x-mcp-* keys on each operation. Path items are taken by method name;
// non-operation keys (parameters, servers, ...) sit in fields the struct
// ignores.
type mcpExposureDoc struct {
	Paths map[string]mcpExposureItem `yaml:"paths"`
}

type mcpExposureItem struct {
	Get     *mcpExposureOp `yaml:"get"`
	Put     *mcpExposureOp `yaml:"put"`
	Post    *mcpExposureOp `yaml:"post"`
	Delete  *mcpExposureOp `yaml:"delete"`
	Patch   *mcpExposureOp `yaml:"patch"`
	Head    *mcpExposureOp `yaml:"head"`
	Options *mcpExposureOp `yaml:"options"`
	Trace   *mcpExposureOp `yaml:"trace"`
}

func (i mcpExposureItem) ops() []struct {
	method string
	op     *mcpExposureOp
} {
	out := make([]struct {
		method string
		op     *mcpExposureOp
	}, 0, 8)
	for _, e := range []struct {
		method string
		op     *mcpExposureOp
	}{{"GET", i.Get}, {"PUT", i.Put}, {"POST", i.Post}, {"DELETE", i.Delete},
		{"PATCH", i.Patch}, {"HEAD", i.Head}, {"OPTIONS", i.Options}, {"TRACE", i.Trace}} {
		if e.op != nil {
			out = append(out, e)
		}
	}
	return out
}

// mcpExposureOp is one operation's MCP-exposure declaration. Exactly one of
// the two fields may be set. Tool is either a tool name or a list of names
// (one route served by several tools, e.g. pause/resume or the two metrics
// tools over metrics/query); NotExposed is the reason a route has no tool.
type mcpExposureOp struct {
	Tool       interface{} `yaml:"x-mcp-tool"`
	NotExposed *string     `yaml:"x-mcp-not-exposed"`
}

// TestOpenAPIOperationsDeclareMCPExposure is the hub half of the route-to-tool
// parity gate from issue #326 (Decision 7): every operation in the spec must
// state how MCP reaches it, either by naming its tool(s) with `x-mcp-tool` or
// by recording why it is deliberately not exposed with `x-mcp-not-exposed`.
//
// The gate exists because the hub registers most routes as path-only patterns
// and dispatches methods inside handlers, so the router cannot tell an MCP
// tool off a route — the spec is the only place method-level coverage is
// expressible. The MCP side (services/mcp/internal/mcp/parity_test.go) checks
// that named tools are actually registered and that no registered tool is
// missing from the spec; this test only enforces that every operation makes a
// declaration, so the next new hub route must either name its tool or name
// its reason.
func TestOpenAPIOperationsDeclareMCPExposure(t *testing.T) {
	var doc mcpExposureDoc
	if err := yaml.Unmarshal(readOpenAPISpec(t), &doc); err != nil {
		t.Fatalf("parse openapi/hub.yaml: %v", err)
	}

	var problems []string
	checked := 0
	for path, item := range doc.Paths {
		for _, e := range item.ops() {
			op := e.op
			checked++
			at := e.method + " " + path
			hasTool := op.Tool != nil
			hasReason := op.NotExposed != nil && *op.NotExposed != ""
			switch {
			case hasTool && hasReason:
				problems = append(problems, at+" declares both x-mcp-tool and x-mcp-not-exposed")
			case !hasTool && !hasReason:
				problems = append(problems, at+" declares neither x-mcp-tool nor a non-empty x-mcp-not-exposed")
			case hasTool:
				switch v := op.Tool.(type) {
				case string:
					if v == "" {
						problems = append(problems, at+" has an empty x-mcp-tool")
					}
				case []interface{}:
					if len(v) == 0 {
						problems = append(problems, at+" has an empty x-mcp-tool list")
					}
					for _, e := range v {
						if s, ok := e.(string); !ok || s == "" {
							problems = append(problems, at+" has a non-string or empty x-mcp-tool list entry")
						}
					}
				default:
					problems = append(problems, at+" has an x-mcp-tool that is not a string or a list of strings")
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no operations found to check — the exposure gate would pass vacuously")
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Errorf("%d operation(s) in openapi/hub.yaml do not declare their MCP exposure — add x-mcp-tool: <tool_name> (or a list) or x-mcp-not-exposed: \"<reason>\":\n  %s",
			len(problems), strings.Join(problems, "\n  "))
	}
}

// TestOpenAPISpecCoversInternalRoutes pins that the internal surface is documented
// at all. The route inventory test keeps the internal-token table honest against
// the router; this keeps the spec from documenting only the public half and
// leaving an internal route to be discovered by whoever calls it next.
func TestOpenAPISpecCoversInternalRoutes(t *testing.T) {
	spec := loadOpenAPISpec(t)
	patterns := fullRouteServer(t).routePatterns()

	var registered []string
	for _, pattern := range patterns {
		_, path := splitPattern(pattern)
		if strings.HasPrefix(path, "/api/internal/") {
			registered = append(registered, pattern)
		}
	}
	if len(registered) == 0 {
		t.Fatal("no /api/internal/* patterns are registered — the server under test is not fully wired")
	}

	var missing []string
	for _, pattern := range registered {
		_, path := splitPattern(pattern)
		if isPrefixPattern(path) {
			if _, ok := specCoversPrefix(spec.Paths, path); !ok {
				missing = append(missing, pattern)
			}
			continue
		}
		if _, ok := spec.Paths[path]; !ok {
			missing = append(missing, pattern)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("internal route(s) missing from openapi/hub.yaml (tagged `internal`, secured by X-Internal-Token):\n  %s",
			strings.Join(missing, "\n  "))
	}
}
