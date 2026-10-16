package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/DominikPinsel/ainsel/services/hub/internal/eventqueue"
	"github.com/DominikPinsel/ainsel/services/hub/internal/invocations"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testQueueStore connects to the integration database (TEST_DB_URL) and
// returns an eventqueue.Store backed by it. Skipped when TEST_DB_URL is unset.
// Rows created by these tests are prefixed with "test-invtask-" and cleaned up.
func testQueueStore(t *testing.T) *eventqueue.Store {
	t.Helper()
	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		t.Skip("TEST_DB_URL not set, skipping integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect to test db: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM agent_tasks WHERE event_id LIKE 'test-invtask-%'")
		_, _ = pool.Exec(ctx, "DELETE FROM events WHERE id LIKE 'test-invtask-%'")
	})
	return eventqueue.NewStore(pool)
}

// TestInvocations_EventEnrichesTasks verifies that GET /api/v1/invocations?event=…
// attaches the agent_tasks queue state to known invocations and synthesizes
// records for tasks missing from the in-memory store (#195).
func TestInvocations_EventEnrichesTasks(t *testing.T) {
	if os.Getenv("TEST_DB_URL") == "" {
		t.Skip("TEST_DB_URL not set, skipping integration test")
	}
	s := testServer(t)
	s.eventQueue = testQueueStore(t)
	s.mux.HandleFunc("/api/v1/invocations", s.handleInvocations)

	// Event with two tasks for two agents (agent_tasks deduplicates on
	// (event_id, agent_name)): one whose invocation is in memory, one that
	// is not (pre-restart history / still queued).
	eventID := "test-invtask-" + time.Now().UTC().Format("150405.000000000")
	ctx := context.Background()
	if _, err := s.eventQueue.Pool().Exec(ctx,
		`INSERT INTO events (id, connector, headers, data, raw)
		 VALUES ($1, 'test-connector', '{}', '{}', '')
		 ON CONFLICT (id) DO NOTHING`, eventID); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	// In-memory invocation for the completed task.
	mem := s.invocations.Record(invocations.Invocation{
		AgentName:   "agent-a",
		TriggerName: "trigger-a",
		StartTime:   time.Now().UTC().Add(-2 * time.Minute),
	})
	completedAt := time.Now().UTC().Add(-time.Minute)
	for _, tt := range []struct {
		invocationID string
		agent        string
		trigger      string
		status       string
		attempts     int
		errMsg       string
	}{
		{mem.ID, "agent-a", "trigger-a", "completed", 1, ""},
		{"inv-test-synthetic", "agent-b", "trigger-b", "pending", 0, ""},
	} {
		if _, err := s.eventQueue.Pool().Exec(ctx,
			`INSERT INTO agent_tasks (event_id, agent_name, trigger_name, invocation_id, headers, payload, status, attempts, error, completed_at)
			 VALUES ($1, $2, $3, $4, '{}', '{}', $5, $6, $7, CASE WHEN $5 = 'completed' THEN now() END)`,
			eventID, tt.agent, tt.trigger, tt.invocationID, tt.status, tt.attempts, tt.errMsg); err != nil {
			t.Fatalf("insert task: %v", err)
		}
	}
	_ = completedAt

	req := httptest.NewRequest(http.MethodGet, "/api/v1/invocations?event="+eventID, nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Invocations []invocations.Invocation `json:"invocations"`
		Total       int                      `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Total != 2 {
		t.Fatalf("expected 2 invocations (1 in-memory + 1 synthetic), got %d: %s", resp.Total, rec.Body.String())
	}

	byID := map[string]invocations.Invocation{}
	for _, inv := range resp.Invocations {
		byID[inv.ID] = inv
	}

	// The in-memory record carries the queue state of its agent_tasks row.
	enriched, ok := byID[mem.ID]
	if !ok {
		t.Fatalf("in-memory invocation %s missing from response", mem.ID)
	}
	if enriched.Task == nil || enriched.Task.Status != "completed" || enriched.Task.Attempts != 1 {
		t.Errorf("expected task state completed/1 on %s, got %+v", mem.ID, enriched.Task)
	}
	if enriched.Task != nil && enriched.Task.RunState != invocations.StatusSuccess {
		t.Errorf("expected derived run state success on %s, got %q", mem.ID, enriched.Task.RunState)
	}

	// The queued task appears as a synthetic queued invocation with its queue
	// state: no worker has picked it up, so it must not claim to be running.
	synth, ok := byID["inv-test-synthetic"]
	if !ok {
		t.Fatalf("synthetic invocation for queued task missing from response: %s", rec.Body.String())
	}
	if synth.Status != invocations.StatusQueued {
		t.Errorf("expected synthetic status queued, got %s", synth.Status)
	}
	if synth.Task == nil || synth.Task.Status != "pending" || synth.Task.Attempts != 0 || synth.Task.RunState != invocations.StatusQueued {
		t.Errorf("expected task state pending/0 with derived run state queued, got %+v", synth.Task)
	}
	if synth.AgentName != "agent-b" {
		t.Errorf("expected agent-b, got %s", synth.AgentName)
	}
}

// TestInvocations_EventSynthesizesFailedTask checks that a failed task
// without a matching in-memory invocation surfaces as a failure entry with
// the task error attached.
func TestInvocations_EventSynthesizesFailedTask(t *testing.T) {
	if os.Getenv("TEST_DB_URL") == "" {
		t.Skip("TEST_DB_URL not set, skipping integration test")
	}
	s := testServer(t)
	s.eventQueue = testQueueStore(t)
	s.mux.HandleFunc("/api/v1/invocations", s.handleInvocations)

	eventID := "test-invtask-fail-" + time.Now().UTC().Format("150405.000000000")
	if _, err := s.eventQueue.Pool().Exec(context.Background(),
		`INSERT INTO events (id, connector, headers, data, raw)
		 VALUES ($1, 'test-connector', '{}', '{}', '')
		 ON CONFLICT (id) DO NOTHING`, eventID); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if _, err := s.eventQueue.Pool().Exec(context.Background(),
		`INSERT INTO agent_tasks (event_id, agent_name, trigger_name, invocation_id, headers, payload, status, attempts, error)
		 VALUES ($1, 'agent-b', 'trigger-b', 'inv-test-failed', '{}', '{}', 'failed', 3, 'Turn timed out after 600000ms')`,
		eventID); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/invocations?event="+eventID, nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	var resp struct {
		Invocations []invocations.Invocation `json:"invocations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Invocations) != 1 {
		t.Fatalf("expected 1 synthetic invocation, got %d", len(resp.Invocations))
	}
	inv := resp.Invocations[0]
	if inv.Status != invocations.StatusFailure {
		t.Errorf("expected failure status, got %s", inv.Status)
	}
	if inv.Task == nil || inv.Task.Attempts != 3 || inv.Task.Error != "Turn timed out after 600000ms" {
		t.Errorf("unexpected task state: %+v", inv.Task)
	}
	if inv.Error != "Turn timed out after 600000ms" {
		t.Errorf("expected error on invocation, got %q", inv.Error)
	}
}

