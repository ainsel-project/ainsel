// Package invocationstest provides the in-memory invocation Store used by
// tests across the hub.
//
// It is deliberately not part of the invocations package. The hub ships exactly
// one invocation store — the Postgres-backed invocations.NewPgStore, wired
// unconditionally in cmd/hub — and for a while the package advertised a second,
// in-memory implementation next to it as though that were a supported
// deployment. It never was: nothing outside a test ever constructed it, and a
// hub with no database does not fall back to it. Keeping it in the shipped
// package is what let the two implementations disagree about what
// ListOptions.AgentNames means, which is how a restricted telemetry scope came
// to list rows for agents the caller cannot read on the Postgres path (#178)
// while the in-memory path looked correct.
//
// What is true of any fake in this repo is true here: it is a stand-in for the
// real store, not a second opinion about its behaviour. Two consequences worth
// reading before writing an assertion against it:
//
//   - Its semantics are not production semantics. It bounds retention by
//     count (evicting the oldest insert at capacity) and Prune is a no-op; the
//     Postgres store bounds it by age and its Capacity reports a fixed figure
//     for API compatibility. An assertion about capacity or pruning behaviour
//     that passes here says nothing about what the hub does.
//   - A contract asserted only against it can be satisfied while the shipped
//     store violates it. Where a behaviour matters on the Postgres path, assert
//     it against PgStore too (see pgstore_test.go and the skip-without-Docker
//     convention there — a skipped test is not a passing one).
package invocationstest

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/invocations"
)

// Store is an in-memory, thread-safe ring buffer of recent invocations
// satisfying invocations.Store, for tests that need invocation history without
// a database.
//
// When the buffer reaches capacity, recording a new invocation evicts the
// oldest one. Lookups by ID remain O(1) via a map index. Records do not survive
// a restart — which is precisely why the hub does not use this store.
type Store struct {
	mu       sync.RWMutex
	capacity int
	// order holds invocation IDs in insertion order. The oldest is at the
	// front; the newest is at the back. We use a slice rather than a ring
	// to keep the implementation straightforward — the buffer is small
	// enough (default 1000) that O(n) eviction is negligible.
	order []string
	byID  map[string]*invocations.Invocation
}

// New returns a Store with the given capacity. If capacity <= 0,
// invocations.DefaultCapacity is used.
func New(capacity int) *Store {
	if capacity <= 0 {
		capacity = invocations.DefaultCapacity
	}
	return &Store{
		capacity: capacity,
		order:    make([]string, 0, capacity),
		byID:     make(map[string]*invocations.Invocation, capacity),
	}
}

// Record creates a new invocation record in StatusRunning and stores it.
// The returned invocation is a copy; the canonical record lives in the store.
func (s *Store) Record(inv invocations.Invocation) invocations.Invocation {
	if inv.ID == "" {
		inv.ID = invocations.NewID()
	}
	if inv.StartTime.IsZero() {
		inv.StartTime = time.Now().UTC()
	}
	if inv.Status == "" {
		inv.Status = invocations.StatusRunning
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Evict oldest if at capacity. Avoid evicting the same ID we're inserting.
	for len(s.order) >= s.capacity {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.byID, oldest)
	}

	stored := inv
	s.byID[inv.ID] = &stored
	s.order = append(s.order, inv.ID)

	// Return a copy so callers can't mutate the stored record.
	return stored
}

