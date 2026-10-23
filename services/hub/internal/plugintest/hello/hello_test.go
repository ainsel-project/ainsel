package hello

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	plugin "github.com/ainsel-project/ainsel/shared/api/plugin"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// These tests are the contract of the hello fixture plugin: the manifest the
// hub installs from must parse and validate, every file the manifest points at
// must actually be served, the visibility lookup must call back into the hub
// with the caller's own credential, and the MCP tools must answer with the
// exact text the host's end-to-end test asserts.

const (
	// testCredential is the scoped credential the hub would stamp on a proxied
	// request (X-Ainsel-Credential) and the bearer the fixture must send back.
	testCredential = "ainselp_test"
	// testInvocationID is the invocation id the agent runtime sends on the MCP
	// request; the remember tool echoes it back.
	testInvocationID = "inv-7"
)

// newFixture starts the plugin behind an httptest server. hubURL is the address
// of the hub the plugin calls back into; pass "" when the test never reaches
// the hub.
func newFixture(t *testing.T, hubURL string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(New(hubURL))
	t.Cleanup(ts.Close)
	return ts
}

// fakeHub stands in for the AInsel hub: it records what the plugin sent to
// POST /api/v1/plugin-context/visible and answers with a fixed visible list.
type fakeHub struct {
	ts       *httptest.Server
	mu       sync.Mutex
	auth     []string
	paths    []string
	bodies   []string
	visible  []string
	status   int
	requests int
}

func newFakeHub(t *testing.T, visible []string) *fakeHub {
	t.Helper()
	h := &fakeHub{visible: visible, status: http.StatusOK}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("hub: read body: %v", err)
		}
		h.mu.Lock()
		h.requests++
		h.auth = append(h.auth, r.Header.Get("Authorization"))
		h.paths = append(h.paths, r.URL.Path)
		h.bodies = append(h.bodies, string(raw))
		status := h.status
		visible := h.visible
		h.mu.Unlock()

		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string][]string{"visible": visible})
	}))
	t.Cleanup(ts.Close)
	h.ts = ts
	return h
}

// ts is the hub's address, for passing to New.
func (h *fakeHub) url() string { return h.ts.URL }

// calls reports how many requests the plugin made to the hub, and the recorded
// Authorization headers, paths and raw bodies of those requests.
func (h *fakeHub) calls() (int, []string, []string, []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.requests, h.auth, h.paths, h.bodies
}

// failWith makes the hub answer every later request with status.
func (h *fakeHub) failWith(status int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status = status
}

