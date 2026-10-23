package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/plugincreds"
	"github.com/ainsel-project/ainsel/services/hub/internal/pluginmcp"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"k8s.io/apimachinery/pkg/runtime"

	connectorv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	pluginpkg "github.com/ainsel-project/ainsel/shared/api/plugin"
)

// The hub is the only MCP client of a plugin: operators see their
// operator/both tools through GET /api/internal/plugin-tools (X-Internal-Token)
// and call them through POST /api/v1/plugin-tools/call with a user credential
// attached. Agent-audience tools must be invisible and uncallable on both.

// pluginToolsInternalToken is the X-Internal-Token the fixtures accept.
const pluginToolsInternalToken = "hub-internal-token"

// pluginToolsUserSub / pluginToolsUser are the caller the auth middleware
// injects for the /api/v1 path.
const (
	pluginToolsUserSub = "user-1"
	pluginToolsUser    = "alice"
)

// recordedJSONRPCRequest is one request that reached the fixture MCP endpoint.
type recordedJSONRPCRequest struct {
	method string
	body   []byte
	header http.Header
}

// pluginToolsFixture is an in-process plugin MCP server (mcp-go over
// streamable HTTP, mounted at /mcp) with the three tools the brief names:
// search (default audience, so "both"), remember ("agent") and reset
// ("operator"). It records every JSON-RPC request it served.
type pluginToolsFixture struct {
	ts *httptest.Server

	mu       sync.Mutex
	requests []recordedJSONRPCRequest
}

func newPluginToolsFixture(t *testing.T) *pluginToolsFixture {
	t.Helper()

	srv := server.NewMCPServer("mem0", "1.2.3", server.WithToolCapabilities(true))
	srv.AddTool(
		mcp.NewTool("search",
			mcp.WithDescription("Search memories"),
			mcp.WithString("query", mcp.Required(), mcp.Description("Text to search for")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("search:" + req.GetString("query", "")), nil
		},
	)
	srv.AddTool(
		mcp.NewTool("remember",
			mcp.WithDescription("Remember a memory"),
			mcp.WithString("text", mcp.Required()),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("remember:" + req.GetString("text", "")), nil
		},
	)
	srv.AddTool(
		mcp.NewTool("reset",
			mcp.WithDescription("Reset all memories"),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("reset ok"), nil
		},
	)
	stream := server.NewStreamableHTTPServer(srv)

	f := &pluginToolsFixture{}
	mux := http.NewServeMux()
	mux.Handle("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))

		var msg struct {
			Method string `json:"method"`
		}
		method := ""
		if json.Unmarshal(raw, &msg) == nil {
			method = msg.Method
		}

		f.mu.Lock()
		f.requests = append(f.requests, recordedJSONRPCRequest{method: method, body: raw, header: r.Header.Clone()})
		f.mu.Unlock()

		stream.ServeHTTP(w, r)
	}))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	f.ts = ts
	return f
}

// endpoint is the "host:port" the fixture listens on, in the shape
// status.verifiedEndpoints carries.
func (f *pluginToolsFixture) endpoint() string {
	return strings.TrimPrefix(f.ts.URL, "http://")
}

func (f *pluginToolsFixture) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if r.method == method {
			n++
		}
	}
	return n
}

// total is every request the fixture served, including the initialize
// handshake: zero means the hub never reached the plugin at all.
func (f *pluginToolsFixture) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

// credential returns the X-Ainsel-Credential of the single request that served
// the given JSON-RPC method.
func (f *pluginToolsFixture) credential(method string) (string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cred := ""
	n := 0
	for _, r := range f.requests {
		if r.method == method {
			n++
			cred = r.header.Get("X-Ainsel-Credential")
		}
	}
	return cred, n
}

// pluginToolsManifest is the manifest the reconciler would have published for
// the fixture plugin: an MCP endpoint with per-tool audience.
func pluginToolsManifest(id string) *pluginpkg.Manifest {
	return &pluginpkg.Manifest{
		ID:        id,
		PluginAPI: "1.1",
		Version:   "1.2.3",
		MCP: &pluginpkg.MCPSpec{
			Path: "/mcp",
			Audience: map[string]string{
				"remember": pluginpkg.AudienceAgent,
				"reset":    pluginpkg.AudienceOperator,
			},
		},
	}
}