func TestInvocationStatusFromTask(t *testing.T) {
	started := time.Now().UTC().Add(-2 * time.Minute)
	cases := []struct {
		name string
		task eventqueue.Task
		want string
	}{
		{"pending unstarted is queued", eventqueue.Task{Status: "pending"}, invocations.StatusQueued},
		// The per-task Job case: the row stays "pending" while the pod works.
		// Conversation evidence (surfaced as StartedAt) is what makes it running.
		{"pending with start evidence is running", eventqueue.Task{Status: "pending", StartedAt: &started}, invocations.StatusRunning},
		{"claimed is running (resident path)", eventqueue.Task{Status: "claimed"}, invocations.StatusRunning},
		{"completed is success", eventqueue.Task{Status: "completed"}, invocations.StatusSuccess},
		{"failed is failure", eventqueue.Task{Status: "failed"}, invocations.StatusFailure},
	}
	for _, tc := range cases {
		if got := invocationStatusFromTask(tc.task); got != tc.want {
			t.Errorf("%s: invocationStatusFromTask(%+v) = %q, want %q", tc.name, tc.task, got, tc.want)
		}
	}
}

func TestDeriveRunState(t *testing.T) {
	now := time.Now().UTC()
	recent := now.Add(-time.Minute)
	started := now.Add(-2 * time.Minute)
	stuckAfter := 15 * time.Minute

	cases := []struct {
		name      string
		task      *eventqueue.Task
		inv       *invocations.Invocation
		want      string
		wantStuck bool
	}{
		{
			"no task, no invocation: unknown",
			nil, nil,
			invocations.StatusUnknown, false,
		},
		{
			"no task, running invocation (record without enqueue): unknown, not running",
			nil, &invocations.Invocation{Status: invocations.StatusRunning},
			invocations.StatusUnknown, false,
		},
		{
			"no task, pruned record: unknown, not queued",
			nil, nil,
			invocations.StatusUnknown, false,
		},
		{
			"no task, terminal invocation keeps its outcome",
			nil, &invocations.Invocation{Status: invocations.StatusTimeout},
			invocations.StatusTimeout, false,
		},
		{
			"pending unstarted: queued",
			&eventqueue.Task{Status: "pending", CreatedAt: recent}, nil,
			invocations.StatusQueued, false,
		},
		{
			"pending with conversation evidence: running (Job-mode live run)",
			&eventqueue.Task{Status: "pending", CreatedAt: recent, StartedAt: &started}, nil,
			invocations.StatusRunning, false,
		},
		{
			"claimed: running (resident path, no enrichment join)",
			&eventqueue.Task{Status: "claimed", CreatedAt: recent}, nil,
			invocations.StatusRunning, false,
		},
		{
			"pending past the start deadline: queued and stuck",
			&eventqueue.Task{Status: "pending", CreatedAt: now.Add(-time.Hour)}, nil,
			invocations.StatusQueued, true,
		},
		{
			"started past the deadline is never stuck",
			&eventqueue.Task{Status: "pending", CreatedAt: now.Add(-time.Hour), StartedAt: &started}, nil,
			invocations.StatusRunning, false,
		},
		{
			"task beats a stale running invocation",
			&eventqueue.Task{Status: "completed", CreatedAt: recent},
			&invocations.Invocation{Status: invocations.StatusRunning},
			invocations.StatusSuccess, false,
		},
		{
			"failed task is failure even with a success invocation",
			&eventqueue.Task{Status: "failed", CreatedAt: recent},
			&invocations.Invocation{Status: invocations.StatusSuccess},
			invocations.StatusFailure, false,
		},
	}
	for _, tc := range cases {
		got, stuck := deriveRunState(tc.task, tc.inv, now, stuckAfter)
		if got != tc.want || stuck != tc.wantStuck {
			t.Errorf("%s: deriveRunState = (%q, %v), want (%q, %v)", tc.name, got, stuck, tc.want, tc.wantStuck)
		}
	}
}

