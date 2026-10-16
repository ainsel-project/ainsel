package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

// Pausing an agent withholds task delivery: the hub's claim endpoint hands a
// paused agent nothing, so routed tasks pile up as a backlog (visible via the
// queue-depth fields) and drain in id order on resume. Ingestion and routing
// keep running — nothing is lost while paused. In-flight tasks already
// claimed are never interrupted; they complete normally.
//
// Pause state lives on the Agent CRD (spec.pause), so it is declarative,
// kubectl-editable and GitOps-friendly. Expiry is evaluated at read time
// (see agentv1alpha1.Agent.IsPaused), so an expiring pause needs no timers.
//
// Interactive chat is deliberately not gated: a human talking to the agent
// directly is deliberate intent, not channel noise.

// agentPauseRequest is the body of POST /api/v1/agents/{name}/pause. All
// fields optional; an absent or empty body means an indefinite pause.
type agentPauseRequest struct {
	// Duration is a Go duration string ("30m", "8h") measured from now.
	// Mutually exclusive with Until.
	Duration string `json:"duration,omitempty"`
	// Until is an RFC3339 timestamp at which the pause ends. Must be in the
	// future. Mutually exclusive with Duration.
	Until string `json:"until,omitempty"`
}

// handleAgentPause serves the /pause subresource on an agent:
// POST pauses (optionally for a window), DELETE resumes.
func (s *Server) handleAgentPause(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodPost:
		s.pauseAgent(w, r, name)
	case http.MethodDelete:
		s.resumeAgent(w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) pauseAgent(w http.ResponseWriter, r *http.Request, name string) {
	var req agentPauseRequest
	// An absent body is a valid request: indefinite pause.
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
	}

	now := time.Now()
	var until *metav1.Time
	switch {
	case req.Duration != "" && req.Until != "":
		writeError(w, http.StatusBadRequest, "specify either duration or until, not both")
		return
	case req.Duration != "":
		d, err := time.ParseDuration(req.Duration)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid duration: "+err.Error())
			return
		}
		if d <= 0 {
			writeError(w, http.StatusBadRequest, "duration must be positive")
			return
		}
		t := metav1.NewTime(now.Add(d))
		until = &t
	case req.Until != "":
		t, err := time.Parse(time.RFC3339, req.Until)
		if err != nil {
			writeError(w, http.StatusBadRequest, "until must be an RFC3339 timestamp")
			return
		}
		if !t.After(now) {
			writeError(w, http.StatusBadRequest, "until must be in the future")
			return
		}
		mt := metav1.NewTime(t)
		until = &mt
	}

	agent, code, msg := s.getAgentForPauseWrite(r.Context(), name)
	if code != 0 {
		writeError(w, code, msg)
		return
	}
	agent.Spec.Pause = &agentv1alpha1.AgentPause{PausedUntil: until}
	if agent.Annotations == nil {
		agent.Annotations = map[string]string{}
	}
	agent.Annotations[AgentUpdatedAtAnnotation] = stampAgentUpdatedAt()
	if err := s.client.Update(r.Context(), agent); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to pause agent: "+err.Error())
		return
	}
	slog.Info("agent paused", "agent", name, "paused_until", until)
	s.writeAgentResponse(w, r, agent)
}

func (s *Server) resumeAgent(w http.ResponseWriter, r *http.Request, name string) {
	agent, code, msg := s.getAgentForPauseWrite(r.Context(), name)
	if code != 0 {
		writeError(w, code, msg)
		return
	}
	agent.Spec.Pause = nil
	if agent.Annotations == nil {
		agent.Annotations = map[string]string{}
	}
	agent.Annotations[AgentUpdatedAtAnnotation] = stampAgentUpdatedAt()
	if err := s.client.Update(r.Context(), agent); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resume agent: "+err.Error())
		return
	}
	// Nudge the queue so a waiting runtime picks the backlog up immediately
	// instead of after its long-poll timeout. Best effort: a failed notify
	// only delays the first claim, it cannot lose a queued task.
	if s.eventQueue != nil {
		if err := s.eventQueue.NotifyAgent(r.Context(), name); err != nil {
			slog.Warn("resume notify failed", "agent", name, "error", err)
		}
	}
	slog.Info("agent resumed", "agent", name)
	s.writeAgentResponse(w, r, agent)
}

// getAgentForPauseWrite fetches the agent for a pause/resume write, returning
// an HTTP status and message on failure (0, "" on success).
func (s *Server) getAgentForPauseWrite(ctx context.Context, name string) (*agentv1alpha1.Agent, int, string) {
	var agent agentv1alpha1.Agent
	if err := s.client.Get(ctx, types.NamespacedName{Name: name, Namespace: s.ns}, &agent); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, http.StatusNotFound, "agent not found"
		}
		return nil, http.StatusInternalServerError, "failed to get agent: " + err.Error()
	}
	return &agent, 0, ""
}

// writeAgentResponse renders an agent as the standard simple response.
func (s *Server) writeAgentResponse(w http.ResponseWriter, r *http.Request, agent *agentv1alpha1.Agent) {
	displayName := s.resolveImageDisplayName(r.Context(), agent.Spec.ImageRef.Name)
	writeJSON(w, http.StatusOK, toSimpleAgentResponse(*agent, displayName))
}

// agentPauseState reports whether the named agent is currently paused and,
// when the pause expires on its own, when it ends (zero time for an
// indefinite pause).
//
// A missing agent or a failed lookup reads as not paused: the queue is the
// authority on what may be claimed, and a transient API-server hiccup must
// not stop a running agent from working.
func (s *Server) agentPauseState(ctx context.Context, name string) (paused bool, resumesAt time.Time) {
	if s.client == nil {
		return false, time.Time{}
	}
	var agent agentv1alpha1.Agent
	if err := s.client.Get(ctx, types.NamespacedName{Name: name, Namespace: s.ns}, &agent); err != nil {
		if !apierrors.IsNotFound(err) {
			slog.Warn("pause check: agent lookup failed", "agent", name, "error", err)
		}
		return false, time.Time{}
	}
	now := time.Now()
	if !agent.IsPaused(now) {
		return false, time.Time{}
	}
	if agent.Spec.Pause.PausedUntil != nil {
		return true, agent.Spec.Pause.PausedUntil.Time
	}
	return true, time.Time{}
}
