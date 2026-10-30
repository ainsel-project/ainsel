package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/eventqueue"
	"github.com/ainsel-project/ainsel/services/hub/internal/invocations"
)

// Activity status values. These mirror the ActivityStatus union in the
// frontend (frontend/src/api/events.ts).
const (
	activityStatusUnmatched = "unmatched"
	activityStatusMatched   = "matched"
	activityStatusError     = "error"
)

// Default and maximum page sizes for the activity list endpoint. The
// frontend requests one page at a time via limit/offset; the cap prevents
// unbounded queries.
const (
	defaultEventLimit = 100
	maxEventLimit     = 500
)

// validActivityStatuses are the status values accepted by the list filter.
var validActivityStatuses = map[string]bool{
	activityStatusUnmatched: true,
	activityStatusMatched:   true,
	activityStatusError:     true,
}

// activityMatch is a single agent/trigger that an event was routed to.
type activityMatch struct {
	Trigger string `json:"trigger"`
	Agent   string `json:"agent"`
	// AgentID is the agent's API id (its resource name) — the route key the
	// console links a match onto. Carried explicitly so views never have to
	// assume the display name doubles as the id; empty on hubs that predate
	// the field, which leaves the link unrendered.
	AgentID string `json:"agentId,omitempty"`
	// RunStatus is the raw invocation status, kept for compatibility. Views
	// should prefer RunState, which is derived from the task and distinguishes
	// queued from running.
	RunStatus  string `json:"runStatus,omitempty"`
	DurationMs *int64 `json:"durationMs,omitempty"`
	Error      string `json:"error,omitempty"`
	// TaskStatus is the raw agent_tasks status (pending, claimed, completed,
	// failed) when a task row exists.
	TaskStatus string `json:"taskStatus,omitempty"`
	// RunState is the derived display state — queued, running, success,
	// failure, timeout or unknown — shared with the invocation list so every
	// view renders the same truth.
	RunState string `json:"runState,omitempty"`
	// StartedAt is the first evidence of work (claim or first conversation
	// row); nil while the task is queued.
	StartedAt *time.Time `json:"startedAt,omitempty"`
	// Stuck marks a queued task waiting for a worker past the start deadline.
	Stuck bool `json:"stuck,omitempty"`
}

