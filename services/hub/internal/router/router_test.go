package router

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/eventqueue"
	"github.com/ainsel-project/ainsel/services/hub/internal/invocations"
	"github.com/ainsel-project/ainsel/services/hub/internal/triggerindex"
	"github.com/ainsel-project/ainsel/services/hub/internal/triggers"
	"github.com/ainsel-project/ainsel/services/hub/internal/types"
)

// mockQueue implements the Queue interface for testing.
type mockQueue struct {
	events    []eventqueue.Event
	enqueued  []eventqueue.Task
	routedIDs []string
	enqueueFn func(task eventqueue.Task) error
}

func (m *mockQueue) FetchUnrouted(_ context.Context, limit int) ([]eventqueue.Event, error) {
	if len(m.events) > limit {
		return m.events[:limit], nil
	}
	return m.events, nil
}

func (m *mockQueue) MarkRouted(_ context.Context, eventID string) error {
	m.routedIDs = append(m.routedIDs, eventID)
	return nil
}

func (m *mockQueue) EnqueueTask(_ context.Context, task eventqueue.Task) error {
	if m.enqueueFn != nil {
		return m.enqueueFn(task)
	}
	m.enqueued = append(m.enqueued, task)
	return nil
}

type stubBroadcaster struct{}

func (s *stubBroadcaster) BroadcastError(entry types.ErrorEntry)    {}
func (s *stubBroadcaster) BroadcastEvent(entry types.ActivityEntry) {}
func (s *stubBroadcaster) BroadcastStats(ctx context.Context)       {}

// recordingBroadcaster captures the context passed to BroadcastStats.
type recordingBroadcaster struct {
	statsCtxs []context.Context
}

func (r *recordingBroadcaster) BroadcastError(entry types.ErrorEntry)    {}
func (r *recordingBroadcaster) BroadcastEvent(entry types.ActivityEntry) {}
func (r *recordingBroadcaster) BroadcastStats(ctx context.Context) {
	r.statsCtxs = append(r.statsCtxs, ctx)
}

func newValidTrigger(name, connector, _, agentRef string) *triggers.Trigger {
	return &triggers.Trigger{
		ID:             name,
		ConnectorRef:   connector,
		AgentRef:       agentRef,
		AgentValid:     true,
		ConnectorValid: true,
	}
}

func newTestQueueEvent() eventqueue.Event {
	headers, _ := json.Marshal(map[string]string{
		"type":   "issue.opened",
		"source": "test",
		"actor":  "actor",
		"action": "opened",
	})
	return eventqueue.Event{
		ID:        "evt-1",
		Connector: "conn-1",
		Headers:   headers,
		Data:      json.RawMessage(`{}`),
		Raw:       `{}`,
	}
}

func TestHandleEvent_EnqueuesWhenMatched(t *testing.T) {
	q := &mockQueue{}
	idx := triggerindex.NewIndex()
	idx.Update(newValidTrigger("trig-1", "conn-1", "issue.opened", "agent-1"))

	r := &Router{
		eq:          q,
		index:       idx,
		invocations: invocations.NewMemoryStore(10),
		broadcaster: &stubBroadcaster{},
	}

	r.handleEvent(context.Background(), newTestQueueEvent())

	if len(q.enqueued) != 1 {
		t.Errorf("expected 1 enqueued task, got %d", len(q.enqueued))
	}
	if len(q.routedIDs) != 1 || q.routedIDs[0] != "evt-1" {
		t.Errorf("expected event to be marked routed, got %v", q.routedIDs)
	}
}

func TestHandleEvent_MarksRoutedWhenNoMatches(t *testing.T) {
	q := &mockQueue{}
	idx := triggerindex.NewIndex()
	// no triggers

	r := &Router{
		eq:          q,
		index:       idx,
		invocations: invocations.NewMemoryStore(10),
		broadcaster: &stubBroadcaster{},
	}

	r.handleEvent(context.Background(), newTestQueueEvent())

	if len(q.enqueued) != 0 {
		t.Errorf("expected 0 enqueued tasks, got %d", len(q.enqueued))
	}
	if len(q.routedIDs) != 1 {
		t.Errorf("expected event to be marked routed even with no matches")
	}
}

func TestHandleEvent_MarksRoutedOnEnqueueFailure(t *testing.T) {
	q := &mockQueue{
		enqueueFn: func(task eventqueue.Task) error {
			return errors.New("db error")
		},
	}
	idx := triggerindex.NewIndex()
	idx.Update(newValidTrigger("trig-1", "conn-1", "issue.opened", "agent-1"))

	r := &Router{
		eq:          q,
		index:       idx,
		invocations: invocations.NewMemoryStore(10),
		broadcaster: &stubBroadcaster{},
	}

	r.handleEvent(context.Background(), newTestQueueEvent())

	// Event is still marked routed to avoid infinite retry loops.
	if len(q.routedIDs) != 1 {
		t.Errorf("expected event to be marked routed even on enqueue failure")
	}
}

func TestHandleEvent_MultipleMatches(t *testing.T) {
	q := &mockQueue{}
	idx := triggerindex.NewIndex()
	idx.Update(newValidTrigger("trig-a", "conn-1", "issue.opened", "agent-a"))
	idx.Update(newValidTrigger("trig-b", "conn-1", "issue.opened", "agent-b"))

	r := &Router{
		eq:          q,
		index:       idx,
		invocations: invocations.NewMemoryStore(10),
		broadcaster: &stubBroadcaster{},
	}

	r.handleEvent(context.Background(), newTestQueueEvent())

	if len(q.enqueued) != 2 {
		t.Errorf("expected 2 enqueued tasks, got %d", len(q.enqueued))
	}
}

