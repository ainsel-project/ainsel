package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/ainsel-project/ainsel/shared/auth/oidc"
)

// pluginHubStub is a fake hub for the aggregator tests. It serves
// GET /api/internal/plugin-tools from a settable tool list and records the last
// POST /api/v1/plugin-tools/call it received.
type pluginHubStub struct {
	mu sync.Mutex

	tools      []map[string]any
	listStatus int

	listCalls        int
	gotInternalToken string

	callStatus int
	callBody   any
	callAuth   string
	callRaw    []byte
	callType   string
	callCount  int
}

func (h *pluginHubStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/internal/plugin-tools":
		h.listCalls++
		h.gotInternalToken = r.Header.Get("X-Internal-Token")
		if h.listStatus != 0 && h.listStatus != http.StatusOK {
			w.WriteHeader(h.listStatus)
			_, _ = w.Write([]byte(`{"error":"hub is down"}`))
			return
		}
		tools := h.tools
		if tools == nil {
			tools = []map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"tools": tools})
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/plugin-tools/call":
		h.callCount++
		h.callAuth = r.Header.Get("Authorization")
		h.callType = r.Header.Get("Content-Type")
		h.callRaw, _ = io.ReadAll(r.Body)
		status := h.callStatus
		if status == 0 {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if h.callBody == nil {
			_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok from plugin"}]}`))
			return
		}
		_ = json.NewEncoder(w).Encode(h.callBody)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"unexpected request"}`))
	}
}

// setTools replaces the tool list the stub serves.
func (h *pluginHubStub) setTools(tools ...map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tools = tools
}

func (h *pluginHubStub) lastCall() (auth string, raw []byte, ctype string, count int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.callAuth, append([]byte(nil), h.callRaw...), h.callType, h.callCount
}

func pluginToolJSON(pluginID, name, description string, schema map[string]any) map[string]any {
	return map[string]any{
		"pluginId":    pluginID,
		"name":        name,
		"description": description,
		"inputSchema": schema,
	}
}

// newTestMCPServer returns a server with the same tool capabilities the
// production server declares, plus one built-in tool so collisions are real.
func newTestMCPServer() *server.MCPServer {
	s := server.NewMCPServer("test", "0.1.0", server.WithToolCapabilities(true))
	s.AddTool(mcp.NewTool("list_agents", mcp.WithDescription("built-in")), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("builtin list_agents"), nil
	})
	return s
}

func builtinNames() map[string]bool {
	return map[string]bool{"list_agents": true}
}

func registeredNames(t *testing.T, s *server.MCPServer) []string {
	t.Helper()
	names := make([]string, 0, len(s.ListTools()))
	for name := range s.ListTools() {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func callRegisteredTool(t *testing.T, s *server.MCPServer, name string, args map[string]any) (*mcp.CallToolResult, error) {
	t.Helper()
	entry, ok := s.ListTools()[name]
	if !ok {
		t.Fatalf("tool %q is not registered (have %v)", name, registeredNames(t, s))
	}
	return entry.Handler(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: name, Arguments: args},
	})
}

func captureAggregatorLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func TestPluginToolPrefixedName(t *testing.T) {
	cases := []struct {
		pluginID, tool, want string
	}{
		{"mem0", "search", "mem0_search"},
		{"my-mem", "search", "my_mem_search"},
		{"my-mem", "get_entry", "my_mem_get_entry"},
	}
	for _, tc := range cases {
		if got := pluginToolPrefixedName(tc.pluginID, tc.tool); got != tc.want {
			t.Errorf("pluginToolPrefixedName(%q, %q) = %q; want %q", tc.pluginID, tc.tool, got, tc.want)
		}
	}
}

func TestSync_RegistersPrefixedTools(t *testing.T) {
	hub := &pluginHubStub{}
	hub.setTools(
		pluginToolJSON("mem0", "search", "Search memories", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"q": map[string]any{"type": "string"},
			},
		}),
		pluginToolJSON("my-mem", "search", "Search my-mem notes", map[string]any{"type": "object"}),
	)
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	s := newTestMCPServer()
	a := NewPluginToolAggregator(srv.URL, "internal-tok", s, builtinNames(), time.Minute)

	if err := a.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if hub.gotInternalToken != "internal-tok" {
		t.Errorf("X-Internal-Token = %q; want %q", hub.gotInternalToken, "internal-tok")
	}

	want := []string{"list_agents", "mem0_search", "my_mem_search"}
	if got := registeredNames(t, s); !slices.Equal(got, want) {
		t.Errorf("registered tools = %v; want %v", got, want)
	}

	entry := s.ListTools()["my_mem_search"]
	if entry == nil {
		t.Fatal("my_mem_search missing")
	}
	if entry.Tool.Description != "Search my-mem notes" {
		t.Errorf("description = %q; want %q", entry.Tool.Description, "Search my-mem notes")
	}
	var schema map[string]any
	if err := json.Unmarshal(entry.Tool.RawInputSchema, &schema); err != nil {
		t.Fatalf("input schema is not valid JSON (%s): %v", entry.Tool.RawInputSchema, err)
	}
	if !reflect.DeepEqual(schema, map[string]any{"type": "object"}) {
		t.Errorf("input schema = %v; want {type:object}", schema)
	}

	schemaEntry := s.ListTools()["mem0_search"]
	if schemaEntry == nil {
		t.Fatal("mem0_search missing")
	}
	var gotSchema map[string]any
	if err := json.Unmarshal(schemaEntry.Tool.RawInputSchema, &gotSchema); err != nil {
		t.Fatalf("mem0_search input schema is not valid JSON (%s): %v", schemaEntry.Tool.RawInputSchema, err)
	}
	wantSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"q": map[string]any{"type": "string"},
		},
	}
	if !reflect.DeepEqual(gotSchema, wantSchema) {
		t.Errorf("mem0_search input schema = %v; want %v", gotSchema, wantSchema)
	}
}

func TestSync_RemovesGoneTools(t *testing.T) {
	hub := &pluginHubStub{}
	hub.setTools(
		pluginToolJSON("mem0", "search", "Search memories", map[string]any{"type": "object"}),
		pluginToolJSON("mem0", "remember", "Remember a memory", map[string]any{"type": "object"}),
	)
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	s := newTestMCPServer()
	a := NewPluginToolAggregator(srv.URL, "internal-tok", s, builtinNames(), time.Minute)

	if err := a.Sync(context.Background()); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	hub.setTools(pluginToolJSON("mem0", "search", "Search memories", map[string]any{"type": "object"}))
	if err := a.Sync(context.Background()); err != nil {
		t.Fatalf("second Sync: %v", err)
	}

	want := []string{"list_agents", "mem0_search"}
	if got := registeredNames(t, s); !slices.Equal(got, want) {
		t.Errorf("registered tools = %v; want %v", got, want)
	}
}

func TestSync_SkipsBuiltinCollision(t *testing.T) {
	hub := &pluginHubStub{}
	hub.setTools(pluginToolJSON("list", "agents", "Plugin tool shadowing a built-in", map[string]any{"type": "object"}))
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	s := newTestMCPServer()
	a := NewPluginToolAggregator(srv.URL, "internal-tok", s, builtinNames(), time.Minute)
	logs := captureAggregatorLogs(t)

	if err := a.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	want := []string{"list_agents"}
	if got := registeredNames(t, s); !slices.Equal(got, want) {
		t.Errorf("registered tools = %v; want %v", got, want)
	}
	res, err := callRegisteredTool(t, s, "list_agents", nil)
	if err != nil {
		t.Fatalf("call list_agents: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_agents returned an error result: %+v", res)
	}
	if got := res.Content[0].(mcp.TextContent).Text; got != "builtin list_agents" {
		t.Errorf("list_agents handler text = %q; want the built-in handler to survive", got)
	}
	if !strings.Contains(logs.String(), "plugin tool name collides with a built-in tool") {
		t.Errorf("logs = %q; want a warn about the built-in collision", logs.String())
	}
}

func TestSync_FailedPollKeepsTools(t *testing.T) {
	hub := &pluginHubStub{}
	hub.setTools(pluginToolJSON("mem0", "search", "Search memories", map[string]any{"type": "object"}))
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	s := newTestMCPServer()
	a := NewPluginToolAggregator(srv.URL, "internal-tok", s, builtinNames(), time.Minute)

	if err := a.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	hub.listStatus = http.StatusInternalServerError
	err := a.Sync(context.Background())
	if err == nil {
		t.Fatal("Sync returned nil; want an error when the poll fails")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %q; want it to mention the 500 status", err.Error())
	}

	want := []string{"list_agents", "mem0_search"}
	if got := registeredNames(t, s); !slices.Equal(got, want) {
		t.Errorf("registered tools after failed poll = %v; want %v", got, want)
	}
	res, err := callRegisteredTool(t, s, "mem0_search", map[string]any{"q": "x"})
	if err != nil {
		t.Fatalf("call mem0_search: %v", err)
	}
	if res.IsError {
		t.Errorf("mem0_search returned an error result after a failed poll: %+v", res)
	}
}

func TestPluginTool_CallForwardsBearerAndArguments(t *testing.T) {
	hub := &pluginHubStub{}
	hub.setTools(pluginToolJSON("mem0", "search", "Search memories", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"q": map[string]any{"type": "string"},
		},
	}))
	hub.callBody = map[string]any{
		"content": []map[string]any{{"type": "text", "text": "3 memories"}},
	}
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	s := newTestMCPServer()
	a := NewPluginToolAggregator(srv.URL, "internal-tok", s, builtinNames(), time.Minute)
	if err := a.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	entry := s.ListTools()["mem0_search"]
	if entry == nil {
		t.Fatal("mem0_search missing")
	}
	ctx := oidc.ContextWithToken(context.Background(), "user-tok")
	res, err := entry.Handler(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "mem0_search", Arguments: map[string]any{"q": "x"}},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res == nil {
		t.Fatal("handler returned a nil result")
	}
	if res.IsError {
		t.Fatalf("result is an error: %+v", res)
	}
	if len(res.Content) != 1 {
		t.Fatalf("content length = %d; want 1 (%+v)", len(res.Content), res.Content)
	}
	if got := res.Content[0].(mcp.TextContent).Text; got != "3 memories" {
		t.Errorf("result text = %q; want %q", got, "3 memories")
	}

	auth, raw, ctype, count := hub.lastCall()
	if count != 1 {
		t.Fatalf("call count = %d; want 1", count)
	}
	if auth != "Bearer user-tok" {
		t.Errorf("Authorization = %q; want %q", auth, "Bearer user-tok")
	}
	if ctype != "application/json" {
		t.Errorf("Content-Type = %q; want application/json", ctype)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("call body is not JSON (%s): %v", raw, err)
	}
	want := map[string]any{
		"pluginId":  "mem0",
		"tool":      "search",
		"arguments": map[string]any{"q": "x"},
	}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("call body = %s; want %v", raw, want)
	}
}

func TestPluginTool_AgentOnlyNotListed(t *testing.T) {
	// The hub is the only place that knows the audience of a tool: what it
	// returns here is already restricted to operator/both. The aggregator must
	// register exactly that set — no client-side guessing, nothing added and
	// nothing dropped.
	hub := &pluginHubStub{}
	hub.setTools(
		pluginToolJSON("mem0", "remember_note", "Remember", map[string]any{"type": "object"}),
		pluginToolJSON("mem0", "delete_all", "Delete everything", map[string]any{"type": "object"}),
		pluginToolJSON("my-mem", "search", "Search", map[string]any{"type": "object"}),
	)
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	s := newTestMCPServer()
	a := NewPluginToolAggregator(srv.URL, "internal-tok", s, builtinNames(), time.Minute)
	if err := a.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	want := []string{"list_agents", "mem0_delete_all", "mem0_remember_note", "my_mem_search"}
	if got := registeredNames(t, s); !slices.Equal(got, want) {
		t.Errorf("registered tools = %v; want %v", got, want)
	}
}

func TestSync_ReAddsChangedTool(t *testing.T) {
	hub := &pluginHubStub{}
	hub.setTools(pluginToolJSON("mem0", "search", "v1 description", map[string]any{"type": "object"}))
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	s := newTestMCPServer()
	a := NewPluginToolAggregator(srv.URL, "internal-tok", s, builtinNames(), time.Minute)
	if err := a.Sync(context.Background()); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	hub.setTools(pluginToolJSON("mem0", "search", "v2 description", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"q": map[string]any{"type": "string"},
		},
	}))
	if err := a.Sync(context.Background()); err != nil {
		t.Fatalf("second Sync: %v", err)
	}

	entry := s.ListTools()["mem0_search"]
	if entry == nil {
		t.Fatal("mem0_search missing after change")
	}
	if entry.Tool.Description != "v2 description" {
		t.Errorf("description = %q; want %q", entry.Tool.Description, "v2 description")
	}
	var schema map[string]any
	if err := json.Unmarshal(entry.Tool.RawInputSchema, &schema); err != nil {
		t.Fatalf("schema is not JSON (%s): %v", entry.Tool.RawInputSchema, err)
	}
	want := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"q": map[string]any{"type": "string"},
		},
	}
	if !reflect.DeepEqual(schema, want) {
		t.Errorf("schema = %v; want %v", schema, want)
	}
}

func TestSync_RegistersToolWithoutInputSchema(t *testing.T) {
	hub := &pluginHubStub{}
	hub.setTools(map[string]any{"pluginId": "mem0", "name": "ping", "description": "No schema declared"})
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	s := newTestMCPServer()
	a := NewPluginToolAggregator(srv.URL, "internal-tok", s, builtinNames(), time.Minute)
	if err := a.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	entry := s.ListTools()["mem0_ping"]
	if entry == nil {
		t.Fatal("mem0_ping missing")
	}
	if got := string(entry.Tool.RawInputSchema); got != `{"type":"object"}` {
		t.Errorf("input schema = %q; want %q", got, `{"type":"object"}`)
	}
}

func TestPluginTool_CallReturnsHubError(t *testing.T) {
	hub := &pluginHubStub{}
	hub.setTools(pluginToolJSON("mem0", "gone", "Gone", map[string]any{"type": "object"}))
	hub.callStatus = http.StatusNotFound
	hub.callBody = map[string]any{"error": "tool not found"}
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	s := newTestMCPServer()
	a := NewPluginToolAggregator(srv.URL, "internal-tok", s, builtinNames(), time.Minute)
	if err := a.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	res, err := callRegisteredTool(t, s, "mem0_gone", map[string]any{})
	if err != nil {
		t.Fatalf("handler returned Go error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("result is not an error result: %+v", res)
	}
	got := res.Content[0].(mcp.TextContent).Text
	for _, want := range []string{"404", "tool not found"} {
		if !strings.Contains(got, want) {
			t.Errorf("error text = %q; want it to contain %q", got, want)
		}
	}
}

func TestRun_DisabledWithoutInternalToken(t *testing.T) {
	hub := &pluginHubStub{}
	hub.setTools(pluginToolJSON("mem0", "search", "Search memories", map[string]any{"type": "object"}))
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	s := newTestMCPServer()
	a := NewPluginToolAggregator(srv.URL, "", s, builtinNames(), time.Minute)
	logs := captureAggregatorLogs(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.Run(ctx)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return with an empty internal token")
	}

	if hub.listCalls != 0 {
		t.Errorf("hub list calls = %d; want 0 (aggregator must not start)", hub.listCalls)
	}
	if !strings.Contains(logs.String(), "plugin tools disabled: no internal token") {
		t.Errorf("logs = %q; want the disabled message", logs.String())
	}
	if got, want := registeredNames(t, s), []string{"list_agents"}; !slices.Equal(got, want) {
		t.Errorf("registered tools = %v; want %v", got, want)
	}
}

func TestRun_SyncsImmediatelyAndStopsWithContext(t *testing.T) {
	hub := &pluginHubStub{}
	hub.setTools(pluginToolJSON("mem0", "search", "Search memories", map[string]any{"type": "object"}))
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	s := newTestMCPServer()
	a := NewPluginToolAggregator(srv.URL, "internal-tok", s, builtinNames(), 20*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.Run(ctx)
	}()

	// First poll happens immediately, then one per interval.
	if err := waitUntil(2*time.Second, func() bool {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return hub.listCalls >= 2
	}); err != nil {
		cancel()
		t.Fatalf("Run did not poll repeatedly: %v", err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}

	want := []string{"list_agents", "mem0_search"}
	if got := registeredNames(t, s); !slices.Equal(got, want) {
		t.Errorf("registered tools = %v; want %v", got, want)
	}
}

func waitUntil(timeout time.Duration, cond func() bool) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return context.DeadlineExceeded
}
