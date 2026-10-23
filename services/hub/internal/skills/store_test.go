package skills_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/skills"
)

func TestStoreCreateAndGet(t *testing.T) {
	ctx := context.Background()
	store, cleanup := newTestStore(t)
	defer cleanup()

	sk := skills.Skill{
		ID:          "code-review",
		Name:        "Code Review",
		Description: "Reviews PRs for bugs.",
		Body:        "When asked to review, do X.",
	}
	if err := store.Create(ctx, &sk); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sk.CreatedAt.IsZero() || sk.UpdatedAt.IsZero() {
		t.Errorf("expected timestamps to be set")
	}

	got, err := store.Get(ctx, "code-review")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Code Review" || got.Body != "When asked to review, do X." {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestStoreCreateDuplicateID(t *testing.T) {
	ctx := context.Background()
	store, cleanup := newTestStore(t)
	defer cleanup()

	if err := store.Create(ctx, &skills.Skill{ID: "dup", Name: "first"}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	err := store.Create(ctx, &skills.Skill{ID: "dup", Name: "second"})
	if !errors.Is(err, skills.ErrIDTaken) {
		t.Errorf("expected ErrIDTaken, got %v", err)
	}
}

func TestStoreGetNotFound(t *testing.T) {
	ctx := context.Background()
	store, cleanup := newTestStore(t)
	defer cleanup()

	_, err := store.Get(ctx, "missing")
	if !errors.Is(err, skills.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestStoreList(t *testing.T) {
	ctx := context.Background()
	store, cleanup := newTestStore(t)
	defer cleanup()

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(store.Create(ctx, &skills.Skill{ID: "alpha", Name: "Alpha", Body: "long body alpha"}))
	must(store.Create(ctx, &skills.Skill{ID: "beta", Name: "Beta", Body: "long body beta"}))

	got, err := store.List(ctx, skills.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(got))
	}
	// List returns summaries — there's no Body field on SkillSummary, so the
	// projection itself prevents leaking large body strings. Names should be set.
	names := map[string]bool{got[0].Name: true, got[1].Name: true}
	if !names["Alpha"] || !names["Beta"] {
		t.Errorf("unexpected names: %+v", got)
	}
}

func TestStoreUpdatePartial(t *testing.T) {
	ctx := context.Background()
	store, cleanup := newTestStore(t)
	defer cleanup()

	if err := store.Create(ctx, &skills.Skill{ID: "u", Name: "old", Description: "od", Body: "ob"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := store.Update(ctx, "u", skills.UpdateRequest{Name: ptr("new")})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "new" || updated.Description != "od" || updated.Body != "ob" {
		t.Errorf("partial update mismatch: %+v", updated)
	}
	if !updated.UpdatedAt.After(updated.CreatedAt) {
		t.Errorf("expected UpdatedAt to advance: %v vs %v", updated.UpdatedAt, updated.CreatedAt)
	}
}

func TestStoreUpdateNotFound(t *testing.T) {
	ctx := context.Background()
	store, cleanup := newTestStore(t)
	defer cleanup()

	_, err := store.Update(ctx, "nope", skills.UpdateRequest{Name: ptr("x")})
	if !errors.Is(err, skills.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestStoreDelete(t *testing.T) {
	ctx := context.Background()
	store, cleanup := newTestStore(t)
	defer cleanup()

	if err := store.Create(ctx, &skills.Skill{ID: "del", Name: "x"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Delete(ctx, "del"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := store.Get(ctx, "del")
	if !errors.Is(err, skills.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestStoreDeleteNotFound(t *testing.T) {
	ctx := context.Background()
	store, cleanup := newTestStore(t)
	defer cleanup()

	err := store.Delete(ctx, "nope")
	if !errors.Is(err, skills.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// seedSkills inserts a fixed set of skills used by the List filter tests.
func seedSkills(t *testing.T, store *skills.Store) {
	t.Helper()
	ctx := context.Background()
	rows := []skills.Skill{
		{ID: "code-review", Name: "Code Review", Description: "Reviews PRs for bugs", Tags: []string{"review", "go"}},
		{ID: "security-audit", Name: "Security Audit", Description: "Reviews code for vulnerabilities", Tags: []string{"security", "review"}},
		{ID: "docs-writer", Name: "Docs Writer", Description: "Writes documentation", Tags: []string{"docs"}},
	}
	for i := range rows {
		if err := store.Create(ctx, &rows[i]); err != nil {
			t.Fatalf("seed Create(%s): %v", rows[i].ID, err)
		}
	}
}

func ids(summaries []skills.SkillSummary) map[string]bool {
	out := make(map[string]bool, len(summaries))
	for _, s := range summaries {
		out[s.ID] = true
	}
	return out
}

func TestStoreListSearchByIDNameDescription(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	seedSkills(t, store)
	ctx := context.Background()

	cases := []struct {
		name   string
		search string
		want   []string
	}{
		{"match on id", "security-audit", []string{"security-audit"}},
		{"match on name case-insensitive", "code review", []string{"code-review"}},
		{"match on description", "vulnerabilities", []string{"security-audit"}},
		{"match multiple rows", "reviews", []string{"code-review", "security-audit"}},
		{"no match returns empty", "zzz-no-such-term", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := store.List(ctx, skills.ListFilter{Search: c.search})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			gotIDs := ids(got)
			if len(gotIDs) != len(c.want) {
				t.Fatalf("search %q: expected %v, got %v", c.search, c.want, gotIDs)
			}
			for _, w := range c.want {
				if !gotIDs[w] {
					t.Errorf("search %q: missing expected id %q in %v", c.search, w, gotIDs)
				}
			}
		})
	}
}

func TestStoreListSearchEscapesWildcards(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	seedSkills(t, store)
	ctx := context.Background()

	// A bare "%" must be treated literally, not as a match-everything wildcard.
	got, err := store.List(ctx, skills.ListFilter{Search: "%"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected literal %% to match nothing, got %d rows", len(got))
	}
}

func TestStoreListFilterByTags(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	seedSkills(t, store)
	ctx := context.Background()

	t.Run("single tag", func(t *testing.T) {
		got, err := store.List(ctx, skills.ListFilter{Tags: []string{"security"}})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		gotIDs := ids(got)
		if len(gotIDs) != 1 || !gotIDs["security-audit"] {
			t.Errorf("expected [security-audit], got %v", gotIDs)
		}
	})

	t.Run("multiple tags use overlap (OR)", func(t *testing.T) {
		got, err := store.List(ctx, skills.ListFilter{Tags: []string{"docs", "go"}})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		gotIDs := ids(got)
		if len(gotIDs) != 2 || !gotIDs["docs-writer"] || !gotIDs["code-review"] {
			t.Errorf("expected [docs-writer code-review], got %v", gotIDs)
		}
	})

	t.Run("unknown tag returns empty", func(t *testing.T) {
		got, err := store.List(ctx, skills.ListFilter{Tags: []string{"nope"}})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected no rows, got %d", len(got))
		}
	})
}

func TestStoreListCombinedSearchAndTags(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	seedSkills(t, store)
	ctx := context.Background()

	// Both code-review and security-audit carry the "review" tag, but only
	// security-audit matches the search term "vulnerabilities".
	got, err := store.List(ctx, skills.ListFilter{Search: "vulnerabilities", Tags: []string{"review"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	gotIDs := ids(got)
	if len(gotIDs) != 1 || !gotIDs["security-audit"] {
		t.Errorf("expected [security-audit], got %v", gotIDs)
	}
}

func TestStoreListEmptyFilterReturnsAll(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	seedSkills(t, store)
	ctx := context.Background()

	got, err := store.List(ctx, skills.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("expected 3 rows, got %d", len(got))
	}
	// Tags should round-trip as non-nil slices.
	for _, s := range got {
		if s.Tags == nil {
			t.Errorf("skill %s: expected non-nil tags", s.ID)
		}
	}
}

// --- managed_by / plugin-managed rows ---------------------------------------

func TestStoreCreateDefaultsManagedByToUser(t *testing.T) {
	ctx := context.Background()
	store, cleanup := newTestStore(t)
	defer cleanup()

	sk := skills.Skill{ID: "mine", Name: "Mine", Body: "b"}
	if err := store.Create(ctx, &sk); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sk.ManagedBy != "user" {
		t.Errorf("Create should report ManagedBy=user, got %q", sk.ManagedBy)
	}
	got, err := store.Get(ctx, "mine")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ManagedBy != "user" {
		t.Errorf("ManagedBy: want user, got %q", got.ManagedBy)
	}
}

// TestStoreUpsertManagedInsertsAndUpdates: the syncer writes rows under a
// caller-chosen id — user-facing Create validates ids from request input, the
// plugin path must accept the fixed "<pluginId>-<skillName>" shape as-is.
func TestStoreUpsertManagedInsertsAndUpdates(t *testing.T) {
	ctx := context.Background()
	store, cleanup := newTestStore(t)
	defer cleanup()

	sk := &skills.Skill{
		ID:          "mem0-memory-usage",
		Name:        "mem0-memory-usage",
		Description: "How to use memories",
		Body:        "v1",
		Tags:        []string{"plugin:mem0"},
	}
	if err := store.UpsertManaged(ctx, sk); err != nil {
		t.Fatalf("UpsertManaged insert: %v", err)
	}
	got, err := store.Get(ctx, "mem0-memory-usage")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ManagedBy != "plugin" {
		t.Errorf("ManagedBy: want plugin, got %q", got.ManagedBy)
	}
	if got.Body != "v1" || !slices.Equal(got.Tags, []string{"plugin:mem0"}) {
		t.Errorf("row: %+v", got)
	}
	createdAt := got.CreatedAt

	sk.Body = "v2"
	sk.Description = "Updated"
	if err := store.UpsertManaged(ctx, sk); err != nil {
		t.Fatalf("UpsertManaged update: %v", err)
	}
	got, err = store.Get(ctx, "mem0-memory-usage")
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got.Body != "v2" || got.Description != "Updated" {
		t.Errorf("row not updated: %+v", got)
	}
	if !got.CreatedAt.Equal(createdAt) {
		t.Errorf("created_at changed on upsert: %v -> %v", createdAt, got.CreatedAt)
	}
}

func TestStoreListManagedReturnsOnlyPluginRows(t *testing.T) {
	ctx := context.Background()
	store, cleanup := newTestStore(t)
	defer cleanup()

	if err := store.Create(ctx, &skills.Skill{ID: "user-skill", Name: "User", Body: "user body"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.UpsertManaged(ctx, &skills.Skill{
		ID: "mem0-usage", Name: "mem0-usage", Description: "d", Body: "plugin body",
		Tags: []string{"plugin:mem0"},
	}); err != nil {
		t.Fatalf("UpsertManaged: %v", err)
	}

	got, err := store.ListManaged(ctx)
	if err != nil {
		t.Fatalf("ListManaged: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 managed row, got %d (%+v)", len(got), got)
	}
	row := got[0]
	if row.ID != "mem0-usage" || row.Name != "mem0-usage" || row.Description != "d" || row.Body != "plugin body" {
		t.Errorf("row: %+v", row)
	}
	if row.ManagedBy != "plugin" {
		t.Errorf("ManagedBy: %q", row.ManagedBy)
	}
	if !slices.Equal(row.Tags, []string{"plugin:mem0"}) {
		t.Errorf("tags: %v", row.Tags)
	}
}

func TestStoreDeleteManaged(t *testing.T) {
	ctx := context.Background()
	store, cleanup := newTestStore(t)
	defer cleanup()

	if err := store.UpsertManaged(ctx, &skills.Skill{ID: "mem0-usage", Name: "mem0-usage", Body: "b"}); err != nil {
		t.Fatalf("UpsertManaged: %v", err)
	}
	if err := store.DeleteManaged(ctx, "mem0-usage"); err != nil {
		t.Fatalf("DeleteManaged: %v", err)
	}
	if _, err := store.Get(ctx, "mem0-usage"); !errors.Is(err, skills.ErrNotFound) {
		t.Errorf("expected row gone, got %v", err)
	}
	if err := store.DeleteManaged(ctx, "mem0-usage"); !errors.Is(err, skills.ErrNotFound) {
		t.Errorf("expected ErrNotFound on second delete, got %v", err)
	}
}

func TestStoreUpsertManagedNeverTakesOverUserSkill(t *testing.T) {
	ctx := context.Background()
	store, cleanup := newTestStore(t)
	defer cleanup()

	if err := store.Create(ctx, &skills.Skill{ID: "mem0-memory-usage", Name: "mine", Body: "user body"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	err := store.UpsertManaged(ctx, &skills.Skill{ID: "mem0-memory-usage", Name: "mem0-memory-usage", Body: "plugin body", Tags: []string{"plugin:mem0"}})
	if !errors.Is(err, skills.ErrIDTakenByUser) {
		t.Fatalf("UpsertManaged err = %v, want ErrIDTakenByUser", err)
	}
	got, err := store.Get(ctx, "mem0-memory-usage")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ManagedBy != "user" || got.Body != "user body" || got.Name != "mine" {
		t.Errorf("user skill was modified: %+v", got)
	}
}
