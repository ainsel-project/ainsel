package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

// --- Token-authenticated MCP servers (issue #251) ---------------------------

func refreshMCPRequest(t *testing.T, s *Server, name string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent-images/"+name+"/refresh-mcp", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec.Code, out
}

func TestAgentImageRefreshMCP_UsesTokenFromImageEnv(t *testing.T) {
	const sentinel = "tok-refresh-sentinel" // #nosec G101 -- test fixture
	srv, authSeen := fakeTokenMCPServer(t, sentinel, false)

	img := mcpImage("img-refresh-token1", "forgejo", srv.URL, "FORGEJO_PAT",
		agentv1alpha1.AgentImageEnvVar{Name: "FORGEJO_PAT", Value: sentinel, Secret: true})
	s := testServer(t, img)
	registerAgentImageRoutes(s)

	code, out := refreshMCPRequest(t, s, "img-refresh-token1")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", code, out)
	}
	if w, _ := out["warnings"].([]any); len(w) != 0 {
		t.Errorf("expected no warnings, got %v", w)
	}
	image, _ := out["image"].(map[string]any)
	tools, _ := image["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 merged tool, got %v", tools)
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "mcp__forgejo__echo" {
		t.Errorf("tool name: %v", tool["name"])
	}
	// The server must have been asked with the bearer, never anonymously.
	for _, a := range authSeen() {
		if a != "Bearer "+sentinel {
			t.Errorf("expected bearer token on every call, saw %q", a)
		}
	}
	// The secret value must not be echoed back in the response body.
	if strings.Contains(fmt.Sprint(out), sentinel) {
		t.Errorf("response leaked the secret value: %v", out)
	}
}

func TestAgentImageRefreshMCP_MissingEnvStaysAnonymous(t *testing.T) {
	// The row names a var the image does not define: resolution finds nothing,
	// the listing stays anonymous (never an error), and the failure is a
	// per-server warning — the pre-existing empty-merge behaviour.
	const sentinel = "tok-refresh-absent" // #nosec G101 -- test fixture
	srv, authSeen := fakeTokenMCPServer(t, sentinel, false)

	img := mcpImage("img-refresh-noenv1", "forgejo", srv.URL, "FORGEJO_PAT")
	s := testServer(t, img)
	registerAgentImageRoutes(s)

	code, out := refreshMCPRequest(t, s, "img-refresh-noenv1")
	if code != http.StatusOK {
		t.Fatalf("expected 200 with warnings, got %d: %v", code, out)
	}
	w, _ := out["warnings"].([]any)
	if len(w) != 1 || !strings.Contains(fmt.Sprint(w[0]), "forgejo") {
		t.Errorf("expected one warning naming forgejo, got %v", w)
	}
	for _, a := range authSeen() {
		if a != "" {
			t.Errorf("unresolvable token must stay anonymous, saw %q", a)
		}
	}
}

func TestAgentImageRefreshMCP_WarningDoesNotLeakToken(t *testing.T) {
	// The server rejects even the correct bearer and echoes the Authorization
	// header back in its 401 body: the recorded warning must not carry the
	// credential.
	const sentinel = "tok-refresh-leaktest" // #nosec G101 -- test fixture
	srv, _ := fakeTokenMCPServer(t, sentinel, true)

	img := mcpImage("img-refresh-leaktest", "forgejo", srv.URL, "FORGEJO_PAT",
		agentv1alpha1.AgentImageEnvVar{Name: "FORGEJO_PAT", Value: sentinel, Secret: true})
	s := testServer(t, img)
	registerAgentImageRoutes(s)

	code, out := refreshMCPRequest(t, s, "img-refresh-leaktest")
	if code != http.StatusOK {
		t.Fatalf("expected 200 with warnings, got %d: %v", code, out)
	}
	body := fmt.Sprint(out)
	if !strings.Contains(body, "forgejo") {
		t.Errorf("warning should name the failing server, got %v", out["warnings"])
	}
	if strings.Contains(body, sentinel) {
		t.Errorf("warning leaked the token value: %v", out["warnings"])
	}
}
