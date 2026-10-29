package api

// TestPluginEndToEnd drives the plugin system from end to end: a real HTTP hub
// with the production route table, the fixture plugin behind a second HTTP
// server, and nothing stubbed in between. Unit tests pin one seam at a time;
// this one pins that the seams fit together, because every failure mode that
// makes a plugin unusable lives at a seam — a header the proxy sets but the
// callback does not read, a credential the hub mints but the plugin cannot
// verify, a manifest path the syncer promises and the plugin does not serve.
//
// The five legs are the five ways anything reaches a plugin, and each is
// asserted with the exact bytes or fields a real caller sees:
//
//  1. The console's UI bundle, fetched with no session at all: the plugin's
//     module is public by design, and the hub adds no credential to it.
//  2. The plugin's own API, called as the signed-in user: the hub attaches a
//     credential and the plugin asks the hub what that user may read. This is
//     the only leg where the answer differs per caller, so it is where group
//     visibility is proven end to end rather than at the checker alone.
//  3. The operator tool surface: services/mcp lists tools over the internal
//     token and a console user calls one.
//  4. The agent tool surface: an agent runtime speaks MCP to the hub, which
//     speaks MCP to the plugin, and the operator-only tool is absent.
//  5. The registry sync: the hub turns the same manifest into an MCP server row
//     and a skill row whose body it read off the plugin itself.
//
// Everything shares one hub, one plugin and one group model, so a subtest that
// passes while another's wiring is broken cannot hide it.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	"github.com/ainsel-project/ainsel/services/hub/internal/invocations/invocationstest"
	"github.com/ainsel-project/ainsel/services/hub/internal/mcpservers"
	"github.com/ainsel-project/ainsel/services/hub/internal/plugincreds"
	"github.com/ainsel-project/ainsel/services/hub/internal/pluginmcp"
	"github.com/ainsel-project/ainsel/services/hub/internal/pluginsync"
	"github.com/ainsel-project/ainsel/services/hub/internal/plugintest/hello"
	connectorv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

const (
	// e2ePlugin is the fixture plugin's id, which the manifest, the plugin
	// resource and every URL below agree on.
	e2ePlugin = "hello"

	// e2eUser is the only signed-in user in this test. It is not an admin: an
	// admin sees every agent regardless of groups, which would make the
	// visibility leg prove nothing.
	e2eUser = "u1"

	// e2eAgent is the agent the agent-tool leg names in X-Ainsel-Agent. It sits
	// in g1, the group e2eUser may read. e2eAgentOther sits in g2, which the
	// user may not.
	e2eAgent      = "a1"
	e2eAgentOther = "a2"

	// e2eInvocation travels in X-Ainsel-Invocation-Id and must come back out of
	// the plugin's tool verbatim, proving the hub forwarded it.
	e2eInvocation = "inv-7"

	// e2eInternalSecret is the hub's cluster-internal bearer: the credential an
	// agent pod sends for a plugin's agent MCP endpoint, named by the registry
	// row the syncer writes.
	e2eInternalSecret = "e2e-internal-secret-0123456789abcdef" // #nosec G101 -- test fixture, not a credential

	// e2eCredSecret signs plugin credentials. Any 32+ bytes work; the hub and
	// nothing else verifies them here, since the plugin only echoes it back to
	// the hub's own callback.
	e2eCredSecret = "e2e-plugin-credential-secret-0123456789" // #nosec G101 -- test fixture, not a credential

	// e2eSelfURL is the hub's in-cluster address, the base every registry URL
	// the syncer writes is built from. It is deliberately not the httptest
	// address: an agent outside the cluster must not be handed a URL it can
	// reach only from inside the test process.
	e2eSelfURL = "http://hub-backend.test-ns.svc.cluster.local:8080"

	// e2eStaticDir is the fixture plugin's static tree, read from disk so the
	// bytes the hub served or stored can be compared with the bytes the plugin
	// ships. Relative to this package, where `go test` runs.
	e2eStaticDir = "../plugintest/hello/static"
)

