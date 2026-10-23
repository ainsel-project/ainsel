package pluginmcp

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

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// recordedRequest is one HTTP request that reached the fixture's /mcp endpoint.
type recordedRequest struct {
	method string
	body   []byte
	header http.Header
}

// fixture is an in-process plugin MCP server over streamable HTTP, mounted at
// /mcp, recording the JSON-RPC method and headers of every request.
type fixture struct {
	mu       sync.Mutex
	requests []recordedRequest
	failList bool
	ts       *httptest.Server
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	srv := server.NewMCPServer("test-plugin", "1", server.WithToolCapabilities(true))
	srv.AddTool(
		mcp.NewTool("search", mcp.WithString("query")),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if fail, _ := req.GetArguments()["fail"].(bool); fail {
				return mcp.NewToolResultError("search failed"), nil
			}
			return mcp.NewToolResultText("search:" + req.GetString("query", "")), nil
		},
	)
	srv.AddTool(
		mcp.NewTool("reset"),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("reset ok"), nil
		},
	)
	stream := server.NewStreamableHTTPServer(srv)

	f := &fixture{}
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
		f.requests = append(f.requests, recordedRequest{method: method, body: raw, header: r.Header.Clone()})
		fail := f.failList && method == "tools/list"
		f.mu.Unlock()

		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		stream.ServeHTTP(w, r)
	}))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	f.ts = ts
	return f
}

func (f *fixture) target() Target {
	return Target{
		PluginID:        "mem0",
		Endpoint:        strings.TrimPrefix(f.ts.URL, "http://"),
		Path:            "/mcp",
		ManifestVersion: "1.2.3",
	}
}

func (f *fixture) setFailList(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failList = v
}

func (f *fixture) count(method string) int {
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

func (f *fixture) byMethod(method string) []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []recordedRequest
	for _, r := range f.requests {
		if r.method == method {
			out = append(out, r)
		}
	}
	return out
}

// fakeClock is a settable clock for TTL tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock(t time.Time) *fakeClock {
	return &fakeClock{t: t}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func toolNames(tools []mcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tl := range tools {
		names = append(names, tl.Name)
	}
	sort.Strings(names)
	return names
}

func textOf(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) != 1 {
		t.Fatalf("content length: got %d, want 1", len(res.Content))
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] type: got %T, want mcp.TextContent", res.Content[0])
	}
	return tc.Text
}

