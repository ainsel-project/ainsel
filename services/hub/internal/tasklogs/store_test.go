package tasklogs

import (
	"context"
	"fmt"
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
	return NewStore(pool)
}

// TestTokenUsageBySubject covers the by-subject group-by (issue #411): the
// four components and the cost summed per (agent, repo, event_type, model),
// the [since, until) window semantics shared with TokenUsageSince, and the
// empty-subject rows migration 0024 leaves behind — kept, not dropped, so the
// table's total still equals the summary tile's.
func TestTokenUsageBySubject(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	agent := fmt.Sprintf("subj-agent-%d", suffix)

	msgs := []*ConversationMessage{
		// Same subject, two turns: must collapse into one group.
		{AgentName: agent, Role: "assistant", Content: "a", Model: "kimi",
			Repo: "org/repo", EventType: "pull_request.opened",
			InputTokens: 100, OutputTokens: 10, CacheReadTokens: 500, CacheWriteTokens: 50, CostUSD: 0.1},
		{AgentName: agent, Role: "assistant", Content: "b", Model: "kimi",
			Repo: "org/repo", EventType: "pull_request.opened",
			InputTokens: 20, OutputTokens: 5, CostUSD: 0.025},
		// Different model: its own group.
		{AgentName: agent, Role: "assistant", Content: "c", Model: "gpt",
			Repo: "org/repo", EventType: "pull_request.opened",
			InputTokens: 7, OutputTokens: 3},
		// Pre-migration subject: empty repo/event_type, must still be returned.
		{AgentName: agent, Role: "assistant", Content: "d", Model: "kimi",
			InputTokens: 40, OutputTokens: 8},
	}
	for _, m := range msgs {
		if err := s.InsertConversation(ctx, m); err != nil {
			t.Fatalf("insert conversation: %v", err)
		}
	}

	rows, err := s.TokenUsageBySubject(ctx, time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("TokenUsageBySubject: %v", err)
	}
	type key struct{ agent, repo, eventType, model string }
	byKey := map[key]TokenSubjectUsageRow{}
	for _, r := range rows {
		if r.AgentName != agent {
			continue
		}
		byKey[key{r.AgentName, r.Repo, r.EventType, r.Model}] = r
	}

	main, ok := byKey[key{agent, "org/repo", "pull_request.opened", "kimi"}]
	if !ok {
		t.Fatalf("main subject group missing from %+v", rows)
	}
	if main.InputTokens != 120 || main.OutputTokens != 15 || main.CacheReadTokens != 500 || main.CacheWriteTokens != 50 {
		t.Errorf("main components = %+v, want 120/15/500/50", main)
	}
	if main.CostUSD != 0.125 {
		t.Errorf("main cost = %v, want 0.125", main.CostUSD)
	}
	if _, ok := byKey[key{agent, "org/repo", "pull_request.opened", "gpt"}]; !ok {
		t.Fatalf("model split missing; groups must be per model: %+v", rows)
	}
	empty, ok := byKey[key{agent, "", "", "kimi"}]
	if !ok {
		t.Fatalf("empty-subject group must not be dropped: %+v", rows)
	}
	if empty.InputTokens != 40 || empty.OutputTokens != 8 || empty.CostUSD != 0 {
		t.Errorf("empty-subject group = %+v, want 40/8/0", empty)
	}

	// A window starting in the future excludes every row: the bounds are
	// [since, until) like TokenUsageSince's.
	rows, err = s.TokenUsageBySubject(ctx, time.Now().UTC().Add(time.Hour), time.Time{})
	if err != nil {
		t.Fatalf("TokenUsageBySubject windowed: %v", err)
	}
	for _, r := range rows {
		if r.AgentName == agent {
			t.Fatalf("seeded agent must not appear in a future window: %+v", rows)
		}
	}
}

