package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

// Issue #146: spec.managedBy marks a CRD-backed resource externally managed.
// The hub REST API must refuse every mutation with 409 naming the manager,
// expose managedBy on reads, and never accept a client-supplied value.

func managedTestAgent(name, managedBy string) *agentv1alpha1.Agent {
	return &agentv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test-ns"},
		Spec: agentv1alpha1.AgentSpec{
			DisplayName: name,
			ImageRef:    agentv1alpha1.AgentImageRef{Name: "img"},
			ManagedBy:   managedBy,
		},
	}
}

func managedTestImage(name, managedBy string) *agentv1alpha1.AgentImage {
	return &agentv1alpha1.AgentImage{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test-ns"},
		Spec: agentv1alpha1.AgentImageSpec{
			DisplayName: name,
			ImageURL:    "registry.test/img:v1",
			ManagedBy:   managedBy,
			MCPServers: []agentv1alpha1.AgentImageMCPServer{
				{Name: "s1", URL: "http://mcp.test"},
			},
		},
	}
}

func managedTestConnector(name, managedBy string) *agentv1alpha1.WebhookConnector {
	return &agentv1alpha1.WebhookConnector{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test-ns"},
		Spec: agentv1alpha1.WebhookConnectorSpec{
			DisplayName:     name,
			WebhookEndpoint: "https://hub.test/hooks/" + name,
			SignatureHeader: "X-Hub-Signature-256",
			WebhookSecret:   agentv1alpha1.SecretKeyRef{SecretRef: agentv1alpha1.SecretRef{Name: name + "-secret"}},
			ManagedBy:       managedBy,
		},
	}
}

func managedReq(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec
}

func requireManaged409(t *testing.T, rec *httptest.ResponseRecorder, manager string) {
	t.Helper()
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := decodeSingleError(t, rec.Body.Bytes())["error"]; got != "agent is managed by "+manager &&
		got != "agent image is managed by "+manager &&
		got != "connector is managed by "+manager {
		t.Errorf("409 body must name the manager, got: %v", got)
	}
}

func agentTestServerWith(t *testing.T, objs ...runtime.Object) *Server {
	t.Helper()
	s := testServer(t, objs...)
	s.mux.HandleFunc("/api/v1/agents", s.handleAgents)
	s.mux.HandleFunc("/api/v1/agents/", s.handleAgent)
	return s
}

func TestManagedAgentMutations409(t *testing.T) {
	s := agentTestServerWith(t, managedTestAgent("managed", "gitops"))

	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPut, "/api/v1/agents/managed", `{"description":"mine"}`},
		{http.MethodDelete, "/api/v1/agents/managed", ""},
		{http.MethodPost, "/api/v1/agents/managed/pause", `{}`},
		{http.MethodDelete, "/api/v1/agents/managed/pause", ""},
	} {
		rec := managedReq(t, s, tc.method, tc.path, tc.body)
		requireManaged409(t, rec, "gitops")
	}

	// The refused mutations must not have touched the CR.
	var a agentv1alpha1.Agent
	if err := s.client.Get(t.Context(), types.NamespacedName{Name: "managed", Namespace: "test-ns"}, &a); err != nil {
		t.Fatalf("get: %v", err)
	}
	if a.Spec.Description != "" || a.Spec.Pause != nil {
		t.Errorf("spec changed despite the refused mutations: %+v", a.Spec)
	}
}

func TestManagedAgentGETExposesManagedBy(t *testing.T) {
	s := agentTestServerWith(t, managedTestAgent("managed", "gitops"))

	rec := managedReq(t, s, http.MethodGet, "/api/v1/agents/managed", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp SimpleAgentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ManagedBy != "gitops" {
		t.Errorf("managedBy = %q, want gitops", resp.ManagedBy)
	}
}

func TestUnmanagedAgentPUTIgnoresManagedByAndSucceeds(t *testing.T) {
	s := agentTestServerWith(t, managedTestAgent("plain", ""))

	// managedBy is not part of the request shape: a client-sent value is
	// ignored, and the stored value (empty here) is preserved.
	rec := managedReq(t, s, http.MethodPut, "/api/v1/agents/plain", `{"description":"mine","managedBy":"gitops"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp SimpleAgentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ManagedBy != "" {
		t.Errorf("client must not be able to set managedBy, got %q", resp.ManagedBy)
	}
	if resp.Description != "mine" {
		t.Errorf("description = %q, want mine", resp.Description)
	}
}

func TestManagedImageMutations409(t *testing.T) {
	s := testServer(t, managedTestImage("managed", "helm"))
	s.mux.HandleFunc("/api/v1/agent-images/", s.handleAgentImagePath)

	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPut, "/api/v1/agent-images/managed", `{"description":"mine"}`},
		{http.MethodDelete, "/api/v1/agent-images/managed", ""},
		{http.MethodPost, "/api/v1/agent-images/managed/refresh-mcp", ""},
	} {
		req := httptest.NewRequest(tc.method, tc.path, bytes.NewReader([]byte(tc.body)))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s %s: status = %d, body = %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
		if got := decodeSingleError(t, rec.Body.Bytes())["error"]; got != "agent image is managed by helm" {
			t.Errorf("409 body: %v", got)
		}
	}
}

func TestManagedImageGETExposesManagedBy(t *testing.T) {
	s := testServer(t, managedTestImage("managed", "helm"))
	s.mux.HandleFunc("/api/v1/agent-images/", s.handleAgentImagePath)

	rec := managedReq(t, s, http.MethodGet, "/api/v1/agent-images/managed", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp SimpleAgentImageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ManagedBy != "helm" {
		t.Errorf("managedBy = %q, want helm", resp.ManagedBy)
	}
}

func TestManagedConnectorMutations409(t *testing.T) {
	s := connectorTestServer(t)
	if err := s.client.Create(t.Context(), managedTestConnector("managed", "gitops")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPut, "/api/v1/connectors/managed", `{"disabled":true}`},
		{http.MethodDelete, "/api/v1/connectors/managed", ""},
		{http.MethodPost, "/api/v1/connectors/managed/rotate-secret", ""},
	} {
		rec := managedReq(t, s, tc.method, tc.path, tc.body)
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s %s: status = %d, body = %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
		if got := decodeSingleError(t, rec.Body.Bytes())["error"]; got != "connector is managed by gitops" {
			t.Errorf("409 body: %v", got)
		}
	}
}

func TestManagedConnectorGETExposesManagedBy(t *testing.T) {
	s := connectorTestServer(t)
	if err := s.client.Create(t.Context(), managedTestConnector("managed", "gitops")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rec := managedReq(t, s, http.MethodGet, "/api/v1/connectors/managed", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp WebhookConnectorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ManagedBy != "gitops" {
		t.Errorf("managedBy = %q, want gitops", resp.ManagedBy)
	}
}

func TestIsManagedCR(t *testing.T) {
	for _, tc := range []struct {
		managedBy string
		want      bool
	}{
		{"", false},
		{"user", false},
		{"gitops", true},
		{"helm", true},
		{"plugin", true},
	} {
		if got := isManagedCR(tc.managedBy); got != tc.want {
			t.Errorf("isManagedCR(%q) = %v, want %v", tc.managedBy, got, tc.want)
		}
	}
}
