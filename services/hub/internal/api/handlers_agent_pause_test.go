package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	"github.com/ainsel-project/ainsel/services/hub/internal/eventqueue"
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

// The claim gate: a paused agent's next-task must return no content without
// ever touching the queue. With no event queue wired, an unpaused agent hits
// the 503 "event queue not configured" path — so 204 vs 503 proves the gate
// runs before any queue access.
func TestNextTaskPausedGate(t *testing.T) {
	until := metav1.NewTime(time.Now().Add(time.Hour))
	paused := pausedTestAgent("paused-agent", &agentv1alpha1.AgentPause{PausedUntil: &until})
	lapsed := pausedTestAgent("lapsed-agent", &agentv1alpha1.AgentPause{
		PausedUntil: &metav1.Time{Time: time.Now().Add(-time.Minute)},
	})
	s := testServer(t, paused, lapsed)
	s.internalValidateSecret = "sekret"
	s.mux.HandleFunc("/api/internal/agents/", s.handleInternalAgent)

	get := func(name string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/internal/agents/"+name+"/next-task", nil)
		req.Header.Set("X-Internal-Token", "sekret")
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, req)
		return rec
	}

	rec := get("paused-agent")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("paused agent: status = %d, want 204", rec.Code)
	}
	if rec.Header().Get("X-Agent-Paused") != "true" {
		t.Errorf("paused agent: X-Agent-Paused header = %q, want true", rec.Header().Get("X-Agent-Paused"))
	}

	// Lapsed pause: not gated, falls through to the queue (unconfigured → 503).
	if rec := get("lapsed-agent"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("lapsed agent: status = %d, want 503", rec.Code)
	}
	// Unknown agent: not gated either (fail open, the queue decides).
	if rec := get("ghost"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("ghost agent: status = %d, want 503", rec.Code)
	}
}

// A pause expiring mid-poll must release the held request shortly after the
// expiry, not after the full poll timeout.
func TestNextTaskPausedBoundedWait(t *testing.T) {
	// 2s, not less: the fake client may round metav1.Time to whole seconds,
	// so keep the remaining wait comfortably above the truncation error.
	until := metav1.NewTime(time.Now().Add(2 * time.Second))
	s := testServer(t, pausedTestAgent("worker", &agentv1alpha1.AgentPause{PausedUntil: &until}))
	s.internalValidateSecret = "sekret"
	s.mux.HandleFunc("/api/internal/agents/", s.handleInternalAgent)

	req := httptest.NewRequest(http.MethodGet, "/api/internal/agents/worker/next-task?timeout=30s", nil)
	req.Header.Set("X-Internal-Token", "sekret")
	rec := httptest.NewRecorder()

	start := time.Now()
	s.mux.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if elapsed < 500*time.Millisecond {
		t.Errorf("released after %v, want the remaining pause, not immediately", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Errorf("held the request for %v, want ~the remaining pause, not the 30s timeout", elapsed)
	}
}

func TestAgentPauseStateLookup(t *testing.T) {
	until := metav1.NewTime(time.Now().Add(time.Hour))
	s := testServer(t, pausedTestAgent("indefinite-but-expiring", &agentv1alpha1.AgentPause{PausedUntil: &until}))

	paused, resumesAt := s.agentPauseState(context.Background(), "indefinite-but-expiring")
	if !paused || resumesAt.IsZero() {
		t.Errorf("agentPauseState() = (%v, %v), want (true, expiry)", paused, resumesAt)
	}
	if paused, resumesAt := s.agentPauseState(context.Background(), "missing"); paused || !resumesAt.IsZero() {
		t.Errorf("missing agent: got (%v, %v), want (false, zero)", paused, resumesAt)
	}
}

// setAgentPause flips spec.pause on the fake-client agent so the claim gate
// sees the change without a hub restart.
func setAgentPause(t *testing.T, s *Server, name string, pause *agentv1alpha1.AgentPause) {
	t.Helper()
	ctx := context.Background()
	var agent agentv1alpha1.Agent
	if err := s.client.Get(ctx, types.NamespacedName{Name: name, Namespace: s.ns}, &agent); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	agent.Spec.Pause = pause
	if err := s.client.Update(ctx, &agent); err != nil {
		t.Fatalf("update agent: %v", err)
	}
}

func nextTask(t *testing.T, s *Server, agent string, timeoutSecs int) (int, string, eventqueue.Task) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet,
		"/api/internal/agents/"+agent+"/next-task?timeout="+strconv.Itoa(timeoutSecs), nil)
	req.Header.Set("X-Internal-Token", "test-secret")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	var task eventqueue.Task
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &task); err != nil {
			t.Fatalf("decode task: %v (body %s)", err, rec.Body.String())
		}
	}
	return rec.Code, rec.Header().Get("X-Agent-Paused"), task
}

