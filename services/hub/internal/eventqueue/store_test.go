package eventqueue

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testPool returns a pgxpool.Pool connected to the test database.
// Tests are skipped if TEST_DB_URL is not set.
func testPool(t *testing.T) *pgxpool.Pool {
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
	// Clean up any leftover test data.
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM agent_tasks WHERE agent_name LIKE 'test-reap-%'")
		_, _ = pool.Exec(ctx, "DELETE FROM events WHERE id LIKE 'test-reap-%'")
	})
	return pool
}

// insertTestEvent inserts a minimal event row for use as a foreign key target.
func insertTestEvent(t *testing.T, pool *pgxpool.Pool, eventID string) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx,
		`INSERT INTO events (id, connector, headers, data, raw)
		 VALUES ($1, 'test-connector', '{}', '{}', '')
		 ON CONFLICT (id) DO NOTHING`, eventID)
	if err != nil {
		t.Fatalf("insert test event: %v", err)
	}
}

// insertTestTask inserts an agent_tasks row and returns its ID.
func insertTestTask(t *testing.T, pool *pgxpool.Pool, eventID, agentName string) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	err := pool.QueryRow(ctx,
		`INSERT INTO agent_tasks (event_id, agent_name, trigger_name, invocation_id, headers, payload)
		 VALUES ($1, $2, 'test-trigger', '', '{}', '{}')
		 RETURNING id`, eventID, agentName).Scan(&id)
	if err != nil {
		t.Fatalf("insert test task: %v", err)
	}
	return id
}

func TestReapStaleClaims_ReapsOldClaim(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	eventID := "test-reap-old-evt"
	agentName := "test-reap-agent-old"
	insertTestEvent(t, pool, eventID)
	taskID := insertTestTask(t, pool, eventID, agentName)

	// Simulate a claim that happened 600s ago.
	_, err := pool.Exec(ctx,
		`UPDATE agent_tasks
		 SET status = 'claimed', claimed_at = now() - interval '600 seconds', attempts = 1
		 WHERE id = $1`, taskID)
	if err != nil {
		t.Fatalf("set claimed: %v", err)
	}

	reaped, err := store.ReapStaleClaims(ctx, 300*time.Second)
	if err != nil {
		t.Fatalf("ReapStaleClaims: %v", err)
	}
	if len(reaped) != 1 {
		t.Fatalf("expected 1 reaped task, got %d", len(reaped))
	}
	if reaped[0].ID != taskID {
		t.Errorf("expected reaped task ID %d, got %d", taskID, reaped[0].ID)
	}
	if reaped[0].AgentName != agentName {
		t.Errorf("expected agent name %q, got %q", agentName, reaped[0].AgentName)
	}

	// Verify the task is now pending with error set.
	var status, errMsg string
	var retryAfter *time.Time
	err = pool.QueryRow(ctx,
		`SELECT status, error, retry_after FROM agent_tasks WHERE id = $1`, taskID,
	).Scan(&status, &errMsg, &retryAfter)
	if err != nil {
		t.Fatalf("query task: %v", err)
	}
	if status != "pending" {
		t.Errorf("expected status 'pending', got %q", status)
	}
	if errMsg != "claim timeout: agent did not ack/nak within deadline" {
		t.Errorf("unexpected error message: %q", errMsg)
	}
	if retryAfter == nil {
		t.Error("expected retry_after to be set")
	} else if time.Until(*retryAfter) > 35*time.Second {
		t.Errorf("retry_after too far in future: %v", time.Until(*retryAfter))
	}
}