// pluginToolsPluginCR builds the fixture plugin: Ready, manifest published, and
// pointing at endpoint ("" leaves verifiedEndpoints empty).
func pluginToolsPluginCR(t *testing.T, id string, disabled bool, endpoint string) *connectorv1alpha1.Plugin {
	t.Helper()
	p := pluginCRReady(pluginCR(id, disabled), true, "", "", nil)
	raw, err := json.Marshal(pluginToolsManifest(id))
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	p.Status.Manifest = &runtime.RawExtension{Raw: raw}
	if endpoint != "" {
		p.Status.VerifiedEndpoints = []string{endpoint}
	} else {
		p.Status.VerifiedEndpoints = nil
	}
	return p
}

// pluginToolsTestServer wires a dev-mode server with the plugin-tool routes
// exactly as New() registers them, an internal token, a plugin credential
// issuer, and an auth middleware that injects the caller.
func pluginToolsTestServer(t *testing.T, objects ...runtime.Object) (*Server, *plugincreds.Issuer) {
	t.Helper()

	s := testServer(t, objects...)
	s.mux.HandleFunc("/api/internal/plugin-tools", s.handleInternalPluginTools)
	s.mux.HandleFunc("/api/v1/plugin-tools/call", s.handlePluginToolCall)
	s.SetInternalValidateSecret(pluginToolsInternalToken)

	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i + 1)
	}
	issuer, err := plugincreds.NewIssuer(secret, 5*time.Minute)
	if err != nil {
		t.Fatalf("new issuer: %v", err)
	}
	s.SetPluginCredentialIssuer(issuer)

	// The hub is a plain MCP client of the plugin: an in-cluster HTTP client is
	// enough for the fixtures, but wire an explicit one to pin the setter too.
	s.SetPluginMCPClient(pluginmcp.New(&http.Client{Timeout: 10 * time.Second}))

	s.SetAuthMiddleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := oidc.ContextWithUser(r.Context(), &oidc.User{
				Sub:      pluginToolsUserSub,
				Username: pluginToolsUser,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	return s, issuer
}

// pluginToolsListBody is the decoded GET /api/internal/plugin-tools body.
type pluginToolsListBody struct {
	Tools []struct {
		PluginID    string         `json:"pluginId"`
		Name        string         `json:"name"`
		Description string         `json:"description"`
		InputSchema map[string]any `json:"inputSchema"`
	} `json:"tools"`
}

func doPluginToolsList(t *testing.T, s *Server, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/internal/plugin-tools", nil)
	if token != "" {
		req.Header.Set("X-Internal-Token", token)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func decodePluginToolsList(t *testing.T, rec *httptest.ResponseRecorder) pluginToolsListBody {
	t.Helper()
	var out pluginToolsListBody
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode list body %q: %v", rec.Body.String(), err)
	}
	return out
}

func doPluginToolsCall(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/plugin-tools/call", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

// toolNames lists the plugin's own tool names in response order.
func toolNames(resp pluginToolsListBody) []string {
	names := make([]string, 0, len(resp.Tools))
	for _, tl := range resp.Tools {
		names = append(names, tl.Name)
	}
	return names
}

// TestInternalPluginTools_ListsOperatorTools pins the operator view: only the
// operator/both tools, sorted by plugin then name, carrying the plugin id, the
// description and the input schema; and the internal-token gate.
func TestInternalPluginTools_ListsOperatorTools(t *testing.T) {
	f := newPluginToolsFixture(t)
	s, _ := pluginToolsTestServer(t, pluginToolsPluginCR(t, "mem0", false, f.endpoint()))

	rec := doPluginToolsList(t, s, pluginToolsInternalToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/internal/plugin-tools: got %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	resp := decodePluginToolsList(t, rec)
	want := []string{"reset", "search"}
	if got := toolNames(resp); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("tool names: got %v, want %v", got, want)
	}
	for _, tl := range resp.Tools {
		if tl.PluginID != "mem0" {
			t.Errorf("tool %s pluginId: got %q, want \"mem0\"", tl.Name, tl.PluginID)
		}
	}

	for _, tl := range resp.Tools {
		switch tl.Name {
		case "search":
			if tl.Description != "Search memories" {
				t.Errorf("search description: got %q, want \"Search memories\"", tl.Description)
			}
			if tl.InputSchema["type"] != "object" {
				t.Errorf("search inputSchema.type: got %v, want \"object\"", tl.InputSchema["type"])
			}
			props, ok := tl.InputSchema["properties"].(map[string]any)
			if !ok {
				t.Fatalf("search inputSchema.properties: got %v, want an object", tl.InputSchema["properties"])
			}
			if _, ok := props["query"]; !ok {
				t.Errorf("search inputSchema.properties has no query: %v", props)
			}
		case "reset":
			if tl.Description != "Reset all memories" {
				t.Errorf("reset description: got %q, want \"Reset all memories\"", tl.Description)
			}
		}
	}

	// The list is served with the plugin's background credential, never a
	// user's.
	cred, n := f.credential("tools/list")
	if n != 1 {
		t.Fatalf("tools/list requests: got %d, want 1", n)
	}
	if cred == "" {
		t.Fatal("tools/list carried no X-Ainsel-Credential")
	}

	// A wrong internal token is rejected before any plugin is contacted.
	rec = doPluginToolsList(t, s, "wrong-token")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong internal token: got %d, want 401", rec.Code)
	}
	if msg := decodePluginToolError(t, rec); msg != "invalid internal token" {
		t.Fatalf("wrong internal token error: got %q, want \"invalid internal token\"", msg)
	}
}

func decodePluginToolError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var out map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return out["error"]
}

// TestInternalPluginTools_UnreachablePluginSkipped: one plugin whose endpoint
// is a closed port must not fail the response, and must contribute nothing.
func TestInternalPluginTools_UnreachablePluginSkipped(t *testing.T) {
	f := newPluginToolsFixture(t)
	mem0 := pluginToolsPluginCR(t, "mem0", false, f.endpoint())
	broken := pluginToolsPluginCR(t, "broken", false, "127.0.0.1:1")

	s, _ := pluginToolsTestServer(t, mem0, broken)

	rec := doPluginToolsList(t, s, pluginToolsInternalToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/internal/plugin-tools: got %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	resp := decodePluginToolsList(t, rec)
	want := []string{"reset", "search"}
	if got := toolNames(resp); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("tool names: got %v, want %v", got, want)
	}
	for _, tl := range resp.Tools {
		if tl.PluginID != "mem0" {
			t.Errorf("tool %s pluginId: got %q, want \"mem0\"", tl.Name, tl.PluginID)
		}
	}
}

// TestInternalPluginTools_NoMCPSpecContributesNothing: a plugin without an mcp
// spec is never contacted and the response stays a JSON array, not null.
func TestInternalPluginTools_NoMCPSpecContributesNothing(t *testing.T) {
	p := pluginCRReady(pluginCR("mem0", false), true, "", "", nil)
	raw, err := json.Marshal(&pluginpkg.Manifest{ID: "mem0", PluginAPI: "1.1", Version: "1.2.3"})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	p.Status.Manifest = &runtime.RawExtension{Raw: raw}
	p.Status.VerifiedEndpoints = []string{"127.0.0.1:1"}

	s, _ := pluginToolsTestServer(t, p)

	rec := doPluginToolsList(t, s, pluginToolsInternalToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/internal/plugin-tools: got %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "{\"tools\":[]}\n" {
		t.Fatalf("body: got %q, want \"{\\\"tools\\\":[]}\\n\"", got)
	}
}

// TestInternalPluginTools_DisabledPluginSkipped: a disabled plugin contributes
// nothing and is not contacted.
func TestInternalPluginTools_DisabledPluginSkipped(t *testing.T) {
	f := newPluginToolsFixture(t)
	s, _ := pluginToolsTestServer(t, pluginToolsPluginCR(t, "mem0", true, f.endpoint()))

	rec := doPluginToolsList(t, s, pluginToolsInternalToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/internal/plugin-tools: got %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "{\"tools\":[]}\n" {
		t.Fatalf("body: got %q, want \"{\\\"tools\\\":[]}\\n\"", got)
	}
	if n := f.total(); n != 0 {
		t.Fatalf("requests to a disabled plugin: got %d, want 0", n)
	}
}

// TestPluginToolsCall_ForwardsUserCredential: a user call reaches the plugin
// with a user-kind credential for the caller and the result passes through
// unchanged.
func TestPluginToolsCall_ForwardsUserCredential(t *testing.T) {
	f := newPluginToolsFixture(t)
	s, issuer := pluginToolsTestServer(t, pluginToolsPluginCR(t, "mem0", false, f.endpoint()))

	rec := doPluginToolsCall(t, s, `{"pluginId":"mem0","tool":"search","arguments":{"query":"hello"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/plugin-tools/call: got %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	var res struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode result body %q: %v", rec.Body.String(), err)
	}
	if res.IsError {
		t.Fatalf("isError: got true, want false")
	}
	if len(res.Content) != 1 || res.Content[0].Text != "search:hello" {
		t.Fatalf("content: got %v, want one text block \"search:hello\"", res.Content)
	}

	cred, n := f.credential("tools/call")
	if n != 1 {
		t.Fatalf("tools/call requests: got %d, want 1", n)
	}
	if cred == "" {
		t.Fatal("tools/call carried no X-Ainsel-Credential")
	}
	claims, err := issuer.Verify(cred)
	if err != nil {
		t.Fatalf("verify credential the plugin saw: %v", err)
	}
	if claims.Kind != plugincreds.KindUser {
		t.Errorf("credential kind: got %q, want %q", claims.Kind, plugincreds.KindUser)
	}
	if claims.Sub != pluginToolsUserSub {
		t.Errorf("credential sub: got %q, want %q", claims.Sub, pluginToolsUserSub)
	}
	if claims.PluginID != "mem0" {
		t.Errorf("credential pluginId: got %q, want \"mem0\"", claims.PluginID)
	}
}

// TestPluginToolsCall_AgentOnlyRejected: an agent-audience tool is not found
// for an operator call, and the plugin is never asked to run it.
func TestPluginToolsCall_AgentOnlyRejected(t *testing.T) {
	f := newPluginToolsFixture(t)
	s, _ := pluginToolsTestServer(t, pluginToolsPluginCR(t, "mem0", false, f.endpoint()))

	rec := doPluginToolsCall(t, s, `{"pluginId":"mem0","tool":"remember","arguments":{"text":"x"}}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/v1/plugin-tools/call remember: got %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	if msg := decodePluginToolError(t, rec); msg != "tool not found" {
		t.Fatalf("error: got %q, want \"tool not found\"", msg)
	}
	if n := f.count("tools/call"); n != 0 {
		t.Fatalf("tools/call requests for an agent-only tool: got %d, want 0", n)
	}
}

// TestPluginToolsCall_UnknownTool: a name the plugin does not serve is also
// "tool not found", not a forwarded call.
func TestPluginToolsCall_UnknownTool(t *testing.T) {
	f := newPluginToolsFixture(t)
	s, _ := pluginToolsTestServer(t, pluginToolsPluginCR(t, "mem0", false, f.endpoint()))

	rec := doPluginToolsCall(t, s, `{"pluginId":"mem0","tool":"does-not-exist"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/v1/plugin-tools/call does-not-exist: got %d, want 404", rec.Code)
	}
	if msg := decodePluginToolError(t, rec); msg != "tool not found" {
		t.Fatalf("error: got %q, want \"tool not found\"", msg)
	}
	if n := f.count("tools/call"); n != 0 {
		t.Fatalf("tools/call requests for an unknown tool: got %d, want 0", n)
	}
}

// TestPluginToolsCall_DisabledPlugin: an unavailable plugin is 404 and is
// never contacted.
func TestPluginToolsCall_DisabledPlugin(t *testing.T) {
	f := newPluginToolsFixture(t)
	s, _ := pluginToolsTestServer(t, pluginToolsPluginCR(t, "mem0", true, f.endpoint()))

	rec := doPluginToolsCall(t, s, `{"pluginId":"mem0","tool":"search","arguments":{"query":"hello"}}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/v1/plugin-tools/call: got %d, want 404", rec.Code)
	}
	if msg := decodePluginToolError(t, rec); msg != "plugin not found" {
		t.Fatalf("error: got %q, want \"plugin not found\"", msg)
	}
	if n := f.total(); n != 0 {
		t.Fatalf("requests to a disabled plugin: got %d, want 0", n)
	}
}

// TestPluginToolsCall_UnreachablePlugin: a plugin whose endpoint is a closed
// port is 502.
func TestPluginToolsCall_UnreachablePlugin(t *testing.T) {
	s, _ := pluginToolsTestServer(t, pluginToolsPluginCR(t, "mem0", false, "127.0.0.1:1"))

	rec := doPluginToolsCall(t, s, `{"pluginId":"mem0","tool":"search","arguments":{"query":"hello"}}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("POST /api/v1/plugin-tools/call: got %d, want 502 (body %s)", rec.Code, rec.Body.String())
	}
	if msg := decodePluginToolError(t, rec); msg != "plugin unreachable" {
		t.Fatalf("error: got %q, want \"plugin unreachable\"", msg)
	}
}

// TestPluginToolsCall_NoVerifiedEndpoint: a Ready plugin with nothing verified
// is unreachable too, and no connection is attempted.
func TestPluginToolsCall_NoVerifiedEndpoint(t *testing.T) {
	s, _ := pluginToolsTestServer(t, pluginToolsPluginCR(t, "mem0", false, ""))

	rec := doPluginToolsCall(t, s, `{"pluginId":"mem0","tool":"search","arguments":{"query":"hello"}}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("POST /api/v1/plugin-tools/call: got %d, want 502 (body %s)", rec.Code, rec.Body.String())
	}
	if msg := decodePluginToolError(t, rec); msg != "plugin unreachable" {
		t.Fatalf("error: got %q, want \"plugin unreachable\"", msg)
	}
}

// TestPluginToolsCall_RequiresUser: the call endpoint is on the user-auth
// surface, not an open one.
func TestPluginToolsCall_RequiresUser(t *testing.T) {
	s, _ := pluginToolsTestServer(t, pluginToolsPluginCR(t, "mem0", false, "127.0.0.1:1"))
	s.SetAuthMiddleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
		})
	})

	rec := doPluginToolsCall(t, s, `{"pluginId":"mem0","tool":"search"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST /api/v1/plugin-tools/call without a user: got %d, want 401", rec.Code)
	}
}

// TestPluginToolsRoutesRegistered pins that New() mounts both routes: a
// request that reaches the handler's own guard differs from the mux's 404.
func TestPluginToolsRoutesRegistered(t *testing.T) {
	s := New(nil, "test-ns", ConnectorConfig{}, nil, nil, nil, nil, nil, &Config{})
	s.SetInternalValidateSecret(pluginToolsInternalToken)

	rec := doPluginToolsList(t, s, "wrong-token")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/internal/plugin-tools: got %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/plugin-tools/call", strings.NewReader("{}")))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST /api/v1/plugin-tools/call: got %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestPluginToolsCall_LazyMCPClient: a server that was never given a plugin MCP
// client still reaches the plugin through a default one.
func TestPluginToolsCall_LazyMCPClient(t *testing.T) {
	f := newPluginToolsFixture(t)
	s, _ := pluginToolsTestServer(t, pluginToolsPluginCR(t, "mem0", false, f.endpoint()))
	s.pluginMCP = nil // exercise the lazily built default client

	rec := doPluginToolsCall(t, s, `{"pluginId":"mem0","tool":"reset"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/plugin-tools/call reset: got %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if n := f.count("tools/call"); n != 1 {
		t.Fatalf("tools/call requests: got %d, want 1", n)
	}
}

// TestPluginToolsCall_SortsAndListsAcrossPlugins is a guard on the ordering
// rule with two reachable plugins: entries are ordered by plugin id, then name.
func TestPluginToolsCall_SortsAndListsAcrossPlugins(t *testing.T) {
	mem0 := newPluginToolsFixture(t)
	other := newPluginToolsFixture(t)

	mem0CR := pluginToolsPluginCR(t, "mem0", false, mem0.endpoint())
	otherCR := pluginToolsPluginCR(t, "aaa", false, other.endpoint())

	s, _ := pluginToolsTestServer(t, mem0CR, otherCR)

	rec := doPluginToolsList(t, s, pluginToolsInternalToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/internal/plugin-tools: got %d, want 200", rec.Code)
	}
	resp := decodePluginToolsList(t, rec)
	got := make([]string, 0, len(resp.Tools))
	for _, tl := range resp.Tools {
		got = append(got, tl.PluginID+"/"+tl.Name)
	}
	want := []string{"aaa/reset", "aaa/search", "mem0/reset", "mem0/search"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ordered tools: got %v, want %v", got, want)
	}
	if !sort.StringsAreSorted(got) {
		t.Fatalf("entries are not in plugin-then-name order: %v", got)
	}
}
