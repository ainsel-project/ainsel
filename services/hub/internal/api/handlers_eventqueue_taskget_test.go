package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/eventqueue"
	"github.com/ainsel-project/ainsel/services/hub/internal/invocations"
)

// The jobPerTask fetch guard. The Job's own backoffLimit schedules the next pod
// independently of the hub's retry_after, so without this check a task that
// fails immediately (model endpoint down) could burn its whole max_attempts
// budget in seconds — the hub's nack backoff would exist on paper only.
func TestInRetryBackoff(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Minute)
	future := now.Add(time.Minute)

	tests := []struct {
		name string
		task *eventqueue.Task
		want bool
	}{
		{"nil task", nil, false},
		{"never nacked", &eventqueue.Task{Status: "pending"}, false},
		{"backoff already elapsed", &eventqueue.Task{Status: "pending", RetryAfter: &past}, false},
		{"inside backoff", &eventqueue.Task{Status: "pending", RetryAfter: &future}, true},
		// A terminal row is reported as terminal; the worker exits 0 on the
		// status, the backoff is moot.
		{"completed with future retry_after", &eventqueue.Task{Status: "completed", RetryAfter: &future}, false},
		{"failed with future retry_after", &eventqueue.Task{Status: "failed", RetryAfter: &future}, false},
		{"claimed with future retry_after (deployment mode)", &eventqueue.Task{Status: "claimed", RetryAfter: &future}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := inRetryBackoff(tc.task, now); got != tc.want {
				t.Errorf("inRetryBackoff() = %v, want %v", got, tc.want)
			}
		})
	}
}

// End-to-end over a real database: the four outcomes a jobPerTask worker can
// see from GET /api/internal/agents/{name}/tasks/{id}.
func TestAgentTaskGet_JobModeFetch(t *testing.T) {
	s := testServer(t)
	s.internalValidateSecret = "test-secret"
	s.eventQueue = testQueueStore(t)
	s.mux.HandleFunc("/api/internal/agents/", s.handleInternalAgent)

	ctx := context.Background()
	suffix := time.Now().UTC().Format("150405.000000000")
	agent := "taskget-agent-" + suffix

	// One event fans out to two tasks (agent_tasks dedups per agent).
	insert := func(eventID, agentName, status string, retryAfter *time.Time) int64 {
		t.Helper()
		if _, err := s.eventQueue.Pool().Exec(ctx,
			`INSERT INTO events (id, connector, headers, data, raw)
			 VALUES ($1, 'test-connector', '{}', '{}', '')
			 ON CONFLICT (id) DO NOTHING`, eventID); err != nil {
			t.Fatalf("insert event: %v", err)
		}
		var id int64
		err := s.eventQueue.Pool().QueryRow(ctx,
			`INSERT INTO agent_tasks (event_id, agent_name, trigger_name, invocation_id, headers, payload, status, retry_after)
			 VALUES ($1, $2, 'trig', 'inv-1', '{}', '{}', $3, $4) RETURNING id`,
			eventID, agentName, status, retryAfter).Scan(&id)
		if err != nil {
			t.Fatalf("insert task: %v", err)
		}
		return id
	}
	t.Cleanup(func() {
		_, _ = s.eventQueue.Pool().Exec(ctx, "DELETE FROM agent_tasks WHERE agent_name LIKE 'taskget-agent-%'")
		_, _ = s.eventQueue.Pool().Exec(ctx, "DELETE FROM events WHERE id LIKE 'test-taskget-%'")
	})

	pending := insert("test-taskget-"+suffix+".1", agent, "pending", nil)
	backoffUntil := time.Now().Add(time.Minute)
	backingOff := insert("test-taskget-"+suffix+".2", agent, "pending", &backoffUntil)
	terminal := insert("test-taskget-"+suffix+".3", agent, "completed", nil)

	get := func(agentName string, id int64) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/internal/agents/"+agentName+"/tasks/"+strconv.FormatInt(id, 10), nil)
		req.Header.Set("X-Internal-Token", "test-secret")
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, req)
		return rec
	}

	// The happy path: a due pending row is handed out with its status.
	rec := get(agent, pending)
	if rec.Code != http.StatusOK {
		t.Fatalf("pending task: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var task eventqueue.Task
	if err := json.Unmarshal(rec.Body.Bytes(), &task); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if task.ID != pending || task.Status != "pending" {
		t.Errorf("task = {ID:%d Status:%q}, want the pending row", task.ID, task.Status)
	}

	// Inside nack backoff: refused with 409 so the worker stands down and the
	// hub's pacing holds until the due-list edge re-dispatches.
	if rec := get(agent, backingOff); rec.Code != http.StatusConflict {
		t.Errorf("backing-off task: got %d, want 409 (%s)", rec.Code, rec.Body.String())
	}

	// Terminal: reported, not hidden — the worker exits 0 on the status.
	if rec := get(agent, terminal); rec.Code != http.StatusOK {
		t.Errorf("terminal task: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	// Unknown id and a task belonging to another agent are both 404: the fetch
	// must not leak other agents' tasks.
	if rec := get(agent, 999999999); rec.Code != http.StatusNotFound {
		t.Errorf("unknown id: got %d, want 404", rec.Code)
	}
	if rec := get("taskget-someone-else", pending); rec.Code != http.StatusNotFound {
		t.Errorf("cross-agent fetch: got %d, want 404", rec.Code)
	}

	// Without the internal token: 401, even though the route matched.
	req := httptest.NewRequest(http.MethodGet, "/api/internal/agents/"+agent+"/tasks/"+strconv.FormatInt(pending, 10), nil)
	rec = httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: got %d, want 401", rec.Code)
	}
}

// The user-facing /api/v1/agents/ route must not expose the jobPerTask fetch
// or its ack/nack companions: they live solely on /api/internal/agents/, so
// every shape 404s here (even with a valid internal token — the legacy
// aliases were removed outright).
func TestAgentTaskGet_NotRoutedOnPublicAPI(t *testing.T) {
	s := newAgentServer(t)
	s.internalValidateSecret = "test-secret"
	paths := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/agents/some-agent/tasks/42"},
		{http.MethodPost, "/api/v1/agents/some-agent/tasks/42/ack"},
		{http.MethodPost, "/api/v1/agents/some-agent/tasks/42/nack"},
	}
	for _, tc := range paths {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
		req.Header.Set("X-Internal-Token", "test-secret")
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("public %s %s: got %d, want 404", tc.method, tc.path, rec.Code)
		}
	}
}