func TestReapStaleClaims_MarksFailedWhenMaxAttemptsReached(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	eventID := "test-reap-max-evt"
	agentName := "test-reap-agent-max"
	insertTestEvent(t, pool, eventID)
	taskID := insertTestTask(t, pool, eventID, agentName)

	// Set attempts = max_attempts (default 10) and claim in the past.
	_, err := pool.Exec(ctx,
		`UPDATE agent_tasks
		 SET status = 'claimed', claimed_at = now() - interval '600 seconds',
		     attempts = 10, max_attempts = 10
		 WHERE id = $1`, taskID)
	if err != nil {
		t.Fatalf("set claimed: %v", err)
	}

	reaped, err := store.ReapStaleClaims(ctx, 300*time.Second)
	if err != nil {
		t.Fatalf("ReapStaleClaims: %v", err)
	}
	if len(reaped) != 1 {
		t.Fatalf("expected 1 reaped task, got %d", len(reaped))
	}

	var status string
	err = pool.QueryRow(ctx,
		`SELECT status FROM agent_tasks WHERE id = $1`, taskID).Scan(&status)
	if err != nil {
		t.Fatalf("query task: %v", err)
	}
	if status != "failed" {
		t.Errorf("expected status 'failed', got %q", status)
	}
}

func TestReapStaleClaims_NoOpWhenNoStaleClaims(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	reaped, err := store.ReapStaleClaims(ctx, 300*time.Second)
	if err != nil {
		t.Fatalf("ReapStaleClaims: %v", err)
	}
	if len(reaped) != 0 {
		t.Errorf("expected 0 reaped tasks, got %d", len(reaped))
	}
}

func TestReapStaleClaims_DoesNotReapFreshClaims(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	eventID := "test-reap-fresh-evt"
	agentName := "test-reap-agent-fresh"
	insertTestEvent(t, pool, eventID)
	taskID := insertTestTask(t, pool, eventID, agentName)

	// Claim 10s ago — well within the 300s timeout.
	_, err := pool.Exec(ctx,
		`UPDATE agent_tasks
		 SET status = 'claimed', claimed_at = now() - interval '10 seconds', attempts = 1
		 WHERE id = $1`, taskID)
	if err != nil {
		t.Fatalf("set claimed: %v", err)
	}

	reaped, err := store.ReapStaleClaims(ctx, 300*time.Second)
	if err != nil {
		t.Fatalf("ReapStaleClaims: %v", err)
	}
	if len(reaped) != 0 {
		t.Errorf("expected 0 reaped tasks (fresh claim), got %d", len(reaped))
	}

	// Verify task is still claimed.
	var status string
	err = pool.QueryRow(ctx,
		`SELECT status FROM agent_tasks WHERE id = $1`, taskID).Scan(&status)
	if err != nil {
		t.Fatalf("query task: %v", err)
	}
	if status != "claimed" {
		t.Errorf("expected status 'claimed', got %q", status)
	}
}

// insertExpiringTask inserts a pending task stamped with a pickup deadline
// offset from now (negative = already past) and returns its ID.
func insertExpiringTask(t *testing.T, pool *pgxpool.Pool, eventID, agentName string, offset time.Duration) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	err := pool.QueryRow(ctx,
		`INSERT INTO agent_tasks (event_id, agent_name, trigger_name, invocation_id, headers, payload, expires_at)
		 VALUES ($1, $2, 'test-trigger', '', '{}', '{}', now() + make_interval(secs => $3))
		 RETURNING id`, eventID, agentName, offset.Seconds()).Scan(&id)
	if err != nil {
		t.Fatalf("insert expiring task: %v", err)
	}
	return id
}

func TestExpirePendingTasks_ExpiresPastDeadline(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	eventID := "test-reap-expire-old-evt"
	agentName := "test-reap-agent-expire-old"
	insertTestEvent(t, pool, eventID)
	taskID := insertExpiringTask(t, pool, eventID, agentName, -60*time.Second)

	expired, err := store.ExpirePendingTasks(ctx)
	if err != nil {
		t.Fatalf("ExpirePendingTasks: %v", err)
	}
	if len(expired) != 1 {
		t.Fatalf("expected 1 expired task, got %d", len(expired))
	}
	if expired[0].ID != taskID || expired[0].AgentName != agentName {
		t.Errorf("unexpected expired task: %+v", expired[0])
	}

	var status, errMsg string
	var completedAt *time.Time
	err = pool.QueryRow(ctx,
		`SELECT status, error, completed_at FROM agent_tasks WHERE id = $1`, taskID,
	).Scan(&status, &errMsg, &completedAt)
	if err != nil {
		t.Fatalf("query task: %v", err)
	}
	if status != "expired" {
		t.Errorf("expected status 'expired', got %q", status)
	}
	if errMsg != "not picked up in time" {
		t.Errorf("unexpected error message: %q", errMsg)
	}
	if completedAt == nil {
		t.Error("expected completed_at to be set on a terminal task")
	}
}