// The headline promise of the pause, asserted against a real queue: while the
// agent is paused next-task hands out nothing and the pending rows stay put as
// a backlog; on resume they are handed out in id order; and an expiring pause
// resumes delivery on its own, with no hub restart.
func TestPauseClaimGate_BacklogDrainsOnResume(t *testing.T) {
	s := testServer(t)
	s.internalValidateSecret = "test-secret"
	s.eventQueue = testQueueStore(t)
	s.mux.HandleFunc("/api/internal/agents/", s.handleInternalAgent)
	pauseTestAgentName := "pause-drain-" + time.Now().UTC().Format("150405.000000000")
	agent := pausedTestAgent(pauseTestAgentName, nil)
	if err := s.client.Create(context.Background(), agent); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	ctx := context.Background()
	// Two tasks from two events, created_at spaced so claim order is fixed.
	ids := make([]int64, 0, 2)
	for i := 0; i < 2; i++ {
		eventID := fmt.Sprintf("test-invtask-pause-%d-%d", time.Now().UnixNano(), i)
		if _, err := s.eventQueue.Pool().Exec(ctx,
			`INSERT INTO events (id, connector, headers, data, raw)
			 VALUES ($1, 'test-connector', '{}', '{}', '')
			 ON CONFLICT (id) DO NOTHING`, eventID); err != nil {
			t.Fatalf("insert event: %v", err)
		}
		var id int64
		err := s.eventQueue.Pool().QueryRow(ctx,
			`INSERT INTO agent_tasks (event_id, agent_name, trigger_name, invocation_id, headers, payload, status, created_at)
			 VALUES ($1, $2, 'trig', 'inv-1', '{}', '{}', 'pending', now() - make_interval(secs => $3)) RETURNING id`,
			eventID, pauseTestAgentName, 10-i).Scan(&id)
		if err != nil {
			t.Fatalf("insert task: %v", err)
		}
		ids = append(ids, id)
	}
	countPending := func() int {
		t.Helper()
		var n int
		if err := s.eventQueue.Pool().QueryRow(ctx,
			`SELECT count(*) FROM agent_tasks WHERE agent_name = $1 AND status = 'pending'`,
			pauseTestAgentName).Scan(&n); err != nil {
			t.Fatalf("count pending: %v", err)
		}
		return n
	}

	// Paused: the claim gate answers no content and the backlog is untouched.
	setAgentPause(t, s, pauseTestAgentName, &agentv1alpha1.AgentPause{})
	code, pausedHdr, task := nextTask(t, s, pauseTestAgentName, 1)
	if code != http.StatusNoContent || pausedHdr != "true" || task.ID != 0 {
		t.Fatalf("paused claim: code=%d X-Agent-Paused=%q task=%v, want 204/true/none", code, pausedHdr, task)
	}
	if got := countPending(); got != 2 {
		t.Errorf("pending rows while paused = %d, want 2 (backlog must not be consumed)", got)
	}

	// Resumed: the backlog drains in id order.
	setAgentPause(t, s, pauseTestAgentName, nil)
	for i, want := range ids {
		code, pausedHdr, task := nextTask(t, s, pauseTestAgentName, 5)
		if code != http.StatusOK || pausedHdr != "" || task.ID != want {
			t.Fatalf("drain %d: code=%d X-Agent-Paused=%q task=%d, want 200//%d", i, code, pausedHdr, task.ID, want)
		}
	}

	// Expiry: a pause with a short window releases on its own — the next poll
	// after it lapses claims again, with no hub restart.
	var id int64
	eventID := fmt.Sprintf("test-invtask-pause-expiry-%d", time.Now().UnixNano())
	if _, err := s.eventQueue.Pool().Exec(ctx,
		`INSERT INTO events (id, connector, headers, data, raw)
		 VALUES ($1, 'test-connector', '{}', '{}', '')
		 ON CONFLICT (id) DO NOTHING`, eventID); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if err := s.eventQueue.Pool().QueryRow(ctx,
		`INSERT INTO agent_tasks (event_id, agent_name, trigger_name, invocation_id, headers, payload, status)
		 VALUES ($1, $2, 'trig', 'inv-1', '{}', '{}', 'pending') RETURNING id`,
		eventID, pauseTestAgentName).Scan(&id); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	expiring := metav1.NewTime(time.Now().Add(300 * time.Millisecond))
	setAgentPause(t, s, pauseTestAgentName, &agentv1alpha1.AgentPause{PausedUntil: &expiring})
	code, _, _ = nextTask(t, s, pauseTestAgentName, 5) // held to expiry, then 204
	if code != http.StatusNoContent {
		t.Fatalf("expiring pause: code=%d, want 204", code)
	}
	code, _, task = nextTask(t, s, pauseTestAgentName, 5)
	if code != http.StatusOK || task.ID != id {
		t.Fatalf("after expiry: code=%d task=%d, want 200/%d (auto-resume)", code, task.ID, id)
	}
}
