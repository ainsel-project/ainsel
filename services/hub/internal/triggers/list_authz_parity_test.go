package triggers_test

import (
	"context"
	"fmt"
	"testing"

	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/ainsel-project/ainsel/services/hub/internal/db"
	"github.com/ainsel-project/ainsel/services/hub/internal/triggers"
)

// newTestStore boots a Postgres testcontainer, applies migrations via
// db.Migrate, and returns a Store wired to a fresh pool. Skips the test if
// Docker is unavailable, matching the skills/personas test convention.
func newTestStore(t *testing.T) (*triggers.Store, func()) {
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
	return triggers.NewStore(pool), cleanup
}

// These tests pin the nil / empty / non-empty contract of
// TriggerListOptions.AllowedIDs and CronTriggerListOptions.AllowedIDs against
// the handler short-circuit in handlers_triggers.go and
// handlers_cron_triggers.go, plus total-is-pre-pagination and stable
// ORDER BY id paging. Same contract as skills/personas — keep in sync.

func TestListTriggersEmptyAllowedIDsIsClosed(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		tr := &triggers.Trigger{ID: fmt.Sprintf("tg-%d", i), DisplayName: "t", AgentRef: "a"}
		if err := store.CreateTrigger(ctx, tr); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	items, total, err := store.ListTriggers(ctx, triggers.TriggerListOptions{AllowedIDs: []string{}})
	if err != nil {
		t.Fatalf("ListTriggers: %v", err)
	}
	if len(items) != 0 || total != 0 {
		t.Errorf("empty AllowedIDs must return nothing, got %d items total=%d", len(items), total)
	}
}

func TestListTriggersAllowedIDsSubsetAndNilUnrestricted(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		tr := &triggers.Trigger{ID: fmt.Sprintf("tg-%d", i), DisplayName: "t", AgentRef: "a"}
		if err := store.CreateTrigger(ctx, tr); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	items, total, err := store.ListTriggers(ctx, triggers.TriggerListOptions{AllowedIDs: []string{"tg-0", "tg-2"}})
	if err != nil {
		t.Fatalf("ListTriggers: %v", err)
	}
	if total != 2 || len(items) != 2 || items[0].ID != "tg-0" || items[1].ID != "tg-2" {
		t.Errorf("expected exactly tg-0,tg-2 total=2, got %d items total=%d", len(items), total)
	}

	if _, total, err := store.ListTriggers(ctx, triggers.TriggerListOptions{}); err != nil || total != 3 {
		t.Errorf("nil AllowedIDs must see everything: total=%d err=%v", total, err)
	}
}

func TestListTriggersPaginationTotalAndStableOrder(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const n = 25
	for i := 0; i < n; i++ {
		tr := &triggers.Trigger{ID: fmt.Sprintf("tg-%03d", i), DisplayName: "t", AgentRef: "a"}
		if err := store.CreateTrigger(ctx, tr); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	seen := map[string]bool{}
	for page := 0; page*10 < n; page++ {
		items, total, err := store.ListTriggers(ctx, triggers.TriggerListOptions{Limit: 10, Offset: page * 10})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if total != n {
			t.Fatalf("page %d: total=%d, want %d", page, total, n)
		}
		if want := min(10, n-page*10); len(items) != want {
			t.Fatalf("page %d: %d items, want %d", page, len(items), want)
		}
		for _, it := range items {
			if seen[it.ID] {
				t.Fatalf("page %d: %s seen twice — paging is unstable", page, it.ID)
			}
			seen[it.ID] = true
		}
	}
	if len(seen) != n {
		t.Fatalf("union of pages = %d, want %d", len(seen), n)
	}
}

func TestListCronTriggersEmptyAllowedIDsIsClosed(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		ct := &triggers.CronTrigger{ID: fmt.Sprintf("ct-%d", i), DisplayName: "c", AgentRef: "a", Schedule: "* * * * *"}
		if err := store.CreateCronTrigger(ctx, ct); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	items, total, err := store.ListCronTriggers(ctx, triggers.CronTriggerListOptions{AllowedIDs: []string{}})
	if err != nil {
		t.Fatalf("ListCronTriggers: %v", err)
	}
	if len(items) != 0 || total != 0 {
		t.Errorf("empty AllowedIDs must return nothing, got %d items total=%d", len(items), total)
	}
}

func TestListCronTriggersSubsetAndPagination(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		ct := &triggers.CronTrigger{ID: fmt.Sprintf("ct-%02d", i), DisplayName: "c", AgentRef: "a", Schedule: "* * * * *"}
		if err := store.CreateCronTrigger(ctx, ct); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	items, total, err := store.ListCronTriggers(ctx, triggers.CronTriggerListOptions{
		AllowedIDs: []string{"ct-01", "ct-03", "ct-04"},
		Limit:      2,
		Offset:     1,
	})
	if err != nil {
		t.Fatalf("ListCronTriggers: %v", err)
	}
	// total is the authz-filtered count (3), not the page size.
	if total != 3 {
		t.Fatalf("total=%d, want 3", total)
	}
	if len(items) != 2 || items[0].ID != "ct-03" || items[1].ID != "ct-04" {
		t.Errorf("unexpected page: %+v", items)
	}
}
