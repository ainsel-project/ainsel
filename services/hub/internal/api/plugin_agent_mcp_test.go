package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/plugincreds"
	"github.com/ainsel-project/ainsel/services/hub/internal/pluginmcp"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"k8s.io/apimachinery/pkg/runtime"
)

// An agent reaches a plugin's agent-audience tools through
// POST /api/internal/plugins/<id>/agent-mcp: the hub is the only MCP client of the
// plugin, the agent runtime only ever sees the hub, and the credential the plugin
// sees names the agent that called. These tests drive the endpoint with a real
// mcp-go client and check what arrives at an in-process plugin MCP server.

const (
	// agentMCPSecret is the hub's internal bearer secret for these fixtures.
	agentMCPSecret = "agent-mcp-internal-secret"
	// agentMCPAgent / agentMCPInvocation are the caller the tests present.
	agentMCPAgent      = "reviewer"
	agentMCPInvocation = "inv-42"
	// agentMCPPlugin is the fixture plugin id.
	agentMCPPlugin = "mem0"
)

// agentMCPServer wires the per-plugin internal routes on a dev-mode server with
// the internal bearer secret, a plugin credential issuer and a plugin MCP client.
func agentMCPServer(t *testing.T, objects ...runtime.Object) (*Server, *plugincreds.Issuer) {
	t.Helper()

	s := testServer(t, objects...)
	s.mux.HandleFunc("/api/internal/plugins/", s.handleInternalPluginRoutes)
	s.SetInternalValidateSecret(agentMCPSecret)

	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(200 - i)
	}
	issuer, err := plugincreds.NewIssuer(secret, 5*time.Minute)
	if err != nil {
		t.Fatalf("new issuer: %v", err)
	}
	s.SetPluginCredentialIssuer(issuer)
	s.SetPluginMCPClient(pluginmcp.New(&http.Client{Timeout: 10 * time.Second}))
	return s, issuer
}

// agentMCPHeaders is the header set an agent runtime sends: the internal bearer
// secret, its own name, and the invocation the calls belong to.
func agentMCPHeaders(bearer, agent, invocation string) map[string]string {
	h := map[string]string{"X-Ainsel-Agent": agent}
	if bearer != "" {
		h["Authorization"] = "Bearer " + bearer
	}
	if invocation != "" {
		h["X-Ainsel-Invocation-Id"] = invocation
	}
	return h
}

// agentMCPClient serves the hub on a local address and returns a started,
// initialized MCP client for the fixture plugin's agent endpoint.
func agentMCPClient(t *testing.T, s *Server, headers map[string]string) *mcpclient.Client {
	t.Helper()

	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)

	cl, err := mcpclient.NewStreamableHttpClient(
		ts.URL+"/api/internal/plugins/"+agentMCPPlugin+"/agent-mcp",
		transport.WithHTTPHeaders(headers),
	)
	if err != nil {
		t.Fatalf("new mcp client: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	if err := cl.Start(ctx); err != nil {
		t.Fatalf("start mcp client: %v", err)
	}
	if _, err := cl.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.ProtocolVersion20251125,
		ClientInfo:      mcp.Implementation{Name: "ainsel-agent-test", Version: "1"},
	}}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return cl
}

// agentMCPRecorded returns the headers of the n-th request of a JSON-RPC method
// that reached the fixture plugin, failing when there is no such request.
func agentMCPRecorded(t *testing.T, f *pluginToolsFixture, method string, n int) http.Header {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	seen := 0
	for _, r := range f.requests {
		if r.method == method {
			if seen == n {
				return r.header
			}
			seen++
		}
	}
	t.Fatalf("no %q request #%d recorded, saw %d of %d requests", method, n, seen, len(f.requests))
	return nil
}

// callToolText calls a tool through an MCP client and returns its text content.
func callToolText(t *testing.T, cl *mcpclient.Client, name string, args map[string]any) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := cl.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: name, Arguments: args},
	})
	if err != nil {
		return "", err
	}
	var texts []string
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			texts = append(texts, tc.Text)
		}
	}
	return strings.Join(texts, ""), nil
}

