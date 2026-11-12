package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

func TestMergeMCPTools_PreservesEnabledState(t *testing.T) {
	existing := []agentv1alpha1.AgentImageTool{
		{Name: "run_shell", Kind: agentv1alpha1.AgentImageToolKindShell},
		{Name: "mcp__example-mcp__list", Kind: agentv1alpha1.AgentImageToolKindMCP, McpSource: "example-mcp"},
		{Name: "mcp__example-mcp__add", Kind: agentv1alpha1.AgentImageToolKindMCP, McpSource: "example-mcp", Disabled: true},
	}
	freshByServer := map[string][]mcpToolSummary{
		"example-mcp": {
			{Name: "list", Description: "List memories"},
			{Name: "add", Description: "Add memory"},
			{Name: "delete", Description: "Delete memory"}, // new tool
		},
	}

	got := mergeMCPTools(existing, freshByServer, []string{"example-mcp"})

	// native tool preserved
	if len(got) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(got))
	}
	if got[0].Name != "run_shell" {
		t.Errorf("expected run_shell first, got %s", got[0].Name)
	}
	// mcp__example-mcp__list: was not disabled, should remain not disabled
	listTool := findTool(got, "mcp__example-mcp__list")
	if listTool == nil {
		t.Fatal("mcp__example-mcp__list missing")
	}
	if listTool.Disabled {
		t.Error("mcp__example-mcp__list should remain enabled (Disabled=false)")
	}
	// mcp__example-mcp__add: was explicitly disabled, should stay disabled
	addTool := findTool(got, "mcp__example-mcp__add")
	if addTool == nil {
		t.Fatal("mcp__example-mcp__add missing")
	}
	if !addTool.Disabled {
		t.Error("mcp__example-mcp__add should remain disabled")
	}
	// mcp__example-mcp__delete: new — should be disabled and marked IsNew
	delTool := findTool(got, "mcp__example-mcp__delete")
	if delTool == nil {
		t.Fatal("mcp__example-mcp__delete missing")
	}
	if !delTool.Disabled {
		t.Error("new tool mcp__example-mcp__delete should start disabled")
	}
	if !delTool.IsNew {
		t.Error("new tool mcp__example-mcp__delete should be marked IsNew")
	}
}

func TestMergeMCPTools_RemovesGoneTools(t *testing.T) {
	existing := []agentv1alpha1.AgentImageTool{
		{Name: "mcp__example-mcp__list", Kind: agentv1alpha1.AgentImageToolKindMCP, McpSource: "example-mcp"},
		{Name: "mcp__example-mcp__gone", Kind: agentv1alpha1.AgentImageToolKindMCP, McpSource: "example-mcp"},
	}
	freshByServer := map[string][]mcpToolSummary{
		"example-mcp": {
			{Name: "list", Description: "List memories"},
			// "gone" is no longer on the server
		},
	}

	got := mergeMCPTools(existing, freshByServer, []string{"example-mcp"})

	if findTool(got, "mcp__example-mcp__gone") != nil {
		t.Error("mcp__example-mcp__gone should have been removed")
	}
	if findTool(got, "mcp__example-mcp__list") == nil {
		t.Error("mcp__example-mcp__list should still be present")
	}
}

func TestMergeMCPTools_DoesNotClearIsNewOnExisting(t *testing.T) {
	// A tool that was already IsNew (user hasn't saved yet) should keep IsNew
	// through a second refresh if it still exists on the server.
	existing := []agentv1alpha1.AgentImageTool{
		{Name: "mcp__example-mcp__new", Kind: agentv1alpha1.AgentImageToolKindMCP, McpSource: "example-mcp", Disabled: true, IsNew: true},
	}
	freshByServer := map[string][]mcpToolSummary{
		"example-mcp": {{Name: "new", Description: "new tool"}},
	}

	got := mergeMCPTools(existing, freshByServer, []string{"example-mcp"})
	tool := findTool(got, "mcp__example-mcp__new")
	if tool == nil {
		t.Fatal("tool missing")
	}
	if !tool.IsNew {
		t.Error("IsNew should be preserved across re-refresh")
	}
}

func findTool(tools []agentv1alpha1.AgentImageTool, name string) *agentv1alpha1.AgentImageTool {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}

