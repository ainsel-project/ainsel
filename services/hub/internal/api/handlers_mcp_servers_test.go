package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/dbtest"
	"github.com/ainsel-project/ainsel/services/hub/internal/mcpservers"
	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func mcpServiceForTest(t *testing.T) *mcpservers.Service {
	t.Helper()
	pool := dbtest.Open(t)
	return mcpservers.NewService(mcpservers.NewStore(pool))
}

func mcpTestServer(t *testing.T) *Server {
	t.Helper()
	s := testServer(t)
	s.mcp = mcpServiceForTest(t)
	s.mux.HandleFunc("/api/v1/mcp-servers", s.handleMCPServers)
	s.mux.HandleFunc("/api/v1/mcp-servers/", s.handleMCPServer)
	return s
}

func TestMCPServers_ListEmpty(t *testing.T) {
	s := mcpTestServer(t)
	status, body := do(t, s, http.MethodGet, "/api/v1/mcp-servers", nil)
	if status != http.StatusOK {
		t.Fatalf("status: %d body: %s", status, string(body))
	}
	var out []map[string]any
	_ = json.Unmarshal(body, &out)
	if len(out) != 0 {
		t.Errorf("expected empty list, got %v", out)
	}
}

func TestMCPServers_CreateAndList(t *testing.T) {
	s := mcpTestServer(t)
	status, body := do(t, s, http.MethodPost, "/api/v1/mcp-servers", `{"name":"github","displayName":"GitHub","url":"https://mcp.github.com/sse","tokenFromEnv":"GITHUB_TOKEN"}`)
	if status != http.StatusCreated {
		t.Fatalf("create status: %d body: %s", status, string(body))
	}
	var created map[string]any
	_ = json.Unmarshal(body, &created)
	if created["name"] != "github" {
		t.Errorf("name: %v", created["name"])
	}
	// tokenFromEnv is not a secret; it must round-trip in the response.
	if created["tokenFromEnv"] != "GITHUB_TOKEN" {
		t.Errorf("tokenFromEnv: %v", created["tokenFromEnv"])
	}

	// List should return the created row.
	_, listBody := do(t, s, http.MethodGet, "/api/v1/mcp-servers", nil)
	var list []map[string]any
	_ = json.Unmarshal(listBody, &list)
	if len(list) != 1 || list[0]["name"] != "github" {
		t.Errorf("list: %v", list)
	}
	if list[0]["tokenFromEnv"] != "GITHUB_TOKEN" {
		t.Errorf("list tokenFromEnv: %v", list[0]["tokenFromEnv"])
	}
}

func TestMCPServers_CreateRejectsInvalidTokenFromEnv(t *testing.T) {
	s := mcpTestServer(t)
	status, body := do(t, s, http.MethodPost, "/api/v1/mcp-servers", `{"name":"a","displayName":"A","url":"http://a","tokenFromEnv":"forgejo_pat"}`)
	if status != http.StatusBadRequest {
		t.Errorf("expected 400 for lowercase env name, got %d body=%s", status, string(body))
	}
}

func TestMCPServers_CreateRejectsMissingURL(t *testing.T) {
	s := mcpTestServer(t)
	status, body := do(t, s, http.MethodPost, "/api/v1/mcp-servers", `{"name":"x","displayName":"X"}`)
	if status != http.StatusBadRequest {
		t.Errorf("expected 400, got %d body=%s", status, string(body))
	}
}

func TestMCPServers_CreateConflict(t *testing.T) {
	s := mcpTestServer(t)
	reqBody := `{"name":"dup","displayName":"Dup","url":"http://x"}`
	for i := range 2 {
		status, _ := do(t, s, http.MethodPost, "/api/v1/mcp-servers", reqBody)
		if i == 0 && status != http.StatusCreated {
			t.Fatalf("first create: %d", status)
		}
		if i == 1 && status != http.StatusConflict {
			t.Errorf("expected 409, got %d", status)
		}
	}
}

