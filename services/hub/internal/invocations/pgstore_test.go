package invocations_test

import (
	"context"
	"testing"
	"time"

	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/ainsel-project/ainsel/services/hub/internal/db"
	"github.com/ainsel-project/ainsel/services/hub/internal/invocations"
)

// newTestStore boots a Postgres testcontainer, applies migrations via
// db.Migrate, and returns a PgStore wired to a fresh pool. Skips the test if
// Docker is unavailable, matching the existing test convention.
func newTestStore(t *testing.T) (*invocations.PgStore, func()) {
	t.Helper()
	ctx := context.Background()

	c, err := pgcontainer.Run(ctx, "postgres:17-alpine",
		pgcontainer.WithDatabase("ainsel_test"),
		pgcontainer.WithUsername("test"),
		pgcontainer.WithPassword("test"),
		pgcontainer.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
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

	cleanup := func() {
		pool.Close()
		_ = c.Terminate(context.Background())
	}
	return invocations.NewPgStore(pool), cleanup
}

func TestPgStoreRecordGetComplete(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	rec := s.Record(invocations.Invocation{
		AgentName:   "a-test",
		TriggerName: "t-test",
		EventID:     "evt-1",
		Connector:   "c-1",
	})
	if rec.ID == "" {
		t.Fatal("Record did not assign an ID")
	}
	if rec.Status != invocations.StatusRunning {
		t.Fatalf("Record status = %q, want running", rec.Status)
	}

	got, ok := s.Get(rec.ID)
	if !ok {
		t.Fatal("Get after Record missed the record")
	}
	if got.AgentName != "a-test" || got.EventID != "evt-1" || got.Status != invocations.StatusRunning {
		t.Fatalf("Get returned %+v", got)
	}
	if got.EndTime != nil || got.DurationMs != nil {
		t.Fatalf("running invocation has end time/duration: %+v", got)
	}

	if !s.Complete(rec.ID, invocations.StatusSuccess, "", time.Time{}) {
		t.Fatal("Complete returned false for an existing record")
	}
	got, ok = s.Get(rec.ID)
	if !ok {
		t.Fatal("Get after Complete missed the record")
	}
	if got.Status != invocations.StatusSuccess || got.EndTime == nil || got.DurationMs == nil {
		t.Fatalf("completed invocation = %+v", got)
	}

	if s.Complete("inv-doesnotexist", invocations.StatusSuccess, "", time.Time{}) {
		t.Fatal("Complete returned true for a missing record")
	}
}

func TestPgStoreSubjectRoundTrips(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	subject := &invocations.Subject{
		Repo:   "apps/app",
		Kind:   invocations.SubjectKindIssue,
		Number: 412,
		Title:  "Login fails with a trailing slash",
		Body:   "Repro: open /login/ and get a 500.",
		URL:    "https://git.example/apps/app/issues/412",
	}
	withSubject := s.Record(invocations.Invocation{AgentName: "a-subject", Subject: subject})
	bareRec := s.Record(invocations.Invocation{AgentName: "a-bare"})

	got, ok := s.Get(withSubject.ID)
	if !ok {
		t.Fatal("Get after Record missed the record")
	}
	if got.Subject == nil {
		t.Fatal("Get lost the subject")
	}
	if *got.Subject != *subject {
		t.Errorf("Get subject = %+v, want %+v", *got.Subject, *subject)
	}

	// A row with no subject reads back as nil, not as an empty subject.
	bare, ok := s.Get(bareRec.ID)
	if !ok {
		t.Fatal("Get did not find the subject-less row")
	}
	if bare.Subject != nil {
		t.Errorf("subject-less row read back as %+v, want nil", *bare.Subject)
	}

	// List reads the subject too — it is the query a plugin listing recent work uses.
	listed := s.List(invocations.ListOptions{AgentName: "a-subject"})
	if len(listed) != 1 || listed[0].Subject == nil || listed[0].Subject.Number != 412 {
		t.Fatalf("List lost the subject: %+v", listed)
	}
}

func TestPgStoreListFiltersAndTotal(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	first := s.Record(invocations.Invocation{AgentName: "a-test", TriggerName: "t-a", EventID: "evt-1"})
	time.Sleep(5 * time.Millisecond) // ensure distinct start_time ordering
	second := s.Record(invocations.Invocation{AgentName: "a-other", TriggerName: "t-b", EventID: "evt-1"})
	_ = s.Complete(second.ID, invocations.StatusFailure, "boom", time.Time{})

	// No filters: newest first.
	all := s.List(invocations.ListOptions{})
	if len(all) != 2 || all[0].ID != second.ID || all[1].ID != first.ID {
		t.Fatalf("List = %+v", all)
	}

	// Filter by agent.
	only := s.List(invocations.ListOptions{AgentName: "a-test"})
	if len(only) != 1 || only[0].ID != first.ID {
		t.Fatalf("List(agent) = %+v", only)
	}

	// Filter by event ID.
	byEvent := s.List(invocations.ListOptions{EventID: "evt-1"})
	if len(byEvent) != 2 {
		t.Fatalf("List(event) = %+v", byEvent)
	}

	// Filter by status sees completed rows.
	failed := s.List(invocations.ListOptions{Status: invocations.StatusFailure})
	if len(failed) != 1 || failed[0].ID != second.ID || failed[0].Error != "boom" {
		t.Fatalf("List(status) = %+v", failed)
	}

	// Total ignores the limit.
	items, total := s.ListWithTotal(invocations.ListOptions{Limit: 1})
	if len(items) != 1 || total != 2 {
		t.Fatalf("ListWithTotal(limit=1) = %d items, total %d", len(items), total)
	}
}

func TestPgStoreListFilterByConnectors(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	first := s.Record(invocations.Invocation{AgentName: "a", Connector: "c-1"})
	second := s.Record(invocations.Invocation{AgentName: "b", Connector: "c-2"})
	s.Record(invocations.Invocation{AgentName: "c", Connector: "c-3"})

	got := s.List(invocations.ListOptions{Connectors: []string{"c-1", "c-3"}})
	if len(got) != 2 {
		t.Fatalf("List(c-1,c-3) = %+v, want the c-1 and c-3 rows", got)
	}
	seen := map[string]bool{}
	ids := map[string]bool{}
	for _, inv := range got {
		seen[inv.Connector] = true
		ids[inv.ID] = true
	}
	if !seen["c-1"] || !seen["c-3"] {
		t.Errorf("List(c-1,c-3) returned connectors %v", seen)
	}
	if !ids[first.ID] {
		t.Errorf("List(c-1,c-3) dropped the c-1 row %s", first.ID)
	}

	items, total := s.ListWithTotal(invocations.ListOptions{Connectors: []string{"c-1", "c-2"}, Limit: 1})
	if len(items) != 1 || total != 2 {
		t.Fatalf("ListWithTotal(c-1,c-2, limit=1) = %d items, total %d; want 1 item, total 2", len(items), total)
	}

	only := s.List(invocations.ListOptions{Connectors: []string{"c-2"}})
	if len(only) != 1 || only[0].ID != second.ID {
		t.Fatalf("List(c-2) = %+v, want only the c-2 row", only)
	}

	if got := s.List(invocations.ListOptions{Connectors: []string{"missing"}}); len(got) != 0 {
		t.Fatalf("expected 0 for an unknown connector, got %+v", got)
	}

	// An empty slice means "no restriction", not "match nothing" — same as the
	// memory store, so a caller cannot get different rows by restarting the hub
	// onto the other implementation.
	if got := s.List(invocations.ListOptions{Connectors: []string{}}); len(got) != 3 {
		t.Fatalf("empty Connectors must not restrict, got %d of 3", len(got))
	}
}

func TestPgStorePrune(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	// Record with an explicit StartTime in the past (Record keeps a
	// non-zero StartTime), so the row is older than the prune retention.
	old := s.Record(invocations.Invocation{
		AgentName: "a-old",
		StartTime: time.Now().Add(-72 * time.Hour),
	})
	fresh := s.Record(invocations.Invocation{AgentName: "a-fresh"})

	n, err := s.Prune(context.Background(), 48*time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Fatalf("Prune removed %d rows, want 1", n)
	}
	if _, ok := s.Get(old.ID); ok {
		t.Fatal("old invocation survived pruning")
	}
	if _, ok := s.Get(fresh.ID); !ok {
		t.Fatal("fresh invocation was pruned")
	}
}

func TestPgStoreAgentRollups(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()

	// agent-a: success (2s), failure (4s), and one still running. The
	// running row counts toward Invocations — the same bounds List applies,
	// which is what makes the count match GET /api/v1/invocations?agent=a —
	// but not toward the duration average.
	ok := s.Record(invocations.Invocation{AgentName: "a", StartTime: now.Add(-3 * time.Hour)})
	if !s.Complete(ok.ID, invocations.StatusSuccess, "", now.Add(-3*time.Hour).Add(2*time.Second)) {
		t.Fatal("complete ok")
	}
	bad := s.Record(invocations.Invocation{AgentName: "a", StartTime: now.Add(-2 * time.Hour)})
	if !s.Complete(bad.ID, invocations.StatusFailure, "boom", now.Add(-2*time.Hour).Add(4*time.Second)) {
		t.Fatal("complete bad")
	}
	s.Record(invocations.Invocation{AgentName: "a", StartTime: now.Add(-1 * time.Minute)})

	// agent-b: one success 90 minutes ago, outside a 1h window.
	b := s.Record(invocations.Invocation{AgentName: "b", StartTime: now.Add(-90 * time.Minute)})
	if !s.Complete(b.ID, invocations.StatusSuccess, "", now.Add(-90*time.Minute).Add(1*time.Second)) {
		t.Fatal("complete b")
	}

	got, err := s.AgentRollups(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("AgentRollups: %v", err)
	}
	if len(got) != 2 || got[0].AgentName != "a" || got[1].AgentName != "b" {
		t.Fatalf("expected agents a,b sorted, got %+v", got)
	}
	if got[0].Invocations != 3 || got[0].Failures != 1 {
		t.Errorf("a: invocations=%d failures=%d, want 3/1 (all statuses; failure counts)", got[0].Invocations, got[0].Failures)
	}
	if got[0].AvgDurationMs != 3000 {
		t.Errorf("a: avgDurationMs=%v, want 3000 (mean over completed only)", got[0].AvgDurationMs)
	}
	if got[1].Invocations != 1 || got[1].Failures != 0 || got[1].AvgDurationMs != 1000 {
		t.Errorf("b: %+v, want 1/0/1000", got[1])
	}

	// Window bounds (>= since, < until) match listFilterClause, so the
	// windowed count equals ListWithTotal for the same window.
	since := now.Add(-60 * time.Minute)
	until := now.Add(time.Minute)
	windowed, err := s.AgentRollups(ctx, since, until)
	if err != nil {
		t.Fatalf("AgentRollups windowed: %v", err)
	}
	if len(windowed) != 1 || windowed[0].AgentName != "a" || windowed[0].Invocations != 1 {
		t.Fatalf("expected only agent-a's running row in the window, got %+v", windowed)
	}
	if _, total := s.ListWithTotal(invocations.ListOptions{AgentName: "a", Since: since, Until: until}); total != 1 {
		t.Fatalf("ListWithTotal disagrees with AgentRollups on the same window: total=%d", total)
	}
}
