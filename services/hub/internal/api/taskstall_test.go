package api

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ainsel-project/ainsel/services/hub/internal/db"
	"github.com/ainsel-project/ainsel/services/hub/internal/eventqueue"
	"github.com/ainsel-project/ainsel/services/hub/internal/invocations"
	"github.com/ainsel-project/ainsel/services/hub/internal/invocations/invocationstest"
	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

// stalledQueueStore is a task store with the migrations applied. It takes
// TEST_DB_URL when a developer set one, and otherwise boots a throwaway Postgres:
// the point of these tests is the UPDATE that closes a stalled task, and an UPDATE
// nobody runs proves nothing about the schema it names.
func stalledQueueStore(t *testing.T) *eventqueue.Store {
	t.Helper()
	if url := os.Getenv("TEST_DB_URL"); url != "" {
		return testQueueStore(t)
	}
	ctx := context.Background()
	c, err := pgcontainer.Run(ctx, "postgres:17-alpine",
		pgcontainer.WithDatabase("ainsel_test"),
		pgcontainer.WithUsername("test"),
		pgcontainer.WithPassword("test"),
		pgcontainer.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("docker unavailable and TEST_DB_URL unset: %v", err)
	}
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = c.Terminate(ctx)
		t.Fatalf("connection string: %v", err)
	}
	if err := db.Migrate(ctx, dsn); err != nil {
		_ = c.Terminate(ctx)
		t.Fatalf("migrate: %v", err)
	}
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		_ = c.Terminate(ctx)
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		_ = c.Terminate(context.Background())
	})
	return eventqueue.NewStore(pool)
}

// The hub half of issue #342: the operator retires a task Job whose pod never
// starts, and the hub turns that record into a task row that says so. A task whose
// image cannot be pulled must not read QUEUED to everyone who looks at it.

// stalledAgent builds the Agent the operator leaves behind: one task blocked on a
// fatal pull error, one whose cooldown has already run out.
func stalledAgent(name, ns string, liveTask, expiredTask int64, now time.Time) *agentv1alpha1.Agent {
	return &agentv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Status: agentv1alpha1.AgentStatus{
			TaskStartBlocks: []agentv1alpha1.AgentTaskStartBlock{
				{
					TaskID:       liveTask,
					Reason:       "ImagePullBackOff",
					Message:      `Back-off pulling image "dpinsel/ainsel-pi-maui:latest"`,
					BlockedAt:    metav1.NewTime(now.Add(-time.Minute)),
					BlockedUntil: metav1.NewTime(now.Add(29 * time.Minute)),
				},
				{
					TaskID:       expiredTask,
					Reason:       "CreateContainerConfigError",
					BlockedAt:    metav1.NewTime(now.Add(-2 * time.Hour)),
					BlockedUntil: metav1.NewTime(now.Add(-time.Hour)),
				},
			},
		},
	}
}

func TestTaskStartFailureMessage(t *testing.T) {
	got := taskStartFailureMessage(agentv1alpha1.AgentTaskStartBlock{
		Reason:  "ImagePullBackOff",
		Message: `Back-off pulling image "x:latest"`,
	})
	if !strings.Contains(got, "ImagePullBackOff") || !strings.Contains(got, "x:latest") {
		t.Errorf("message = %q, want the reason and the kubelet's line", got)
	}
	if got := taskStartFailureMessage(agentv1alpha1.AgentTaskStartBlock{Reason: "PodNeverStarted"}); got != "pod never started: PodNeverStarted" {
		t.Errorf("message = %q, want the reason alone when the kubelet said nothing", got)
	}
}

// A hub without a task store has nothing to fail — the pass must be a no-op
// rather than a nil dereference on the reaper tick.
func TestFailStalledTasks_NoQueueIsNoOp(t *testing.T) {
	s := testServer(t, stalledAgent("worker", "test-ns", 1, 2, time.Now()))
	n, err := s.FailStalledTasks(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("FailStalledTasks = (%d, %v), want (0, nil) without a queue store", n, err)
	}
}