func TestListTools_SendsHeadersAndCaches(t *testing.T) {
	f := newFixture(t)
	clock := newFakeClock(time.Unix(1700000000, 0))
	c := New(nil)
	c.now = clock.now

	ctx := context.Background()
	tools, err := c.ListTools(ctx, f.target(), "ainselp_secret")
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if got, want := toolNames(tools), []string{"reset", "search"}; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("tool names: got %v, want %v", got, want)
	}
	if n := f.count("tools/list"); n != 1 {
		t.Fatalf("tools/list requests: got %d, want 1", n)
	}

	// Every request must carry the plugin id and credential headers, and no
	// invocation id (ListTools has none).
	for _, r := range f.byMethod("tools/list") {
		if got := r.header.Get("X-Ainsel-Plugin-Id"); got != "mem0" {
			t.Errorf("X-Ainsel-Plugin-Id: got %q, want %q", got, "mem0")
		}
		if got := r.header.Get("X-Ainsel-Credential"); got != "ainselp_secret" {
			t.Errorf("X-Ainsel-Credential: got %q, want %q", got, "ainselp_secret")
		}
		if got := r.header.Get("X-Ainsel-Invocation-Id"); got != "" {
			t.Errorf("X-Ainsel-Invocation-Id: got %q, want empty", got)
		}
	}

	// The initialize handshake must identify the hub.
	inits := f.byMethod("initialize")
	if len(inits) != 1 {
		t.Fatalf("initialize requests: got %d, want 1", len(inits))
	}
	var initReq struct {
		Params struct {
			ClientInfo struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"clientInfo"`
		} `json:"params"`
	}
	if err := json.Unmarshal(inits[0].body, &initReq); err != nil {
		t.Fatalf("unmarshal initialize body: %v", err)
	}
	if initReq.Params.ClientInfo.Name != "ainsel-hub" {
		t.Errorf("clientInfo.name: got %q, want %q", initReq.Params.ClientInfo.Name, "ainsel-hub")
	}
	if initReq.Params.ClientInfo.Version != "1" {
		t.Errorf("clientInfo.version: got %q, want %q", initReq.Params.ClientInfo.Version, "1")
	}

	// Second call within the 30 s TTL must come from the cache.
	tools, err = c.ListTools(ctx, f.target(), "ainselp_secret")
	if err != nil {
		t.Fatalf("ListTools (cached): %v", err)
	}
	if got, want := toolNames(tools), []string{"reset", "search"}; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("cached tool names: got %v, want %v", got, want)
	}
	if n := f.count("tools/list"); n != 1 {
		t.Fatalf("tools/list requests after cached call: got %d, want 1", n)
	}

	// After the TTL has passed the next call must hit the server again.
	clock.advance(31 * time.Second)
	if _, err := c.ListTools(ctx, f.target(), "ainselp_secret"); err != nil {
		t.Fatalf("ListTools (expired): %v", err)
	}
	if n := f.count("tools/list"); n != 2 {
		t.Fatalf("tools/list requests after TTL expiry: got %d, want 2", n)
	}
}

func TestListTools_ErrorNotCached(t *testing.T) {
	f := newFixture(t)
	clock := newFakeClock(time.Unix(1700000000, 0))
	c := New(nil)
	c.now = clock.now

	ctx := context.Background()
	if _, err := c.ListTools(ctx, f.target(), "ainselp_secret"); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if n := f.count("tools/list"); n != 1 {
		t.Fatalf("tools/list requests: got %d, want 1", n)
	}

	// Expire the cache and make the server fail the next list.
	clock.advance(31 * time.Second)
	f.setFailList(true)
	if _, err := c.ListTools(ctx, f.target(), "ainselp_secret"); err == nil {
		t.Fatal("ListTools with failing server: got nil error, want error")
	}

	// The failure must not have been cached: with the server healthy again,
	// the very next call refetches within the same TTL window. (The failed
	// attempt is itself recorded: it reached the server and got a 500.)
	if n := f.count("tools/list"); n != 2 {
		t.Fatalf("tools/list requests after failed call: got %d, want 2", n)
	}
	f.setFailList(false)
	tools, err := c.ListTools(ctx, f.target(), "ainselp_secret")
	if err != nil {
		t.Fatalf("ListTools after recovery: %v", err)
	}
	if got, want := toolNames(tools), []string{"reset", "search"}; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("tool names after recovery: got %v, want %v", got, want)
	}
	if n := f.count("tools/list"); n != 3 {
		t.Fatalf("tools/list requests after recovery: got %d, want 3", n)
	}
}

func TestCallTool_ForwardsInvocationID(t *testing.T) {
	f := newFixture(t)
	c := New(nil)

	ctx := context.Background()
	res, err := c.CallTool(ctx, f.target(), CallOptions{
		Credential:   "ainselp_call",
		InvocationID: "inv-1",
	}, "search", map[string]any{"query": "hello"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError: got true, want false")
	}
	if got, want := textOf(t, res), "search:hello"; got != want {
		t.Fatalf("result text: got %q, want %q", got, want)
	}

	calls := f.byMethod("tools/call")
	if len(calls) != 1 {
		t.Fatalf("tools/call requests: got %d, want 1", len(calls))
	}
	h := calls[0].header
	if got := h.Get("X-Ainsel-Invocation-Id"); got != "inv-1" {
		t.Errorf("X-Ainsel-Invocation-Id: got %q, want %q", got, "inv-1")
	}
	if got := h.Get("X-Ainsel-Plugin-Id"); got != "mem0" {
		t.Errorf("X-Ainsel-Plugin-Id: got %q, want %q", got, "mem0")
	}
	if got := h.Get("X-Ainsel-Credential"); got != "ainselp_call" {
		t.Errorf("X-Ainsel-Credential: got %q, want %q", got, "ainselp_call")
	}
	if !bytes.Contains(calls[0].body, []byte(`"query":"hello"`)) {
		t.Errorf("tools/call body does not contain query: %s", calls[0].body)
	}
}

func TestCallTool_ToolErrorIsResult(t *testing.T) {
	f := newFixture(t)
	c := New(nil)

	ctx := context.Background()
	res, err := c.CallTool(ctx, f.target(), CallOptions{
		Credential: "ainselp_call",
	}, "search", map[string]any{"fail": true})
	if err != nil {
		t.Fatalf("CallTool: got %v, want nil (tool errors stay in the result)", err)
	}
	if !res.IsError {
		t.Fatalf("IsError: got false, want true")
	}
	if got, want := textOf(t, res), "search failed"; got != want {
		t.Fatalf("result text: got %q, want %q", got, want)
	}
}

func TestInvalidate_DropsCachedTools(t *testing.T) {
	f := newFixture(t)
	clock := newFakeClock(time.Unix(1700000000, 0))
	c := New(nil)
	c.now = clock.now

	ctx := context.Background()
	if _, err := c.ListTools(ctx, f.target(), "ainselp_secret"); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if _, err := c.ListTools(ctx, f.target(), "ainselp_secret"); err != nil {
		t.Fatalf("ListTools (cached): %v", err)
	}
	if n := f.count("tools/list"); n != 1 {
		t.Fatalf("tools/list requests: got %d, want 1", n)
	}

	c.Invalidate("mem0")
	if _, err := c.ListTools(ctx, f.target(), "ainselp_secret"); err != nil {
		t.Fatalf("ListTools after invalidate: %v", err)
	}
	if n := f.count("tools/list"); n != 2 {
		t.Fatalf("tools/list requests after invalidate: got %d, want 2", n)
	}

	// An unrelated plugin id must not drop this entry.
	if _, err := c.ListTools(ctx, f.target(), "ainselp_secret"); err != nil {
		t.Fatalf("ListTools (cached): %v", err)
	}
	c.Invalidate("other")
	if _, err := c.ListTools(ctx, f.target(), "ainselp_secret"); err != nil {
		t.Fatalf("ListTools after unrelated invalidate: %v", err)
	}
	if n := f.count("tools/list"); n != 2 {
		t.Fatalf("tools/list requests after unrelated invalidate: got %d, want 2", n)
	}
}
