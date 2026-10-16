package api

import (
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/eventqueue"
	"github.com/ainsel-project/ainsel/services/hub/internal/invocations"
)

// DefaultTaskStartTimeout is how long a task may wait for a worker before the
// UI flags it as stuck (queued · stuck). Overridable via
// TASK_START_TIMEOUT_SECONDS. It is deliberately separate from
// TASK_CLAIM_TIMEOUT_SECONDS, which governs the resident-agent claim reaper:
// in per-task Job mode nothing claims, so the claim timeout governs nothing
// here, and a task that never gets a worker must not need a claim to be
// recognised as stuck.
const DefaultTaskStartTimeout = 15 * time.Minute

// deriveRunState computes the single display run state shared by every view —
// the activity list, the invocation list, and (via the same fields) the event
// detail page — so they can never disagree.
//
// The task row is authoritative when present:
//
//   - completed → success, failed → failure
//   - started (claimed, or a conversation row exists) → running
//   - otherwise → queued, flagged stuck once it has waited past stuckAfter
//
// With no task row there is nothing to back a "queued" or "running" claim: a
// terminal invocation record still reports its outcome, anything else (a hub
// that died between recording and enqueuing, a pruned record) is unknown —
// never running, never queued.
//
// "started" lags up to one LLM turn on the per-task Job path (pod boot to
// first assistant message is invisible until #382's claim endpoint lands);
// that is accepted for display. It must not be confused with a real start
// timestamp for billing or scaling.
func deriveRunState(task *eventqueue.Task, inv *invocations.Invocation, now time.Time, stuckAfter time.Duration) (state string, stuck bool) {
	if task == nil {
		if inv != nil && inv.IsTerminal() {
			return inv.Status, false
		}
		return invocations.StatusUnknown, false
	}
	switch task.Status {
	case "completed":
		return invocations.StatusSuccess, false
	case "failed":
		return invocations.StatusFailure, false
	}
	if task.Started() {
		return invocations.StatusRunning, false
	}
	if stuckAfter > 0 && !task.CreatedAt.IsZero() && now.Sub(task.CreatedAt) > stuckAfter {
		return invocations.StatusQueued, true
	}
	return invocations.StatusQueued, false
}
