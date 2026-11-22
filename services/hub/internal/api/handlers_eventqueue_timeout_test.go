package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/eventqueue"
	"github.com/ainsel-project/ainsel/services/hub/internal/invocations"
	"github.com/ainsel-project/ainsel/services/hub/internal/invocations/invocationstest"
)

// The runner nacks a turn-timeout with `Turn timed out after ${ms}ms`
// (pi/pi-extensions/ainsel-runner/src/task-loop.ts). That attempt's own
// invocation row is the platform ending the run, so it closes as 'timeout';
// every other nack stays a 'failure'.
//
// The literal is asserted here and in the runner's TS test suite so a change to
// the runner's message cannot silently un-badge timed-out runs.
func TestNackInvocationStatus(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want string
	}{
		{"runner turn-timeout literal", "Turn timed out after 21600000ms", invocations.StatusTimeout},
		{"case-insensitive", "turn timed out after 600000ms", invocations.StatusTimeout},
		{"generic nack", "model endpoint down", invocations.StatusFailure},
		{"empty nack", "", invocations.StatusFailure},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nackInvocationStatus(tc.msg); got != tc.want {
				t.Errorf("nackInvocationStatus(%q) = %q, want %q", tc.msg, got, tc.want)
			}
		})
	}
	if nackInvocationStatus(eventqueue.TurnTimeoutMarker+" 1000ms") != invocations.StatusTimeout {
		t.Error("the exported marker must be the string the nack handler matches")
	}
}

// The public shape both the console and any API consumer reads for a task that
// completed after an attempt hit the turn ceiling: success, with the retained
// nack named `lastError` and marked `timedOut` — never a completed task that
// also carries a bare `error`.
func TestTaskStateFromTaskProjectsLastErrorAndTimedOut(t *testing.T) {
	task := eventqueue.Task{
		ID:        8387,
		Status:    "completed",
		Attempts:  2,
		Error:     "Turn timed out after 21600000ms",
		CreatedAt: time.Now().UTC().Add(-7 * time.Hour),
	}
	state := taskStateFromTask(task, time.Now().UTC(), 15*time.Minute)

	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(raw)
	for _, want := range []string{`"runState":"success"`, `"attempts":2`, `"lastError":"Turn timed out after 21600000ms"`, `"timedOut":true`} {
		if !strings.Contains(body, want) {
			t.Errorf("task JSON missing %s: %s", want, body)
		}
	}
	if strings.Contains(body, `"error"`) {
		t.Errorf("a completed task must not expose the retained nack as `error`: %s", body)
	}

	// A plain success keeps the payload quiet.
	plain := taskStateFromTask(eventqueue.Task{Status: "completed", Attempts: 1}, time.Now().UTC(), 15*time.Minute)
	plainRaw, err := json.Marshal(plain)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(plainRaw), "timedOut") || strings.Contains(string(plainRaw), "lastError") {
		t.Errorf("a task with no retained error must emit neither field: %s", plainRaw)
	}
}

// End-to-end over a real database: POST …/tasks/{id}/nack with the runner's
// turn-timeout message closes the attempt's invocation as 'timeout'; a generic
// nack still closes it as 'failure'.
//
// Skipped unless TEST_DB_URL is set (see the eventqueue package header for the
// throwaway Postgres to point it at).
func TestAgentTaskNack_ClosesInvocationByNackReason(t *testing.T) {
	if os.Getenv("TEST_DB_URL") == "" {
		t.Skip("TEST_DB_URL not set, skipping integration test")
	}
	s := testServer(t)
	s.eventQueue = testQueueStore(t)
	s.invocations = invocationstest.New(100)
	ctx := context.Background()

	nack := func(eventID, agent, invocationID, errMsg string) string {
		t.Helper()
		if _, err := s.eventQueue.Pool().Exec(ctx,
			`INSERT INTO events (id, connector, headers, data, raw)
			 VALUES ($1, 'test-connector', '{}', '{}', '')
			 ON CONFLICT (id) DO NOTHING`, eventID); err != nil {
			t.Fatalf("insert event: %v", err)
		}
		var taskID int64
		err := s.eventQueue.Pool().QueryRow(ctx,
			`INSERT INTO agent_tasks (event_id, agent_name, trigger_name, invocation_id, headers, payload)
			 VALUES ($1, $2, 'test-trigger', $3, '{}', '{}') RETURNING id`,
			eventID, agent, invocationID).Scan(&taskID)
		if err != nil {
			t.Fatalf("insert task: %v", err)
		}
		rec := s.invocations.Record(invocations.Invocation{AgentName: agent, EventID: eventID})
		if rec.ID != invocationID {
			t.Fatalf("seeded invocation %q, want %q", rec.ID, invocationID)
		}

		path := "/api/internal/agents/" + agent + "/tasks/" + strconv.FormatInt(taskID, 10) + "/nack"
		req := httptest.NewRequest(http.MethodPost, path,
			strings.NewReader(`{"error":"`+errMsg+`","delay_ms":1000}`))
		w := httptest.NewRecorder()
		s.handleAgentTaskNack(w, req)
		if w.Code != http.StatusNoContent {
			t.Fatalf("nack %s: status %d, body %s", path, w.Code, w.Body.String())
		}
		stored, ok := s.invocations.Get(invocationID)
		if !ok {
			t.Fatalf("invocation %q not found after nack", invocationID)
		}
		return stored.Status
	}

	if got := nack("test-invtask-nack-timeout", "test-invtask-nack-agent", "inv-nack-timeout", "Turn timed out after 21600000ms"); got != invocations.StatusTimeout {
		t.Errorf("turn-timeout nack closed invocation as %q, want %q", got, invocations.StatusTimeout)
	}
	if got := nack("test-invtask-nack-other", "test-invtask-nack-agent", "inv-nack-other", "model endpoint down"); got != invocations.StatusFailure {
		t.Errorf("generic nack closed invocation as %q, want %q", got, invocations.StatusFailure)
	}
}