// Complete updates an existing invocation with its terminal status.
//
// If the invocation is not found (e.g. evicted from the ring buffer),
// Complete returns false. errMsg is recorded when status is failure/timeout.
func (s *Store) Complete(id, status, errMsg string, endTime time.Time) bool {
	if endTime.IsZero() {
		endTime = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.byID[id]
	if !ok {
		return false
	}
	rec.Status = status
	rec.EndTime = &endTime
	d := endTime.Sub(rec.StartTime).Milliseconds()
	rec.DurationMs = &d
	rec.Error = errMsg
	return true
}

// Get returns a copy of the invocation with the given ID, or false if absent.
func (s *Store) Get(id string) (invocations.Invocation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.byID[id]
	if !ok {
		return invocations.Invocation{}, false
	}
	return *rec, true
}

// List returns invocations sorted newest-first, applying the given filters.
//
// The returned slice contains copies of the stored records; callers may
// safely mutate them without affecting the store.
func (s *Store) List(opts invocations.ListOptions) []invocations.Invocation {
	items, _ := s.ListWithTotal(opts)
	return items
}

// ListWithTotal behaves like List but additionally returns the number of
// invocations matching the filters before opts.Limit is applied. This lets
// callers report a truthful total even when the result set is capped.
func (s *Store) ListWithTotal(opts invocations.ListOptions) ([]invocations.Invocation, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]invocations.Invocation, 0, len(s.order))
	for _, id := range s.order {
		rec := s.byID[id]
		if rec == nil {
			continue
		}
		if opts.AgentName != "" && rec.AgentName != opts.AgentName {
			continue
		}
		if len(opts.AgentNames) > 0 && !slices.Contains(opts.AgentNames, rec.AgentName) {
			continue
		}
		if len(opts.Connectors) > 0 && !slices.Contains(opts.Connectors, rec.Connector) {
			continue
		}
		if opts.Status != "" && rec.Status != opts.Status {
			continue
		}
		if opts.TriggerName != "" && rec.TriggerName != opts.TriggerName {
			continue
		}
		if opts.EventID != "" && rec.EventID != opts.EventID {
			continue
		}
		if !opts.Since.IsZero() && rec.StartTime.Before(opts.Since) {
			continue
		}
		if !opts.Until.IsZero() && !rec.StartTime.Before(opts.Until) {
			continue
		}
		out = append(out, *rec)
	}

	// Sort newest first.
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartTime.After(out[j].StartTime)
	})

	total := len(out)
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out, total
}

// Len returns the number of invocations currently stored.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.order)
}

// Capacity returns the maximum number of invocations the buffer will retain.
// Note this is the ring's own size, whereas the Postgres store reports a fixed
// figure because it is bounded by age, not count.
func (s *Store) Capacity() int {
	return s.capacity
}

// Prune is a no-op: the ring buffer bounds itself by capacity, so there is
// nothing to age out. Satisfies the Store interface — unlike the Postgres
// store, which deletes rows older than the retention window.
func (s *Store) Prune(ctx context.Context, retention time.Duration) (int64, error) {
	return 0, nil
}

// AgentRollups aggregates the buffered invocations per agent over
// [since, until) with the same bounds List applies, so the counts agree with
// List for the same window. Results are sorted by agent name for a stable
// response order.
func (s *Store) AgentRollups(ctx context.Context, since, until time.Time) ([]invocations.AgentRollup, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type acc struct {
		rollup   invocations.AgentRollup
		durSum   int64
		durCount int64
	}
	byAgent := map[string]*acc{}
	for _, id := range s.order {
		rec := s.byID[id]
		if rec == nil {
			continue
		}
		if !since.IsZero() && rec.StartTime.Before(since) {
			continue
		}
		if !until.IsZero() && !rec.StartTime.Before(until) {
			continue
		}
		a := byAgent[rec.AgentName]
		if a == nil {
			a = &acc{rollup: invocations.AgentRollup{AgentName: rec.AgentName}}
			byAgent[rec.AgentName] = a
		}
		a.rollup.Invocations++
		if rec.Status == invocations.StatusFailure || rec.Status == invocations.StatusTimeout {
			a.rollup.Failures++
		}
		if rec.DurationMs != nil {
			a.durSum += *rec.DurationMs
			a.durCount++
		}
	}

	out := make([]invocations.AgentRollup, 0, len(byAgent))
	for _, a := range byAgent {
		if a.durCount > 0 {
			a.rollup.AvgDurationMs = float64(a.durSum) / float64(a.durCount)
		}
		out = append(out, a.rollup)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgentName < out[j].AgentName })
	return out, nil
}
