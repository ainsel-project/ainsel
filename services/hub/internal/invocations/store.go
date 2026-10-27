package invocations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// DefaultCapacity is the invocation count reported by Store.Capacity, and the
// default ring size of the in-memory test fake (see invocationstest).
//
// It is not a bound on the shipped store: the Postgres store keeps records for
// Retention, not for a row count, and reports this figure only because the
// invocation history endpoint exposes a capacity field.
const DefaultCapacity = 1000

// Retention is how long invocation records are kept before pruning. It is
// deliberately aligned with tasklogs.ConversationRetention so an invocation
// and its conversation transcript expire together: the UI reaches a
// transcript only through its invocation record, so a conversation that
// outlives its invocation would be unreachable — widening one without the
// other would buy nothing.
const Retention = 7 * 24 * time.Hour

// Store is the persistence contract for invocation history.
//
// The hub has exactly one implementation, the Postgres-backed NewPgStore, wired
// unconditionally in cmd/hub; there is no store selected by configuration. An
// in-memory ring buffer used to live in this package as a second one, which
// implied a deployment that never existed and let the two implementations drift
// apart in ways tests could not see — the reason a restricted telemetry scope
// could list invocation rows for agents the caller cannot read on the Postgres
// path while the in-memory path enforced it (#178). Tests that need history
// without a database now use the fake in the invocationstest package, which
// states plainly that it is a stand-in and not a second opinion about this
// contract: it bounds retention by count and never prunes, so it cannot tell you
// what the hub does. Where a behaviour matters on the shipped path, assert it
// against PgStore.
type Store interface {
	// Record creates a new invocation record in StatusRunning and stores it.
	// The returned invocation is a copy with ID and StartTime populated.
	Record(inv Invocation) Invocation
	// Complete updates an existing invocation with its terminal status and
	// returns false when the invocation is unknown. errMsg is recorded when
	// status is failure/timeout.
	Complete(id, status, errMsg string, endTime time.Time) bool
	// Get returns the invocation with the given ID, or false if absent.
	Get(id string) (Invocation, bool)
	// List returns invocations sorted newest-first, applying the given filters.
	List(opts ListOptions) []Invocation
	// ListWithTotal behaves like List but also returns the number of
	// invocations matching the filters before opts.Limit is applied.
	ListWithTotal(opts ListOptions) ([]Invocation, int)
	// Len returns the number of invocations currently stored.
	Len() int
	// Capacity reports the retention bound the API exposes. The Postgres store
	// is bounded by age (see Retention) and reports DefaultCapacity for API
	// compatibility; the test fake evicts by count and reports its own size.
	Capacity() int
	// Prune removes records older than the given retention, returning how many
	// were removed. The test fake bounds itself by capacity, has nothing to
	// prune, and returns 0.
	Prune(ctx context.Context, retention time.Duration) (int64, error)
	// AgentRollups returns per-agent invocation aggregates over the window
	// [since, until), using the same bounds as List (since inclusive, until
	// exclusive) so the counts match GET /api/v1/invocations?agent=<name> for
	// the same window. Invocations counts every status, including running;
	// Failures counts failure/timeout; AvgDurationMs averages duration_ms over
	// the invocations that have completed (0 when none has). A zero since or
	// until leaves that bound open. Implementations must not fail on a missing
	// window — the caller passes the whole retained range for the all-time view.
	AgentRollups(ctx context.Context, since, until time.Time) ([]AgentRollup, error)
}

// AgentRollup is the per-agent invocation aggregate returned by
// Store.AgentRollups: how often the agent was dispatched in the window, how
// many of those runs failed, and how long a completed run took on average.
type AgentRollup struct {
	// AgentName is the Agent resource name the invocations were dispatched to.
	AgentName string
	// Invocations is the number of invocations started in the window, all statuses.
	Invocations int64
	// Failures is the number of those invocations in a failure or timeout status.
	Failures int64
	// AvgDurationMs is the mean duration over the invocations that have
	// completed; 0 when none has (running-only agents have no duration to average).
	AvgDurationMs float64
}

// ListOptions filters and paginates List results. The zero value lists all
// invocations newest-first with no filters and no limit.
type ListOptions struct {
	// AgentName, if non-empty, restricts results to invocations for this agent.
	AgentName string
	// AgentNames, if non-empty, restricts results to invocations for any of
	// these agents. Used to scope the history to the agents a caller may read;
	// an empty slice applies no restriction, so a caller that may read no
	// agents must not query at all.
	AgentNames []string
	// Connectors, if non-empty, restricts results to invocations produced by any
	// of these connectors — the query a channel's "recent work" view runs, and
	// the reason the row carries (connector, start_time) in an index. An empty
	// slice applies no restriction, so it must never be read as "match nothing".
	Connectors []string
	// Status, if non-empty, restricts results to invocations with this status.
	Status string
	// TriggerName, if non-empty, restricts results to invocations dispatched by this trigger.
	TriggerName string
	// EventID, if non-empty, restricts results to invocations for this event.
	EventID string
	// Since, if non-zero, restricts results to invocations started at or after this time.
	Since time.Time
	// Until, if non-zero, restricts results to invocations started before this time.
	Until time.Time
	// Limit, if > 0, caps the number of results returned.
	Limit int
}

// NewID returns a new opaque invocation ID in the format documented on
// Invocation.ID ("inv-<8 hex>"). Exported so the test fake hands out IDs that
// are indistinguishable from real ones; nothing else should generate them.
func NewID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("inv-%s", hex.EncodeToString(b))
}
