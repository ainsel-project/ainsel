package personas_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/personas"
)

// Parity with the pre-pagination handler behaviour (handlers_personas.go):
// the authz set is applied in SQL via AllowedIDs, an empty set is closed, nil
// is unrestricted, and total is the pre-pagination count. Same three-way
// contract as skills/triggers — keep these per-kind tests in sync.

func seedPersonas(t *testing.T, store *personas.Store, names ...string) map[string]string {
	t.Helper()
	ctx := context.Background()
	byName := map[string]string{}
	for _, n := range names {
		p := &personas.Persona{ID: "01P" + n, Name: n, Text: "t"}
		if err := store.Create(ctx, p); err != nil {
			t.Fatalf("create %s: %v", n, err)
		}
		byName[n] = p.ID
	}
	return byName
}

func TestListEmptyAllowedIDsIsClosed(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedPersonas(t, store, "alpha", "beta", "gamma")

	items, total, err := store.List(ctx, personas.ListFilter{AllowedIDs: []string{}})
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

	ids := seedPersonas(t, store, "alpha", "beta", "gamma")

	items, total, err := store.List(ctx, personas.ListFilter{AllowedIDs: []string{ids["alpha"], ids["gamma"]}})
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
	if !got[ids["alpha"]] || !got[ids["gamma"]] || got[ids["beta"]] {
		t.Errorf("wrong visible set: %v", got)
	}
}

func TestListNilAllowedIDsUnrestricted(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedPersonas(t, store, "alpha", "beta", "gamma")

	items, total, err := store.List(ctx, personas.ListFilter{})
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

	const n = 30
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("persona-%02d", i)
	}
	seedPersonas(t, store, names...)

	seen := map[string]bool{}
	for page := 0; page*7 < n; page++ {
		items, total, err := store.List(ctx, personas.ListFilter{Limit: 7, Offset: page * 7})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if total != n {
			t.Fatalf("page %d: total=%d, want %d (pre-pagination count)", page, total, n)
		}
		want := min(7, n-page*7)
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

func TestListVersionsPaginationTotal(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	p := &personas.Persona{ID: "01PVERSIONS", Name: "versioned", Text: "v1"}
	if err := store.Create(ctx, p); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Each text update appends a version: 1 (create) + 4 updates = 5.
	text := "x"
	for i := 0; i < 4; i++ {
		text = fmt.Sprintf("v%d", i+2)
		if _, err := store.Update(ctx, p.ID, personas.UpdateRequest{Text: &text}); err != nil {
			t.Fatalf("update: %v", err)
		}
	}

	items, total, err := store.ListVersions(ctx, p.ID, 2, 2)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if total != 5 {
		t.Fatalf("total=%d, want 5 (pre-pagination count)", total)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items on page, got %d", len(items))
	}
	// Newest first, offset 2 → versions 3 and 2.
	if items[0].VersionNumber != 3 || items[1].VersionNumber != 2 {
		t.Errorf("unexpected page: %d, %d", items[0].VersionNumber, items[1].VersionNumber)
	}
}
