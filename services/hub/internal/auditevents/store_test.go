package auditevents

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/db"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("HUB_DB_URL")
	if dsn == "" {
		t.Skip("HUB_DB_URL not set, skipping integration test")
	}
	ctx := context.Background()
	if err := db.Migrate(ctx, dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)
	s := NewStore(pool)
	// Isolate this run from rows left by other runs/tests.
	tag, err := pool.Exec(ctx, `DELETE FROM auth_events WHERE actor_sub LIKE 'audit-test:%'`)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	_ = tag
	return s
}

func mkEvent(actor, typ, target, outcome string) *Event {
	return &Event{ActorSub: actor, EventType: typ, Target: target, SourceIP: "10.0.0.1", Outcome: outcome}
}

func TestInsertAndList(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	e := mkEvent("audit-test:alice", TypeLogin, "audit-test:alice", OutcomeSuccess)
	if err := s.Insert(ctx, e); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if e.ID == 0 || e.OccurredAt.IsZero() {
		t.Fatalf("insert did not populate ID/OccurredAt: %+v", e)
	}

	items, total, err := s.List(ctx, ListOptions{User: "audit-test:alice"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].EventType != TypeLogin {
		t.Fatalf("total=%d items=%+v, want 1 login row", total, items)
	}
}

func TestListFiltersAndPaging(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := s.Insert(ctx, mkEvent("audit-test:alice", TypeLogin, "audit-test:alice", OutcomeSuccess)); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	if err := s.Insert(ctx, mkEvent("audit-test:admin", TypeUserDeleted, "audit-test:bob", OutcomeSuccess)); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.Insert(ctx, mkEvent("audit-test:mallory", TypeLoginFailed, "audit-test:mallory", OutcomeFailure)); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// user filter matches actor OR target.
	items, total, err := s.List(ctx, ListOptions{User: "audit-test:bob"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || items[0].EventType != TypeUserDeleted {
		t.Fatalf("user filter: total=%d items=%+v", total, items)
	}

	// type OR-ing.
	items, total, err = s.List(ctx, ListOptions{User: "audit-test:mallory", Types: []string{TypeLogin, TypeLoginFailed}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || items[0].Outcome != OutcomeFailure {
		t.Fatalf("type filter: total=%d items=%+v", total, items)
	}

	// paging: newest-first, stable order, total counts all matches.
	items, total, err = s.List(ctx, ListOptions{User: "audit-test:alice", Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 || len(items) != 1 {
		t.Fatalf("paging: total=%d len=%d, want 3/1", total, len(items))
	}

	// time window: from inclusive, to exclusive.
	now := time.Now()
	items, total, err = s.List(ctx, ListOptions{User: "audit-test:alice", Since: now.Add(-time.Hour), Until: now.Add(-time.Minute)})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 0 || len(items) != 0 {
		t.Fatalf("window: total=%d, want 0 (all rows are newer than Until)", total)
	}
}

func TestPruneAndTrimToMax(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := s.Insert(ctx, mkEvent("audit-test:prune", TypeLogin, "audit-test:prune", OutcomeSuccess)); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	// Backdate two rows past the retention cutoff.
	if _, err := s.pool.Exec(ctx,
		`UPDATE auth_events SET occurred_at = now() - interval '2 hours'
		 WHERE id IN (SELECT id FROM auth_events WHERE actor_sub = 'audit-test:prune' ORDER BY id LIMIT 2)`); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	n, err := s.Prune(ctx, time.Hour)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 2 {
		t.Fatalf("pruned %d rows, want 2", n)
	}

	// Cap of 3 keeps the newest 3.
	n, err = s.TrimToMax(ctx, 3)
	if err != nil {
		t.Fatalf("trim: %v", err)
	}
	if n != 0 {
		t.Fatalf("trimmed %d rows, want 0 (3 remain, cap 3)", n)
	}
	n, err = s.TrimToMax(ctx, 2)
	if err != nil {
		t.Fatalf("trim: %v", err)
	}
	if n != 1 {
		t.Fatalf("trimmed %d rows, want 1", n)
	}
	_, total, err := s.List(ctx, ListOptions{User: "audit-test:prune"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 {
		t.Fatalf("remaining %d rows, want 2", total)
	}
}

func TestTrimToMaxNonPositiveIsNoop(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.Insert(ctx, mkEvent("audit-test:cap", TypeLogin, "audit-test:cap", OutcomeSuccess)); err != nil {
		t.Fatalf("insert: %v", err)
	}
	for _, max := range []int{0, -1} {
		n, err := s.TrimToMax(ctx, max)
		if err != nil {
			t.Fatalf("trim(%d): %v", max, err)
		}
		if n != 0 {
			t.Fatalf("trim(%d) deleted %d rows, want 0", max, n)
		}
	}
	_, total, _ := s.List(ctx, ListOptions{User: "audit-test:cap"})
	if total != 1 {
		t.Fatalf("row count = %d, want 1", total)
	}
}