// TestAgentMCP_ListsAgentTools pins the audience rule on the agent endpoint: the
// agent-audience tool and the default "both" tool are listed with their
// description and input schema, the operator-only tool is not listed at all.
func TestAgentMCP_ListsAgentTools(t *testing.T) {
	f := newPluginToolsFixture(t)
	s, issuer := agentMCPServer(t, pluginToolsPluginCR(t, agentMCPPlugin, false, f.endpoint()))
	cl := agentMCPClient(t, s, agentMCPHeaders(agentMCPSecret, agentMCPAgent, agentMCPInvocation))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := cl.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	names := make([]string, 0, len(resp.Tools))
	for _, tool := range resp.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	if want := []string{"remember", "search"}; !equalStrings(names, want) {
		t.Fatalf("tools = %v, want %v", names, want)
	}

	for _, tool := range resp.Tools {
		switch tool.Name {
		case "search":
			if tool.Description != "Search memories" {
				t.Errorf("search description = %q, want %q", tool.Description, "Search memories")
			}
			if got := tool.InputSchema.Required; !equalStrings(got, []string{"query"}) {
				t.Errorf("search required = %v, want [query]", got)
			}
			if _, ok := tool.InputSchema.Properties["query"]; !ok {
				t.Errorf("search input schema has no query property: %+v", tool.InputSchema.Properties)
			}
		case "remember":
			if tool.Description != "Remember a memory" {
				t.Errorf("remember description = %q, want %q", tool.Description, "Remember a memory")
			}
		}
	}

	// The list is read with the plugin's own background credential, not the
	// agent's: listing tools is not an action on the agent's behalf.
	cred, n := f.credential("tools/list")
	if n != 1 {
		t.Fatalf("plugin saw %d tools/list requests, want 1", n)
	}
	claims, err := issuer.Verify(cred)
	if err != nil {
		t.Fatalf("verify list credential %q: %v", cred, err)
	}
	if claims.Kind != plugincreds.KindPlugin {
		t.Errorf("list credential kind = %q, want %q", claims.Kind, plugincreds.KindPlugin)
	}
	if claims.Sub != "plugin:"+agentMCPPlugin {
		t.Errorf("list credential sub = %q, want %q", claims.Sub, "plugin:"+agentMCPPlugin)
	}
	if claims.InvocationID != "" {
		t.Errorf("list credential invocation = %q, want empty", claims.InvocationID)
	}
}

// TestAgentMCP_ForwardsInvocationID is the happy path: the call reaches the
// plugin, carries the agent-kind credential and the invocation id.
func TestAgentMCP_ForwardsInvocationID(t *testing.T) {
	f := newPluginToolsFixture(t)
	s, issuer := agentMCPServer(t, pluginToolsPluginCR(t, agentMCPPlugin, false, f.endpoint()))
	cl := agentMCPClient(t, s, agentMCPHeaders(agentMCPSecret, agentMCPAgent, agentMCPInvocation))

	text, err := callToolText(t, cl, "remember", map[string]any{"text": "note-1"})
	if err != nil {
		t.Fatalf("call remember: %v", err)
	}
	if want := "remember:note-1"; text != want {
		t.Fatalf("tool text = %q, want %q", text, want)
	}

	if got := f.count("tools/call"); got != 1 {
		t.Fatalf("plugin saw %d tools/call requests, want 1", got)
	}
	header := agentMCPRecorded(t, f, "tools/call", 0)

	if got := header.Get("X-Ainsel-Invocation-Id"); got != agentMCPInvocation {
		t.Errorf("X-Ainsel-Invocation-Id = %q, want %q", got, agentMCPInvocation)
	}
	claims, err := issuer.Verify(header.Get("X-Ainsel-Credential"))
	if err != nil {
		t.Fatalf("verify forwarded credential: %v", err)
	}
	if claims.Kind != plugincreds.KindAgent {
		t.Errorf("credential kind = %q, want %q", claims.Kind, plugincreds.KindAgent)
	}
	if claims.Sub != "agent:"+agentMCPAgent {
		t.Errorf("credential sub = %q, want %q", claims.Sub, "agent:"+agentMCPAgent)
	}
	if claims.PluginID != agentMCPPlugin {
		t.Errorf("credential plugin = %q, want %q", claims.PluginID, agentMCPPlugin)
	}
	if claims.InvocationID != agentMCPInvocation {
		t.Errorf("credential invocation = %q, want %q", claims.InvocationID, agentMCPInvocation)
	}

	// The credential is minted for the plugin that is being called, never the
	// agent's own identity: the plugin sees an agent principal.
	if claims.Username != "" {
		t.Errorf("credential username = %q, want empty for an agent principal", claims.Username)
	}
}