func TestMCPServers_GetNotFound(t *testing.T) {
	s := mcpTestServer(t)
	status, _ := do(t, s, http.MethodGet, "/api/v1/mcp-servers/missing", nil)
	if status != http.StatusNotFound {
		t.Errorf("expected 404, got %d", status)
	}
}

func TestMCPServers_UpdateReplacesTokenFromEnv(t *testing.T) {
	s := mcpTestServer(t)
	ctx := t.Context()

	_ = s.mcp.Create(ctx, &mcpservers.MCPServer{Name: "a", DisplayName: "A", URL: "http://a", TokenFromEnv: "OLD_TOKEN"})

	// PUT with a new tokenFromEnv replaces the old value.
	status, body := do(t, s, http.MethodPut, "/api/v1/mcp-servers/a", `{"displayName":"A2","url":"http://a2","tokenFromEnv":"NEW_TOKEN"}`)
	if status != http.StatusOK {
		t.Fatalf("update: %d body=%s", status, string(body))
	}

	got, _ := s.mcp.Get(ctx, "a")
	if got.TokenFromEnv != "NEW_TOKEN" {
		t.Errorf("tokenFromEnv not updated: %q", got.TokenFromEnv)
	}
	if got.URL != "http://a2" {
		t.Errorf("url not updated: %s", got.URL)
	}
}

func TestMCPServers_UpdateClearsTokenFromEnv(t *testing.T) {
	s := mcpTestServer(t)
	ctx := t.Context()

	_ = s.mcp.Create(ctx, &mcpservers.MCPServer{Name: "a", DisplayName: "A", URL: "http://a", TokenFromEnv: "OLD_TOKEN"})

	// PUT without tokenFromEnv clears it (no longer "preserve on empty").
	status, body := do(t, s, http.MethodPut, "/api/v1/mcp-servers/a", `{"displayName":"A","url":"http://a"}`)
	if status != http.StatusOK {
		t.Fatalf("update: %d body=%s", status, string(body))
	}

	got, _ := s.mcp.Get(ctx, "a")
	if got.TokenFromEnv != "" {
		t.Errorf("tokenFromEnv should be cleared, got %q", got.TokenFromEnv)
	}
}

func TestMCPServers_Delete(t *testing.T) {
	s := mcpTestServer(t)
	ctx := t.Context()
	_ = s.mcp.Create(ctx, &mcpservers.MCPServer{Name: "del", DisplayName: "Del", URL: "http://del"})

	status, body := do(t, s, http.MethodDelete, "/api/v1/mcp-servers/del", nil)
	if status != http.StatusNoContent {
		t.Errorf("expected 204, got %d body=%s", status, string(body))
	}
}

func TestMCPServers_DeleteRejectsWhenReferenced(t *testing.T) {
	s := mcpTestServer(t)
	ctx := t.Context()
	_ = s.mcp.Create(ctx, &mcpservers.MCPServer{Name: "refd", DisplayName: "Referenced", URL: "http://refd"})

	// Create an AgentImage that references the MCP server.
	img := &agentv1alpha1.AgentImage{
		ObjectMeta: metav1.ObjectMeta{Name: "img-ref001", Namespace: "test-ns"},
		Spec: agentv1alpha1.AgentImageSpec{
			DisplayName: "Test Image",
			ImageURL:    "ghcr.io/test/image:latest",
			MCPServers: []agentv1alpha1.AgentImageMCPServer{
				{Name: "refd", URL: "http://refd"},
			},
		},
	}
	if err := s.client.Create(ctx, img); err != nil {
		t.Fatalf("create agent image: %v", err)
	}

	status, body := do(t, s, http.MethodDelete, "/api/v1/mcp-servers/refd", nil)
	if status != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", status, string(body))
	}

	var errResp struct {
		Error          string   `json:"error"`
		AffectedImages []string `json:"affectedImages"`
	}
	if err := json.Unmarshal(body, &errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp.Error != "mcp server referenced by agent image(s)" {
		t.Errorf("expected error 'mcp server referenced by agent image(s)', got %q", errResp.Error)
	}
	if len(errResp.AffectedImages) == 0 {
		t.Errorf("expected at least one affected image, got none")
	}
	found := false
	for _, n := range errResp.AffectedImages {
		if n == "img-ref001" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'img-ref001' in affectedImages, got %v", errResp.AffectedImages)
	}
}

