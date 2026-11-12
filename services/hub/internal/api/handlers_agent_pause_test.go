package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

func pausedTestAgent(name string, pause *agentv1alpha1.AgentPause) *agentv1alpha1.Agent {
	return &agentv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test-ns"},
		Spec: agentv1alpha1.AgentSpec{
			DisplayName: name,
			ImageRef:    agentv1alpha1.AgentImageRef{Name: "img"},
			Pause:       pause,
		},
	}
}

func pauseTestServer(t *testing.T, objs ...runtime.Object) *Server {
	t.Helper()
	s := testServer(t, objs...)
	s.mux.HandleFunc("/api/v1/agents/", s.handleAgent)
	return s
}

func TestAgentIsPaused(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		pause *agentv1alpha1.AgentPause
		want  bool
	}{
		{"no pause block runs", nil, false},
		{"presence without expiry is indefinite", &agentv1alpha1.AgentPause{}, true},
		{"future expiry is paused", &agentv1alpha1.AgentPause{PausedUntil: &metav1.Time{Time: now.Add(time.Hour)}}, true},
		{"past expiry has lapsed", &agentv1alpha1.AgentPause{PausedUntil: &metav1.Time{Time: now.Add(-time.Second)}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := pausedTestAgent("a", tc.pause)
			if got := a.IsPaused(now); got != tc.want {
				t.Errorf("IsPaused() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPauseAgentSubresource(t *testing.T) {
	s := pauseTestServer(t, pausedTestAgent("worker", nil))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/worker/pause",
		bytes.NewReader([]byte(`{"duration":"2h"}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp SimpleAgentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Paused {
		t.Errorf("response paused = false, want true")
	}
	until, err := time.Parse(time.RFC3339, resp.PausedUntil)
	if err != nil {
		t.Fatalf("pausedUntil %q not RFC3339: %v", resp.PausedUntil, err)
	}
	rem := time.Until(until)
	if rem < 115*time.Minute || rem > 125*time.Minute {
		t.Errorf("pausedUntil = %v, want ~2h from now", until)
	}

	// The CRD is the source of truth: spec.pause must be persisted.
	var got agentv1alpha1.Agent
	if err := s.client.Get(context.Background(), agentKey("worker"), &got); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if got.Spec.Pause == nil || got.Spec.Pause.PausedUntil == nil {
		t.Fatalf("spec.pause = %+v, want pausedUntil set", got.Spec.Pause)
	}
}

func TestPauseAgentIndefinite(t *testing.T) {
	s := pauseTestServer(t, pausedTestAgent("worker", nil))

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/agents/worker/pause", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp SimpleAgentResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.Paused || resp.PausedUntil != "" {
		t.Errorf("resp = {paused:%v pausedUntil:%q}, want {true \"\"}", resp.Paused, resp.PausedUntil)
	}
	var got agentv1alpha1.Agent
	if err := s.client.Get(context.Background(), agentKey("worker"), &got); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if got.Spec.Pause == nil || got.Spec.Pause.PausedUntil != nil {
		t.Errorf("spec.pause = %+v, want present with nil pausedUntil", got.Spec.Pause)
	}
}

func TestPauseAgentRejectsBadRequests(t *testing.T) {
	future := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	past := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	tests := []struct {
		name string
		body string
		want int
	}{
		{"duration and until together", `{"duration":"1h","until":"` + future + `"}`, http.StatusBadRequest},
		{"unparsable duration", `{"duration":"overnight"}`, http.StatusBadRequest},
		{"non-positive duration", `{"duration":"-1h"}`, http.StatusBadRequest},
		{"unparsable until", `{"until":"2026-13-45"}`, http.StatusBadRequest},
		{"until in the past", `{"until":"` + past + `"}`, http.StatusBadRequest},
		{"garbage body", `not json`, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := pauseTestServer(t, pausedTestAgent("worker", nil))
			req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/worker/pause", bytes.NewReader([]byte(tc.body)))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			s.mux.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestPauseAgentNotFound(t *testing.T) {
	s := pauseTestServer(t)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/agents/ghost/pause", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestResumeAgentClearsPause(t *testing.T) {
	until := metav1.NewTime(time.Now().Add(time.Hour))
	s := pauseTestServer(t, pausedTestAgent("worker", &agentv1alpha1.AgentPause{PausedUntil: &until}))

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/agents/worker/pause", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp SimpleAgentResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Paused || resp.PausedUntil != "" {
		t.Errorf("resp = {paused:%v pausedUntil:%q}, want {false \"\"}", resp.Paused, resp.PausedUntil)
	}
	var got agentv1alpha1.Agent
	if err := s.client.Get(context.Background(), agentKey("worker"), &got); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if got.Spec.Pause != nil {
		t.Errorf("spec.pause = %+v, want nil", got.Spec.Pause)
	}
}

func TestToSimpleAgentResponsePaused(t *testing.T) {
	expired := metav1.NewTime(time.Now().Add(-time.Minute))
	tests := []struct {
		name        string
		pause       *agentv1alpha1.AgentPause
		wantPaused  bool
		wantHasUnil bool
	}{
		{"running", nil, false, false},
		{"expired pause reads running", &agentv1alpha1.AgentPause{PausedUntil: &expired}, false, false},
		{"indefinite", &agentv1alpha1.AgentPause{}, true, false},
		{"expiring", &agentv1alpha1.AgentPause{PausedUntil: &metav1.Time{Time: time.Now().Add(time.Hour)}}, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := toSimpleAgentResponse(*pausedTestAgent("w", tc.pause), "img")
			if resp.Paused != tc.wantPaused {
				t.Errorf("paused = %v, want %v", resp.Paused, tc.wantPaused)
			}
			if (resp.PausedUntil != "") != tc.wantHasUnil {
				t.Errorf("pausedUntil = %q, want set: %v", resp.PausedUntil, tc.wantHasUnil)
			}
		})
	}
}