// TestAgentMCP_OperatorToolRejected checks that an operator-audience tool is
// invisible to agents: mcp-go rejects the call because the hub never registered
// the tool, and the plugin is never reached for it.
func TestAgentMCP_OperatorToolRejected(t *testing.T) {
	f := newPluginToolsFixture(t)
	s, _ := agentMCPServer(t, pluginToolsPluginCR(t, agentMCPPlugin, false, f.endpoint()))
	cl := agentMCPClient(t, s, agentMCPHeaders(agentMCPSecret, agentMCPAgent, agentMCPInvocation))

	_, err := callToolText(t, cl, "reset", nil)
	if err == nil {
		t.Fatalf("calling the operator tool %q succeeded; agents must not reach it", "reset")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want it to say the tool was not found", err)
	}
	if got := f.count("tools/call"); got != 0 {
		t.Errorf("plugin saw %d tools/call requests, want 0", got)
	}
}

// TestAgentMCP_PluginWithNoMCPSpecIsNotFound pins that a plugin without an MCP
// surface answers 404 before any MCP session is built, so the plugin is never
// contacted.
func TestAgentMCP_PluginWithNoMCPSpecIsNotFound(t *testing.T) {
	p := pluginCRReady(pluginCR(agentMCPPlugin, false), true, "", "", nil)
	p.Status.Manifest = &runtime.RawExtension{Raw: []byte(
		`{"id":"` + agentMCPPlugin + `","pluginApi":"1.1","version":"1.2.3"}`)}
	p.Status.VerifiedEndpoints = []string{"10.0.0.9:8080"}

	s, _ := agentMCPServer(t, p)
	rec := postAgentMCP(t, s, agentMCPHeaders(agentMCPSecret, agentMCPAgent, agentMCPInvocation))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if msg := decodeAgentMCPError(t, rec); msg != "plugin not found" {
		t.Errorf("error = %q, want %q", msg, "plugin not found")
	}
}