// pluginManagedRow writes a registry row as the plugin syncer would: owned by
// plugin "mem0", pointing at the hub's own agent endpoint.
func pluginManagedRow(t *testing.T, s *Server) *mcpservers.MCPServer {
	t.Helper()
	row := &mcpservers.MCPServer{
		Name:         "plugin-mem0",
		DisplayName:  "Mem0",
		Description:  "Tools of the Mem0 plugin",
		URL:          "http://hub-backend.ainsel.svc.cluster.local:8080/api/internal/plugins/mem0/agent-mcp",
		TokenFromEnv: "HUB_INTERNAL_VALIDATE_SECRET", // #nosec G101 -- env var name, not a credential
		ManagedBy:    "plugin",
	}
	if err := s.mcp.Upsert(t.Context(), row); err != nil {
		t.Fatalf("seed plugin row: %v", err)
	}
	return row
}

func decodeSingleError(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %q: %v", string(body), err)
	}
	return out
}

// TestDeleteMCPServer_PluginManaged409 covers both halves of the rule: the
// message names the owning plugin, and the answer comes before the
// agent-image reference check — an image reference is not the user's problem
// when they cannot delete the row at all.
func TestDeleteMCPServer_PluginManaged409(t *testing.T) {
	s := mcpTestServer(t)
	ctx := t.Context()
	pluginManagedRow(t, s)

	img := &agentv1alpha1.AgentImage{
		ObjectMeta: metav1.ObjectMeta{Name: "img-plugin01", Namespace: "test-ns"},
		Spec: agentv1alpha1.AgentImageSpec{
			DisplayName: "Test Image",
			ImageURL:    "ghcr.io/test/image:latest",
			MCPServers:  []agentv1alpha1.AgentImageMCPServer{{Name: "plugin-mem0", URL: "http://hub"}},
		},
	}
	if err := s.client.Create(ctx, img); err != nil {
		t.Fatalf("create agent image: %v", err)
	}

	status, body := do(t, s, http.MethodDelete, "/api/v1/mcp-servers/plugin-mem0", nil)
	if status != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", status, string(body))
	}
	out := decodeSingleError(t, body)
	if len(out) != 1 {
		t.Fatalf("expected only an error field, got %v", out)
	}
	if out["error"] != "managed by plugin mem0" {
		t.Errorf("error: %v", out["error"])
	}
	if got, err := s.mcp.Get(ctx, "plugin-mem0"); err != nil || got == nil {
		t.Errorf("row must survive the refused delete: %v", err)
	}
}

func TestUpdateMCPServer_PluginManaged409(t *testing.T) {
	s := mcpTestServer(t)
	ctx := t.Context()
	row := pluginManagedRow(t, s)

	status, body := do(t, s, http.MethodPut, "/api/v1/mcp-servers/plugin-mem0", `{"displayName":"Mine","url":"http://evil","tokenFromEnv":"OTHER"}`)
	if status != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", status, string(body))
	}
	out := decodeSingleError(t, body)
	if len(out) != 1 {
		t.Fatalf("expected only an error field, got %v", out)
	}
	if out["error"] != "managed by plugin mem0" {
		t.Errorf("error: %v", out["error"])
	}
	got, err := s.mcp.Get(ctx, "plugin-mem0")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.URL != row.URL || got.DisplayName != row.DisplayName || got.TokenFromEnv != row.TokenFromEnv {
		t.Errorf("row changed despite the refused update: %+v", got)
	}
}