func TestExpirePendingTasks_LeavesFreshAndUnstampedTasks(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	freshEvent := "test-reap-expire-fresh-evt"
	freshAgent := "test-reap-agent-expire-fresh"
	insertTestEvent(t, pool, freshEvent)
	freshID := insertExpiringTask(t, pool, freshEvent, freshAgent, time.Hour)

	unstampedEvent := "test-reap-expire-none-evt"
	unstampedAgent := "test-reap-agent-expire-none"
	insertTestEvent(t, pool, unstampedEvent)
	unstampedID := insertTestTask(t, pool, unstampedEvent, unstampedAgent)

	expired, err := store.ExpirePendingTasks(ctx)
	if err != nil {
		t.Fatalf("ExpirePendingTasks: %v", err)
	}
	for _, et := range expired {
		if et.ID == freshID {
			t.Errorf("task with a future deadline must not expire (id %d)", freshID)
		}
		if et.ID == unstampedID {
			t.Errorf("task with no deadline must not expire (id %d)", unstampedID)
		}
	}
}

func TestExpirePendingTasks_LeavesClaimedTasksToClaimReaper(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	eventID := "test-reap-expire-claimed-evt"
	agentName := "test-reap-agent-expire-claimed"
	insertTestEvent(t, pool, eventID)
	taskID := insertExpiringTask(t, pool, eventID, agentName, -60*time.Second)

	if _, err := pool.Exec(ctx,
		`UPDATE agent_tasks SET status = 'claimed', claimed_at = now() - interval '600 seconds', attempts = 1
		 WHERE id = $1`, taskID); err != nil {
		t.Fatalf("set claimed: %v", err)
	}

	expired, err := store.ExpirePendingTasks(ctx)
	if err != nil {
		t.Fatalf("ExpirePendingTasks: %v", err)
	}
	for _, et := range expired {
		if et.ID == taskID {
			t.Errorf("claimed task must not be expired by the pickup-deadline reaper (id %d)", taskID)
		}
	}
}

func TestEnqueueTask_StampsExpiry(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	agentName := "test-reap-agent-expire-stamp"
	stampedEvent := "test-reap-expire-stamped-evt"
	unstampedEvent := "test-reap-expire-unstamped-evt"
	insertTestEvent(t, pool, stampedEvent)
	insertTestEvent(t, pool, unstampedEvent)

	store.SetTaskExpiry(time.Hour)
	if err := store.EnqueueTask(ctx, Task{EventID: stampedEvent, AgentName: agentName, Headers: json.RawMessage("{}"), Payload: json.RawMessage("{}")}); err != nil {
		t.Fatalf("enqueue stamped: %v", err)
	}
	store.SetTaskExpiry(0)
	if err := store.EnqueueTask(ctx, Task{EventID: unstampedEvent, AgentName: agentName, Headers: json.RawMessage("{}"), Payload: json.RawMessage("{}")}); err != nil {
		t.Fatalf("enqueue unstamped: %v", err)
	}

	var stamped, unstamped *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT expires_at FROM agent_tasks WHERE event_id = $1 AND agent_name = $2`, stampedEvent, agentName,
	).Scan(&stamped); err != nil {
		t.Fatalf("query stamped task: %v", err)
	}
	if stamped == nil {
		t.Fatal("expected expires_at to be stamped when expiry is configured")
	}
	if left := time.Until(*stamped); left < 55*time.Minute || left > time.Hour {
		t.Errorf("stamped deadline off: %v remaining, want ~1h", left)
	}
	if err := pool.QueryRow(ctx,
		`SELECT expires_at FROM agent_tasks WHERE event_id = $1 AND agent_name = $2`, unstampedEvent, agentName,
	).Scan(&unstamped); err != nil {
		t.Fatalf("query unstamped task: %v", err)
	}
	if unstamped != nil {
		t.Errorf("expected expires_at NULL when expiry is disabled, got %v", *unstamped)
	}
}