func TestFailStalledTasks_FailsPendingTaskAndReports(t *testing.T) {
	s := testServer(t)
	s.eventQueue = stalledQueueStore(t)
	s.invocations = invocationstest.New(100)
	ctx := context.Background()
	now := time.Now()

	agent := "test-invtask-stall-agent"
	stalledInv := invocations.Invocation{AgentName: agent, EventID: "test-invtask-stall-live"}
	recorded := s.invocations.Record(stalledInv)

	// The task the operator is still blocked on, and the one whose cooldown has
	// run out — the operator may have dispatched that one again, so the hub must
	// leave it alone.
	stalledID := insertPendingTaskWithDeadline(t, s, "test-invtask-stall-live", agent, recorded.ID, time.Hour)
	expiredBlockID := insertPendingTaskWithDeadline(t, s, "test-invtask-stall-expired", agent, "", time.Hour)

	// The agent object has to carry the ids the rows got.
	ag := stalledAgent("worker", "test-ns", stalledID, expiredBlockID, now)
	if err := s.client.Create(ctx, ag); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	n, err := s.FailStalledTasks(ctx)
	if err != nil {
		t.Fatalf("FailStalledTasks: %v", err)
	}
	if n != 1 {
		t.Fatalf("failed %d tasks, want 1 (the live block only)", n)
	}

	var status, errMsg string
	if err := s.eventQueue.Pool().QueryRow(ctx,
		`SELECT status, error FROM agent_tasks WHERE id = $1`, stalledID).Scan(&status, &errMsg); err != nil {
		t.Fatalf("query stalled task: %v", err)
	}
	if status != "failed" {
		t.Errorf("stalled task status = %q, want failed", status)
	}
	if !strings.Contains(errMsg, "ImagePullBackOff") || !strings.Contains(errMsg, "ainsel-pi-maui") {
		t.Errorf("stalled task error = %q, want the operator's reason and the kubelet's line", errMsg)
	}

	if err := s.eventQueue.Pool().QueryRow(ctx,
		`SELECT status FROM agent_tasks WHERE id = $1`, expiredBlockID).Scan(&status); err != nil {
		t.Fatalf("query task past its block: %v", err)
	}
	if status != "pending" {
		t.Errorf("task past its cooldown = %q, want pending — the operator may have dispatched it again", status)
	}

	if inv, ok := s.invocations.Get(recorded.ID); !ok {
		t.Errorf("invocation %s is gone, want it closed as %q", recorded.ID, invocations.StatusTimeout)
	} else if inv.Status != invocations.StatusTimeout {
		t.Errorf("invocation status = %q, want %q", inv.Status, invocations.StatusTimeout)
	}
}

// A task that already reached a terminal status is not this pass's to overrule:
// the guard is on 'pending', and the count must not claim a change that did not
// happen.
func TestFailStalledTasks_LeavesTerminalTaskAlone(t *testing.T) {
	s := testServer(t)
	s.eventQueue = stalledQueueStore(t)
	ctx := context.Background()

	agent := "test-invtask-stall-done"
	id := insertPendingTaskWithDeadline(t, s, "test-invtask-stall-done", agent, "", time.Hour)
	if _, err := s.eventQueue.Pool().Exec(ctx,
		`UPDATE agent_tasks SET status = 'completed', completed_at = now() WHERE id = $1`, id); err != nil {
		t.Fatalf("complete task: %v", err)
	}

	ag := stalledAgent("worker", "test-ns", id, 0, time.Now())
	if err := s.client.Create(ctx, ag); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	n, err := s.FailStalledTasks(ctx)
	if err != nil {
		t.Fatalf("FailStalledTasks: %v", err)
	}
	if n != 0 {
		t.Fatalf("failed %d tasks, want 0 — the task already completed", n)
	}
	var status string
	if err := s.eventQueue.Pool().QueryRow(ctx,
		`SELECT status FROM agent_tasks WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("query task: %v", err)
	}
	if status != "completed" {
		t.Errorf("status = %q, want the completed row untouched", status)
	}
}
