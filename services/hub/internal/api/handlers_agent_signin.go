package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"github.com/ainsel-project/ainsel/services/hub/internal/signin"
	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

// ChatGPT sign-in relay (issue #89). The console starts a session; the hub
// records it and names it in an annotation on the Agent; the operator runs a
// sign-in pod on the agent's credential volume; the pod posts OpenAI's
// authorization URL here and polls for the redirect URL the user pastes after
// signing in. The pod exchanges the code itself and writes the credential to
// the volume — tokens never reach the hub.

// signInSessionAnnotation names the sign-in session the operator should run a
// sign-in pod for. The operator remembers the last one it handled.
const signInSessionAnnotation = "ainsel.dev/chatgpt-sign-in-session"

// signInCallbackPrefix is the only redirect the official flow uses (pi-ai's
// REDIRECT_URI). Anything else pasted is a mistake, caught here with a clear
// message instead of a failed exchange in the pod.
const signInCallbackPrefix = "http://127.0.0.1:1455/auth/callback"

// signInAuthURLPrefix is where OpenAI's authorization page lives. The agent
// pods share one internal token, so the hub refuses to show the user a link
// to anywhere else.
const signInAuthURLPrefix = "https://auth.openai.com/"

// maxSignInErrorLen bounds the failure text a pod reports.
const maxSignInErrorLen = 500

type signInStatusResponse struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	AuthURL   string `json:"authUrl,omitempty"`
	Error     string `json:"error,omitempty"`
	ExpiresAt string `json:"expiresAt"`
}

type signInRedirectRequest struct {
	RedirectURL string `json:"redirectUrl"`
}

type signInAuthURLRequest struct {
	URL string `json:"url"`
}

type signInResultRequest struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func toSignInStatus(s signin.Session) signInStatusResponse {
	return signInStatusResponse{
		ID:        s.ID,
		State:     string(s.State),
		AuthURL:   s.AuthURL,
		Error:     s.Error,
		ExpiresAt: s.ExpiresAt.UTC().Format(time.RFC3339),
	}
}

// writeSignInError maps store errors to HTTP statuses.
func writeSignInError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, signin.ErrNotFound):
		writeError(w, http.StatusNotFound, "no sign-in session")
	case errors.Is(err, signin.ErrLive):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, signin.ErrClosed):
		writeError(w, http.StatusGone, "this sign-in has ended; start a new one")
	case errors.Is(err, signin.ErrWrongState):
		writeError(w, http.StatusConflict, "the sign-in is not ready for this step yet")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// handleAgentSignIn serves the console side:
//
//	POST   /api/v1/agents/{name}/chatgpt-sign-in           start
//	GET    /api/v1/agents/{name}/chatgpt-sign-in           latest session
//	DELETE /api/v1/agents/{name}/chatgpt-sign-in           cancel
//	POST   /api/v1/agents/{name}/chatgpt-sign-in/redirect  paste the redirect URL
func (s *Server) handleAgentSignIn(w http.ResponseWriter, r *http.Request, name string, redirect bool) {
	if s.signIn == nil {
		writeError(w, http.StatusServiceUnavailable, "ChatGPT sign-in needs the hub database")
		return
	}
	ctx := r.Context()
	if redirect {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.submitSignInRedirect(ctx, w, r, name)
		return
	}
	switch r.Method {
	case http.MethodPost:
		s.startSignIn(ctx, w, name)
	case http.MethodGet:
		sess, err := s.signIn.Latest(ctx, name)
		if err != nil {
			writeSignInError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toSignInStatus(sess))
	case http.MethodDelete:
		if err := s.signIn.Cancel(ctx, name); err != nil {
			writeSignInError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) startSignIn(ctx context.Context, w http.ResponseWriter, name string) {
	var agent agentv1alpha1.Agent
	if err := s.client.Get(ctx, types.NamespacedName{Namespace: s.ns, Name: name}, &agent); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "agent not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !agent.UsesChatGPTSignIn() {
		writeError(w, http.StatusBadRequest, "this agent does not use ChatGPT sign-in: choose it as the OpenAI auth method first")
		return
	}
	sess, err := s.signIn.Start(ctx, name)
	if err != nil {
		writeSignInError(w, err)
		return
	}
	// Only the annotation changes: the operator reacts to it, and no spec
	// field the user owns is touched. Update after Get like every other
	// agent write here, so a concurrent edit fails loudly instead of being
	// overwritten.
	if agent.Annotations == nil {
		agent.Annotations = map[string]string{}
	}
	agent.Annotations[signInSessionAnnotation] = sess.ID
	if err := s.client.Update(ctx, &agent); err != nil {
		_ = s.signIn.Cancel(ctx, name)
		writeError(w, http.StatusInternalServerError, "failed to request the sign-in pod: "+err.Error())
		return
	}
	slog.Info("chatgpt sign-in started", "agent", name, "session", sess.ID)
	writeJSON(w, http.StatusCreated, toSignInStatus(sess))
}

func (s *Server) submitSignInRedirect(ctx context.Context, w http.ResponseWriter, r *http.Request, name string) {
	var req signInRedirectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	url := strings.TrimSpace(req.RedirectURL)
	if !strings.HasPrefix(url, signInCallbackPrefix) {
		writeError(w, http.StatusBadRequest, "the pasted address must start with "+signInCallbackPrefix+
			": copy the whole address from the page that did not load after signing in")
		return
	}
	if err := s.signIn.SubmitRedirect(ctx, name, url); err != nil {
		writeSignInError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// handleInternalSignIn serves the sign-in pod, behind X-Internal-Token
// (checked by handleInternalAgent):
//
//	POST /api/internal/agents/{name}/chatgpt-sign-in/{session}/auth-url
//	GET  /api/internal/agents/{name}/chatgpt-sign-in/{session}/redirect
//	POST /api/internal/agents/{name}/chatgpt-sign-in/{session}/result
func (s *Server) handleInternalSignIn(w http.ResponseWriter, r *http.Request, path string) {
	if s.signIn == nil {
		writeError(w, http.StatusServiceUnavailable, "ChatGPT sign-in needs the hub database")
		return
	}
	agent, rest, _ := strings.Cut(path, "/chatgpt-sign-in/")
	id, verb, ok := strings.Cut(rest, "/")
	if agent == "" || id == "" || !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	ctx := r.Context()
	switch {
	case verb == "auth-url" && r.Method == http.MethodPost:
		var req signInAuthURLRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
		if !strings.HasPrefix(req.URL, signInAuthURLPrefix) {
			writeError(w, http.StatusBadRequest, "authorization URL must start with "+signInAuthURLPrefix)
			return
		}
		if err := s.signIn.SetAuthURL(ctx, agent, id, req.URL); err != nil {
			writeSignInError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case verb == "redirect" && r.Method == http.MethodGet:
		url, err := s.signIn.TakeRedirect(ctx, agent, id)
		if err != nil {
			writeSignInError(w, err)
			return
		}
		if url == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, http.StatusOK, signInRedirectRequest{RedirectURL: url})
	case verb == "result" && r.Method == http.MethodPost:
		var req signInResultRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
		msg := req.Error
		if len(msg) > maxSignInErrorLen {
			msg = msg[:maxSignInErrorLen]
		}
		if err := s.signIn.Finish(ctx, agent, id, req.OK, msg); err != nil {
			writeSignInError(w, err)
			return
		}
		slog.Info("chatgpt sign-in finished", "agent", agent, "session", id, "ok", req.OK)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusNotFound, fmt.Sprintf("unknown sign-in call %s %s", r.Method, verb))
	}
}