// The console's "list tools" on a registry row has to reach the server the way an
// agent does. The row names the env var; the value sits on the images that attach
// it, so the hub reads it from there rather than asking anonymously and being told
// the door is locked.
func TestMCPServerTools_SendsTheCredentialTheRowNames(t *testing.T) {
	s := mcpTestServer(t)
	ctx := t.Context()
	url, seenAuth := authedMCPServer(t, "forge-token")

	if err := s.mcp.Create(ctx, &mcpservers.MCPServer{
		Name:         "forge",
		DisplayName:  "Forge",
		URL:          url,
		TokenFromEnv: "FORGEJO_TOKEN", // #nosec G101 -- env var name, not a credential
	}); err != nil {
		t.Fatalf("create row: %v", err)
	}
	img := imageWithMCPServer("img-a", url, "FORGEJO_TOKEN", []agentv1alpha1.AgentImageEnvVar{
		{Name: "FORGEJO_TOKEN", Value: "forge-token", Secret: true},
	})
	if err := s.client.Create(ctx, img); err != nil {
		t.Fatalf("seed image: %v", err)
	}

	status, body := do(t, s, http.MethodGet, "/api/v1/mcp-servers/forge/tools", nil)

	if status != http.StatusOK {
		t.Fatalf("status: %d body: %s", status, string(body))
	}
	var out []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, string(body))
	}
	if len(out) != 1 || out[0].Name != "create_issue" {
		t.Fatalf("expected the server's tool, got %+v", out)
	}
	if got := seenAuth(); got != "Bearer forge-token" {
		t.Errorf("server saw %q, want the token the image holds", got)
	}
}

// With no image carrying the named var there is nothing to send: the row is listed
// as it always was, and a server that requires a token answers the way it does for
// any other uncredentialed caller.
func TestMCPServerTools_FailsAsAnonymousWhenNoImageCarriesTheVar(t *testing.T) {
	s := mcpTestServer(t)
	ctx := t.Context()
	url, _ := authedMCPServer(t, "forge-token")

	if err := s.mcp.Create(ctx, &mcpservers.MCPServer{
		Name:         "forge",
		DisplayName:  "Forge",
		URL:          url,
		TokenFromEnv: "FORGEJO_TOKEN", // #nosec G101 -- env var name, not a credential
	}); err != nil {
		t.Fatalf("create row: %v", err)
	}

	status, body := do(t, s, http.MethodGet, "/api/v1/mcp-servers/forge/tools", nil)

	if status != http.StatusBadGateway {
		t.Fatalf("expected 502 from a server that refuses anonymous calls, got %d body=%s", status, string(body))
	}
}

// The row was edited after the image was written, so the image's snapshot names the
// old variable while the row names a new one. The credential the snapshot names is
// not this row's credential — the tools read stays anonymous rather than sending a
// secret the registry never pointed at the row's URL.
func TestMCPServerTools_StaysAnonymousWhenTheRowAndImageSnapshotDisagree(t *testing.T) {
	s := mcpTestServer(t)
	ctx := t.Context()
	url, seenAuth := authedMCPServer(t, "forge-token")

	if err := s.mcp.Create(ctx, &mcpservers.MCPServer{
		Name:         "forge",
		DisplayName:  "Forge",
		URL:          url,
		TokenFromEnv: "FORGEJO_TOKEN", // #nosec G101 -- env var name, not a credential
	}); err != nil {
		t.Fatalf("create row: %v", err)
	}
	// The image attached the row before the edit: its reference still names the old var.
	img := imageWithMCPServer("img-a", url, "OLD_FORGEJO_TOKEN", []agentv1alpha1.AgentImageEnvVar{
		{Name: "OLD_FORGEJO_TOKEN", Value: "forge-token", Secret: true},
	})
	if err := s.client.Create(ctx, img); err != nil {
		t.Fatalf("seed image: %v", err)
	}

	status, body := do(t, s, http.MethodGet, "/api/v1/mcp-servers/forge/tools", nil)

	if status != http.StatusBadGateway {
		t.Fatalf("expected 502 from an anonymous call to a server that refuses it, got %d body=%s", status, string(body))
	}
	if got := seenAuth(); got != "" {
		t.Errorf("expected no Authorization header, got %q", got)
	}
}
