// Package queuesignal publishes per-agent queue depth onto Agent status, so the
// agent operator can scale pods from real work instead of a standing replica
// count.
//
// The hub owns the task store, so it is the only component that knows whether an
// agent has work waiting. The operator owns the Deployment. This package is the
// seam between them: it watches queue transitions, re-measures the affected
// agent's queue, and writes the four queue fields on Agent status.
//
// Writes are edge-triggered with a debounce in between. A queue going from empty
// to non-empty is published on the next tick, because that is the case where
// latency is visible to a human: a chat message or webhook is sitting behind a
// pod that still has to be created, scheduled and booted. Movement inside a
// non-empty queue is debounced, because the number only matters near the replica
// ceiling and a burst of fifty events is fifty measurements of the same "yes,
// there is work".
//
// A delivery gate (see WithGate) can withhold an agent's due-task ids without
// touching its counts - which is how working hours keep a pod from being spawned
// for work that has to wait. Counts stay honest because the backlog is real: the
// operator and the console both need to see tasks that are queued but not running.
//
// Every published signal carries an observation timestamp, and a periodic sweep
// republishes known agents whether or not anything changed. That heartbeat is
// what lets the operator tell "this queue is empty" from "nobody has looked at
// this queue since the hub died", and treat the latter as unknown rather than as
// permission to sleep.
package queuesignal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ainsel-project/ainsel/services/hub/internal/eventqueue"
	v1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

// Defaults for New. Tick is the wake-latency budget: an enqueued task waits at
// most one tick to become visible to the operator, against a pod boot that takes
// seconds. Debounce caps write frequency for agents with continuously busy
// queues. Sweep is the heartbeat interval, and therefore sets the floor for how
// long the operator may wait before calling a published signal stale.
const (
	DefaultTick     = 250 * time.Millisecond
	DefaultDebounce = 2 * time.Second
	DefaultSweep    = 60 * time.Second
)

// Signal is what gets written to Agent status.
type Signal struct {
	Pending int32
	Active  int32
	// DueIDs are the oldest runnable pending task ids (retry backoff elapsed),
	// oldest first, capped at DueIDsCap. The operator turns each into one
	// per-task Job — they are cheap enough to publish with every signal so the
	// operator never has to ask.
	DueIDs []int64
	// ObservedAt stamps when Pending/Active were measured. The operator uses it
	// to tell "queue is empty" from "nobody has looked lately".
	ObservedAt time.Time
	// LastInvocation, when non-zero, advances the agent's idle clock. It is set
	// only when work arrived, so it means "the last time this agent was handed
	// something to do", and scale-down measures quiet against it.
	LastInvocation time.Time
}

// DueIDsCap bounds how many runnable task ids one signal carries. It lives here
// rather than in the store so the status object's size is a property of this
// publisher, not of the queue's shape.
const DueIDsCap = 25

// Empty reports whether an agent has no work waiting or in flight.
func (s Signal) Empty() bool { return s.Pending == 0 && s.Active == 0 }

// sameDueIDs reports whether two measurements found the same set of runnable
// task ids. The counts can sit still while a retried task's backoff elapses and
// the set turns over, and the operator must learn about the new member
// — that is a change even though pending did not move.
func sameDueIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Counter measures queue depth. Satisfied by *eventqueue.Store.
type Counter interface {
	QueueCounts(ctx context.Context, agentName string) (eventqueue.QueueDepth, error)
	AllQueueCounts(ctx context.Context) (map[string]eventqueue.QueueDepth, error)
	DueTaskIDs(ctx context.Context, agentName string, limit int) ([]int64, error)
}

// DeliveryGate reports whether an agent may be handed work right now, and when
// that answer changes. Satisfied by *workinghours.Gate; kept as an interface so
// the publisher's holding behaviour can be tested without a k8s reader.
type DeliveryGate interface {
	Hold(ctx context.Context, agentName string) (held bool, opensAt time.Time)
}

