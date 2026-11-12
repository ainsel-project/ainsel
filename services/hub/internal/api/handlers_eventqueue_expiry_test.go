package api

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/invocations"
	"github.com/ainsel-project/ainsel/services/hub/internal/invocations/invocationstest"
)

// insertPendingTaskWithDeadline inserts a pending agent_tasks row stamped with
// a pickup deadline offset from now, carrying the given invocation id. Returns
// the task id. Uses the "test-invtask-" event prefix so the shared
// testQueueStore cleanup reclaims it.
func insertPendingTaskWithDeadline(t *testing.T, s *Server, eventID, agentName, invocationID string, offset time.Duration) int64 {
	t.Helper()
	ctx := context.Background()
	if _, err := s.eventQueue.Pool().Exec(ctx,
		`INSERT INTO events (id, connector, headers, data, raw)
		 VALUES ($1, 'test-connector', '{}', '{}', '')
		 ON CONFLICT (id) DO NOTHING`, eventID); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	var id int64
	err := s.eventQueue.Pool().QueryRow(ctx,
		`INSERT INTO agent_tasks (event_id, agent_name, trigger_name, invocation_id, headers, payload, expires_at)
		 VALUES ($1, $2, 'test-trigger', $3, '{}', '{}', now() + make_interval(secs => $4))
		 RETURNING id`, eventID, agentName, invocationID, offset.Seconds()).Scan(&id)
	if err != nil {
		t.Fatalf("insert task: %v", err)
	}
	return id
}

// TestExpireQueuedTasks_ExpiresAndReports covers the pickup-deadline reaper
// end to end (issue #236): a pending task past its deadline becomes 'expired'
// and its invocation is closed as 'timeout' through the same report path a
// nacked task uses, while a task inside its deadline is left alone.
func TestExpireQueuedTasks_ExpiresAndReports(t *testing.T) {
	if os.Getenv("TEST_DB_URL") == "" {
		t.Skip("TEST_DB_URL not set, skipping integration test")
	}
	s := testServer(t)
	s.eventQueue = testQueueStore(t)
	s.invocations = invocationstest.New(100)
	ctx := context.Background()

	agent := "test-invtask-expire-agent"
	expiredInv := invocations.Invocation{AgentName: agent, EventID: "test-invtask-expire-old"}
	recorded := s.invocations.Record(expiredInv)

	expiredID := insertPendingTaskWithDeadline(t, s, "test-invtask-expire-old", agent, recorded.ID, -time.Minute)
	freshID := insertPendingTaskWithDeadline(t, s, "test-invtask-expire-fresh", agent, "", time.Hour)

	n, err := s.ExpireQueuedTasks(ctx)
	if err != nil {
		t.Fatalf("ExpireQueuedTasks: %v", err)
	}
	if n != 1 {
		t.Fatalf("expired %d tasks, want 1", n)
	}

	var status, errMsg string
	if err := s.eventQueue.Pool().QueryRow(ctx,
		`SELECT status, error FROM agent_tasks WHERE id = $1`, expiredID).Scan(&status, &errMsg); err != nil {
		t.Fatalf("query expired task: %v", err)
	}
	if status != "expired" {
		t.Errorf("expired task status = %q, want expired", status)
	}
	if errMsg != "not picked up in time" {
		t.Errorf("expired task error = %q, want %q", errMsg, "not picked up in time")
	}

	if err := s.eventQueue.Pool().QueryRow(ctx,
		`SELECT status FROM agent_tasks WHERE id = $1`, freshID).Scan(&status); err != nil {
		t.Fatalf("query fresh task: %v", err)
	}
	if status != "pending" {
		t.Errorf("fresh task status = %q, want pending", status)
	}

	// The sender-facing report closes the invocation as a timeout, the same
	// terminal vocabulary the failure path uses.
	inv, ok := s.invocations.Get(recorded.ID)
	if !ok {
		t.Fatal("invocation record missing after expiry")
	}
	if inv.Status != invocations.StatusTimeout {
		t.Errorf("invocation status = %q, want %q", inv.Status, invocations.StatusTimeout)
	}
	if inv.Error != "not picked up in time" {
		t.Errorf("invocation error = %q, want %q", inv.Error, "not picked up in time")
	}
}

// TestExpireQueuedTasks_NoQueueIsNoOp guards the nil-store path: a hub without
// an event queue must not panic when the reaper loop fires.
func TestExpireQueuedTasks_NoQueueIsNoOp(t *testing.T) {
	s := testServer(t)
	s.eventQueue = nil
	n, err := s.ExpireQueuedTasks(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("ExpireQueuedTasks with no queue = (%d, %v), want (0, nil)", n, err)
	}
}
