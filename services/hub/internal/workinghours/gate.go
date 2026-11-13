// Package workinghours answers the one question the hub's delivery paths ask
// before handing an agent work: is this agent inside its working hours right now?
//
// The answer is read off the Agent CR at the moment of the question, which keeps
// the whole feature free of timers: nobody has to be told when a window opens, a
// kubectl edit takes effect on the next look, and a hub restart has nothing to
// restore. The cost is one cached Get per question, against a queue that is
// measured a few times a second.
//
// One path consults it, and it is the only dispatch decision the hub has: the
// queue-signal publisher withholds an agent's due-task ids while it is held, so the
// operator creates no task Job and no pod is spawned for work that must wait. The
// tasks stay pending and go out in id order when the window opens.
package workinghours

import (
	"context"
	"log/slog"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

// Gate answers "may this agent be handed work now?" from the Agent CRD.
type Gate struct {
	client client.Client
	ns     string
	// now is the clock the window is evaluated against. Production uses
	// time.Now; tests pin it so "currently closed" is a fact rather than a
	// coincidence of when the suite ran.
	now func() time.Time
}

// New builds a Gate reading Agents in ns. A nil client yields a Gate that holds
// nothing, so a hub started without a k8s reader behaves as it did before this
// feature existed.
func New(c client.Client, ns string) *Gate {
	return &Gate{client: c, ns: ns, now: time.Now}
}

// Hold reports whether task delivery to agentName is currently withheld by its
// working hours and, when it is, when the window next opens. The instant is the
// caller's cue to look again: a publisher that stopped publishing due tasks has to
// remember to start, because nothing else wakes it.
//
// It fails open. A missing agent or a failed lookup is not evidence that the
// agent must not work — the queue is the authority on what may be claimed, and a
// transient API-server hiccup must not stop a running agent from working. An
// unreadable window is a configuration error: it is logged and also fails open,
// because a typo in a timezone must not become a queue that never drains.
func (g *Gate) Hold(ctx context.Context, agentName string) (held bool, opensAt time.Time) {
	if g == nil || g.client == nil || agentName == "" {
		return false, time.Time{}
	}

	var agent agentv1alpha1.Agent
	if err := g.client.Get(ctx, types.NamespacedName{Name: agentName, Namespace: g.ns}, &agent); err != nil {
		if !apierrors.IsNotFound(err) {
			slog.Warn("working hours: agent lookup failed, delivering anyway", "agent", agentName, "error", err)
		}
		return false, time.Time{}
	}

	open, next, err := agent.WorkingHoursState(g.now())
	if err != nil {
		slog.Error("working hours: unreadable window, delivering anyway", "agent", agentName, "error", err)
		return false, time.Time{}
	}
	if open {
		return false, time.Time{}
	}
	// Debug, not Warn: this fires on every measurement of a held agent, which for
	// an agent with a night-long window and a busy queue is thousands of times.
	slog.Debug("working hours: delivery held",
		"agent", agentName, "opens_at", next.UTC().Format(time.RFC3339))
	return true, next
}