// get issues a GET against the fixture and returns status, Content-Type and body.
func get(t *testing.T, ts *httptest.Server, path, credential string) (int, string, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if credential != "" {
		req.Header.Set("X-Ainsel-Credential", credential)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return res.StatusCode, res.Header.Get("Content-Type"), string(raw)
}

// --- manifest ---

func TestManifest_ParsesAndValidates(t *testing.T) {
	m, err := plugin.Parse(Manifest())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if m.ID != "hello" {
		t.Errorf("id: got %q, want %q", m.ID, "hello")
	}
	if m.DisplayName != "Hello" {
		t.Errorf("displayName: got %q, want %q", m.DisplayName, "Hello")
	}
	if m.Version != "1.0.0" {
		t.Errorf("version: got %q, want %q", m.Version, "1.0.0")
	}
	if m.PluginAPI != "1.1" {
		t.Errorf("pluginApi: got %q, want %q", m.PluginAPI, "1.1")
	}
	if m.Description != "Fixture plugin exercising the AInsel plugin contract" {
		t.Errorf("description: got %q, want %q", m.Description, "Fixture plugin exercising the AInsel plugin contract")
	}

	// Compatibility with the host's plugin API is what makes the hub install it.
	ok, err := plugin.Compatible(m.PluginAPI, plugin.HostAPIVersion)
	if err != nil {
		t.Fatalf("Compatible: %v", err)
	}
	if !ok {
		t.Errorf("Compatible(%q, %q): got false, want true", m.PluginAPI, plugin.HostAPIVersion)
	}

	if m.UI == nil {
		t.Fatal("ui: got nil, want a UI spec")
	}
	if m.UI.Entry != "/static/ui/index.js" {
		t.Errorf("ui.entry: got %q, want %q", m.UI.Entry, "/static/ui/index.js")
	}
	wantPages := []plugin.Page{
		{Path: "/", Label: "Hello", Section: "Library"},
		{Path: "/admin", Label: "Hello admin", Component: "AdminPage", Visibility: "admin"},
	}
	if len(m.UI.Pages) != len(wantPages) {
		t.Fatalf("ui.pages: got %d pages, want %d", len(m.UI.Pages), len(wantPages))
	}
	for i, want := range wantPages {
		got := m.UI.Pages[i]
		if got != want {
			t.Errorf("ui.pages[%d]: got %+v, want %+v", i, got, want)
		}
	}
	if len(m.UI.Slots) != 1 || m.UI.Slots["agent.panel"] != "AgentPanel" {
		t.Errorf("ui.slots: got %v, want map with exactly agent.panel=AgentPanel", m.UI.Slots)
	}

	if m.API == nil {
		t.Fatal("api: got nil, want an API spec")
	}
	if m.API.BasePath != "/api" {
		t.Errorf("api.basePath: got %q, want %q", m.API.BasePath, "/api")
	}

	if m.MCP == nil {
		t.Fatal("mcp: got nil, want an MCP spec")
	}
	if m.MCP.Path != "/mcp" {
		t.Errorf("mcp.path: got %q, want %q", m.MCP.Path, "/mcp")
	}
	if len(m.MCP.Audience) != 2 {
		t.Errorf("mcp.audience: got %v, want exactly remember=agent and reset=operator", m.MCP.Audience)
	}
	if m.MCP.Audience["remember"] != "agent" {
		t.Errorf(`mcp.audience["remember"]: got %q, want %q`, m.MCP.Audience["remember"], "agent")
	}
	if m.MCP.Audience["reset"] != "operator" {
		t.Errorf(`mcp.audience["reset"]: got %q, want %q`, m.MCP.Audience["reset"], "operator")
	}
	// greet is deliberately absent from the map: absent means "both".
	if got := m.ToolAudience("greet"); got != "both" {
		t.Errorf("ToolAudience(greet): got %q, want %q", got, "both")
	}

	if len(m.Permissions) != 1 || m.Permissions[0] != "agents:read" {
		t.Errorf("permissions: got %v, want [agents:read]", m.Permissions)
	}

	wantSkill := plugin.SkillSpec{
		Name:        "hello-usage",
		Description: "How agents use the hello plugin",
		Path:        "/static/skills/hello-usage.md",
	}
	if len(m.Skills) != 1 || m.Skills[0] != wantSkill {
		t.Errorf("skills: got %+v, want [%+v]", m.Skills, wantSkill)
	}
}

func TestServesManifest_AtWellKnownPath(t *testing.T) {
	ts := newFixture(t, "")

	status, contentType, body := get(t, ts, "/ainsel-plugin.json", "")
	if status != http.StatusOK {
		t.Fatalf("status: got %d, want %d", status, http.StatusOK)
	}
	if contentType != "application/json" {
		t.Errorf("Content-Type: got %q, want %q", contentType, "application/json")
	}
	if body != string(Manifest()) {
		t.Errorf("body: got %s, want the embedded manifest bytes", body)
	}
}

// --- static files ---

func TestServesUIModule_WithJavaScriptContentType(t *testing.T) {
	ts := newFixture(t, "")

	status, contentType, body := get(t, ts, "/static/ui/index.js", "")
	if status != http.StatusOK {
		t.Fatalf("status: got %d, want %d", status, http.StatusOK)
	}
	// The console imports this file as an ES module, so it must be served as
	// JavaScript rather than sniffed to text/plain.
	if contentType != "text/javascript; charset=utf-8" {
		t.Errorf("Content-Type: got %q, want %q", contentType, "text/javascript; charset=utf-8")
	}

	// Only the two allowed externals, in exactly these import statements.
	for _, want := range []string{
		"import { jsx as _jsx } from 'react/jsx-runtime'",
		"import { usePluginContext } from '@ainsel/plugin-sdk'",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("ui module: missing import line %q", want)
		}
	}
	// No other module may be imported: the console's import map only resolves
	// these two, so anything else fails at load time in the browser.
	imports := 0
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "import ") {
			imports++
		}
	}
	if imports != 2 {
		t.Errorf("ui module: got %d import statements, want exactly 2", imports)
	}

	// The exports the console resolves by name from the manifest.
	for _, want := range []string{
		"export default function HelloPage()",
		"export function AdminPage()",
		"export function AgentPanel({ agentName })",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("ui module: missing export %q", want)
		}
	}

	// One text node per component: string concatenation, never a template
	// literal splitting text across elements.
	for _, want := range []string{
		"'Hello from ' + pluginId",
		"'Hello admin'",
		"'Hello agent ' + agentName",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("ui module: missing text node %q", want)
		}
	}
	if strings.Contains(body, "`") {
		t.Error("ui module: contains a template literal, want plain string concatenation")
	}
}