// authedMCPServer stands up a streamable-HTTP MCP server that answers only to the
// bearer it expects — the shape this deployment's forgejo-mcp has. It is the real
// mcp-go server rather than a stub so the credential travels the same header the
// runtime uses. want "" means the server is anonymous.
func authedMCPServer(t *testing.T, want string) (url string, seenAuth func() string) {
	t.Helper()
	srv := mcpserver.NewMCPServer("forge-test", "1.0.0")
	srv.AddTool(
		mcp.NewTool("create_issue", mcp.WithDescription("Open an issue")),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		},
	)
	inner := mcpserver.NewStreamableHTTPServer(srv)

	var mu sync.Mutex
	var seen string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = r.Header.Get("Authorization")
		mu.Unlock()
		if want != "" && seen != "Bearer "+want {
			// Same shape as forgejo-mcp's refusal.
			http.Error(w, "Unauthorized: this server requires a per-request token", http.StatusUnauthorized)
			return
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts.URL, func() string {
		mu.Lock()
		defer mu.Unlock()
		return seen
	}
}

// refreshImage posts the refresh and returns the catalog and warnings the hub reported.
func refreshImage(t *testing.T, s *Server, name string) (tools []AgentImageToolInfo, warnings []string) {
	t.Helper()
	s.mux.HandleFunc("/api/v1/agent-images/", s.handleAgentImagePath)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent-images/"+name+"/refresh-mcp", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Image    SimpleAgentImageResponse `json:"image"`
		Warnings []string                 `json:"warnings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return out.Image.Tools, out.Warnings
}

func imageWithMCPServer(name, url, tokenFromEnv string, env []agentv1alpha1.AgentImageEnvVar) *agentv1alpha1.AgentImage {
	return &agentv1alpha1.AgentImage{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test-ns"},
		Spec: agentv1alpha1.AgentImageSpec{
			DisplayName: name,
			ImageURL:    "registry.test/agent:v1",
			Env:         env,
			MCPServers: []agentv1alpha1.AgentImageMCPServer{
				{Name: "forge", URL: url, TokenFromEnv: tokenFromEnv},
			},
		},
	}
}

// The server the image points at demands a token, and the image holds it under the
// name the registry row names: the refresh must present it, or the image ends up
// with no tools and the wizard has nothing to profile.
func TestRefreshMCP_SendsTheCredentialTheImageNames(t *testing.T) {
	url, seenAuth := authedMCPServer(t, "forge-token")
	s := testServer(t, imageWithMCPServer("img-a", url, "FORGEJO_TOKEN", []agentv1alpha1.AgentImageEnvVar{
		{Name: "FORGEJO_TOKEN", Value: "forge-token", Secret: true},
	}))

	tools, warnings := refreshImage(t, s, "img-a")

	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
	if len(tools) != 1 || tools[0].Name != "mcp__forge__create_issue" {
		t.Fatalf("expected the server's tool to be catalogued, got %+v", tools)
	}
	if got := seenAuth(); got != "Bearer forge-token" {
		t.Errorf("server saw %q, want the image's bearer", got)
	}
}

// An anonymous server stays anonymous: no name means no credential is sent, and a
// server that wants none still answers.
func TestRefreshMCP_LeavesAnAnonymousServerAnonymous(t *testing.T) {
	url, seenAuth := authedMCPServer(t, "")
	s := testServer(t, imageWithMCPServer("img-a", url, "", nil))

	tools, warnings := refreshImage(t, s, "img-a")

	if len(warnings) != 0 || len(tools) != 1 {
		t.Fatalf("expected the tool with no warnings, got tools=%+v warnings=%v", tools, warnings)
	}
	if got := seenAuth(); got != "" {
		t.Errorf("expected no Authorization header, got %q", got)
	}
}

// The name without a value is not a credential: the server still refuses, and the
// refusal surfaces as the warning it used to be rather than as an empty catalog.
func TestRefreshMCP_AnonymousWhenTheImageLacksTheVar(t *testing.T) {
	url, seenAuth := authedMCPServer(t, "forge-token")
	s := testServer(t, imageWithMCPServer("img-a", url, "FORGEJO_TOKEN", []agentv1alpha1.AgentImageEnvVar{
		{Name: "SOME_OTHER_VAR", Value: "not-it"},
	}))

	tools, warnings := refreshImage(t, s, "img-a")

	if len(tools) != 0 {
		t.Errorf("expected no tools from a refusing server, got %+v", tools)
	}
	if len(warnings) != 1 || warnings[0] == "" {
		t.Fatalf("expected one warning naming the failure, got %v", warnings)
	}
	if got := seenAuth(); got != "" {
		t.Errorf("expected no Authorization header, got %q", got)
	}
}

func TestMCPBearerForImage(t *testing.T) {
	img := agentv1alpha1.AgentImage{
		Spec: agentv1alpha1.AgentImageSpec{
			Env: []agentv1alpha1.AgentImageEnvVar{
				{Name: "FORGEJO_TOKEN", Value: "tok", Secret: true},
				{Name: "PLAIN", Value: "plain-value"},
			},
		},
	}
	cases := []struct {
		why    string
		server agentv1alpha1.AgentImageMCPServer
		want   string
	}{
		{"secret env by name", agentv1alpha1.AgentImageMCPServer{TokenFromEnv: "FORGEJO_TOKEN"}, "tok"}, // #nosec G101 -- test data, env var name not a credential
		{"non-secret env by name", agentv1alpha1.AgentImageMCPServer{TokenFromEnv: "PLAIN"}, "plain-value"},
		{"no name means anonymous", agentv1alpha1.AgentImageMCPServer{}, ""},
		{"name the image does not carry", agentv1alpha1.AgentImageMCPServer{TokenFromEnv: "MISSING"}, ""},
	}
	for _, c := range cases {
		if got := mcpBearerForImage(img, c.server); got != c.want {
			t.Errorf("%s: got %q, want %q", c.why, got, c.want)
		}
	}
}

// A registry row read on its own takes the token from an image that attaches it:
// the value lives on the image, and the row is what the console asks about.
func TestMCPBearerForServer(t *testing.T) {
	s := testServer(t,
		imageWithMCPServer("img-z", "http://forge.test/mcp", "FORGEJO_TOKEN", []agentv1alpha1.AgentImageEnvVar{
			{Name: "FORGEJO_TOKEN", Value: "from-z", Secret: true},
		}),
		imageWithMCPServer("img-a", "http://forge.test/mcp", "FORGEJO_TOKEN", []agentv1alpha1.AgentImageEnvVar{
			{Name: "FORGEJO_TOKEN", Value: "from-a", Secret: true},
		}),
	)

	if got := s.mcpBearerForServer(t.Context(), "forge", "FORGEJO_TOKEN"); got != "from-a" {
		t.Errorf("expected the first image by name to answer, got %q", got)
	}
	// A row no image attaches: nothing to resolve, so nothing is sent.
	if got := s.mcpBearerForServer(t.Context(), "nobody-attaches-me", "FORGEJO_TOKEN"); got != "" {
		t.Errorf("expected empty for an unattached row, got %q", got)
	}
	// An anonymous row is never looked up at all.
	if got := s.mcpBearerForServer(t.Context(), "forge", ""); got != "" {
		t.Errorf("expected empty without a name, got %q", got)
	}
}

// The image must actually name the server: a token for a different MCP row is not
// this one's credential.
func TestMCPBearerForServer_RequiresTheImageToAttachTheRow(t *testing.T) {
	s := testServer(t, &agentv1alpha1.AgentImage{
		ObjectMeta: metav1.ObjectMeta{Name: "img-a", Namespace: "test-ns"},
		Spec: agentv1alpha1.AgentImageSpec{
			DisplayName: "img-a",
			ImageURL:    "registry.test/agent:v1",
			Env:         []agentv1alpha1.AgentImageEnvVar{{Name: "FORGEJO_TOKEN", Value: "leaked", Secret: true}},
			MCPServers:  []agentv1alpha1.AgentImageMCPServer{{Name: "other", URL: "http://other.test/mcp", TokenFromEnv: "FORGEJO_TOKEN"}}, // #nosec G101 -- test data, env var name not a credential
		},
	})

	if got := s.mcpBearerForServer(t.Context(), "forge", "FORGEJO_TOKEN"); got != "" {
		t.Errorf("expected no token from an image that does not attach the row, got %q", got)
	}
}
