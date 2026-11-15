package eventqueue

import (
	"context"
	"strings"
	"testing"
)

// The task enrichment queries run once per invocations or events page, and the
// tables behind them grow with every run. On a test-sized table the planner
// picks a sequential scan regardless, so these tests ask a sharper question:
// with sequential scans priced out, can the plan reach each table through an
// index at all? A predicate that does not match a partial index's own leaves a
// "Seq Scan" in the plan even then — the regression that made a 200-row
// invocations page cost seconds.
//
// Skipped unless TEST_DB_URL is set; see queuecounts_db_test.go.

func TestTasksForInvocationsPlanUsesIndexes(t *testing.T) {
	plan := explainTaskQuery(t,
		`SELECT `+taskSelectColumns+` `+taskSelectFrom+`
		 WHERE t.invocation_id = ANY($1) AND t.invocation_id <> ''
		 ORDER BY t.id ASC`)

	for _, want := range []string{"idx_task_conversations_invocation", "idx_agent_tasks_invocation"} {
		if !strings.Contains(plan, want) {
			t.Errorf("plan does not use %s:\n%s", want, plan)
		}
	}
	assertNoSeqScan(t, plan, "agent_tasks", "task_conversations")
}

func TestTasksForEventsPlanUsesConversationIndex(t *testing.T) {
	plan := explainTaskQuery(t,
		`SELECT `+taskSelectColumns+` `+taskSelectFrom+`
		 WHERE t.event_id = ANY($1)
		 ORDER BY t.id ASC`)

	if !strings.Contains(plan, "idx_task_conversations_invocation") {
		t.Errorf("plan does not use idx_task_conversations_invocation:\n%s", plan)
	}
	assertNoSeqScan(t, plan, "task_conversations")
}

// explainTaskQuery returns the plan for query with sequential scans disabled on
// one connection, so the setting cannot leak into other tests sharing the pool.
func explainTaskQuery(t *testing.T, query string) string {
	t.Helper()
	ctx := context.Background()
	pool := migratePool(t)

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
		t.Fatalf("disable seqscan: %v", err)
	}
	rows, err := tx.Query(ctx, "EXPLAIN "+query, []string{"inv-plan-test"})
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()

	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read plan: %v", err)
	}
	return strings.Join(lines, "\n")
}

func assertNoSeqScan(t *testing.T, plan string, tables ...string) {
	t.Helper()
	for _, table := range tables {
		if strings.Contains(plan, "Seq Scan on "+table) {
			t.Errorf("plan scans %s sequentially:\n%s", table, plan)
		}
	}
}