// pluginEndToEnd is one running install: the hub behind a real listener, the
// fixture plugin behind another, and the two registries the syncer writes to.
type pluginEndToEnd struct {
	s     *Server
	base  string // hub base URL, e.g. http://127.0.0.1:34567
	store *fakeAuthzStore
	mcp   *e2eMCPRegistry
	skill *e2eSkillRegistry
}

// newPluginEndToEnd wires the install the subtests share.
//
// The two servers are started in the only order that works: the hub has to be
// listening before the fixture can be built, because the fixture is given the
// hub's address to call back into. The Plugin resource is created without
// verifiedEndpoints and updated with the fixture's address afterwards, which is
// what the reconciler does after its first successful probe.
func newPluginEndToEnd(t *testing.T) *pluginEndToEnd {
	t.Helper()

	// The Plugin as the reconciler would leave it, minus the endpoint: the
	// manifest is the fixture's own, byte for byte, so a path the manifest
	// promises has to be a path the fixture really serves.
	plugin := pluginCRReady(pluginCR(e2ePlugin, false), true, "Ready", "plugin is ready", []string{"agents:read"})
	plugin.Spec.ApprovedPermissions = []string{"agents:read"}
	plugin.Status.Manifest = &runtime.RawExtension{Raw: hello.Manifest()}

	// The production constructor, so the route table is the one a real hub
	// serves rather than a test's shortlist.
	s := New(newFakeClient(plugin), "test-ns", ConnectorConfig{}, nil, invocationstest.New(100), nil, nil, nil, &Config{})

	store := newFakeAuthzStore()
	ctx := context.Background()
	if _, err := store.CreateGroup(ctx, "g1", "Platform"); err != nil {
		t.Fatalf("create group g1: %v", err)
	}
	if _, err := store.CreateGroup(ctx, "g2", "Finance"); err != nil {
		t.Fatalf("create group g2: %v", err)
	}
	if err := store.SetResourceGroup(ctx, "agent", e2eAgent, "g1", false); err != nil {
		t.Fatalf("file agent %s under g1: %v", e2eAgent, err)
	}
	if err := store.SetResourceGroup(ctx, "agent", e2eAgentOther, "g2", false); err != nil {
		t.Fatalf("file agent %s under g2: %v", e2eAgentOther, err)
	}
	if _, err := store.UpsertUser(ctx, e2eUser, "ada@example.com", "ada"); err != nil {
		t.Fatalf("create user %s: %v", e2eUser, err)
	}
	if err := store.AddGroupMember(ctx, "g1", e2eUser, authz.RoleReader); err != nil {
		t.Fatalf("add %s to g1: %v", e2eUser, err)
	}

	// The checker's group roles come from the cache, whose loader is the only
	// place the group model is stated for this install: u1 is a reader in g1
	// and nothing in g2. A plugin-credential request carries no principal, so
	// this is the path the checker takes — exactly the one a real hub fills
	// from Postgres.
	loader := func(userID string) (map[string]authz.GroupRole, error) {
		if userID != e2eUser {
			return nil, nil
		}
		return map[string]authz.GroupRole{"g1": authz.RoleReader}, nil
	}
	s.SetAuthZ(store, authz.NewChecker(store, authz.NewGroupCache(loader, time.Minute)))

	issuer, err := plugincreds.NewIssuer([]byte(e2eCredSecret), 5*time.Minute)
	if err != nil {
		t.Fatalf("plugin credential issuer: %v", err)
	}
	s.SetPluginCredentialIssuer(issuer)
	s.SetInternalValidateSecret(e2eInternalSecret)
	s.SetPluginMCPClient(pluginmcp.New(&http.Client{Timeout: 20 * time.Second}))

	// Production wiring, from cmd/hub/wire.go: plugin credentials are checked
	// by the plugin middleware, everything else falls through to a chain that
	// here stands in for OIDC and resolves every request to e2eUser.
	s.SetAuthMiddleware(s.PluginCredentialMiddleware(
		authz.NewDisabledGuard(store),
		func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r.WithContext(oidc.ContextWithUser(r.Context(), &oidc.User{
					Sub:      e2eUser,
					Username: "ada",
					Email:    "ada@example.com",
				})))
			})
		},
	))

	hub := httptest.NewServer(s)
	t.Cleanup(hub.Close)

	fixture := httptest.NewServer(hello.New(hub.URL))
	t.Cleanup(fixture.Close)

	var live connectorv1alpha1.Plugin
	key := types.NamespacedName{Name: e2ePlugin, Namespace: "test-ns"}
	if err := s.client.Get(ctx, key, &live); err != nil {
		t.Fatalf("get plugin: %v", err)
	}
	live.Status.VerifiedEndpoints = []string{strings.TrimPrefix(fixture.URL, "http://")}
	if err := s.client.Update(ctx, &live); err != nil {
		t.Fatalf("record verified endpoint: %v", err)
	}

	return &pluginEndToEnd{
		s:     s,
		base:  hub.URL,
		store: store,
		mcp:   newE2EMCPRegistry(),
		skill: newE2ESkillRegistry(),
	}
}

