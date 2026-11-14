package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/personas"
	"github.com/ainsel-project/ainsel/services/hub/internal/plugincreds"
	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Issue #190: a plugin-authenticated caller may stamp managedBy on the
// resources it creates, and is the one caller allowed past the #146 managed
// guard on those rows. Everything else — console users, other plugins,
// free-form managers, and the bare legacy "plugin" marker — keeps the #146
// behavior exactly.

// managedCallerReq builds a request whose context carries the given plugin
// caller, standing in for what PluginCredentialMiddleware does on a real
// plugin request. A nil caller is a console request.
func managedCallerReq(t *testing.T, caller *plugincreds.Caller, method, path, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if caller != nil {
		req = req.WithContext(plugincreds.WithCaller(req.Context(), caller))
	}
	return req
}

func pluginCaller(id string) *plugincreds.Caller {
	return &plugincreds.Caller{PluginID: id, Kind: plugincreds.KindPlugin}
}

func doManaged(t *testing.T, s *Server, caller *plugincreds.Caller, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, managedCallerReq(t, caller, method, path, body))
	return rec
}

func managedErr(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	got, _ := decodeSingleError(t, rec.Body.Bytes())["error"].(string)
	return got
}

func TestResolveCreateManagedBy(t *testing.T) {
	longID := strings.Repeat("p", 60) // "plugin-" + 60 chars = 67 > the 63-char CRD pattern limit

	tests := []struct {
		name      string
		caller    *plugincreds.Caller
		requested string
		want      string
		wantErr   bool
	}{
		{"plugin caller stamping itself", pluginCaller("forgejo"), "plugin-forgejo", "plugin-forgejo", false},
		{"plugin caller naming another plugin", pluginCaller("forgejo"), "plugin-github", "", true},
		{"plugin caller naming a free-form manager", pluginCaller("forgejo"), "gitops", "", true},
		{"plugin caller using the legacy bare marker", pluginCaller("forgejo"), "plugin", "", true},
		{"plugin caller with an over-long composed stamp", pluginCaller(longID), "plugin-" + longID, "", true},
		{"plugin caller sending nothing stays user", pluginCaller("forgejo"), "", "user", false},
		{"user caller sending a value is ignored", nil, "gitops", "user", false},
		{"agent-kind credential cannot stamp", &plugincreds.Caller{PluginID: "forgejo", Kind: plugincreds.KindAgent}, "plugin-forgejo", "user", false},
		{"user-kind plugin credential cannot stamp", &plugincreds.Caller{PluginID: "forgejo", Kind: plugincreds.KindUser}, "plugin-forgejo", "user", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveCreateManagedBy(tc.caller, tc.requested)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got value %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCallerIsManager(t *testing.T) {
	tests := []struct {
		name      string
		caller    *plugincreds.Caller
		managedBy string
		want      bool
	}{
		{"own stamp", pluginCaller("forgejo"), "plugin-forgejo", true},
		{"another plugin's stamp", pluginCaller("github"), "plugin-forgejo", false},
		{"console row", pluginCaller("forgejo"), "user", false},
		{"unmanaged row", pluginCaller("forgejo"), "", false},
		{"free-form manager", pluginCaller("forgejo"), "gitops", false},
		{"legacy bare plugin marker", pluginCaller("forgejo"), "plugin", false},
		{"agent-kind credential", &plugincreds.Caller{PluginID: "forgejo", Kind: plugincreds.KindAgent}, "plugin-forgejo", false},
		{"no caller", nil, "plugin-forgejo", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := callerIsManager(tc.caller, tc.managedBy); got != tc.want {
				t.Errorf("callerIsManager(%+v, %q) = %v, want %v", tc.caller, tc.managedBy, got, tc.want)
			}
		})
	}
}

// --- create-time stamping, per kind ---------------------------------------

func TestManagedStamp_AgentCreate(t *testing.T) {
	img := testAgentImage("img-1", "git")
	s := agentTestServerWith(t, img)

	create := func(caller *plugincreds.Caller, managedBy string) *httptest.ResponseRecorder {
		body := `{"name":"a","imageRef":{"name":"img-1"},"llm":{"model":"m","provider":"ollama-cloud"},"enabledTools":["git"]}`
		if managedBy != "" {
			body = body[:len(body)-1] + `,"managedBy":"` + managedBy + `"}`
		}
		return doManaged(t, s, caller, http.MethodPost, "/api/v1/agents", body)
	}

	// Plugin X stamping itself -> created managed, and the stamp is readable.
	rec := create(pluginCaller("forgejo"), "plugin-forgejo")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created SimpleAgentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.ManagedBy != "plugin-forgejo" {
		t.Errorf("response managedBy = %q, want plugin-forgejo", created.ManagedBy)
	}
	var cr agentv1alpha1.Agent
	if err := s.client.Get(t.Context(), types.NamespacedName{Name: created.ID, Namespace: "test-ns"}, &cr); err != nil {
		t.Fatalf("get: %v", err)
	}
	if cr.Spec.ManagedBy != "plugin-forgejo" {
		t.Errorf("spec.managedBy = %q, want plugin-forgejo", cr.Spec.ManagedBy)
	}

	// Another plugin's id, or a free-form manager -> 400, nothing created.
	for _, bad := range []string{"plugin-github", "gitops", "plugin"} {
		rec := create(pluginCaller("forgejo"), bad)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("managedBy %q: status = %d, want 400 (body %s)", bad, rec.Code, rec.Body.String())
		}
	}

	// A plugin caller that sends nothing is not stamped.
	rec = create(pluginCaller("forgejo"), "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("unstamped plugin create: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var unstamped SimpleAgentResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &unstamped)
	var cr2 agentv1alpha1.Agent
	if err := s.client.Get(t.Context(), types.NamespacedName{Name: unstamped.ID, Namespace: "test-ns"}, &cr2); err != nil {
		t.Fatalf("get: %v", err)
	}
	if cr2.Spec.ManagedBy != "" {
		t.Errorf("unstamped create wrote spec.managedBy = %q, want it left absent", cr2.Spec.ManagedBy)
	}

	// A console user sending a value keeps the #146 behavior: ignored, "user".
	rec = create(nil, "gitops")
	if rec.Code != http.StatusCreated {
		t.Fatalf("user create: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var byUser SimpleAgentResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &byUser)
	var cr3 agentv1alpha1.Agent
	if err := s.client.Get(t.Context(), types.NamespacedName{Name: byUser.ID, Namespace: "test-ns"}, &cr3); err != nil {
		t.Fatalf("get: %v", err)
	}
	if cr3.Spec.ManagedBy != "" {
		t.Errorf("user-supplied managedBy changed the row: %q", cr3.Spec.ManagedBy)
	}
}

func TestManagedStamp_AgentImageCreate(t *testing.T) {
	s := testServer(t)
	registerAgentImageRoutes(s)

	create := func(caller *plugincreds.Caller, managedBy string) *httptest.ResponseRecorder {
		body := `{"displayName":"i","imageURL":"registry.test/i:v1"}`
		if managedBy != "" {
			body = strings.TrimSuffix(body, "}") + `,"managedBy":"` + managedBy + `"}`
		}
		return doManaged(t, s, caller, http.MethodPost, "/api/v1/agent-images", body)
	}

	rec := create(pluginCaller("forgejo"), "plugin-forgejo")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp SimpleAgentImageResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	var cr agentv1alpha1.AgentImage
	if err := s.client.Get(t.Context(), types.NamespacedName{Name: resp.ID, Namespace: "test-ns"}, &cr); err != nil {
		t.Fatalf("get: %v", err)
	}
	if cr.Spec.ManagedBy != "plugin-forgejo" {
		t.Errorf("spec.managedBy = %q, want plugin-forgejo", cr.Spec.ManagedBy)
	}

	for _, bad := range []string{"plugin-github", "gitops"} {
		if rec := create(pluginCaller("forgejo"), bad); rec.Code != http.StatusBadRequest {
			t.Errorf("managedBy %q: status = %d, want 400", bad, rec.Code)
		}
	}
	if rec := create(nil, "gitops"); rec.Code != http.StatusCreated {
		t.Fatalf("user create: status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestManagedStamp_ConnectorCreate(t *testing.T) {
	s := connectorTestServer(t)

	create := func(caller *plugincreds.Caller, managedBy string) *httptest.ResponseRecorder {
		body := `{"name":"c"}`
		if managedBy != "" {
			body = strings.TrimSuffix(body, "}") + `,"managedBy":"` + managedBy + `"}`
		}
		return doManaged(t, s, caller, http.MethodPost, "/api/v1/connectors", body)
	}

	rec := create(pluginCaller("forgejo"), "plugin-forgejo")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp WebhookConnectorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	var cr agentv1alpha1.WebhookConnector
	if err := s.client.Get(t.Context(), types.NamespacedName{Name: resp.ID, Namespace: "test-ns"}, &cr); err != nil {
		t.Fatalf("get: %v", err)
	}
	if cr.Spec.ManagedBy != "plugin-forgejo" {
		t.Errorf("spec.managedBy = %q, want plugin-forgejo", cr.Spec.ManagedBy)
	}

	for _, bad := range []string{"plugin-github", "gitops"} {
		if rec := create(pluginCaller("forgejo"), bad); rec.Code != http.StatusBadRequest {
			t.Errorf("managedBy %q: status = %d, want 400", bad, rec.Code)
		}
	}
}

// stubManagedPersonaService records the CreateRequest the handler builds, so a
// test can assert the server-side managedBy value without a database.
type stubManagedPersonaService struct {
	created *personas.CreateRequest
}

func (s *stubManagedPersonaService) Create(_ context.Context, req personas.CreateRequest) (*personas.Persona, error) {
	s.created = &req
	return &personas.Persona{ID: "01X", Name: req.Name, Text: req.Text, CurrentVersion: 1, ManagedBy: req.ManagedBy}, nil
}

func (s *stubManagedPersonaService) Get(context.Context, string) (*personas.Persona, error) {
	return nil, personas.ErrNotFound
}
func (s *stubManagedPersonaService) List(context.Context, personas.ListFilter) ([]personas.PersonaSummary, int, error) {
	return nil, 0, nil
}
func (s *stubManagedPersonaService) Update(context.Context, string, personas.UpdateRequest) (*personas.Persona, error) {
	return nil, personas.ErrNotFound
}
func (s *stubManagedPersonaService) Delete(context.Context, string) error {
	return personas.ErrNotFound
}
func (s *stubManagedPersonaService) ListVersions(context.Context, string, int, int) ([]personas.VersionSummary, int, error) {
	return nil, 0, nil
}
func (s *stubManagedPersonaService) GetVersion(context.Context, string, int) (*personas.Version, error) {
	return nil, personas.ErrNotFound
}
func (s *stubManagedPersonaService) Rollback(context.Context, string, int) (*personas.Persona, error) {
	return nil, personas.ErrNotFound
}
func (s *stubManagedPersonaService) OwnedBy(context.Context, string) (*personas.Persona, error) {
	return nil, personas.ErrNotFound
}
func (s *stubManagedPersonaService) EnsureOwned(context.Context, string, string, personas.UpdateRequest) (*personas.Persona, error) {
	return nil, personas.ErrNotFound
}
func (s *stubManagedPersonaService) DeleteOwned(context.Context, string, string) error {
	return personas.ErrNotFound
}

func TestManagedStamp_PersonaCreate(t *testing.T) {
	newServer := func(t *testing.T) (*Server, *stubManagedPersonaService) {
		t.Helper()
		s := testServer(t)
		stub := &stubManagedPersonaService{}
		RegisterPersonaRoutes(s.mux, stub, nil, nil)
		return s, stub
	}
	body := `{"name":"p","text":"You are...","managedBy":"%s"}`

	s, stub := newServer(t)
	rec := doManaged(t, s, pluginCaller("forgejo"), http.MethodPost, "/api/v1/personas", strings.Replace(body, "%s", "plugin-forgejo", 1))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if stub.created == nil || stub.created.ManagedBy != "plugin-forgejo" {
		t.Errorf("service got %+v, want managedBy plugin-forgejo", stub.created)
	}

	// The value is never taken from the body for another manager.
	s, stub = newServer(t)
	rec = doManaged(t, s, pluginCaller("forgejo"), http.MethodPost, "/api/v1/personas", strings.Replace(body, "%s", "plugin-github", 1))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if stub.created != nil {
		t.Errorf("service was called with %+v despite the rejection", stub.created)
	}

	// A console user sending managedBy keeps the #146 behavior: ignored.
	s, stub = newServer(t)
	rec = doManaged(t, s, nil, http.MethodPost, "/api/v1/personas", strings.Replace(body, "%s", "gitops", 1))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if stub.created == nil || stub.created.ManagedBy != managedByUser {
		t.Errorf("service got %+v, want managedBy %q", stub.created, managedByUser)
	}
}

func TestManagedStamp_MCPServerCreate(t *testing.T) {
	s := testServer(t)
	s.mcp = mcpServiceForTest(t)
	s.mux.HandleFunc("/api/v1/mcp-servers", s.handleMCPServers)
	s.mux.HandleFunc("/api/v1/mcp-servers/", s.handleMCPServer)

	body := `{"name":"s","url":"http://mcp.test","managedBy":"%s"}`
	for _, bad := range []string{"plugin-github", "gitops"} {
		rec := doManaged(t, s, pluginCaller("forgejo"), http.MethodPost, "/api/v1/mcp-servers", strings.Replace(body, "%s", bad, 1))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("managedBy %q: status = %d, want 400 (body %s)", bad, rec.Code, rec.Body.String())
		}
	}
}

// --- the named manager passes the guard ----------------------------------

func TestManagedNamedManager_AgentMutations(t *testing.T) {
	s := agentTestServerWith(t, managedTestAgent("mine", "plugin-forgejo"), managedTestAgent("theirs", "plugin-github"))

	update := `{"description":"touched"}`

	// Plugin X updates its own row, and the stored marker survives.
	rec := doManaged(t, s, pluginCaller("forgejo"), http.MethodPut, "/api/v1/agents/mine", update)
	if rec.Code != http.StatusOK {
		t.Fatalf("own plugin PUT: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var a agentv1alpha1.Agent
	if err := s.client.Get(t.Context(), types.NamespacedName{Name: "mine", Namespace: "test-ns"}, &a); err != nil {
		t.Fatalf("get: %v", err)
	}
	if a.Spec.Description != "touched" {
		t.Errorf("description not written: %q", a.Spec.Description)
	}
	if a.Spec.ManagedBy != "plugin-forgejo" {
		t.Errorf("update changed the marker: %q", a.Spec.ManagedBy)
	}

	// Pause/resume is the plugin's own too.
	rec = doManaged(t, s, pluginCaller("forgejo"), http.MethodPost, "/api/v1/agents/mine/pause", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("own plugin pause: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = doManaged(t, s, pluginCaller("forgejo"), http.MethodDelete, "/api/v1/agents/mine/pause", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("own plugin resume: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Everyone else keeps the 409, on every operation.
	for _, tc := range []struct {
		caller *plugincreds.Caller
		name   string
	}{
		{nil, "console user"},
		{pluginCaller("github"), "another plugin"},
	} {
		for _, op := range []struct{ method, path, body string }{
			{http.MethodPut, "/api/v1/agents/mine", update},
			{http.MethodPost, "/api/v1/agents/mine/pause", `{}`},
			{http.MethodDelete, "/api/v1/agents/mine/pause", ""},
			{http.MethodDelete, "/api/v1/agents/mine", ""},
		} {
			rec := doManaged(t, s, tc.caller, op.method, op.path, op.body)
			requireManaged409(t, rec, "plugin-forgejo")
		}
	}

	// Another plugin's row is refused too, and deleting one stays refused.
	rec = doManaged(t, s, pluginCaller("forgejo"), http.MethodDelete, "/api/v1/agents/theirs", "")
	requireManaged409(t, rec, "plugin-github")
}

func TestManagedNamedManager_AgentPersonaWrite(t *testing.T) {
	s := agentTestServerWith(t, managedTestAgent("mine", "plugin-forgejo"))
	s.personas = &stubManagedPersonaService{}

	rec := doManaged(t, s, pluginCaller("github"), http.MethodPut, "/api/v1/agents/mine/persona", `{"text":"hello"}`)
	requireManaged409(t, rec, "plugin-forgejo")

	rec = doManaged(t, s, pluginCaller("forgejo"), http.MethodPut, "/api/v1/agents/mine/persona", `{"text":"hello"}`)
	if rec.Code == http.StatusConflict {
		t.Errorf("own plugin persona write refused: %s", rec.Body.String())
	}
}

func TestManagedNamedManager_AgentImageMutations(t *testing.T) {
	s := testServer(t, managedTestImage("mine", "plugin-forgejo"), managedTestImage("theirs", "plugin-github"))
	registerAgentImageRoutes(s)

	// Plugin X may update, refresh and delete its own image.
	rec := doManaged(t, s, pluginCaller("forgejo"), http.MethodPut, "/api/v1/agent-images/mine", `{"description":"touched"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("own plugin PUT: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = doManaged(t, s, pluginCaller("forgejo"), http.MethodPost, "/api/v1/agent-images/mine/refresh-mcp", "")
	if rec.Code == http.StatusConflict {
		t.Errorf("own plugin refresh-mcp refused: %s", rec.Body.String())
	}

	// Everyone else keeps the 409.
	for _, caller := range []*plugincreds.Caller{nil, pluginCaller("github")} {
		rec := doManaged(t, s, caller, http.MethodPut, "/api/v1/agent-images/mine", `{"description":"mine"}`)
		requireManaged409(t, rec, "plugin-forgejo")
		rec = doManaged(t, s, caller, http.MethodPost, "/api/v1/agent-images/mine/refresh-mcp", "")
		requireManaged409(t, rec, "plugin-forgejo")
		rec = doManaged(t, s, caller, http.MethodDelete, "/api/v1/agent-images/mine", "")
		requireManaged409(t, rec, "plugin-forgejo")
	}
	rec = doManaged(t, s, pluginCaller("forgejo"), http.MethodDelete, "/api/v1/agent-images/theirs", "")
	requireManaged409(t, rec, "plugin-github")
}

func TestManagedNamedManager_ConnectorMutations(t *testing.T) {
	s := connectorTestServer(t)
	for _, c := range []*agentv1alpha1.WebhookConnector{managedTestConnector("mine", "plugin-forgejo"), managedTestConnector("theirs", "plugin-github")} {
		if err := s.client.Create(t.Context(), c); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	// rotate-secret rewrites the connector's existing HMAC secret in place.
	if err := s.client.Create(t.Context(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: webhookSecretName("mine"), Namespace: "test-ns"},
		Data:       map[string][]byte{"secret": []byte("old-hmac")},
	}); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	rec := doManaged(t, s, pluginCaller("forgejo"), http.MethodPut, "/api/v1/connectors/mine", `{"name":"renamed"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("own plugin PUT: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = doManaged(t, s, pluginCaller("forgejo"), http.MethodPost, "/api/v1/connectors/mine/rotate-secret", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("own plugin rotate-secret: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	for _, caller := range []*plugincreds.Caller{nil, pluginCaller("github")} {
		rec := doManaged(t, s, caller, http.MethodPut, "/api/v1/connectors/mine", `{"name":"mine"}`)
		requireManaged409(t, rec, "plugin-forgejo")
		rec = doManaged(t, s, caller, http.MethodPost, "/api/v1/connectors/mine/rotate-secret", "")
		requireManaged409(t, rec, "plugin-forgejo")
		rec = doManaged(t, s, caller, http.MethodDelete, "/api/v1/connectors/mine", "")
		requireManaged409(t, rec, "plugin-forgejo")
	}
	rec = doManaged(t, s, pluginCaller("forgejo"), http.MethodDelete, "/api/v1/connectors/theirs", "")
	requireManaged409(t, rec, "plugin-github")
}

func TestManagedNamedManager_MCPServerMutations(t *testing.T) {
	s := testServer(t)
	s.mcp = mcpServiceForTest(t)
	s.mux.HandleFunc("/api/v1/mcp-servers", s.handleMCPServers)
	s.mux.HandleFunc("/api/v1/mcp-servers/", s.handleMCPServer)

	for _, caller := range []*plugincreds.Caller{nil, pluginCaller("github")} {
		rec := doManaged(t, s, caller, http.MethodPut, "/api/v1/mcp-servers/mine", `{"url":"http://evil.test"}`)
		if rec.Code != http.StatusConflict {
			t.Errorf("PUT as %v: status = %d, want 409", caller, rec.Code)
		}
		rec = doManaged(t, s, caller, http.MethodDelete, "/api/v1/mcp-servers/mine", "")
		if rec.Code != http.StatusConflict {
			t.Errorf("DELETE as %v: status = %d, want 409", caller, rec.Code)
		}
	}
}