func TestServesSkillDocument_AtManifestPath(t *testing.T) {
	ts := newFixture(t, "")

	status, contentType, body := get(t, ts, "/static/skills/hello-usage.md", "")
	if status != http.StatusOK {
		t.Fatalf("status: got %d, want %d", status, http.StatusOK)
	}
	// The hub stores this body verbatim as a platform skill, so the exact bytes
	// are the contract: they must match what is embedded.
	if contentType != "text/markdown; charset=utf-8" {
		t.Errorf("Content-Type: got %q, want %q", contentType, "text/markdown; charset=utf-8")
	}

	raw, err := fs.ReadFile(staticFS, "static/skills/hello-usage.md")
	if err != nil {
		t.Fatalf("read embedded skill: %v", err)
	}
	if body != string(raw) {
		t.Errorf("body: got %q, want the embedded skill bytes %q", body, string(raw))
	}
	for _, want := range []string{
		"# hello-usage",
		"Call `greet` with `name` when you want a greeting",
		"Call `remember` when you want to keep something from this invocation",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("skill body: missing line %q", want)
		}
	}
}

func TestStatic_RejectsTraversalAndMissingFiles(t *testing.T) {
	ts := newFixture(t, "")

	for _, path := range []string{"/static/nope.js", "/static/../hello.go", "/static/"} {
		status, _, _ := get(t, ts, path, "")
		if status != http.StatusNotFound {
			t.Errorf("GET %s: status got %d, want %d", path, status, http.StatusNotFound)
		}
	}
}

// --- visibility API ---

func TestVisibleAgents_WithoutCredentialIsUnauthorized(t *testing.T) {
	hub := newFakeHub(t, []string{"a1"})
	ts := newFixture(t, hub.url())

	status, contentType, body := get(t, ts, "/api/visible-agents?names=a1", "")
	if status != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want %d", status, http.StatusUnauthorized)
	}
	if contentType != "application/json" {
		t.Errorf("Content-Type: got %q, want %q", contentType, "application/json")
	}
	if body != `{"error":"missing credential"}` {
		t.Errorf("body: got %s, want %s", body, `{"error":"missing credential"}`)
	}
	if n, _, _, _ := hub.calls(); n != 0 {
		t.Errorf("hub requests: got %d, want 0 (the lookup must not run without a caller)", n)
	}
}