// activityEntry is the per-event shape consumed by the frontend Activity page.
type activityEntry struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Connector string `json:"connector,omitempty"`
	// ChannelID is the stream the event was born in. It is the stable
	// handle the UI links a producer label onto; empty for events recorded
	// before the channel registry existed and for direct births.
	ChannelID string          `json:"channelId,omitempty"`
	Status    string          `json:"status"`
	Matches   []activityMatch `json:"matches,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// eventsEnvelope wraps one page of the activity list. Events holds the
// requested page; Total is the number of all events matching the filters,
// which the frontend uses to render the pager.
type eventsEnvelope struct {
	Events []activityEntry `json:"events"`
	Total  int             `json:"total"`
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listEvents(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleEvent(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.getEvent(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) getEvent(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/events/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing event id")
		return
	}
	if s.eventQueue == nil {
		writeError(w, http.StatusServiceUnavailable, "event queue not configured")
		return
	}

	evt, err := s.eventQueue.GetEvent(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get event")
		return
	}
	if evt == nil {
		writeError(w, http.StatusNotFound, "event not found")
		return
	}

	tasks, err := s.eventQueue.TasksForEvents(r.Context(), []string{id})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get event tasks")
		return
	}

	// Same rule as listEvents: visible when the caller can read the connector
	// that ingested the event or any agent it reached. Reported as 404 rather
	// than 403 so an event id cannot be used to confirm that another tenant's
	// event exists.
	if scope := s.telemetryScopeFor(r); !scope.unrestricted {
		allowed := scope.allowsConnector(evt.Connector)
		if !allowed {
			for _, t := range tasks {
				if scope.allowsAgent(t.AgentName) {
					allowed = true
					break
				}
			}
		}
		if !allowed {
			writeError(w, http.StatusNotFound, "event not found")
			return
		}
	}

	writeJSON(w, http.StatusOK, s.buildActivityEntry(*evt, tasks))
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	if s.eventQueue == nil {
		writeError(w, http.StatusServiceUnavailable, "event queue not configured")
		return
	}

	q := r.URL.Query()

	limit := defaultEventLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = n
	}
	if limit > maxEventLimit {
		limit = maxEventLimit
	}

	offset := 0
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid offset")
			return
		}
		offset = n
	}

	filter := eventqueue.EventFilter{Connector: q.Get("connector"), Agent: q.Get("agent")}

	if v := q.Get("status"); v != "" {
		if !validActivityStatuses[v] {
			writeError(w, http.StatusBadRequest, "invalid status: expected matched, unmatched or error")
			return
		}
		filter.Status = v
	}

	if v := q.Get("outcome"); v != "" {
		switch v {
		case invocations.StatusQueued,
			invocations.StatusRunning,
			invocations.StatusSuccess,
			invocations.StatusFailure,
			invocations.StatusTimeout:
			filter.Outcome = v
		default:
			writeError(w, http.StatusBadRequest, "invalid outcome: expected queued, running, success, failure or timeout")
			return
		}
	}

	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid since: expected RFC 3339 timestamp")
			return
		}
		filter.Since = t
	}

	// An activity entry carries the raw webhook payload, so it is tenant data
	// like anything else. It is visible when the caller can read the connector
	// that ingested the event or any agent the event reached. An explicit
	// ?connector=/?agent= outside that scope yields an empty page rather than
	// a 403, so the filter cannot be used to probe which resources exist.
	scope := s.telemetryScopeFor(r)
	if !scope.unrestricted {
		if (filter.Connector != "" && !scope.allowsConnector(filter.Connector)) ||
			(filter.Agent != "" && !scope.allowsAgent(filter.Agent)) ||
			scope.isEmpty() {
			writeJSON(w, http.StatusOK, eventsEnvelope{Events: []activityEntry{}, Total: 0})
			return
		}
		filter.Connectors = scope.connectors
		filter.Agents = scope.agents
	}

	events, err := s.eventQueue.QueryEvents(r.Context(), filter, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list events")
		return
	}

	total, err := s.eventQueue.CountEvents(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count events")
		return
	}

	entries := make([]activityEntry, 0, len(events))
	if len(events) > 0 {
		ids := make([]string, len(events))
		for i, e := range events {
			ids[i] = e.ID
		}
		tasks, err := s.eventQueue.TasksForEvents(r.Context(), ids)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list event tasks")
			return
		}
		tasksByEvent := make(map[string][]eventqueue.Task, len(events))
		for _, t := range tasks {
			tasksByEvent[t.EventID] = append(tasksByEvent[t.EventID], t)
		}
		for _, e := range events {
			entries = append(entries, s.buildActivityEntry(e, tasksByEvent[e.ID]))
		}
	}

	writeJSON(w, http.StatusOK, eventsEnvelope{Events: entries, Total: total})
}

// buildActivityEntry maps a stored event and its tasks into the frontend
// activity shape. Status is derived from the tasks: no tasks → unmatched,
// any failed task → error, otherwise → matched.
//
// Each match carries the derived run state (runState, taskStatus, startedAt,
// stuck) computed by deriveRunState from the task and, when the invocation
// store has one, its record — the same state the invocation list exposes, so
// the activity page and the routing/runs views cannot disagree. runStatus is
// the raw invocation status, kept for compatibility; records older than the
// store's retention may have been pruned, which leaves runStatus empty and
// makes the derived state unknown when no task backs the match either.
func (s *Server) buildActivityEntry(evt eventqueue.Event, tasks []eventqueue.Task) activityEntry {
	entry := activityEntry{
		ID:        evt.ID,
		Timestamp: evt.ReceivedAt.UTC().Format(time.RFC3339),
		Connector: evt.Connector,
		ChannelID: evt.ChannelID,
		Status:    activityStatusUnmatched,
	}

	if len(tasks) > 0 {
		entry.Status = activityStatusMatched
		matches := make([]activityMatch, 0, len(tasks))
		now := time.Now().UTC()
		for _, t := range tasks {
			if t.Status == "failed" {
				entry.Status = activityStatusError
			}
			m := activityMatch{Trigger: t.TriggerName, Agent: t.AgentName, AgentID: t.AgentName}
			task := t
			var inv *invocations.Invocation
			if s.invocations != nil && t.InvocationID != "" {
				if rec, ok := s.invocations.Get(t.InvocationID); ok {
					m.RunStatus = rec.Status
					m.DurationMs = rec.DurationMs
					m.Error = rec.Error
					inv = &rec
				}
			}
			m.TaskStatus = t.Status
			m.StartedAt = t.StartedAt
			m.RunState, m.Stuck = deriveRunState(&task, inv, now, s.taskStartTimeout)
			matches = append(matches, m)
		}
		entry.Matches = matches
	}

	if payload := activityPayload(evt.Data); payload != nil {
		entry.Payload = payload
	}

	return entry
}

// activityPayload returns the event data as a raw JSON payload, or nil when
// the data is empty or an empty object so the field is omitted from the
// response.
func activityPayload(data json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "{}" || trimmed == "null" {
		return nil
	}
	return data
}
