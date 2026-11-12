package queuesignal

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeGate answers "is this agent held?" from a map, so holding behaviour is a
// table rather than a cluster and an injected clock.
type fakeGate struct {
	mu   sync.Mutex
	held map[string]time.Time
	seen []string
}

func newGate() *fakeGate { return &fakeGate{held: map[string]time.Time{}} }

// hold says the agent may not be handed work until opensAt.
func (g *fakeGate) hold(agent string, opensAt time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.held[agent] = opensAt
}

// release says the window is open again.
func (g *fakeGate) release(agent string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.held, agent)
}

func (g *fakeGate) Hold(_ context.Context, agentName string) (bool, time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.seen = append(g.seen, agentName)
	at, ok := g.held[agentName]
	if !ok {
		return false, time.Time{}
	}
	return true, at
}

// wakeOf reports the instant this publisher promised to look again, which is the
// publisher's only memory of a hold it did not cause.
func (p *Publisher) wakeOf(t *testing.T, agent string) (time.Time, bool) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	at, ok := p.wakes[agent]
	return at, ok
}

// Held work must not be dispatched. Publishing an empty due-id list is the whole
// mechanism: the operator turns each id into one Job, so no ids means no pod.
func TestPublishOne_HeldAgentPublishesNoDueIDs(t *testing.T) {
	counter := newCounter()
	counter.set("olli", 3, 0)
	counter.setDue("olli", 1, 2, 3)
	gate := newGate()
	gate.hold("olli", time.Now().Add(2*time.Hour))
	patcher := &fakePatcher{}

	p := New(counter, patcher, WithGate(gate))
	done, err := p.publishOne(context.Background(), "olli", false)
	if err != nil || !done {
		t.Fatalf("publishOne = (%v, %v), want (true, nil)", done, err)
	}

	sig := patcher.last(t).sig
	if len(sig.DueIDs) != 0 {
		t.Errorf("DueIDs = %v, want none (held work must not reach the dispatcher)", sig.DueIDs)
	}
	// The backlog stays visible: held is not the same as absent, and a console or
	// an operator reading the status has to be able to tell them apart.
	if sig.Pending != 3 {
		t.Errorf("Pending = %d, want 3 (a held backlog is still queued work)", sig.Pending)
	}
}

// A hold must overwrite ids published while the agent was open. Leaving the last
// list in place - the right answer for a failed query - would keep the operator
// spawning Jobs for work that is supposed to wait.
func TestPublishOne_HoldClearsPreviouslyPublishedDueIDs(t *testing.T) {
	counter := newCounter()
	counter.set("olli", 3, 0)
	counter.setDue("olli", 1, 2, 3)
	gate := newGate()
	patcher := &fakePatcher{}
	p := New(counter, patcher, WithGate(gate))

	if _, err := p.publishOne(context.Background(), "olli", false); err != nil {
		t.Fatalf("publishOne: %v", err)
	}
	if got := patcher.last(t).sig.DueIDs; len(got) != 3 {
		t.Fatalf("open window: DueIDs = %v, want the three due tasks", got)
	}

	gate.hold("olli", time.Now().Add(time.Hour))
	done, err := p.publishOne(context.Background(), "olli", false)
	if err != nil || !done {
		t.Fatalf("publishOne after hold = (%v, %v), want (true, nil)", done, err)
	}
	if got := patcher.last(t).sig.DueIDs; len(got) != 0 {
		t.Errorf("held: DueIDs = %v, want the list cleared", got)
	}
}

// Nothing happens to the queue when a window opens, so nothing marks the agent
// dirty. The publisher has to remember the opening instant and re-measure itself,
// or a backlog sits until the next sweep.
func TestPublishOne_WindowOpeningWakesThePublisher(t *testing.T) {
	opensAt := time.Now().Add(30 * time.Minute)
	counter := newCounter()
	counter.set("olli", 2, 0)
	counter.setDue("olli", 7, 8)
	gate := newGate()
	gate.hold("olli", opensAt)
	patcher := &fakePatcher{}
	p := New(counter, patcher, WithGate(gate))

	if _, err := p.publishOne(context.Background(), "olli", false); err != nil {
		t.Fatalf("publishOne: %v", err)
	}
	got, ok := p.wakeOf(t, "olli")
	if !ok || !got.Equal(opensAt) {
		t.Errorf("wake = (%s, %v), want (%s, true)", got, ok, opensAt)
	}

	// The window opens. No Observe, no queue movement - only the clock.
	gate.release("olli")
	patcher.reset()
	p.markWakes(opensAt)
	p.publishRound(context.Background(), false)

	if patcher.count() == 0 {
		t.Fatal("no signal published when the window opened; the backlog waits for the sweep")
	}
	if got := patcher.last(t).sig.DueIDs; len(got) != 2 {
		t.Errorf("DueIDs = %v, want the queued tasks dispatched", got)
	}
	if _, ok := p.wakeOf(t, "olli"); ok {
		t.Error("wake kept after the window opened, so the agent is measured twice per round forever")
	}
}

// A hold with no known opening time must not leave a stale wake behind: the sweep
// is the only thing that can notice the window opened, and a wake in the past
// would fire every round.
func TestPublishOne_ReleaseClearsTheWake(t *testing.T) {
	counter := newCounter()
	counter.set("olli", 1, 0)
	gate := newGate()
	gate.hold("olli", time.Now().Add(time.Hour))
	p := New(counter, &fakePatcher{}, WithGate(gate))
	if _, err := p.publishOne(context.Background(), "olli", false); err != nil {
		t.Fatalf("publishOne: %v", err)
	}
	if _, ok := p.wakeOf(t, "olli"); !ok {
		t.Fatal("no wake recorded while held")
	}

	gate.release("olli")
	if _, err := p.publishOne(context.Background(), "olli", true); err != nil {
		t.Fatalf("publishOne: %v", err)
	}
	if _, ok := p.wakeOf(t, "olli"); ok {
		t.Error("wake kept after release")
	}
}

// Without a gate - the shape every hub had before working hours - due tasks are
// published whether or not anything else changed.
func TestPublishOne_WithoutGateDispatchesDueIDs(t *testing.T) {
	counter := newCounter()
	counter.set("olli", 2, 0)
	counter.setDue("olli", 5)
	patcher := &fakePatcher{}

	p := New(counter, patcher)
	if _, err := p.publishOne(context.Background(), "olli", false); err != nil {
		t.Fatalf("publishOne: %v", err)
	}
	if got := patcher.last(t).sig.DueIDs; len(got) != 1 || got[0] != 5 {
		t.Errorf("DueIDs = %v, want [5]", got)
	}
}