func TestVisibleAgents_ForwardsCredentialAndNames(t *testing.T) {
	hub := newFakeHub(t, []string{"a1"})
	ts := newFixture(t, hub.url())

	status, contentType, body := get(t, ts, "/api/visible-agents?names=a1,a2", testCredential)
	if status != http.StatusOK {
		t.Fatalf("status: got %d, want %d", status, http.StatusOK)
	}
	if contentType != "application/json" {
		t.Errorf("Content-Type: got %q, want %q", contentType, "application/json")
	}
	// The hub's answer is relayed verbatim: the caller sees the hub's judgement,
	// not the fixture's.
	if body != `{"visible":["a1"]}` {
		t.Errorf("body: got %s, want %s", body, `{"visible":["a1"]}`)
	}

	n, auth, paths, bodies := hub.calls()
	if n != 1 {
		t.Fatalf("hub requests: got %d, want 1", n)
	}
	if want := "Bearer " + testCredential; auth[0] != want {
		t.Errorf("Authorization: got %q, want %q", auth[0], want)
	}
	if want := "/api/v1/plugin-context/visible"; paths[0] != want {
		t.Errorf("path: got %q, want %q", paths[0], want)
	}
	if want := `{"resourceType":"agent","names":["a1","a2"]}`; bodies[0] != want {
		t.Errorf("body: got %s, want %s", bodies[0], want)
	}
}

func TestVisibleAgents_WithoutNamesIsBadRequest(t *testing.T) {
	hub := newFakeHub(t, []string{"a1"})
	ts := newFixture(t, hub.url())

	status, _, body := get(t, ts, "/api/visible-agents", testCredential)
	if status != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", status, http.StatusBadRequest)
	}
	if want := `{"error":"names is required"}`; body != want {
		t.Errorf("body: got %s, want %s", body, want)
	}
	if n, _, _, _ := hub.calls(); n != 0 {
		t.Errorf("hub requests: got %d, want 0", n)
	}
}

func TestVisibleAgents_HubFailureIsBadGateway(t *testing.T) {
	hub := newFakeHub(t, []string{"a1"})
	hub.failWith(http.StatusInternalServerError)
	ts := newFixture(t, hub.url())

	status, _, body := get(t, ts, "/api/visible-agents?names=a1", testCredential)
	if status != http.StatusBadGateway {
		t.Fatalf("status: got %d, want %d", status, http.StatusBadGateway)
	}
	if want := `{"error":"hub visibility check failed"}`; body != want {
		t.Errorf("body: got %s, want %s", body, want)
	}
}

// --- MCP tools ---

// mcpSession opens an MCP session against the fixture's /mcp endpoint with the
// headers the hub would send, and performs the initialize handshake.
func mcpSession(t *testing.T, ts *httptest.Server, headers map[string]string) *mcpclient.Client {
	t.Helper()

	cl, err := mcpclient.NewStreamableHttpClient(ts.URL+"/mcp",
		transport.WithHTTPHeaderFunc(func(context.Context) map[string]string {
			out := make(map[string]string, len(headers)+1)
			for k, v := range headers {
				out[k] = v
			}
			return out
		}),
	)
	if err != nil {
		t.Fatalf("mcp client: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })

	ctx := context.Background()
	if err := cl.Start(ctx); err != nil {
		t.Fatalf("mcp start: %v", err)
	}
	if _, err := cl.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.ProtocolVersion20251125,
		ClientInfo:      mcp.Implementation{Name: "ainsel-test", Version: "1"},
	}}); err != nil {
		t.Fatalf("mcp initialize: %v", err)
	}
	return cl
}