// A job-mode retry — a row NakTask left pending with attempts == 1 and an
// invocation the nack handler closed 'failure' — must come back from
// fetch-by-id repointed at a fresh 'running' invocation. Without the repoint
// the retried pod runs under the dead id: nothing 'running' in the Activity
// feed, and its logs and transcript filed under a completed record.
func TestAgentTaskGet_JobModeRetryRepointsInvocation(t *testing.T) {
	s := testServer(t)
	s.internalValidateSecret = "test-secret"
	s.eventQueue = testQueueStore(t)
	s.mux.HandleFunc("/api/internal/agents/", s.handleInternalAgent)

	ctx := context.Background()
	suffix := time.Now().UTC().Format("150405.000000000")
	agent := "taskget-retry-" + suffix
	eventID := "test-taskget-retry-" + suffix
	nackedInv := "inv-taskget-retry-" + suffix

	t.Cleanup(func() {
		_, _ = s.eventQueue.Pool().Exec(ctx, "DELETE FROM agent_tasks WHERE agent_name LIKE 'taskget-retry-%'")
		_, _ = s.eventQueue.Pool().Exec(ctx, "DELETE FROM events WHERE id LIKE 'test-taskget-retry-%'")
	})

	// Mirror what the nack handler left behind when the first attempt gave up:
	// a terminal 'failure' invocation row.
	s.invocations.Record(invocations.Invocation{ID: nackedInv, AgentName: agent, TriggerName: "trig", EventID: eventID})
	if !s.invocations.Complete(nackedInv, invocations.StatusFailure, "boom", time.Time{}) {
		t.Fatal("seeding nacked invocation failed")
	}

	// The queue row after the nack: still pending, attempts counted on the
	// row (job mode never claims), still pointing at the closed invocation.
	if _, err := s.eventQueue.Pool().Exec(ctx,
		`INSERT INTO events (id, connector, headers, data, raw)
		 VALUES ($1, 'test-connector', '{}', '{}', '')`, eventID); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	var taskID int64
	err := s.eventQueue.Pool().QueryRow(ctx,
		`INSERT INTO agent_tasks (event_id, agent_name, trigger_name, invocation_id, headers, payload, status, attempts)
		 VALUES ($1, $2, 'trig', $3, '{"type":"issue_comment","X-Invocation-ID":"inv-old"}', '{}', 'pending', 1)
		 RETURNING id`, eventID, agent, nackedInv).Scan(&taskID)
	if err != nil {
		t.Fatalf("insert task: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/internal/agents/"+agent+"/tasks/"+strconv.FormatInt(taskID, 10), nil)
	req.Header.Set("X-Internal-Token", "test-secret")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("retry fetch: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	// The response the worker builds its run from must carry the new id.
	var task eventqueue.Task
	if err := json.Unmarshal(rec.Body.Bytes(), &task); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if task.InvocationID == "" || task.InvocationID == nackedInv {
		t.Fatalf("response invocation_id = %q, want a fresh id (not the closed %q)", task.InvocationID, nackedInv)
	}
	var hdr map[string]any
	if err := json.Unmarshal(task.Headers, &hdr); err != nil {
		t.Fatalf("response headers not JSON: %v", err)
	}
	if hdr[headerInvocationID] != task.InvocationID {
		t.Errorf("response header X-Invocation-ID = %v, want %q", hdr[headerInvocationID], task.InvocationID)
	}

	// The fresh row is running and labelled from the event's connector.
	fresh, ok := s.invocations.Get(task.InvocationID)
	if !ok {
		t.Fatalf("fresh invocation %q not in the store", task.InvocationID)
	}
	if fresh.Status != invocations.StatusRunning {
		t.Errorf("fresh status = %q, want running", fresh.Status)
	}
	if fresh.AgentName != agent || fresh.TriggerName != "trig" || fresh.EventID != eventID || fresh.Connector != "test-connector" {
		t.Errorf("fresh invocation fields wrong: %+v", fresh)
	}

	// The closed first attempt stays as history.
	orig, ok := s.invocations.Get(nackedInv)
	if !ok || !orig.IsTerminal() {
		t.Errorf("nacked attempt's row must stay terminal, got ok=%v", ok)
	}

	// A second fetch (worker restarted mid-retry) must not stack a third row:
	// the running fresh invocation is not terminal, so the id is kept.
	rec2 := httptest.NewRecorder()
	s.mux.ServeHTTP(rec2, req)
	var task2 eventqueue.Task
	if err := json.Unmarshal(rec2.Body.Bytes(), &task2); err != nil {
		t.Fatalf("decode second fetch: %v", err)
	}
	if task2.InvocationID != task.InvocationID {
		t.Errorf("second fetch repointed again: inv=%q, want the running %q", task2.InvocationID, task.InvocationID)
	}
}
