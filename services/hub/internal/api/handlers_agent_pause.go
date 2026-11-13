package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/httpx"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

// Pausing an agent records spec.pause on the Agent CRD. Delivery was withheld
// by the legacy next-task claim endpoint, which is gone: the per-task Job path
// (queuesignal -> operator -> fetch by id) never reads spec.pause, so a pause
// is currently recorded but not enforced. The feature is slated for removal in
// favour of replicas: 0 (issue #249).
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
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) pauseAgent(w http.ResponseWriter, r *http.Request, name string) {
	var req agentPauseRequest
	// An absent body is a valid request: indefinite pause.
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
	}

	now := time.Now()
	var until *metav1.Time
	switch {
	case req.Duration != "" && req.Until != "":
		httpx.WriteError(w, http.StatusBadRequest, "specify either duration or until, not both")
		return
	case req.Duration != "":
		d, err := time.ParseDuration(req.Duration)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid duration: "+err.Error())
			return
		}
		if d <= 0 {
			httpx.WriteError(w, http.StatusBadRequest, "duration must be positive")
			return
		}
		t := metav1.NewTime(now.Add(d))
		until = &t
	case req.Until != "":
		t, err := time.Parse(time.RFC3339, req.Until)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "until must be an RFC3339 timestamp")
			return
		}
		if !t.After(now) {
			httpx.WriteError(w, http.StatusBadRequest, "until must be in the future")
			return
		}
		mt := metav1.NewTime(t)
		until = &mt
	}

	agent, code, msg := s.getAgentForPauseWrite(r.Context(), name)
	if code != 0 {
		httpx.WriteError(w, code, msg)
		return
	}
	agent.Spec.Pause = &agentv1alpha1.AgentPause{PausedUntil: until}
	if agent.Annotations == nil {
		agent.Annotations = map[string]string{}
	}
	agent.Annotations[AgentUpdatedAtAnnotation] = stampAgentUpdatedAt()
	if err := s.client.Update(r.Context(), agent); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to pause agent: "+err.Error())
		return
	}
	slog.Info("agent paused", "agent", name, "paused_until", until)
	s.writeAgentResponse(w, r, agent)
}

func (s *Server) resumeAgent(w http.ResponseWriter, r *http.Request, name string) {
	agent, code, msg := s.getAgentForPauseWrite(r.Context(), name)
	if code != 0 {
		httpx.WriteError(w, code, msg)
		return
	}
	agent.Spec.Pause = nil
	if agent.Annotations == nil {
		agent.Annotations = map[string]string{}
	}
	agent.Annotations[AgentUpdatedAtAnnotation] = stampAgentUpdatedAt()
	if err := s.client.Update(r.Context(), agent); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to resume agent: "+err.Error())
		return
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
	// Pause is a spec field: an externally managed agent's pause belongs to its
	// author, which a reconciler would revert anyway.
	if isManagedCR(agent.Spec.ManagedBy) {
		return nil, http.StatusConflict, fmt.Sprintf("agent is managed by %s", agent.Spec.ManagedBy)
	}
	return &agent, 0, ""
}

// writeAgentResponse renders an agent as the standard simple response.
func (s *Server) writeAgentResponse(w http.ResponseWriter, r *http.Request, agent *agentv1alpha1.Agent) {
	displayName := s.resolveImageDisplayName(r.Context(), agent.Spec.ImageRef.Name)
	httpx.WriteJSON(w, http.StatusOK, toSimpleAgentResponse(*agent, displayName))
}