// listToolNames returns the fixture's tools sorted, with their descriptions and
// required argument names, so a test can pin the whole tool surface at once.
func listToolNames(t *testing.T, cl *mcpclient.Client) ([]string, map[string]string, map[string][]string) {
	t.Helper()

	res, err := cl.ListTools(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	names := make([]string, 0, len(res.Tools))
	descs := make(map[string]string, len(res.Tools))
	required := make(map[string][]string, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		descs[tool.Name] = tool.Description
		req := tool.InputSchema.Required
		if req == nil {
			req = []string{}
		}
		required[tool.Name] = req
	}
	sort.Strings(names)
	return names, descs, required
}

// callText runs one tool and returns its single text content and its error flag.
func callText(t *testing.T, cl *mcpclient.Client, name string, args map[string]any) (string, bool) {
	t.Helper()

	res, err := cl.CallTool(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: name, Arguments: args},
	})
	if err != nil {
		t.Fatalf("tools/call %s: %v", name, err)
	}
	if len(res.Content) != 1 {
		t.Fatalf("tools/call %s: got %d content items, want 1", name, len(res.Content))
	}
	text, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("tools/call %s: content is %T, want mcp.TextContent", name, res.Content[0])
	}
	return text.Text, res.IsError
}

func TestMCPToolSurface(t *testing.T) {
	ts := newFixture(t, "")
	cl := mcpSession(t, ts, map[string]string{"X-Ainsel-Credential": testCredential})

	names, descs, required := listToolNames(t, cl)
	wantNames := []string{"greet", "remember", "reset"}
	if strings.Join(names, ",") != strings.Join(wantNames, ",") {
		t.Fatalf("tools: got %v, want %v", names, wantNames)
	}

	wantDescs := map[string]string{
		"greet":    "Greet someone by name",
		"remember": "Remember something for the current invocation",
		"reset":    "Forget everything this plugin remembered",
	}
	for name, want := range wantDescs {
		if descs[name] != want {
			t.Errorf("tool %s description: got %q, want %q", name, descs[name], want)
		}
	}

	wantRequired := map[string][]string{
		"greet":    {"name"},
		"remember": {},
		"reset":    {},
	}
	for name, want := range wantRequired {
		if strings.Join(required[name], ",") != strings.Join(want, ",") {
			t.Errorf("tool %s required args: got %v, want %v", name, required[name], want)
		}
	}
}

func TestMCPGreet(t *testing.T) {
	ts := newFixture(t, "")
	cl := mcpSession(t, ts, map[string]string{"X-Ainsel-Credential": testCredential})

	text, isErr := callText(t, cl, "greet", map[string]any{"name": "Ada"})
	if isErr {
		t.Fatalf("greet: got an error result %q, want a success result", text)
	}
	if want := "hello, Ada"; text != want {
		t.Errorf("greet: got %q, want %q", text, want)
	}
}

func TestMCPRemember_EchoesInvocationID(t *testing.T) {
	ts := newFixture(t, "")
	cl := mcpSession(t, ts, map[string]string{
		"X-Ainsel-Credential":    testCredential,
		"X-Ainsel-Invocation-Id": testInvocationID,
	})

	text, isErr := callText(t, cl, "remember", map[string]any{"text": "the answer is 42"})
	if isErr {
		t.Fatalf("remember: got an error result %q, want a success result", text)
	}
	if want := "remembered for inv-7"; text != want {
		t.Errorf("remember: got %q, want %q", text, want)
	}
}

func TestMCPRemember_WithoutInvocationID(t *testing.T) {
	ts := newFixture(t, "")
	cl := mcpSession(t, ts, map[string]string{"X-Ainsel-Credential": testCredential})

	text, isErr := callText(t, cl, "remember", map[string]any{"text": "the answer is 42"})
	if !isErr {
		t.Fatalf("remember: got success %q, want an error result", text)
	}
	if want := "remember: missing X-Ainsel-Invocation-Id"; text != want {
		t.Errorf("remember: got %q, want %q", text, want)
	}
}

func TestMCPReset(t *testing.T) {
	ts := newFixture(t, "")
	cl := mcpSession(t, ts, map[string]string{"X-Ainsel-Credential": testCredential})

	text, isErr := callText(t, cl, "reset", nil)
	if isErr {
		t.Fatalf("reset: got an error result %q, want a success result", text)
	}
	if want := "reset"; text != want {
		t.Errorf("reset: got %q, want %q", text, want)
	}
}
