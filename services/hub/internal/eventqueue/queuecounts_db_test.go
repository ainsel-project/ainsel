package eventqueue

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ainsel-project/ainsel/services/hub/internal/db"
)

// Queue depth decides how many containers an agent runs, so these queries are
// load-bearing in a way the rest of the store is not: a count that silently
// includes finished work parks pods on nothing, and one that excludes work in
// retry backoff scales an agent down while its tasks are still coming.
//
// Skipped unless TEST_DB_URL is set. CI has no database for this package, so
// run them locally against a throwaway Postgres:
//
//	docker run -d --name pg -e POSTGRES_PASSWORD=x -e POSTGRES_DB=ainsel_test -p 55432:5432 postgres:16-alpine
//	TEST_DB_URL=postgres://postgres:x@localhost:55432/ainsel_test?sslmode=disable \
//	  go test ./internal/eventqueue/ -run TestQueueCounts -v

// migratePool returns a pool on a schema that actually exists.
func migratePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := testPool(t)
	if err := db.Migrate(context.Background(), os.Getenv("TEST_DB_URL")); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return pool
}

// watchObserver installs an observer that records every agent name the store
// reports, so a test can assert on who was woken rather than on timings.
func watchObserver(t *testing.T, s *Store) <-chan string {
	t.Helper()
	seen := make(chan string, 64)
	s.SetQueueObserver(func(agentName string) {
		select {
		case seen <- agentName:
		default:
		}
	})
	t.Cleanup(func() { s.SetQueueObserver(nil) })
	return seen
}

func drained(t *testing.T, seen <-chan string) []string {
	t.Helper()
	var got []string
	for {
		select {
		case name := <-seen:
			got = append(got, name)
		default:
			return got
		}
	}
}