// ctxKey is a private context key type used by the regression tests below.
type ctxKey string

func TestHandleEvent_BroadcastStatsPropagatesCallerContext(t *testing.T) {
	idx := triggerindex.NewIndex()
	idx.Update(newValidTrigger("trig-1", "conn-1", "issue.opened", "agent-1"))

	br := &recordingBroadcaster{}
	r := &Router{
		eq:          &mockQueue{},
		index:       idx,
		invocations: invocations.NewMemoryStore(10),
		broadcaster: br,
	}

	const key ctxKey = "trace-id"
	const val = "abc-123"
	ctx := context.WithValue(context.Background(), key, val)

	r.handleEvent(ctx, newTestQueueEvent())

	if len(br.statsCtxs) != 1 {
		t.Fatalf("expected exactly 1 BroadcastStats call, got %d", len(br.statsCtxs))
	}
	got, _ := br.statsCtxs[0].Value(key).(string)
	if got != val {
		t.Errorf("BroadcastStats ctx did not carry caller value: got %q, want %q", got, val)
	}
}

func TestHandleEvent_BroadcastStatsSurvivesCanceledCallerCtx(t *testing.T) {
	idx := triggerindex.NewIndex()
	// No triggers -> exercises the "unmatched" broadcast site.

	br := &recordingBroadcaster{}
	r := &Router{
		eq:          &mockQueue{},
		index:       idx,
		invocations: invocations.NewMemoryStore(10),
		broadcaster: br,
	}

	const key ctxKey = "trace-id"
	const val = "xyz-789"
	parent := context.WithValue(context.Background(), key, val)
	ctx, cancel := context.WithCancel(parent)
	cancel() // pre-cancel to simulate router shutdown mid-handle

	r.handleEvent(ctx, newTestQueueEvent())

	if len(br.statsCtxs) != 1 {
		t.Fatalf("expected exactly 1 BroadcastStats call, got %d", len(br.statsCtxs))
	}
	got := br.statsCtxs[0]
	if err := got.Err(); err != nil {
		t.Errorf("BroadcastStats ctx is canceled (Err=%v); expected WithoutCancel-wrapped ctx", err)
	}
	if v, _ := got.Value(key).(string); v != val {
		t.Errorf("BroadcastStats ctx did not preserve caller value: got %q, want %q", v, val)
	}
}

// TestHandleEvent_RecordsInvocationSubject covers the subject the router stores
// on each invocation: a plugin listing "what the agents worked on" reads it off
// the invocation row, so it has to be there from the moment the run is recorded.
func TestHandleEvent_RecordsInvocationSubject(t *testing.T) {
	q := &mockQueue{}
	idx := triggerindex.NewIndex()
	idx.Update(newValidTrigger("trig-1", "conn-1", "issue.opened", "agent-1"))
	idx.Update(newValidTrigger("trig-2", "conn-1", "issue.opened", "agent-2"))

	store := invocations.NewMemoryStore(10)
	r := &Router{
		eq:          q,
		index:       idx,
		invocations: store,
		broadcaster: &stubBroadcaster{},
	}

	evt := newTestQueueEvent()
	evt.Data = json.RawMessage(`{
		"action": "opened",
		"repository": {"full_name": "apps/app"},
		"issue": {"number": 412, "title": "Login fails with a trailing slash"}
	}`)
	r.handleEvent(context.Background(), evt)

	list := store.List(invocations.ListOptions{})
	if len(list) != 2 {
		t.Fatalf("expected 2 recorded invocations, got %d", len(list))
	}
	for _, inv := range list {
		if inv.Subject == nil {
			t.Fatalf("invocation %s has no subject", inv.ID)
		}
		if inv.Subject.Repo != "apps/app" || inv.Subject.Number != 412 || inv.Subject.Kind != "issue" {
			t.Errorf("invocation %s subject = %+v, want apps/app#412 as an issue", inv.ID, *inv.Subject)
		}
		if inv.Subject.Title != "Login fails with a trailing slash" {
			t.Errorf("invocation %s subject title = %q", inv.ID, inv.Subject.Title)
		}
	}
}

// TestHandleEvent_RecordsNoSubjectForNonWorkItemEvent: an event that is not
// about an issue or a pull records no subject rather than an empty one.
func TestHandleEvent_RecordsNoSubjectForNonWorkItemEvent(t *testing.T) {
	idx := triggerindex.NewIndex()
	idx.Update(newValidTrigger("trig-1", "conn-1", "push", "agent-1"))

	store := invocations.NewMemoryStore(10)
	r := &Router{
		eq:          &mockQueue{},
		index:       idx,
		invocations: store,
		broadcaster: &stubBroadcaster{},
	}

	evt := newTestQueueEvent()
	evt.Data = json.RawMessage(`{"ref":"refs/heads/main","repository":{"full_name":"apps/app"}}`)
	r.handleEvent(context.Background(), evt)

	list := store.List(invocations.ListOptions{})
	if len(list) != 1 {
		t.Fatalf("expected 1 recorded invocation, got %d", len(list))
	}
	if list[0].Subject != nil {
		t.Errorf("push event recorded subject %+v, want none", *list[0].Subject)
	}
}
