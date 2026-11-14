package skills_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/skills"
)

// These tests pin the authz-set semantics of Store.List against the handler's
// empty-set short-circuit (handlers_skills.go items()): an empty AllowedIDs is
// a closed set that must never reach SQL, a non-empty one is exactly the
// visible set, and nil means "no authz restriction". The same three-way
// contract governs personas, triggers, and cron triggers — keep the per-kind
// parity tests in sync. They also pin that total is the pre-pagination count
// and that paging is stable (the id tiebreaker).

func seedNamedSkills(t *testing.T, store *skills.Store, names ...string) map[string]string {
	t.Helper()
	ctx := context.Background()
	byName := map[string]string{}
	for _, n := range names {
		s := &skills.Skill{ID: "sk-" + n, Name: n, Description: "d", Body: "c"}
		if err := store.Create(ctx, s); err != nil {
			t.Fatalf("create %s: %v", n, err)
		}
		byName[n] = s.ID
	}
	return byName
}

func TestListEmptyAllowedIDsIsClosed(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedNamedSkills(t, store, "a", "b", "c")

	// The handler short-circuits before calling List; the store must agree:
	// an empty set is closed, not "everything".
	items, total, err := store.List(ctx, skills.ListFilter{AllowedIDs: []string{}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 0 || total != 0 {
		t.Errorf("empty AllowedIDs must return nothing, got %d items total=%d", len(items), total)
	}
}

func TestListAllowedIDsSubsetMatchesHandlerFilter(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	ids := seedNamedSkills(t, store, "a", "b", "c")

	// SQL-side id = ANY(...) must match exactly what the old handler-side
	// `if !allowed[s.Name] { continue }` filter kept.
	items, total, err := store.List(ctx, skills.ListFilter{AllowedIDs: []string{ids["a"], ids["c"]}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("expected 2 items total=2, got %d total=%d", len(items), total)
	}
	got := map[string]bool{}
	for _, it := range items {
		got[it.ID] = true
	}
	if !got[ids["a"]] || !got[ids["c"]] || got[ids["b"]] {
		t.Errorf("wrong visible set: %v", got)
	}
}

func TestListNilAllowedIDsUnrestricted(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedNamedSkills(t, store, "a", "b", "c")

	items, total, err := store.List(ctx, skills.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 3 || total != 3 {
		t.Errorf("nil AllowedIDs must see everything, got %d total=%d", len(items), total)
	}
}

func TestListPaginationTotalAndStableOrder(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const n = 55
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("skill-%02d", i)
	}
	seedNamedSkills(t, store, names...)

	seen := map[string]bool{}
	for page := 0; page*20 < n; page++ {
		items, total, err := store.List(ctx, skills.ListFilter{
			OrderBy: "name",
			Limit:   20,
			Offset:  page * 20,
		})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if total != n {
			t.Fatalf("page %d: total=%d, want %d (pre-pagination count)", page, total, n)
		}
		want := 20
		if page == 2 {
			want = n - 40
		}
		if len(items) != want {
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

func TestListOrderByAllowlistRejectsInjection(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedNamedSkills(t, store, "b", "a")

	if _, _, err := store.List(ctx, skills.ListFilter{OrderBy: "name", Desc: true}); err != nil {
		t.Fatalf("allowed column rejected: %v", err)
	}
	for _, bad := range []string{"content", "name; DROP TABLE skills", "id DESC, name"} {
		if _, _, err := store.List(ctx, skills.ListFilter{OrderBy: bad}); err == nil {
			t.Errorf("OrderBy %q must be rejected by the allowlist", bad)
		}
	}
}
