package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/httpx"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		httpx.WriteError(w, http.StatusNotFound, "no sign-in session")
	case errors.Is(err, signin.ErrLive):
		httpx.WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, signin.ErrClosed):
		httpx.WriteError(w, http.StatusGone, "this sign-in has ended; start a new one")
	case errors.Is(err, signin.ErrWrongState):
		httpx.WriteError(w, http.StatusConflict, "the sign-in is not ready for this step yet")
	default:
		httpx.WriteError(w, http.StatusInternalServerError, err.Error())
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
		httpx.WriteError(w, http.StatusServiceUnavailable, "ChatGPT sign-in needs the hub database")
		return
	}
	ctx := r.Context()
	if redirect {
		if r.Method != http.MethodPost {
			httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
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
		httpx.WriteJSON(w, http.StatusOK, toSignInStatus(sess))
	case http.MethodDelete:
		if err := s.signIn.Cancel(ctx, name); err != nil {
			writeSignInError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) startSignIn(ctx context.Context, w http.ResponseWriter, name string) {
	var agent agentv1alpha1.Agent
	if err := s.client.Get(ctx, types.NamespacedName{Namespace: s.ns, Name: name}, &agent); err != nil {
		if apierrors.IsNotFound(err) {
			httpx.WriteError(w, http.StatusNotFound, "agent not found")
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !agent.UsesChatGPTSignIn() {
		httpx.WriteError(w, http.StatusBadRequest, "this agent does not use ChatGPT sign-in: choose it as the OpenAI auth method first")
		return
	}
	// The sign-in pod's own token: kept in a Secret the operator mounts into
	// that pod alone, stored here only as a hash.
	podToken, err := randomToken()
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to create the sign-in token")
		return
	}
	sess, err := s.signIn.Start(ctx, name, hashToken(podToken))
	if err != nil {
		writeSignInError(w, err)
		return
	}
	if err := s.putSignInPodSecret(ctx, &agent, podToken); err != nil {
		_ = s.signIn.Cancel(ctx, name)
		httpx.WriteError(w, http.StatusInternalServerError, "failed to store the sign-in token: "+err.Error())
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
		httpx.WriteError(w, http.StatusInternalServerError, "failed to request the sign-in pod: "+err.Error())
		return
	}
	slog.Info("chatgpt sign-in started", "agent", name, "session", sess.ID)
	httpx.WriteJSON(w, http.StatusCreated, toSignInStatus(sess))
}

func (s *Server) submitSignInRedirect(ctx context.Context, w http.ResponseWriter, r *http.Request, name string) {
	var req signInRedirectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	pasted := strings.TrimSpace(req.RedirectURL)
	if !strings.HasPrefix(pasted, signInCallbackPrefix) {
		httpx.WriteError(w, http.StatusBadRequest, "the pasted address must start with "+signInCallbackPrefix+
			": copy the whole address from the page that did not load after signing in")
		return
	}
	sess, err := s.signIn.Latest(ctx, name)
	if err != nil {
		writeSignInError(w, err)
		return
	}
	// At the paste step, check the address the way the pod will. At any
	// other step the store answers why a paste is not possible (409/404).
	if sess.State == signin.StateAwaitingRedirect {
		if msg := callbackProblem(pasted, sess.AuthURL); msg != "" {
			httpx.WriteError(w, http.StatusBadRequest, msg)
			return
		}
	}
	if err := s.signIn.SubmitRedirect(ctx, name, pasted); err != nil {
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
		httpx.WriteError(w, http.StatusServiceUnavailable, "ChatGPT sign-in needs the hub database")
		return
	}
	agent, rest, _ := strings.Cut(path, "/chatgpt-sign-in/")
	id, verb, ok := strings.Cut(rest, "/")
	if agent == "" || id == "" || !ok {
		httpx.WriteError(w, http.StatusNotFound, "not found")
		return
	}
	ctx := r.Context()
	sess, err := s.signIn.Get(ctx, agent, id)
	if err != nil {
		writeSignInError(w, err)
		return
	}
	given := r.Header.Get("X-Sign-In-Token")
	if given == "" || subtle.ConstantTimeCompare([]byte(hashToken(given)), []byte(sess.PodTokenHash)) != 1 {
		httpx.WriteError(w, http.StatusForbidden, "this call needs the sign-in pod's token")
		return
	}
	switch {
	case verb == "auth-url" && r.Method == http.MethodPost:
		var req signInAuthURLRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
		if !strings.HasPrefix(req.URL, signInAuthURLPrefix) {
			httpx.WriteError(w, http.StatusBadRequest, "authorization URL must start with "+signInAuthURLPrefix)
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
		httpx.WriteJSON(w, http.StatusOK, signInRedirectRequest{RedirectURL: url})
	case verb == "result" && r.Method == http.MethodPost:
		var req signInResultRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
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
		httpx.WriteError(w, http.StatusNotFound, fmt.Sprintf("unknown sign-in call %s %s", r.Method, verb))
	}
}

// callbackProblem checks a pasted callback the way the sign-in pod will, so a
// paste that cannot complete the sign-in is refused here with the session
// left open, instead of failing the exchange and the whole session in the pod.
// authURL is the link this session showed; its state must come back.
func callbackProblem(pasted, authURL string) string {
	u, err := url.Parse(pasted)
	if err != nil || u.Scheme != "http" || u.Host != "127.0.0.1:1455" || u.Path != "/auth/callback" {
		return "the pasted address must be " + signInCallbackPrefix + "?…: copy the whole address from the page that did not load"
	}
	q := u.Query()
	if e := q.Get("error"); e != "" {
		return fmt.Sprintf("OpenAI reported %q instead of signing in; cancel and start a new sign-in", e)
	}
	if q.Get("code") == "" {
		return "the pasted address has no code: copy the whole address, including everything after the ?"
	}
	if want := authURLState(authURL); want != "" && q.Get("state") != want {
		return "this address belongs to an earlier sign-in: use the link shown in this dialog and paste the address it leads to"
	}
	if q.Get("client_id") == "" {
		return "the pasted address has no client_id: copy the whole address, including everything after the ?"
	}
	return ""
}

func authURLState(authURL string) string {
	u, err := url.Parse(authURL)
	if err != nil {
		return ""
	}
	return u.Query().Get("state")
}

// signInPodSecretName holds the sign-in pod's token for an agent. The operator
// mounts it into the sign-in Job only; task pods never see it.
func signInPodSecretName(agent string) string {
	return agent + "-chatgpt-sign-in"
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// putSignInPodSecret writes the current session's pod token, owned by the
// Agent so it goes when the agent does. Each sign-in replaces the token.
func (s *Server) putSignInPodSecret(ctx context.Context, agent *agentv1alpha1.Agent, token string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      signInPodSecretName(agent.Name),
			Namespace: s.ns,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: agentv1alpha1.GroupVersion.String(),
				Kind:       "Agent",
				Name:       agent.Name,
				UID:        agent.UID,
			}},
		},
		StringData: map[string]string{"token": token},
	}
	if err := s.client.Create(ctx, secret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		return s.client.Update(ctx, secret)
	}
	return nil
}