func TestQueueCountsMeasuresRealRows(t *testing.T) {
	pool := migratePool(t)
	ctx := context.Background()
	s := NewStore(pool)

	const agent = "test-reap-counts"

	// The unique key is (event_id, agent_name), so four tasks need four events.
	ids := make([]int64, 0, 4)
	for i := 0; i < 4; i++ {
		evt := fmt.Sprintf("test-reap-evt-counts-%d", i)
		insertTestEvent(t, pool, evt)
		ids = append(ids, insertTestTask(t, pool, evt, agent))
	}

	counts, err := s.QueueCounts(ctx, agent)
	if err != nil {
		t.Fatalf("QueueCounts: %v", err)
	}
	if counts.Pending != 4 || counts.Active != 0 {
		t.Errorf("QueueCounts = %+v, want 4 pending 0 active", counts)
	}

	// Terminal states must not hold an agent awake.
	if err := s.AckTask(ctx, ids[0]); err != nil {
		t.Fatalf("AckTask: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agent_tasks SET status = 'failed' WHERE id = $1`, ids[3]); err != nil {
		t.Fatalf("fail a task: %v", err)
	}
	counts, _ = s.QueueCounts(ctx, agent)
	if counts.Pending != 2 || counts.Active != 0 {
		t.Errorf("after ack and failure QueueCounts = %+v, want 2 pending 0 active", counts)
	}

	if empty, err := s.QueueCounts(ctx, "test-reap-nobody"); err != nil || empty.Pending != 0 || empty.Active != 0 {
		t.Errorf("QueueCounts for an unknown agent = %+v (%v), want zeros", empty, err)
	}
}

// A nak'd task sits in `pending` with retry_after in the future. It is still
// work, and an agent scaled to zero around it would strand it until something
// else woke the agent — so it has to count.
func TestQueueCountsIncludesTasksInRetryBackoff(t *testing.T) {
	pool := migratePool(t)
	ctx := context.Background()
	s := NewStore(pool)

	const agent = "test-reap-backoff"
	evt := "test-reap-evt-backoff"
	insertTestEvent(t, pool, evt)
	id := insertTestTask(t, pool, evt, agent)

	if err := s.NakTask(ctx, id, 300*time.Second, "transient"); err != nil {
		t.Fatalf("NakTask: %v", err)
	}

	counts, err := s.QueueCounts(ctx, agent)
	if err != nil {
		t.Fatalf("QueueCounts: %v", err)
	}
	if counts.Pending != 1 {
		t.Errorf("QueueCounts = %+v, want the deferred task counted as pending", counts)
	}

	// ...but it is not due: no Job may be dispatched for it before retry_after.
	due, err := s.DueTaskIDs(ctx, agent, 10)
	if err != nil {
		t.Fatalf("DueTaskIDs: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("DueTaskIDs = %v while retry_after is in the future, want none", due)
	}
}

func TestAllQueueCountsCoversAgentsWithWork(t *testing.T) {
	pool := migratePool(t)
	ctx := context.Background()
	s := NewStore(pool)

	const agentA, agentB = "test-reap-all-a", "test-reap-all-b"
	insertTestEvent(t, pool, "test-reap-evt-all-a")
	insertTestEvent(t, pool, "test-reap-evt-all-b")
	insertTestTask(t, pool, "test-reap-evt-all-a", agentA)
	tb := insertTestTask(t, pool, "test-reap-evt-all-b", agentB)

	all, err := s.AllQueueCounts(ctx)
	if err != nil {
		t.Fatalf("AllQueueCounts: %v", err)
	}
	if got := all[agentA]; got.Pending != 1 || got.Active != 0 {
		t.Errorf("AllQueueCounts[%s] = %+v, want 1 pending", agentA, got)
	}
	if got := all[agentB]; got.Pending != 1 || got.Active != 0 {
		t.Errorf("AllQueueCounts[%s] = %+v, want 1 pending", agentB, got)
	}

	// Drained agents are absent, not zero: the publisher uses absence to forget
	// an agent, and a final zero row is what tells the operator it may scale down.
	if err := s.AckTask(ctx, tb); err != nil {
		t.Fatalf("AckTask: %v", err)
	}
	all, _ = s.AllQueueCounts(ctx)
	if _, ok := all[agentB]; ok {
		t.Errorf("AllQueueCounts still lists %s after its only task completed: %+v", agentB, all[agentB])
	}
}

// The observer is the wake path. If a transition forgets to report, the operator
// never sees the queue grow and an agent sits idle with work waiting.
func TestTransitionsObserveTheirAgent(t *testing.T) {
	pool := migratePool(t)
	ctx := context.Background()
	s := NewStore(pool)
	seen := watchObserver(t, s)

	const agent = "test-reap-observe"
	insertTestEvent(t, pool, "test-reap-evt-observe")

	drained(t, seen)

	if err := s.EnqueueTask(ctx, Task{
		EventID: "test-reap-evt-observe", AgentName: agent, TriggerName: "test",
		Headers: []byte("{}"), Payload: []byte("{}"),
	}); err != nil {
		t.Fatalf("EnqueueTask: %v", err)
	}
	if names := drained(t, seen); len(names) == 0 || names[0] != agent {
		t.Errorf("after enqueue observer saw %v, want %s", names, agent)
	}

	var taskID int64
	if err := pool.QueryRow(ctx,
		`SELECT id FROM agent_tasks WHERE event_id = 'test-reap-evt-observe' AND agent_name = $1`, agent,
	).Scan(&taskID); err != nil {
		t.Fatalf("find enqueued task: %v", err)
	}

	if err := s.NakTask(ctx, taskID, time.Second, "transient"); err != nil {
		t.Fatalf("NakTask: %v", err)
	}
	if names := drained(t, seen); len(names) == 0 || names[0] != agent {
		t.Errorf("after nack observer saw %v, want %s", names, agent)
	}

	if err := s.AckTask(ctx, taskID); err != nil {
		t.Fatalf("AckTask: %v", err)
	}
	if names := drained(t, seen); len(names) == 0 || names[0] != agent {
		t.Errorf("after ack observer saw %v, want %s", names, agent)
	}

	// An ack for a task that never existed changes nothing, so it must not wake
	// anybody — least of all an agent named "".
	if err := s.AckTask(ctx, 999999999); err != nil {
		t.Fatalf("AckTask for an unknown id: %v", err)
	}
	if names := drained(t, seen); len(names) != 0 {
		t.Errorf("unknown ack woke %v, want nobody", names)
	}
}