// AgentLister names the agents that expect a queue signal from the hub —
// currently every agent, since all work runs as per-task Jobs and every task
// Job is created from a queue signal. Satisfied by the K8s reader below.
//
// The publisher needs this because its own memory of what it published is not a
// fact about the world. It is a map in one process, and a restarting hub builds an
// empty one, so an agent with a drained queue would be covered by the sweep again
// only once somebody handed it new work - which for a truly idle agent is never.
// The Agent CRs are the durable answer to "who is waiting for a signal", and a
// missed heartbeat leaves an agent's due-task ids stale until the next write.
type AgentLister interface {
	QueueWatchedAgents(ctx context.Context) ([]string, error)
}

// Patcher writes a signal to one agent. Split out from the k8s implementation so
// the publisher's decisions can be tested without a cluster.
type Patcher interface {
	Patch(ctx context.Context, agentName string, sig Signal) error
}

// Publisher coalesces queue-change observations into status writes.
//
// Create with New, start with Run. Observe is safe to call from any goroutine and
// never blocks, which is what makes it usable from the hub's request paths.
type Publisher struct {
	counters Counter
	patcher  Patcher
	lister   AgentLister
	gate     DeliveryGate
	tick     time.Duration
	debounce time.Duration
	sweep    time.Duration
	log      *slog.Logger

	mu      sync.Mutex
	dirty   map[string]bool      // agents with unpublished changes
	last    map[string]Signal    // last signal written per agent
	nextPub map[string]time.Time // earliest time a non-edge update may be written
	// wakes holds the agents currently held by the gate, mapped to the instant
	// their window opens. A held agent has no queue change to mark it dirty, so
	// without this the backlog would wait for the next sweep to start moving.
	wakes map[string]time.Time
}

// New builds a Publisher. A nil logger becomes slog.Default(). Without
// WithAgentLister the sweep only covers agents this process has already written
// for, which is the right shape for tests and too weak for a hub that restarts.
func New(counters Counter, patcher Patcher, opts ...Option) *Publisher {
	p := &Publisher{
		counters: counters,
		patcher:  patcher,
		tick:     DefaultTick,
		debounce: DefaultDebounce,
		sweep:    DefaultSweep,
		log:      slog.Default(),
		dirty:    map[string]bool{},
		last:     map[string]Signal{},
		nextPub:  map[string]time.Time{},
		wakes:    map[string]time.Time{},
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Option tunes a Publisher. Production passes only WithAgentLister; the timing
// and logger options below are unexported test seams, so the exported surface of
// this package reads as the configuration the hub actually supports. Production
// takes the Default* constants, and a non-default interval is exercised only by
// the tests that set these.
type Option func(*Publisher)

// withTick sets the publish loop interval (wake-latency budget).
func withTick(d time.Duration) Option { return func(p *Publisher) { p.tick = d } }

// withDebounce sets the minimum interval between non-edge updates for one agent.
func withDebounce(d time.Duration) Option { return func(p *Publisher) { p.debounce = d } }

// withSweep sets how often known agents are re-measured and republished, which
// repairs failed writes, catches changes the store did not report, and keeps the
// observation timestamp fresh.
func withSweep(d time.Duration) Option { return func(p *Publisher) { p.sweep = d } }

// withLogger directs the publisher's logs.
func withLogger(l *slog.Logger) Option { return func(p *Publisher) { p.log = l } }

// WithAgentLister points the sweep at the Agent CRDs, so an agent that opted into
// queue scaling keeps beating from the first sweep after a hub restart even when
// it has had no work since.
func WithAgentLister(l AgentLister) Option { return func(p *Publisher) { p.lister = l } }

// WithGate points the publisher at a delivery gate. While an agent is held, its
// signal carries no due-task ids, so the operator creates no task Job for work
// that must wait; the counts still publish. Without a gate every agent with due
// tasks is dispatched whenever it has them, which is the behaviour from before
// working hours existed.
func WithGate(g DeliveryGate) Option { return func(p *Publisher) { p.gate = g } }

// Observe records that an agent's queue may have changed. It never blocks and
// never performs I/O; it is safe to call inline from a request handler.
func (p *Publisher) Observe(agentName string) {
	if agentName == "" {
		return
	}
	p.mu.Lock()
	p.dirty[agentName] = true
	p.mu.Unlock()
}

// Run drives the publish loop until ctx is cancelled.
//
// The first sweep after startup republishes everything the queue currently
// holds: last-written state lives in memory, so agents whose tasks arrived while
// the hub was down would otherwise stay invisible to the operator.
func (p *Publisher) Run(ctx context.Context) error {
	ticker := time.NewTicker(p.tick)
	defer ticker.Stop()
	sweep := time.NewTicker(p.sweep)
	defer sweep.Stop()

	p.log.Info("queue signal publisher started",
		"tick", p.tick.String(), "debounce", p.debounce.String(), "sweep", p.sweep.String())

	// Immediate first sweep rather than waiting out the sweep interval.
	p.markAll(ctx)
	p.publishRound(ctx, false)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			p.publishRound(ctx, false)
		case <-sweep.C:
			p.markAll(ctx)
			p.publishRound(ctx, true)
		}
	}
}