// get issues a plain GET against the running hub.
func (e *pluginEndToEnd) get(t *testing.T, path string, headers map[string]string) (int, http.Header, []byte) {
	t.Helper()
	return e.roundTrip(t, http.MethodGet, path, headers, nil)
}

// post issues a POST with a JSON body against the running hub.
func (e *pluginEndToEnd) post(t *testing.T, path string, headers map[string]string, body string) (int, http.Header, []byte) {
	t.Helper()
	return e.roundTrip(t, http.MethodPost, path, headers, strings.NewReader(body))
}

// roundTrip performs one request over the network — not through an
// httptest.ResponseRecorder — so the auth chain, the reverse proxy and the
// plugin's own callback all run as they do in a cluster.
func (e *pluginEndToEnd) roundTrip(t *testing.T, method, path string, headers map[string]string, body io.Reader) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, e.base+path, body)
	if err != nil {
		t.Fatalf("build request %s %s: %v", method, path, err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body of %s %s: %v", method, path, err)
	}
	return resp.StatusCode, resp.Header, raw
}

func TestPluginEndToEnd(t *testing.T) {
	e := newPluginEndToEnd(t)

	t.Run("ui module is served without a session", func(t *testing.T) {
		// A browser loads a plugin's console module before it has any reason to
		// hold a session for the plugin, so the route is auth-exempt and the
		// proxy attaches no credential. The bytes must be the module the plugin
		// was built with, not a wrapper and not an error page.
		status, header, body := e.get(t, "/api/v1/plugins/"+e2ePlugin+"/ui/index.js", nil)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", status, body)
		}
		if got, want := header.Get("Content-Type"), "text/javascript; charset=utf-8"; got != want {
			t.Errorf("Content-Type = %q, want %q", got, want)
		}
		want, err := os.ReadFile(e2eStaticDir + "/ui/index.js")
		if err != nil {
			t.Fatalf("read the module the plugin ships: %v", err)
		}
		if string(body) != string(want) {
			t.Errorf("module body =\n%s\nwant\n%s", body, want)
		}
		// The console imports this module as an ES module: an export is the
		// contract, restated here so a body that is empty-but-equal cannot pass.
		if !strings.Contains(string(body), "export function AgentPanel") {
			t.Error("module has no AgentPanel export")
		}
	})

	t.Run("plugin API answers with only the agents the caller may read", func(t *testing.T) {
		// The fixture has no group model of its own: it can only answer by
		// asking the hub, as the caller, with the credential the proxy attached.
		// So a body of exactly ["a1"] means the credential travelled out to the
		// plugin and back in to the hub and was resolved against g1 — and that
		// a2, owned by g2, stayed invisible.
		status, _, body := e.get(t, "/api/v1/plugins/"+e2ePlugin+"/api/visible-agents?names="+e2eAgent+","+e2eAgentOther, nil)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", status, body)
		}
		if got, want := string(body), `{"visible":["a1"]}`; got != want {
			t.Errorf("body = %s, want %s", got, want)
		}
	})

	t.Run("operator tools are listed over the internal token", func(t *testing.T) {
		// services/mcp asks the hub, because no service outside the hub may
		// hold a plugin's URL or credential. Operator and "both" tools appear;
		// the agent-only tool does not.
		status, _, body := e.get(t, "/api/internal/plugin-tools", map[string]string{"X-Internal-Token": e2eInternalSecret})
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", status, body)
		}
		var list pluginToolsListResponse
		if err := json.Unmarshal(body, &list); err != nil {
			t.Fatalf("decode tools: %v (body %s)", err, body)
		}
		if len(list.Tools) != 2 {
			t.Fatalf("tools = %d, want 2: %s", len(list.Tools), body)
		}
		want := []struct {
			name        string
			description string
		}{
			{"greet", "Greet someone by name"},
			{"reset", "Forget everything this plugin remembered"},
		}
		for i, tool := range list.Tools {
			if tool.PluginID != e2ePlugin {
				t.Errorf("tools[%d].pluginId = %q, want %q", i, tool.PluginID, e2ePlugin)
			}
			if tool.Name != want[i].name {
				t.Errorf("tools[%d].name = %q, want %q", i, tool.Name, want[i].name)
			}
			if tool.Description != want[i].description {
				t.Errorf("tools[%d].description = %q, want %q", i, tool.Description, want[i].description)
			}
			// The schema is the plugin's own, unrewritten: services/mcp prefixes
			// the name but must not have to repair the arguments.
			var schema struct {
				Type       string         `json:"type"`
				Properties map[string]any `json:"properties"`
				Required   []string       `json:"required"`
			}
			if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
				t.Fatalf("tools[%d].inputSchema: %v", i, err)
			}
			if schema.Type != "object" {
				t.Errorf("tools[%d].inputSchema type = %q, want object", i, schema.Type)
			}
			if tool.Name == "greet" {
				if _, ok := schema.Properties["name"]; !ok {
					t.Errorf("greet inputSchema has no name property: %s", tool.InputSchema)
				}
				if !equalStrings(schema.Required, []string{"name"}) {
					t.Errorf("greet required = %v, want [name]", schema.Required)
				}
			}
		}
	})

	t.Run("a user calls an operator tool through the hub", func(t *testing.T) {
		// The console's own path: a signed-in user, one tool, the plugin's
		// CallToolResult returned unchanged.
		status, _, body := e.post(t, "/api/v1/plugin-tools/call", nil,
			`{"pluginId":"`+e2ePlugin+`","tool":"greet","arguments":{"name":"Ada"}}`)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", status, body)
		}
		// Decoded from the wire rather than into mcp.CallToolResult: this is what
		// a caller actually receives, and the shape is the contract services/mcp
		// passes on to the model.
		var res struct {
			IsError bool `json:"isError"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(body, &res); err != nil {
			t.Fatalf("decode call result: %v (body %s)", err, body)
		}
		if res.IsError {
			t.Errorf("result is an error: %s", body)
		}
		if len(res.Content) != 1 {
			t.Fatalf("content = %d items, want 1: %s", len(res.Content), body)
		}
		if got, want := res.Content[0].Type, "text"; got != want {
			t.Errorf("content[0].type = %q, want %q", got, want)
		}
		if got, want := res.Content[0].Text, "hello, Ada"; got != want {
			t.Errorf("text = %q, want %q", got, want)
		}
	})

	t.Run("an agent cannot reach an operator tool by name", func(t *testing.T) {
		// The audience rule holds on the operator surface too: `remember` is
		// agent-only, so calling it by its exact name is a 404 — the same answer
		// a nonexistent tool gets, so the endpoint discloses nothing.
		status, _, body := e.post(t, "/api/v1/plugin-tools/call", nil,
			`{"pluginId":"`+e2ePlugin+`","tool":"remember","arguments":{}}`)
		if status != http.StatusNotFound {
			t.Fatalf("status = %d, want 404: %s", status, body)
		}
		if got, want := strings.TrimSpace(string(body)), `{"error":"tool not found"}`; got != want {
			t.Errorf("body = %s, want %s", got, want)
		}
	})

	t.Run("agent MCP lists and calls agent tools", func(t *testing.T) {
		cl := e.agentClient(t)

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
		// reset is operator-only and must not be listed here, and greet is
		// listed because an unlisted tool is "both".
		if want := []string{"greet", "remember"}; !equalStrings(names, want) {
			t.Fatalf("tools = %v, want %v", names, want)
		}

		// The invocation id has to survive the hub's proxy: the fixture stores
		// nothing, so the only way it can answer with "inv-7" is by having been
		// given it by the hub.
		text, err := callToolText(t, cl, "remember", map[string]any{"text": "note-1"})
		if err != nil {
			t.Fatalf("call remember: %v", err)
		}
		if want := "remembered for " + e2eInvocation; text != want {
			t.Errorf("text = %q, want %q", text, want)
		}

		text, err = callToolText(t, cl, "greet", map[string]any{"name": "Ada"})
		if err != nil {
			t.Fatalf("call greet: %v", err)
		}
		if want := "hello, Ada"; text != want {
			t.Errorf("text = %q, want %q", text, want)
		}
	})

	t.Run("agent MCP refuses the operator tool", func(t *testing.T) {
		cl := e.agentClient(t)
		if _, err := callToolText(t, cl, "reset", nil); err == nil {
			t.Fatal("calling the operator tool succeeded; agents must not reach it")
		} else if !strings.Contains(err.Error(), "not found") {
			t.Errorf("error = %v, want it to say the tool was not found", err)
		}
	})

	t.Run("registry sync registers the plugin's MCP server and skill", func(t *testing.T) {
		// The last leg is not an HTTP request but the pass that makes a plugin
		// discoverable to agents at all: the same Plugin resource and manifest
		// must produce one MCP server row pointing at the hub's agent endpoint,
		// and one skill row whose body was read off the plugin itself.
		syncer := pluginsync.New(
			e.s.client, "test-ns", e.mcp, e.skill, e2eSelfURL,
			pluginsync.NewHTTPSkillFetcher(&http.Client{Timeout: 20 * time.Second}),
		)
		syncer.SetResourceVisibility(e.store)
		if err := syncer.SyncOnce(context.Background()); err != nil {
			t.Fatalf("sync: %v", err)
		}

		if got := len(e.mcp.rows); got != 1 {
			t.Fatalf("mcp rows = %d, want 1: %v", got, e.mcp.rows)
		}
		row, ok := e.mcp.rows["plugin-"+e2ePlugin]
		if !ok {
			t.Fatalf("mcp rows = %v, want the key %q", e.mcp.rows, "plugin-"+e2ePlugin)
		}
		// The URL is the hub's own agent endpoint at the hub's cluster address:
		// the plugin's real host and port must not leak into a row an agent
		// pod reads.
		wantURL := e2eSelfURL + "/api/internal/plugins/" + e2ePlugin + "/agent-mcp"
		if row.URL != wantURL {
			t.Errorf("row.URL = %q, want %q", row.URL, wantURL)
		}
		if row.DisplayName != "Hello" {
			t.Errorf("row.DisplayName = %q, want %q", row.DisplayName, "Hello")
		}
		if row.Description != "Tools of the Hello plugin" {
			t.Errorf("row.Description = %q, want %q", row.Description, "Tools of the Hello plugin")
		}
		if row.TokenFromEnv != pluginsync.MCPServerTokenFromEnv {
			t.Errorf("row.TokenFromEnv = %q, want %q", row.TokenFromEnv, pluginsync.MCPServerTokenFromEnv)
		}
		if row.ManagedBy != mcpservers.ManagedByPlugin {
			t.Errorf("row.ManagedBy = %q, want %q", row.ManagedBy, mcpservers.ManagedByPlugin)
		}

		if got := len(e.skill.rows); got != 1 {
			t.Fatalf("skill rows = %d, want 1: %v", got, e.skill.rows)
		}
		skill, ok := e.skill.rows[e2ePlugin+"-hello-usage"]
		if !ok {
			t.Fatalf("skill rows = %v, want the key %q", e.skill.rows, e2ePlugin+"-hello-usage")
		}
		if skill.Name != e2ePlugin+"-hello-usage" {
			t.Errorf("skill.Name = %q, want %q", skill.Name, e2ePlugin+"-hello-usage")
		}
		if skill.Description != "How agents use the hello plugin" {
			t.Errorf("skill.Description = %q, want %q", skill.Description, "How agents use the hello plugin")
		}
		if !equalStrings(skill.Tags, []string{"plugin:" + e2ePlugin}) {
			t.Errorf("skill.Tags = %v, want [plugin:%s]", skill.Tags, e2ePlugin)
		}
		// The body came over HTTP from the path the manifest gave. Comparing it
		// with the file on disk pins manifest path, fetch and storage together.
		want, err := os.ReadFile(e2eStaticDir + "/skills/hello-usage.md")
		if err != nil {
			t.Fatalf("read the skill document the plugin ships: %v", err)
		}
		if skill.Body != string(want) {
			t.Errorf("skill.Body =\n%s\nwant\n%s", skill.Body, want)
		}
	})
}

// agentClient returns a live MCP client against the hub's agent endpoint for the
// fixture plugin, as an agent runtime's runtime would build one: the registry
// row's URL and the internal secret from the named env var, plus the two headers
// that name the caller.
func (e *pluginEndToEnd) agentClient(t *testing.T) *mcpclient.Client {
	t.Helper()

	cl, err := mcpclient.NewStreamableHttpClient(
		e.base+"/api/internal/plugins/"+e2ePlugin+"/agent-mcp",
		transport.WithHTTPHeaders(map[string]string{
			"Authorization":          "Bearer " + e2eInternalSecret,
			"X-Ainsel-Agent":         e2eAgent,
			"X-Ainsel-Invocation-Id": e2eInvocation,
		}),
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
		ClientInfo:      mcp.Implementation{Name: "ainsel-e2e-test", Version: "1"},
	}}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return cl
}

// e2eMCPRegistry is the MCP server registry the syncer writes through, kept in a
// map so a test asserts the exact set of rows one pass produced.
type e2eMCPRegistry struct {
	rows map[string]*mcpservers.MCPServer
}

func newE2EMCPRegistry() *e2eMCPRegistry {
	return &e2eMCPRegistry{rows: map[string]*mcpservers.MCPServer{}}
}

func (f *e2eMCPRegistry) List(context.Context) ([]*mcpservers.MCPServer, error) {
	out := make([]*mcpservers.MCPServer, 0, len(f.rows))
	for _, r := range f.rows {
		cp := *r
		out = append(out, &cp)
	}
	return out, nil
}

func (f *e2eMCPRegistry) Upsert(_ context.Context, m *mcpservers.MCPServer) error {
	cp := *m
	f.rows[cp.Name] = &cp
	return nil
}

func (f *e2eMCPRegistry) DeleteManaged(_ context.Context, name string) error {
	delete(f.rows, name)
	return nil
}

// e2eSkillRegistry is the plugin-owned half of the skill registry.
type e2eSkillRegistry struct {
	rows map[string]pluginsync.ManagedSkill
}

func newE2ESkillRegistry() *e2eSkillRegistry {
	return &e2eSkillRegistry{rows: map[string]pluginsync.ManagedSkill{}}
}

func (f *e2eSkillRegistry) ListManaged(context.Context) ([]pluginsync.ManagedSkill, error) {
	out := make([]pluginsync.ManagedSkill, 0, len(f.rows))
	for _, s := range f.rows {
		out = append(out, s)
	}
	return out, nil
}

func (f *e2eSkillRegistry) Upsert(_ context.Context, s pluginsync.ManagedSkill) error {
	f.rows[s.ID] = s
	return nil
}

func (f *e2eSkillRegistry) Delete(_ context.Context, id string) error {
	delete(f.rows, id)
	return nil
}
