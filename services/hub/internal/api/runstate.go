package api

import (
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/eventqueue"
	"github.com/ainsel-project/ainsel/services/hub/internal/invocations"
)

// DefaultTaskStartTimeout is how long a task may wait for a worker before the
// UI flags it as stuck (queued · stuck). Overridable via
// TASK_START_TIMEOUT_SECONDS. In per-task Job mode nothing claims, so a task
// that never gets a worker must not need a claim to be recognised as stuck.
const DefaultTaskStartTimeout = 15 * time.Minute

// taskRunDisplay is what a view renders for a run: the derived state plus the
// modifiers that qualify it. Returned as one struct because the display has
// outgrown "a state and nothing else" — untyped booleans at a call site are
// where a mapping starts lying — and so the next modifier lands as a field.
type taskRunDisplay struct {
	// State is the run vocabulary value: queued, running, success, failure,
	// timeout or unknown.
	State string
	// Stuck marks a queued task that has waited for a worker past the start
	// deadline. It never applies to a started task.
	Stuck bool
	// TimedOut marks a completed task whose retained error is the runner's
	// turn-timeout nack: the retry finished the work, but an earlier attempt hit
	// the ceiling. Only ever set alongside State == success.
	TimedOut bool
}

// deriveRunState computes the single display run state shared by every view —
// the activity list, the invocation list, and (via the same fields) the event
// detail page — so they can never disagree.
//
// The task row is authoritative when present:
//
//   - completed → success, failed → failure, expired → timeout
//     (a task that was never picked up before its deadline; the deadline is a
//     timeout in the run vocabulary — the task itself never ran to fail)
//   - started (claimed on the removed legacy path, or a conversation row
//     exists) → running
//   - otherwise → queued, flagged stuck once it has waited past stuckAfter
//
// A completed task whose retained error is a turn-timeout nack (see
// eventqueue.IsTurnTimeoutError) stays success — the retry really did finish
// the task — but reports TimedOut, so "6 h attempt timed out, 9-min retry
// closed it" cannot render as an unqualified success. The modifier is derived
// inside the completed branch only: on a queued or running row the retained
// error describes an attempt that is not over yet, and a task already derived
// to failure or timeout says so by itself.
//
// With no task row there is nothing to back a "queued" or "running" claim: a
// terminal invocation record still reports its state, anything else (a hub
// that died between recording and enqueuing, a pruned record) is unknown —
// never running, never queued.
//
// "started" lags up to one LLM turn on the per-task Job path (pod boot to
// first assistant message is invisible: nothing marks a task started at pod
// boot, see #47);
// that is accepted for display. It must not be confused with a real start
// timestamp for billing or scaling.
func deriveRunState(task *eventqueue.Task, inv *invocations.Invocation, now time.Time, stuckAfter time.Duration) taskRunDisplay {
	if task == nil {
		if inv != nil && inv.IsTerminal() {
			return taskRunDisplay{State: inv.Status}
		}
		return taskRunDisplay{State: invocations.StatusUnknown}
	}
	switch task.Status {
	case "completed":
		return taskRunDisplay{
			State:    invocations.StatusSuccess,
			TimedOut: eventqueue.IsTurnTimeoutError(task.Error),
		}
	case "failed":
		return taskRunDisplay{State: invocations.StatusFailure}
	case "expired":
		return taskRunDisplay{State: invocations.StatusTimeout}
	}
	if task.Started() {
		return taskRunDisplay{State: invocations.StatusRunning}
	}
	if stuckAfter > 0 && !task.CreatedAt.IsZero() && now.Sub(task.CreatedAt) > stuckAfter {
		return taskRunDisplay{State: invocations.StatusQueued, Stuck: true}
	}
	return taskRunDisplay{State: invocations.StatusQueued}
}