// TestInvocations_ListWithoutEventAttachesTaskState verifies the non-event
// list path (Observability → Routing, a channel's Runs tab): GET
// /api/v1/invocations without ?event= carries the queue state for rows that
// have one — unstarted → queued, pending-with-conversation-evidence (the
// per-task Job case) → running — and leaves rows without a task row
// untouched. Attach-only: no synthesized rows, total unchanged.
func TestInvocations_ListWithoutEventAttachesTaskState(t *testing.T) {
	if os.Getenv("TEST_DB_URL") == "" {
		t.Skip("TEST_DB_URL not set, skipping integration test")
	}
	s := testServer(t)
	s.eventQueue = testQueueStore(t)
	s.mux.HandleFunc("/api/v1/invocations", s.handleInvocations)

	ctx := context.Background()
	suffix := time.Now().UTC().Format("150405.000000000")
	eventID := "test-invtask-" + suffix
	agent := "agent-it-" + suffix
	if _, err := s.eventQueue.Pool().Exec(ctx,
		`INSERT INTO events (id, connector, headers, data, raw)
		 VALUES ($1, 'test-connector', '{}', '{}', '')`, eventID); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	// Three invocations: queued (pending, no evidence), running in Job mode
	// (pending + conversation row), and one with no task row at all.
	queued := s.invocations.Record(invocations.Invocation{AgentName: agent, TriggerName: "t"})
	jobRun := s.invocations.Record(invocations.Invocation{AgentName: agent, TriggerName: "t"})
	noTask := s.invocations.Record(invocations.Invocation{AgentName: agent, TriggerName: "t"})

	for _, id := range []string{queued.ID, jobRun.ID} {
		if _, err := s.eventQueue.Pool().Exec(ctx,
			`INSERT INTO agent_tasks (event_id, agent_name, trigger_name, invocation_id, headers, payload, status)
			 VALUES ($1, $2, 't', $3, '{}', '{}', 'pending')`, eventID, agent, id); err != nil {
			t.Fatalf("insert task: %v", err)
		}
	}
	if _, err := s.eventQueue.Pool().Exec(ctx,
		`INSERT INTO task_conversations (invocation_id, agent_name, role, content)
		 VALUES ($1, $2, 'assistant', 'working')`, jobRun.ID, agent); err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.eventQueue.Pool().Exec(context.Background(),
			"DELETE FROM task_conversations WHERE invocation_id IN ($1, $2)", queued.ID, jobRun.ID)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/invocations?agent="+agent, nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Invocations []invocations.Invocation `json:"invocations"`
		Total       int                      `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// Attach-only: the three in-memory rows, nothing synthesized.
	if resp.Total != 3 || len(resp.Invocations) != 3 {
		t.Fatalf("expected 3 invocations, got %d (total %d)", len(resp.Invocations), resp.Total)
	}
	byID := map[string]invocations.Invocation{}
	for _, inv := range resp.Invocations {
		byID[inv.ID] = inv
	}
	if inv := byID[queued.ID]; inv.Task == nil || inv.Task.RunState != invocations.StatusQueued {
		t.Errorf("queued row: expected derived state queued, got %+v", inv.Task)
	}
	if inv := byID[jobRun.ID]; inv.Task == nil || inv.Task.RunState != invocations.StatusRunning {
		t.Errorf("Job-mode row: expected derived state running, got %+v", inv.Task)
	}
	if inv := byID[noTask.ID]; inv.Task != nil {
		t.Errorf("taskless row: expected no task state, got %+v", inv.Task)
	}
}
