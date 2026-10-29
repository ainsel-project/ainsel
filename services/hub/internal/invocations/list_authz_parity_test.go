package invocations_test

import (
	"testing"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/invocations"
	"github.com/ainsel-project/ainsel/services/hub/internal/invocations/invocationstest"
)

// assertListAgentNamesSemantics pins the ListOptions.AgentNames contract from
// store.go — non-empty restricts to the listed agents, empty means no
// restriction at all — against any Store implementation. AgentNames carries
// the caller's readable-agent scope (see telemetryScopeFor in
// handlers_invocations.go), so a store that ignores it is an authorization
// bypass, not just a filter bug. This helper runs against both MemoryStore
// and PgStore so the two implementations cannot diverge again: the Postgres
// path silently under-enforced this scope (issue #178) while the in-memory
// store behaved correctly, which is exactly what dev/test missed.
func assertListAgentNamesSemantics(t *testing.T, s invocations.Store) {
	t.Helper()

	// Fixed, distinct start times keep newest-first ordering deterministic
	// across both stores. Fixture: agent-a success, agent-a failure,
	// agent-b running, agent-c success.
	base := time.Now().UTC().Add(-time.Hour)
	record := func(agent, status string, offset time.Duration) invocations.Invocation {
		rec := s.Record(invocations.Invocation{
			AgentName: agent,
			StartTime: base.Add(offset),
		})
		if status != "" {
			if !s.Complete(rec.ID, status, "", base.Add(offset).Add(time.Second)) {
				t.Fatalf("complete %s", rec.ID)
			}
			rec.Status = status
		}
		return rec
	}
	record("agent-a", invocations.StatusSuccess, 0)
	failedA := record("agent-a", invocations.StatusFailure, time.Minute)
	record("agent-b", "", 2*time.Minute)
	record("agent-c", invocations.StatusSuccess, 3*time.Minute)

	// 1. Non-empty AgentNames restricts to exactly the listed agents.
	got := s.List(invocations.ListOptions{AgentNames: []string{"agent-a", "agent-c"}})
	if len(got) != 3 {
		t.Fatalf("List(agent-a,agent-c) = %d rows, want 3: %+v", len(got), got)
	}
	for _, inv := range got {
		if inv.AgentName != "agent-a" && inv.AgentName != "agent-c" {
			t.Errorf("List(agent-a,agent-c) returned row for unreadable agent %q", inv.AgentName)
		}
	}

	// 2. Empty AgentNames means unrestricted, not "match nothing". An
	// unguarded = ANY('{}') on the Postgres side would fail here.
	if got := s.List(invocations.ListOptions{AgentNames: []string{}}); len(got) != 4 {
		t.Fatalf("empty AgentNames must not restrict, got %d of 4 rows", len(got))
	}
	if got := s.List(invocations.ListOptions{}); len(got) != 4 {
		t.Fatalf("unset AgentNames must not restrict, got %d of 4 rows", len(got))
	}

	// 3. ListWithTotal reports the scoped total (pre-Limit), so pagination
	// metadata cannot leak the size of the unscoped result set either.
	items, total := s.ListWithTotal(invocations.ListOptions{AgentNames: []string{"agent-a", "agent-b"}, Limit: 1})
	if len(items) != 1 || total != 3 {
		t.Fatalf("ListWithTotal(agent-a,agent-b, limit=1) = %d items, total %d; want 1 item, total 3", len(items), total)
	}

	// 4. AgentNames composes with other filters via AND.
	got = s.List(invocations.ListOptions{AgentNames: []string{"agent-a", "agent-b"}, Status: invocations.StatusFailure})
	if len(got) != 1 || got[0].ID != failedA.ID {
		t.Fatalf("List(agent-a,agent-b, status=failure) = %+v, want only the agent-a failure", got)
	}

	// 5. AgentName and AgentNames can both be set; they must intersect, not
	// override each other.
	if got := s.List(invocations.ListOptions{AgentName: "agent-a", AgentNames: []string{"agent-b"}}); len(got) != 0 {
		t.Fatalf("List(agent-name=agent-a, agent-names=[agent-b]) = %+v, want empty (AND)", got)
	}

	// 6. A non-empty scope that matches nothing returns empty, not everything.
	if got := s.List(invocations.ListOptions{AgentNames: []string{"agent-missing"}}); len(got) != 0 {
		t.Fatalf("expected 0 rows for an out-of-scope agent, got %+v", got)
	}
}

// TestMemoryStoreListAgentNamesSemantics runs the shared authorization
// assertions against the in-memory store. The behavior was already correct
// here; pinning it in the same helper as the Postgres run is what keeps the
// two implementations honest.
func TestMemoryStoreListAgentNamesSemantics(t *testing.T) {
	assertListAgentNamesSemantics(t, invocationstest.New(0))
}
