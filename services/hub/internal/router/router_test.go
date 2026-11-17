package router

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/eventqueue"
	"github.com/ainsel-project/ainsel/services/hub/internal/invocations/invocationstest"
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

// recordingBroadcaster captures the context passed to BroadcastStats and the
// activity frames passed to BroadcastEvent.
type recordingBroadcaster struct {
	statsCtxs []context.Context
	events    []types.ActivityEntry
}

func (r *recordingBroadcaster) BroadcastError(entry types.ErrorEntry) {}
func (r *recordingBroadcaster) BroadcastEvent(entry types.ActivityEntry) {
	r.events = append(r.events, entry)
}
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
		invocations: invocationstest.New(10),
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

// The WebSocket "event" frame is a public surface: it names the match verdict
// `match` and still carries the retired `status` spelling with the same value,
// or the live stream would disagree with the REST list.
func TestHandleEvent_BroadcastEventCarriesMatchAndRetiredStatus(t *testing.T) {
	t.Run("matched", func(t *testing.T) {
		idx := triggerindex.NewIndex()
		idx.Update(newValidTrigger("trig-1", "conn-1", "issue.opened", "agent-1"))
		br := &recordingBroadcaster{}
		r := &Router{
			eq:          &mockQueue{},
			index:       idx,
			invocations: invocationstest.New(10),
			broadcaster: br,
		}

		r.handleEvent(context.Background(), newTestQueueEvent())

		if len(br.events) != 1 {
			t.Fatalf("expected 1 broadcast event, got %d", len(br.events))
		}
		assertActivityMatchFields(t, br.events[0], "matched")
	})

	t.Run("unmatched", func(t *testing.T) {
		br := &recordingBroadcaster{}
		r := &Router{
			eq:          &mockQueue{},
			index:       triggerindex.NewIndex(),
			invocations: invocationstest.New(10),
			broadcaster: br,
		}

		r.handleEvent(context.Background(), newTestQueueEvent())

		if len(br.events) != 1 {
			t.Fatalf("expected 1 broadcast event, got %d", len(br.events))
		}
		assertActivityMatchFields(t, br.events[0], "unmatched")
	})
}

// assertActivityMatchFields checks that a broadcast frame names the match verdict
// `match` and mirrors it in the retired `status` key — in the struct and in the
// JSON a client actually reads.
func assertActivityMatchFields(t *testing.T, entry types.ActivityEntry, want string) {
	t.Helper()
	if entry.Match != want {
		t.Errorf("match = %q, want %q", entry.Match, want)
	}
	if entry.Status != want {
		t.Errorf("retired status = %q, want %q", entry.Status, want)
	}

	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"match", "status"} {
		v, ok := fields[key]
		if !ok {
			t.Fatalf("frame is missing the %q key: %s", key, raw)
		}
		var got string
		if err := json.Unmarshal(v, &got); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if got != want {
			t.Errorf("frame %s = %q, want %q", key, got, want)
		}
	}
}

func TestHandleEvent_MarksRoutedWhenNoMatches(t *testing.T) {
	q := &mockQueue{}
	idx := triggerindex.NewIndex()
	// no triggers

	r := &Router{
		eq:          q,
		index:       idx,
		invocations: invocationstest.New(10),
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
		invocations: invocationstest.New(10),
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
		invocations: invocationstest.New(10),
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
		invocations: invocationstest.New(10),
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
		invocations: invocationstest.New(10),
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
