package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/ainsel-project/ainsel/services/hub/internal/db"
	"github.com/ainsel-project/ainsel/services/hub/internal/mcpservers"
	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func mcpServiceForTest(t *testing.T) *mcpservers.Service {
	t.Helper()
	ctx := t.Context()
	c, err := pgcontainer.Run(ctx, "postgres:17-alpine",
		pgcontainer.WithDatabase("test"),
		pgcontainer.WithUsername("test"),
		pgcontainer.WithPassword("test"),
		pgcontainer.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	dsn, _ := c.ConnectionString(ctx, "sslmode=disable")
	if err := db.Migrate(ctx, dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)
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
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mcp-servers", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
	var out []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out) != 0 {
		t.Errorf("expected empty list, got %v", out)
	}
}

func TestMCPServers_CreateAndList(t *testing.T) {
	s := mcpTestServer(t)
	body := bytes.NewBufferString(`{"name":"github","displayName":"GitHub","url":"https://mcp.github.com/sse","tokenFromEnv":"GITHUB_TOKEN"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status: %d body: %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created["name"] != "github" {
		t.Errorf("name: %v", created["name"])
	}
	// tokenFromEnv is not a secret; it must round-trip in the response.
	if created["tokenFromEnv"] != "GITHUB_TOKEN" {
		t.Errorf("tokenFromEnv: %v", created["tokenFromEnv"])
	}

	// List should return the created row.
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/mcp-servers", nil)
	listRec := httptest.NewRecorder()
	s.mux.ServeHTTP(listRec, listReq)
	var list []map[string]any
	_ = json.Unmarshal(listRec.Body.Bytes(), &list)
	if len(list) != 1 || list[0]["name"] != "github" {
		t.Errorf("list: %v", list)
	}
	if list[0]["tokenFromEnv"] != "GITHUB_TOKEN" {
		t.Errorf("list tokenFromEnv: %v", list[0]["tokenFromEnv"])
	}
}

func TestMCPServers_CreateRejectsInvalidTokenFromEnv(t *testing.T) {
	s := mcpTestServer(t)
	body := bytes.NewBufferString(`{"name":"a","displayName":"A","url":"http://a","tokenFromEnv":"forgejo_pat"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for lowercase env name, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMCPServers_CreateRejectsMissingURL(t *testing.T) {
	s := mcpTestServer(t)
	body := bytes.NewBufferString(`{"name":"x","displayName":"X"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMCPServers_CreateConflict(t *testing.T) {
	s := mcpTestServer(t)
	body := `{"name":"dup","displayName":"Dup","url":"http://x"}`
	for i := range 2 {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, req)
		if i == 0 && rec.Code != http.StatusCreated {
			t.Fatalf("first create: %d", rec.Code)
		}
		if i == 1 && rec.Code != http.StatusConflict {
			t.Errorf("expected 409, got %d", rec.Code)
		}
	}
}

func TestMCPServers_GetNotFound(t *testing.T) {
	s := mcpTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mcp-servers/missing", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestMCPServers_UpdateReplacesTokenFromEnv(t *testing.T) {
	s := mcpTestServer(t)
	ctx := t.Context()

	_ = s.mcp.Create(ctx, &mcpservers.MCPServer{Name: "a", DisplayName: "A", URL: "http://a", TokenFromEnv: "OLD_TOKEN"})

	// PUT with a new tokenFromEnv replaces the old value.
	body := bytes.NewBufferString(`{"displayName":"A2","url":"http://a2","tokenFromEnv":"NEW_TOKEN"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/mcp-servers/a", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d body=%s", rec.Code, rec.Body.String())
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
	body := bytes.NewBufferString(`{"displayName":"A","url":"http://a"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/mcp-servers/a", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d body=%s", rec.Code, rec.Body.String())
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

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/mcp-servers/del", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d body=%s", rec.Code, rec.Body.String())
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

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/mcp-servers/refd", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}

	var errResp struct {
		Error          string   `json:"error"`
		AffectedImages []string `json:"affectedImages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
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

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/mcp-servers/plugin-mem0", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	out := decodeSingleError(t, rec.Body.Bytes())
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

	body := bytes.NewBufferString(`{"displayName":"Mine","url":"http://evil","tokenFromEnv":"OTHER"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/mcp-servers/plugin-mem0", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	out := decodeSingleError(t, rec.Body.Bytes())
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
