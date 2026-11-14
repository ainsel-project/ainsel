package chat_test

import (
	"context"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/chat"
)

// Chat sessions have no group-authz allow-list — the handler scopes by
// user_id/agent_name, which are pushed into the SQL WHERE. What this parity
// test pins is the pagination contract the other kinds share: total is the
// pre-pagination count, pages are bounded and disjoint, and the
// (updated_at DESC, id) order is stable across pages via the id tiebreaker.

func TestListSessionsPaginationTotalIsPrePaginationCount(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const n = 12
	for i := 0; i < n; i++ {
		if _, err := store.CreateSession(ctx, "developer", "user-1"); err != nil {
			t.Fatalf("create session: %v", err)
		}
	}
	// A session for another user must not count toward the total.
	if _, err := store.CreateSession(ctx, "developer", "user-2"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	seen := map[string]bool{}
	for page := 0; page*5 < n; page++ {
		items, total, err := store.ListSessions(ctx, chat.ListSessionsOptions{
			UserID: "user-1",
			Limit:  5,
			Offset: page * 5,
		})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if total != n {
			t.Fatalf("page %d: total=%d, want %d", page, total, n)
		}
		if want := min(5, n-page*5); len(items) != want {
			t.Fatalf("page %d: %d items, want %d", page, len(items), want)
		}
		for _, s := range items {
			if seen[s.ID] {
				t.Fatalf("page %d: %s seen twice — paging is unstable", page, s.ID)
			}
			seen[s.ID] = true
		}
	}
	if len(seen) != n {
		t.Fatalf("union of pages = %d, want %d", len(seen), n)
	}
}

func TestListSessionsOffsetPastEndIsEmptyWithCorrectTotal(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := store.CreateSession(ctx, "developer", "user-1"); err != nil {
			t.Fatalf("create session: %v", err)
		}
	}

	items, total, err := store.ListSessions(ctx, chat.ListSessionsOptions{
		UserID: "user-1",
		Limit:  10,
		Offset: 50,
	})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("past-the-end page must be empty, got %d items", len(items))
	}
	if total != 3 {
		t.Errorf("total=%d, want 3", total)
	}
}