// markAll measures the queue and marks every agent the publisher reports on:
// anything with work waiting or in flight, and anything it has published before.
//
// The second half is the heartbeat, and it is the reason the sweep exists. Only
// agents with rows come back from AllQueueCounts, so marking just the busy set
// would stop covering an agent the moment its queue drained and the empty signal
// was written. Its observation timestamp would freeze at that instant, the
// operator's TTL would expire, and a stale signal means "hold the pods you have"
// — so the one agent the feature exists for, the idle one, would never scale to
// zero. A dead hub and a busy hub look identical from a frozen timestamp, which
// is exactly the distinction this field is there to make.
func (p *Publisher) markAll(ctx context.Context) {
	counts, err := p.counters.AllQueueCounts(ctx)
	if err != nil {
		p.log.Error("queue signal: measurement failed", "error", err)
		return
	}

	// Read the Agent list before taking the lock: Observe runs inline in the hub's
	// request paths and must never wait on a trip to the API server.
	var optedIn []string
	if p.lister != nil {
		optedIn, err = p.lister.QueueWatchedAgents(ctx)
		if err != nil {
			// A failed read is not a reason to stop beating for the agents already
			// known, so it degrades to the previous behaviour rather than to silence.
			// Agents reachable only through the lister age out, and the operator
			// reads that as "hold the pods you have".
			p.log.Error("queue signal: agent list failed", "error", err)
			optedIn = nil
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	for name := range counts {
		p.dirty[name] = true
	}
	// Covers the correction case too: an agent last reported busy that is absent
	// from the busy set has drained without telling us, and needs its counts
	// fixed as well as its timestamp refreshed.
	for name := range p.last {
		p.dirty[name] = true
	}
	for _, name := range optedIn {
		p.dirty[name] = true
	}
}

// publishRound re-measures and writes the marked agents. When force is set, the
// debounce is bypassed so the sweep can keep observation timestamps fresh.
func (p *Publisher) publishRound(ctx context.Context, force bool) {
	// A window that just opened is its own change: nothing happened to the queue,
	// so the publisher has to remember to look.
	p.markWakes(time.Now())

	p.mu.Lock()
	names := make([]string, 0, len(p.dirty))
	for name := range p.dirty {
		names = append(names, name)
	}
	p.dirty = map[string]bool{}
	p.mu.Unlock()

	for _, name := range names {
		done, err := p.publishOne(ctx, name, force)
		if err != nil {
			p.log.Warn("queue signal: publish failed, will retry", "agent", name, "error", err)
			p.mu.Lock()
			p.dirty[name] = true // retry on the next tick, throttled by debounce
			p.mu.Unlock()
			continue
		}
		if !done {
			p.mu.Lock()
			p.dirty[name] = true // still debounced; not forgotten
			p.mu.Unlock()
		}
	}
}

// scheduleWake records when a held agent should be measured again. An agent held
// by the gate has no queue movement to wake this publisher, and the sweep
// interval is too coarse a promise to make about when queued work starts after a
// window opens, so the opening instant is kept and markWakes turns it into a
// re-measure.
func (p *Publisher) scheduleWake(name string, held bool, opensAt time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if held && !opensAt.IsZero() {
		p.wakes[name] = opensAt
		return
	}
	delete(p.wakes, name)
}

// markWakes marks every agent whose hold has elapsed. Called before each round,
// so the tick (not the sweep) is the latency budget for a window opening.
func (p *Publisher) markWakes(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for name, at := range p.wakes {
		if !now.Before(at) {
			p.dirty[name] = true
			delete(p.wakes, name)
		}
	}
}

// publishOne measures one agent and writes it if the debounce rules permit.
// done reports whether the agent has nothing left to say.
func (p *Publisher) publishOne(ctx context.Context, name string, force bool) (bool, error) {
	depth, err := p.counters.QueueCounts(ctx, name)
	if err != nil {
		return false, fmt.Errorf("count: %w", err)
	}
	// The due-id list drives Job dispatch. A failure here must not stall the
	// counts — an agent whose signal froze would hold or lose pods — so the
	// publish goes through rather than failing the whole signal.
	//
	// A held agent gets no due ids at all, deliberately written as an empty list
	// rather than left at the last published one: an empty list is what tells the
	// operator to dispatch nothing, which is the point of holding.
	held, opensAt := false, time.Time{}
	if p.gate != nil {
		held, opensAt = p.gate.Hold(ctx, name)
	}
	var dueIDs []int64
	var dueErr error
	if !held {
		dueIDs, dueErr = p.counters.DueTaskIDs(ctx, name, DueIDsCap)
		if dueErr != nil {
			p.log.Warn("queue signal: due task ids unavailable", "agent", name, "error", dueErr)
		}
	}
	p.scheduleWake(name, held, opensAt)

	now := time.Now()
	p.mu.Lock()
	prev, hadPrev := p.last[name]
	if dueErr != nil {
		// Keep the last published list instead of writing an explicit empty
		// one: an empty list tells the operator to stop dispatching, stalling
		// every due task until the next sweep. A stale list is harmless — the
		// runner exits quietly on 404/409/terminal-status fetches — and the
		// counts still refresh. (No history yet: nothing was published before,
		// so there is nothing stale to keep.)
		dueIDs = prev.DueIDs
	}
	sig := Signal{Pending: depth.Pending, Active: depth.Active, DueIDs: dueIDs, ObservedAt: now}
	// Work arrived: advance the idle clock, so an agent cannot be judged quiet
	// while tasks are queued for it. Only a queue that actually has something in
	// it counts. In particular a first observation with nothing queued — which is
	// every opted-in agent the sweep publishes after a hub restart — must leave the
	// clock alone, or each deploy would restart the countdown before the agent is
	// allowed to park, and status.lastInvocation would report activity that never
	// happened.
	if depth.Pending > 0 && (!hadPrev || depth.Pending > prev.Pending) {
		sig.LastInvocation = now
	}
	// An edge — the queue emptying or filling, or a claim starting or finishing —
	// must be published immediately. While the counts sit still, a change in the
	// runnable set is its own edge: a task whose retry backoff just elapsed is
	// dispatchable now, and a debounced write would delay its Job. Ordinary
	// growth in an already-busy queue keeps waiting for the (short) debounce
	// window, and identical state needs no write at all.
	countsSame := hadPrev && prev.Pending == sig.Pending && prev.Active == sig.Active
	dueSame := sameDueIDs(prev.DueIDs, sig.DueIDs)
	edge := !hadPrev || prev.Empty() != sig.Empty() || prev.Active != sig.Active || (countsSame && !dueSame)
	switch {
	case hadPrev && countsSame && dueSame && !force:
		p.mu.Unlock()
		return true, nil
	case !edge && !force && now.Before(p.nextPub[name]):
		p.mu.Unlock()
		return false, nil // debounced, retried on a later round
	}
	p.nextPub[name] = now.Add(p.debounce)
	p.mu.Unlock()

	if err := p.patcher.Patch(ctx, name, sig); err != nil {
		if apierrors.IsNotFound(err) {
			// No Agent CR to publish to: deleted, or a task routed to a name that
			// never existed. Forget it, so the sweep stops resurrecting it.
			p.mu.Lock()
			delete(p.last, name)
			delete(p.nextPub, name)
			delete(p.wakes, name)
			p.mu.Unlock()
			p.log.Debug("queue signal: agent not found, dropping", "agent", name)
			return true, nil
		}
		return false, fmt.Errorf("patch: %w", err)
	}

	p.mu.Lock()
	p.last[name] = sig
	p.mu.Unlock()
	return true, nil
}

// published reports the last signal written for an agent. It is a test seam: its
// only readers are the tests in this package, and nothing in the hub surfaces it
// over the API.
func (p *Publisher) published(name string) (Signal, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	sig, ok := p.last[name]
	return sig, ok
}

// mergePatchBody renders the JSON merge patch for one agent's queue fields.
//
// The keys are named explicitly rather than marshalling a struct: the counts are
// omitempty in the Go type, so a zero would drop out of the document and an agent
// whose queue had drained would never be told it was empty.
func mergePatchBody(sig Signal) ([]byte, error) {
	// DueIDs must be written explicitly as [] when empty: a merge patch that
	// omits the key leaves a stale list on the object, and a drained agent
	// would keep dispatching Jobs for tasks that no longer exist.
	due := sig.DueIDs
	if due == nil {
		due = []int64{}
	}
	status := map[string]any{
		"pendingTasks":    sig.Pending,
		"activeTasks":     sig.Active,
		"pendingTaskIds":  due,
		"queueObservedAt": metav1.NewTime(sig.ObservedAt.UTC()),
	}
	if !sig.LastInvocation.IsZero() {
		status["lastInvocation"] = metav1.NewTime(sig.LastInvocation.UTC())
	}
	body, err := json.Marshal(map[string]any{"status": status})
	if err != nil {
		return nil, fmt.Errorf("queuesignal: marshal status patch: %w", err)
	}
	return body, nil
}

// K8sPatcher writes queue signals onto Agent status subresources.
//
// It sends a raw JSON merge patch naming exactly the fields the hub owns, rather
// than a read-modify-update, for two reasons. Both the hub and the operator write
// status, and an update would let either clobber the other's fields. And the
// counts are omitempty in the Go struct, so a marshalled zero would vanish from
// the document and an agent whose queue drained would never be told so.
type K8sPatcher struct {
	client    client.Client
	namespace string
}

// NewK8sPatcher builds a patcher writing to agents in the given namespace.
func NewK8sPatcher(c client.Client, namespace string) *K8sPatcher {
	return &K8sPatcher{client: c, namespace: namespace}
}

// Patch writes sig to the named agent's status.
func (k *K8sPatcher) Patch(ctx context.Context, agentName string, sig Signal) error {
	body, err := mergePatchBody(sig)
	if err != nil {
		return err
	}

	obj := &v1alpha1.Agent{}
	obj.SetName(agentName)
	obj.SetNamespace(k.namespace)
	if err := k.client.Status().Patch(ctx, obj, client.RawPatch(types.MergePatchType, body)); err != nil {
		if apierrors.IsNotFound(err) {
			return err // unwrapped, so IsNotFound still sees it upstream
		}
		return fmt.Errorf("queuesignal: patch agent %q status: %w", agentName, err)
	}
	return nil
}

// K8sAgentLister reads the Agent CRDs to find the agents that opted into
// queue-driven scaling, which is the set the sweep owes a heartbeat to.
//
// It lists through the hub's API client rather than keeping a set of names,
// because opting in is a spec change made at any moment by the console, the API or
// the MCP tools, and a cached set would either miss the new one or need a watch.
// The read is one List per sweep.
type K8sAgentLister struct {
	client    client.Client
	namespace string
}

// NewK8sAgentLister builds a lister reading Agents in the given namespace.
func NewK8sAgentLister(c client.Client, namespace string) *K8sAgentLister {
	return &K8sAgentLister{client: c, namespace: namespace}
}

// QueueWatchedAgents names every agent in the namespace: each one's runs are
// driven by the published queue signal, so each one must keep receiving it.
func (k *K8sAgentLister) QueueWatchedAgents(ctx context.Context) ([]string, error) {
	var list v1alpha1.AgentList
	if err := k.client.List(ctx, &list, client.InNamespace(k.namespace)); err != nil {
		return nil, fmt.Errorf("queuesignal: list agents in %q: %w", k.namespace, err)
	}

	names := make([]string, 0, len(list.Items))
	for i := range list.Items {
		names = append(names, list.Items[i].Name)
	}
	return names, nil
}