// TestAgentMCP_DisabledPlugin and the other unavailable shapes answer 404:
// disabled, missing and not Ready are indistinguishable from outside.
func TestAgentMCP_DisabledPlugin(t *testing.T) {
	tests := []struct {
		name string
		seed func(t *testing.T) runtime.Object
	}{
		{"missing", func(*testing.T) runtime.Object { return nil }},
		{"disabled", func(t *testing.T) runtime.Object {
			return pluginToolsPluginCR(t, agentMCPPlugin, true, "10.0.0.9:8080")
		}},
		{"not ready", func(t *testing.T) runtime.Object {
			return pluginCRReady(pluginCR(agentMCPPlugin, false), false, "Progressing", "applying", nil)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objects []runtime.Object
			if obj := tt.seed(t); obj != nil {
				objects = append(objects, obj)
			}
			s, _ := agentMCPServer(t, objects...)
			rec := postAgentMCP(t, s, agentMCPHeaders(agentMCPSecret, agentMCPAgent, agentMCPInvocation))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestAgentMCP_RequiresInternalToken pins the auth gate: the hub's internal bearer
// secret is required and nothing else is accepted — not a user JWT, not a plugin
// credential, not the X-Internal-Token header the other internal routes use. With
// the secret but no X-Ainsel-Agent the caller is unknown: 400.
func TestAgentMCP_RequiresInternalToken(t *testing.T) {
	userJWT := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c2VyLTEifQ.not-a-signature"

	tests := []struct {
		name             string
		secret           string
		headers          map[string]string
		want             int
		pluginCredential bool
	}{
		{"no token", agentMCPSecret, map[string]string{"X-Ainsel-Agent": agentMCPAgent}, http.StatusUnauthorized, false},
		{"user bearer", agentMCPSecret, agentMCPHeaders(userJWT, agentMCPAgent, agentMCPInvocation), http.StatusUnauthorized, false},
		{"plugin credential bearer", agentMCPSecret, nil, http.StatusUnauthorized, true},
		{"wrong bearer", agentMCPSecret, agentMCPHeaders("not-the-secret", agentMCPAgent, agentMCPInvocation), http.StatusUnauthorized, false},
		{"internal token header only", agentMCPSecret, map[string]string{
			"X-Internal-Token": agentMCPSecret,
			"X-Ainsel-Agent":   agentMCPAgent,
		}, http.StatusUnauthorized, false},
		{"token without agent header", agentMCPSecret, map[string]string{
			"Authorization": "Bearer " + agentMCPSecret,
		}, http.StatusBadRequest, false},
		{"internal endpoints disabled", "", agentMCPHeaders(agentMCPSecret, agentMCPAgent, agentMCPInvocation), http.StatusServiceUnavailable, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newPluginToolsFixture(t)
			s, issuer := agentMCPServer(t, pluginToolsPluginCR(t, agentMCPPlugin, false, f.endpoint()))
			s.SetInternalValidateSecret(tt.secret)

			headers := tt.headers
			if tt.pluginCredential {
				// A real, valid plugin credential: this endpoint is not a
				// plugin-facing route, so it must not open it either.
				headers = agentMCPHeaders(mintPluginPluginToken(t, issuer, agentMCPPlugin, nil, nil), agentMCPAgent, agentMCPInvocation)
			}

			rec := postAgentMCP(t, s, headers)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
			if got := f.total(); got != 0 {
				t.Errorf("plugin saw %d requests, want 0", got)
			}
		})
	}
}

// TestAgentMCP_AgentHeader pins the X-Ainsel-Agent rule: it is required and must
// be a well-formed agent name, because it becomes the credential subject.
func TestAgentMCP_AgentHeader(t *testing.T) {
	tests := []struct {
		name  string
		agent string
		ok    bool
	}{
		{"missing", "", false},
		{"uppercase", "Reviewer", false},
		{"underscore", "my_agent", false},
		{"leading dash", "-reviewer", false},
		{"trailing dash", "reviewer-", false},
		{"single char", "r", true},
		{"digits and dash", "review-2", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newPluginToolsFixture(t)
			s, _ := agentMCPServer(t, pluginToolsPluginCR(t, agentMCPPlugin, false, f.endpoint()))
			headers := agentMCPHeaders(agentMCPSecret, tt.agent, agentMCPInvocation)
			if tt.agent == "" {
				delete(headers, "X-Ainsel-Agent")
			}

			rec := postAgentMCP(t, s, headers)
			want := http.StatusBadRequest
			if tt.ok {
				want = http.StatusOK
				if msg := decodeAgentMCPError(t, rec); msg == "missing or invalid X-Ainsel-Agent" {
					t.Fatalf("valid agent name %q was rejected", tt.agent)
				}
			} else if msg := decodeAgentMCPError(t, rec); msg != "missing or invalid X-Ainsel-Agent" {
				t.Fatalf("error = %q, want %q", msg, "missing or invalid X-Ainsel-Agent")
			}
			if rec.Code != want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, want, rec.Body.String())
			}
		})
	}
}

