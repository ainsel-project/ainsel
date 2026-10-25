// Package invocations provides storage and retrieval of agent invocation
// history. An "invocation" represents a single dispatch of an event to a
// matched agent — recording who was invoked, with what trigger, when it
// started, when it completed, and what its outcome was.
package invocations

import "time"

// Status values for an invocation lifecycle.
const (
	// StatusQueued means the dispatched task exists but no worker has picked
	// it up yet. It is produced by the task-derived mapping, not by Record:
	// a record is still created in StatusRunning at dispatch.
	StatusQueued  = "queued"
	StatusRunning = "running"
	StatusSuccess = "success"
	StatusFailure = "failure"
	StatusTimeout = "timeout"
	// StatusUnknown is a display-only derived value: no task row and no
	// terminal invocation record back the run, so nothing can be claimed about
	// it. It is never stored.
	StatusUnknown = "unknown"
)

// Invocation is a single record of an event dispatched to an agent.
//
// An invocation is created in StatusRunning when the router publishes an event
// to an agent's task queue, and is updated to a terminal status (success,
// failure, or timeout) when the agent reports completion via
// hub.invocation.completed.
type Invocation struct {
	// ID is the unique identifier for this invocation. Format: "inv-<8 hex>".
	ID string `json:"id"`

	// AgentName is the name of the agent the event was dispatched to.
	AgentName string `json:"agentName"`

	// TriggerName is the name of the trigger that matched.
	TriggerName string `json:"triggerName"`

	// EventID is the canonical ID of the originating event.
	EventID string `json:"eventId,omitempty"`

	// Connector is the connector that produced the event.
	Connector string `json:"connector,omitempty"`

	// StartTime is when the invocation was dispatched.
	StartTime time.Time `json:"startTime"`

	// EndTime is when the invocation completed. Nil while running.
	EndTime *time.Time `json:"endTime,omitempty"`

	// DurationMs is the duration in milliseconds. Nil while running.
	DurationMs *int64 `json:"durationMs,omitempty"`

	// Status is one of: running, success, failure, timeout.
	Status string `json:"status"`

	// Error is the error message when Status is failure or timeout. Empty otherwise.
	Error string `json:"error,omitempty"`

	// Task mirrors the dispatch state of the underlying agent_tasks row, when
	// known. It lets the UI distinguish "queued, not started yet" from
	// "the agent ran but produced no conversation".
	Task *TaskState `json:"task,omitempty"`

	// Subject is what the dispatched event was about — the issue or pull this
	// invocation worked on — extracted from the payload at dispatch time. Nil
	// when the event is not about an issue or a pull (a push, a build, a chat
	// message) and for rows recorded before the column existed, so readers must
	// treat it as optional.
	Subject *Subject `json:"subject,omitempty"`
}

// Subject is what an invocation's event was about: one issue or pull/merge request.
//
// It is the small, indexed answer to "what did the agents work on": a reader
// gets repo, number and title from the invocation row instead of paging through
// invocations and fetching each event payload back out to find them. Only the
// fields a listing needs are kept — see SubjectFromPayload for where they come
// from in a payload.
type Subject struct {
	// Repo is the repository full name, e.g. "apps/app".
	Repo string `json:"repo"`
	// Kind is "issue" or "pull".
	Kind string `json:"kind"`
	// Number is the issue or pull number within the repo.
	Number int `json:"number"`
	// Title is the issue or pull title.
	Title string `json:"title,omitempty"`
	// Branch is a pull's head ref, empty for an issue.
	Branch string `json:"branch,omitempty"`
	// URL is the html_url of the issue or pull.
	URL string `json:"url,omitempty"`
	// Body is the issue or pull body, capped at 4096 bytes and cut on a UTF-8
	// boundary so the row stays small and the JSON stays valid.
	Body string `json:"body,omitempty"`
}

// TaskState is the queue/dispatch state of the agent_tasks row that
// corresponds to an invocation.
type TaskState struct {
	// Status is the agent_tasks status: pending, claimed, completed or failed.
	Status string `json:"status"`
	// Attempts is how many times the task has been claimed.
	Attempts int `json:"attempts"`
	// Error is the last failure message, if any.
	Error string `json:"error,omitempty"`
	// StartedAt is the first evidence that a worker is working on the task:
	// the claim time, or the first conversation row the worker wrote (the
	// per-task Job path never claims). Nil while the task is queued. Note this
	// is distinct from Invocation.StartTime, which is the dispatch time.
	StartedAt *time.Time `json:"startedAt,omitempty"`
	// RunState is the derived display state — queued, running, success,
	// failure or timeout — computed from the task, the start evidence and the
	// invocation record. Views should prefer it over mapping Status, which is
	// ambiguous in per-task Job mode ("pending" covers both queued and
	// mid-run).
	RunState string `json:"runState,omitempty"`
	// Stuck marks a queued task that has waited for a worker longer than the
	// configured start deadline. It never applies to a started task.
	Stuck bool `json:"stuck,omitempty"`
}

// Subject kinds. Values are stable wire values: plugins and the console match
// on them, so renaming one is a breaking API change.
const (
	// SubjectKindIssue is a plain issue.
	SubjectKindIssue = "issue"
	// SubjectKindPull is a pull/merge request.
	SubjectKindPull = "pull"
)

// IsTerminal returns true if the invocation has reached a final status.
func (i *Invocation) IsTerminal() bool {
	return i.Status == StatusSuccess || i.Status == StatusFailure || i.Status == StatusTimeout
}