func TestInsertAndList(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	e := &Entry{
		AgentName: "reviewer",
		Level:     LevelInfo,
		Message:   "started review",
		Fields:    map[string]any{"repo": "test/repo"},
	}
	if err := s.Insert(ctx, e); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if e.ID == 0 {
		t.Fatal("expected non-zero ID after insert")
	}
	if e.CreatedAt.IsZero() {
		t.Fatal("expected non-zero CreatedAt after insert")
	}

	entries, err := s.List(ctx, ListOptions{AgentName: "reviewer", Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one entry")
	}
	found := false
	for _, entry := range entries {
		if entry.Message == "started review" {
			found = true
			if entry.Fields["repo"] != "test/repo" {
				t.Errorf("expected fields.repo='test/repo', got %v", entry.Fields["repo"])
			}
		}
	}
	if !found {
		t.Error("expected to find 'started review' entry")
	}
}

func TestListFiltersByLevel(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	_ = s.Insert(ctx, &Entry{AgentName: "level-test", Level: LevelInfo, Message: "info msg"})
	_ = s.Insert(ctx, &Entry{AgentName: "level-test", Level: LevelError, Message: "error msg"})

	entries, err := s.List(ctx, ListOptions{AgentName: "level-test", Level: LevelError})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, e := range entries {
		if e.Level != LevelError {
			t.Errorf("expected only error entries, got level %q", e.Level)
		}
	}
}

func TestListDefaultLimit(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	// Insert with no explicit limit — should default to 500.
	entries, err := s.List(ctx, ListOptions{AgentName: "nonexistent-agent"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if entries == nil {
		// nil is fine for no results; just verify no error.
		return
	}
	if len(entries) > 500 {
		t.Errorf("expected at most 500 entries, got %d", len(entries))
	}
}

func TestPrune(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	_ = s.Insert(ctx, &Entry{AgentName: "prune-test", Level: LevelInfo, Message: "to be pruned"})

	// Prune with zero retention deletes everything.
	n, err := s.Prune(ctx, 0)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n < 1 {
		t.Errorf("expected at least 1 pruned row, got %d", n)
	}
}

func TestAgentTokenUsageSince(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	agentA := fmt.Sprintf("agent-a-%d", suffix)
	agentB := fmt.Sprintf("agent-b-%d", suffix)

	msgs := []*ConversationMessage{
		{AgentName: agentA, Role: "user", Content: "hi",
			InputTokens: 100, OutputTokens: 10, CacheReadTokens: 500, CacheWriteTokens: 50},
		{AgentName: agentA, Role: "assistant", Content: "yo",
			InputTokens: 20, OutputTokens: 5},
		{AgentName: agentB, Role: "user", Content: "hi",
			InputTokens: 7, OutputTokens: 3},
	}
	for _, m := range msgs {
		if err := s.InsertConversation(ctx, m); err != nil {
			t.Fatalf("insert conversation: %v", err)
		}
	}

	rows, err := s.AgentTokenUsageSince(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("AgentTokenUsageSince: %v", err)
	}
	byAgent := map[string]AgentTokenRow{}
	for _, r := range rows {
		byAgent[r.AgentName] = r
	}
	a, ok := byAgent[agentA]
	if !ok {
		t.Fatalf("agent-a row missing from %+v", rows)
	}
	if a.InputTokens != 120 || a.OutputTokens != 15 || a.CacheReadTokens != 500 || a.CacheWriteTokens != 50 {
		t.Errorf("agent-a components = %+v, want 120/15/500/50", a)
	}
	b, ok := byAgent[agentB]
	if !ok {
		t.Fatalf("agent-b row missing from %+v", rows)
	}
	if b.InputTokens != 7 || b.OutputTokens != 3 || b.CacheReadTokens != 0 || b.CacheWriteTokens != 0 {
		t.Errorf("agent-b components = %+v, want 7/3/0/0", b)
	}

	// A window starting in the future excludes every row: the bounds are
	// [since, until) like TokenUsageSince.
	rows, err = s.AgentTokenUsageSince(ctx, time.Now().UTC().Add(time.Hour), time.Time{})
	if err != nil {
		t.Fatalf("AgentTokenUsageSince windowed: %v", err)
	}
	for _, r := range rows {
		if r.AgentName == agentA || r.AgentName == agentB {
			t.Fatalf("seeded agents must not appear in a future window: %+v", rows)
		}
	}
}