// TestAgentMCP_UnreachablePlugin is a 502: the hub knows the plugin exists but
// cannot open an MCP session to it.
func TestAgentMCP_UnreachablePlugin(t *testing.T) {
	addr := closedAddr(t)
	s, _ := agentMCPServer(t, pluginToolsPluginCR(t, agentMCPPlugin, false, addr))

	rec := postAgentMCP(t, s, agentMCPHeaders(agentMCPSecret, agentMCPAgent, agentMCPInvocation))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502: %s", rec.Code, rec.Body.String())
	}
	if msg := decodeAgentMCPError(t, rec); msg != "plugin unreachable" {
		t.Errorf("error = %q, want %q", msg, "plugin unreachable")
	}
}

// TestInternalPluginRoutes_Dispatch pins the dispatcher: the existing credentials
// action keeps working through it, an unknown action is 404, and so is anything
// that is not exactly <id>/<action>.
func TestInternalPluginRoutes_Dispatch(t *testing.T) {
	f := newPluginToolsFixture(t)
	s, _ := agentMCPServer(t, pluginToolsPluginCR(t, agentMCPPlugin, false, f.endpoint()))

	tests := []struct {
		method string
		path   string
		want   int
	}{
		// Routed to the agent MCP handler, which demands the bearer secret.
		{http.MethodPost, "/api/internal/plugins/" + agentMCPPlugin + "/agent-mcp", http.StatusUnauthorized},
		// Routed to the credentials handler, which checks the source address.
		{http.MethodPost, "/api/internal/plugins/" + agentMCPPlugin + "/credentials", http.StatusForbidden},
		{http.MethodPost, "/api/internal/plugins/" + agentMCPPlugin + "/destroy", http.StatusNotFound},
		{http.MethodPost, "/api/internal/plugins/destroy", http.StatusNotFound},
		{http.MethodPost, "/api/internal/plugins/", http.StatusNotFound},
		{http.MethodPost, "/api/internal/plugins/" + agentMCPPlugin + "/agent-mcp/extra", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			s.mux.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("%s %s → %d, want %d: %s", tt.method, tt.path, rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

// TestInternalPluginCredentials_StillRouted drives the credentials action through
// the dispatcher end to end, so the restructure cannot silently break the plugin
// side of the endpoint.
func TestInternalPluginCredentials_StillRouted(t *testing.T) {
	s, _ := agentMCPServer(t, pluginToolsPluginCR(t, agentMCPPlugin, false, "10.0.0.5:8080"))

	req := httptest.NewRequest(http.MethodPost, "/api/internal/plugins/"+agentMCPPlugin+"/credentials", nil)
	req.RemoteAddr = "10.0.0.5:51234"
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expiresAt"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	if !strings.HasPrefix(body.Token, plugincreds.TokenPrefix) {
		t.Errorf("token = %q, want prefix %q", body.Token, plugincreds.TokenPrefix)
	}
	if body.ExpiresAt == "" {
		t.Error("expiresAt is empty")
	}
}

// agentMCPInitialize is a well-formed MCP initialize request, so a request that
// passes the hub's own checks gets an answer from the MCP layer instead of a
// transport complaint about an empty body.
const agentMCPInitialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + mcp.ProtocolVersion20251125 + `","clientInfo":{"name":"ainsel-agent-test","version":"1"},"capabilities":{}}}`

// postAgentMCP posts to the agent MCP endpoint of the fixture plugin.
func postAgentMCP(t *testing.T, s *Server, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/internal/plugins/"+agentMCPPlugin+"/agent-mcp", strings.NewReader(agentMCPInitialize))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec
}

// decodeAgentMCPError pulls the error message out of a hub error body.
func decodeAgentMCPError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body.Error
}

// closedAddr returns a host:port that refuses connections.
func closedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// equalStrings compares two string slices exactly.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
