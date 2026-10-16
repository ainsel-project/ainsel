package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"

	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

// i32 avoids a ptr import: another test file in this package declares a local
// symbol of that name.
func i32(v int32) *int32 { return &v }

func agentKey(name string) types.NamespacedName {
	return types.NamespacedName{Name: name, Namespace: "test-ns"}
}

func TestScalingProblem(t *testing.T) {
	tests := []struct {
		name    string
		scaling *agentv1alpha1.AgentScaling
		want    string
	}{
		{"nil scaling is nothing to say", nil, ""},
		{"a plain cap", &agentv1alpha1.AgentScaling{Replicas: i32(3)}, ""},
		{"zero cap pauses the agent, which is allowed", &agentv1alpha1.AgentScaling{Replicas: i32(0)}, ""},
		{"negative cap", &agentv1alpha1.AgentScaling{Replicas: i32(-2)}, "replicas must not be negative"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := scalingProblem(tc.scaling); got != tc.want {
				t.Errorf("scalingProblem() = %q, want %q", got, tc.want)
			}
		})
	}
}

func postAgentReq(t *testing.T, s *Server, req any) (int, string) {
	t.Helper()
	body, _ := json.Marshal(req)
	hreq := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewReader(body))
	hreq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, hreq)
	return rec.Code, rec.Body.String()
}

func putAgentReq(t *testing.T, s *Server, id string, req any) (int, string) {
	t.Helper()
	body, _ := json.Marshal(req)
	hreq := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id, bytes.NewReader(body))
	hreq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, hreq)
	return rec.Code, rec.Body.String()
}

func newAgentServer(t *testing.T) *Server {
	t.Helper()
	s := testServer(t, testAgentImage("img-1", "git"))
	s.mux.HandleFunc("/api/v1/agents", s.handleAgents)
	s.mux.HandleFunc("/api/v1/agents/", s.handleAgent)
	return s
}

func createAgent(t *testing.T, s *Server, req SimpleAgentCreateRequest) SimpleAgentResponse {
	t.Helper()
	code, body := postAgentReq(t, s, req)
	if code != http.StatusCreated {
		t.Fatalf("create agent: got %d, want 201: %s", code, body)
	}
	var out SimpleAgentResponse
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode create response: %v (%s)", err, body)
	}
	return out
}

func TestCreateAgent_ConcurrencyCapRoundTrip(t *testing.T) {
	s := newAgentServer(t)

	created := createAgent(t, s, SimpleAgentCreateRequest{
		Name:     "Busy Agent",
		ImageRef: AgentImageRefInfo{Name: "img-1"},
		LLM:      AgentLLMInfo{Model: "glm-5.1:cloud"},
		Replicas: i32(5),
	})
	if created.Replicas == nil || *created.Replicas != 5 {
		t.Errorf("response replicas = %v, want 5", created.Replicas)
	}

	var stored agentv1alpha1.Agent
	if err := s.client.Get(context.Background(), agentKey(created.ID), &stored); err != nil {
		t.Fatalf("read back agent: %v", err)
	}
	if stored.Spec.Scaling == nil || stored.Spec.Scaling.Replicas == nil || *stored.Spec.Scaling.Replicas != 5 {
		t.Fatalf("stored scaling = %+v, want replicas 5", stored.Spec.Scaling)
	}
}

// A negative cap cannot be honoured, so it is refused with an explanation
// instead of being silently clamped.
func TestCreateAgent_RejectsNegativeCap(t *testing.T) {
	s := newAgentServer(t)

	code, body := postAgentReq(t, s, SimpleAgentCreateRequest{
		Name:     "Bad Agent",
		ImageRef: AgentImageRefInfo{Name: "img-1"},
		LLM:      AgentLLMInfo{Model: "glm-5.1:cloud"},
		Replicas: i32(-1),
	})
	if code != http.StatusBadRequest {
		t.Fatalf("create with negative cap: got %d, want 400 (%s)", code, body)
	}
	if !strings.Contains(body, "replicas must not be negative") {
		t.Errorf("error body = %s, want it to name the offending value", body)
	}
}

func TestUpdateAgent_ConcurrencyCapUpdate(t *testing.T) {
	s := newAgentServer(t)

	created := createAgent(t, s, SimpleAgentCreateRequest{
		Name:     "Quiet Agent",
		ImageRef: AgentImageRefInfo{Name: "img-1"},
		LLM:      AgentLLMInfo{Model: "glm-5.1:cloud"},
	})
	if created.Replicas != nil {
		t.Fatalf("an agent created without a cap should have none, got %v", created.Replicas)
	}

	// Set the cap.
	if code, body := putAgentReq(t, s, created.ID, SimpleAgentUpdateRequest{Replicas: i32(7)}); code != http.StatusOK {
		t.Fatalf("setting the cap: got %d (%s)", code, body)
	}
	var stored agentv1alpha1.Agent
	if err := s.client.Get(context.Background(), agentKey(created.ID), &stored); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored.Spec.Scaling == nil || stored.Spec.Scaling.Replicas == nil || *stored.Spec.Scaling.Replicas != 7 {
		t.Errorf("stored replicas = %v, want 7", stored.Spec.Scaling)
	}

	// A refused update must not have touched the stored agent.
	if code, _ := putAgentReq(t, s, created.ID, SimpleAgentUpdateRequest{Replicas: i32(-1)}); code != http.StatusBadRequest {
		t.Fatalf("negative cap: got %d, want 400", code)
	}
	var after agentv1alpha1.Agent
	if err := s.client.Get(context.Background(), agentKey(created.ID), &after); err != nil {
		t.Fatalf("read back after rejection: %v", err)
	}
	if after.Spec.Scaling == nil || *after.Spec.Scaling.Replicas != 7 {
		t.Errorf("rejected update changed the agent: replicas = %v, want the previous 7", after.Spec.Scaling)
	}
}

// The UI needs the operator's account of its task Jobs, not just the ready pod
// count: with zero pods, "paused" and "cannot schedule" look identical
// otherwise.
func TestAgentResponse_ExposesOperatorJobAccount(t *testing.T) {
	s := newAgentServer(t)

	created := createAgent(t, s, SimpleAgentCreateRequest{
		Name:     "Working Agent",
		ImageRef: AgentImageRefInfo{Name: "img-1"},
		LLM:      AgentLLMInfo{Model: "glm-5.1:cloud"},
		Replicas: i32(3),
	})

	var stored agentv1alpha1.Agent
	if err := s.client.Get(context.Background(), agentKey(created.ID), &stored); err != nil {
		t.Fatalf("read back: %v", err)
	}
	stored.Status.Replicas = 0
	stored.Status.Scaling = &agentv1alpha1.AgentScalingStatus{
		Desired: 2,
		Reason:  "JobPerTask",
		Message: "2 task jobs running, 1 due",
	}
	if err := s.client.Status().Update(context.Background(), &stored); err != nil {
		t.Fatalf("set status: %v", err)
	}

	code, body := putAgentReq(t, s, created.ID, SimpleAgentUpdateRequest{})
	if code != http.StatusOK {
		t.Fatalf("read via update: got %d (%s)", code, body)
	}
	var resp SimpleAgentResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status == nil {
		t.Fatal("response has no status")
	}
	if resp.Status.Desired != 2 || resp.Status.Reason != "JobPerTask" {
		t.Errorf("status = %+v, want the operator's JobPerTask account", resp.Status)
	}
	if !strings.Contains(resp.Status.Message, "task jobs") {
		t.Errorf("status message = %q, want the operator's explanation", resp.Status.Message)
	}
}
