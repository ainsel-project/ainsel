package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	"github.com/ainsel-project/ainsel/shared/api/llmprovider"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// POST /api/v1/llm/check asks the hub whether a (provider, model) pair can
// actually serve a request. The question can only be answered here: the
// provider→base-URL mapping and the stored API keys are hub-side, while
// plugins hold only the two strings. The answer is a status code the
// ainsel-plugin-github "Check now" button maps to ok / unreachable / unknown:
//
//	200 → the pair served
//	502 → the probe failed (bad key, refused connection, timeout); the
//	      body's "error" carries a sanitized sentence safe to show
//	400/404/405/500 → the question could not be asked (bad request, nothing
//	      to probe with, internal fault) — the plugin reports "unknown"
//
// The hub never echoes upstream statuses: an upstream 401/403 (bad or revoked
// key) becomes 502 here, never a passed-through 401/403, which the plugin
// would misread as "route not permitted" rather than a real failure.

// llmCheckDefaultTimeout caps one upstream probe. The plugin caps the whole
// call at 10 s; answering inside that window means the plugin shows this
// handler's sanitized sentence instead of a bare transport failure.
const llmCheckDefaultTimeout = 8 * time.Second

// llmCheckReasonMaxLen caps the sanitized failure sentence. The plugin caps
// what it renders anyway; capping here keeps the contract honest on both ends.
const llmCheckReasonMaxLen = 300

// llmCheckMaxBodyBytes caps the request body. The body is two short strings;
// the cap matches the MaxBytesReader guard the other plugin-facing handlers
// use, so a hostile caller cannot make the hub buffer an unbounded body.
const llmCheckMaxBodyBytes = 1 << 20

// llmCheckHTTPClient is the probe's HTTP client. A dedicated client keeps the
// probe independent of any future changes to http.DefaultClient's transport;
// the per-request context timeout is what caps a probe.
var llmCheckHTTPClient = &http.Client{}

// llmCheckRequest is the request body: the two strings the plugin holds. A
// fuller agent-like spec was rejected on purpose — resolution is the hub's
// job, and the plugin must not learn or send base URLs and keys.
type llmCheckRequest struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// handleLLMCheck serves POST /api/v1/llm/check.
func (s *Server) handleLLMCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Plugin credentials are gated by RoutePermission (llm:check) in the
	// plugin-credential middleware; every other caller must be an admin.
	// There is no resource-scoped authz object for "llm", so a non-admin
	// session has no answer to be given.
	if _, isPlugin := PluginCallerFrom(r.Context()); !isPlugin {
		if !s.requireAdmin(w, r) {
			return
		}
	}

	r.Body = http.MaxBytesReader(w, r.Body, llmCheckMaxBodyBytes)
	var req llmCheckRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, `request body must be {"provider": "<name>", "model": "<name>"}`)
		return
	}
	req.Provider = strings.TrimSpace(req.Provider)
	req.Model = strings.TrimSpace(req.Model)
	if req.Provider == "" || req.Model == "" {
		writeError(w, http.StatusBadRequest, "provider and model are required")
		return
	}
	p, known := llmprovider.Lookup(req.Provider)
	if !known {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown provider %q", req.Provider))
		return
	}
	if p.CheckBaseURL == "" {
		// A bare pair cannot resolve a provider without a fixed endpoint
		// (custom): its URL exists only on a specific agent's spec, so there
		// is nothing to probe from here.
		writeError(w, http.StatusBadRequest, fmt.Sprintf("provider %q cannot be checked from a provider/model pair", req.Provider))
		return
	}

	candidates, err := s.llmCheckCandidates(r, req.Provider, req.Model)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(candidates) == 0 {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no agent is configured with provider %q", req.Provider))
		return
	}

	// The representative is the first candidate (in preference order) that
	// has a usable stored key: a candidate whose provider block, selector, or
	// secret is unusable is skipped rather than ending the check, so the 404
	// below honestly means *no* visible agent can probe the pair.
	var baseURL, key string
	var usable bool
	for _, a := range candidates {
		target, secretRef, ok := llmCheckAgentTarget(a)
		if !ok {
			continue
		}
		k, err := s.readLLMCheckKey(r.Context(), secretRef)
		if err != nil {
			if errors.Is(err, errLLMCheckKeyMissing) {
				continue
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		baseURL, key, usable = target, k, true
		break
	}
	if !usable {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no agent with provider %q has a usable stored API key", req.Provider))
		return
	}

	reason, probeErr := s.probeLLMCheck(r.Context(), baseURL, req.Provider, req.Model, key)
	if probeErr != nil {
		// Every probe failure — upstream status, refused connection, timeout,
		// invalid stored URL — is the same answer to the plugin: the pair
		// cannot serve. 502 is the one status the plugin maps to
		// "unreachable"; passing an upstream 401/403 through would read as
		// "cannot check" and hide a real failure.
		slog.Warn("llm check probe failed",
			"provider", req.Provider, "model", req.Model, "error", reason)
		writeError(w, http.StatusBadGateway, reason)
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// llmCheckCandidates returns the agents that could answer the check for
// (provider, model), in probe order: exact (provider, model) matches first,
// then provider-only matches, each in name order so the choice is
// deterministic. Only agents visible to the caller are considered (plugin
// callers: their effective groups plus public resources; admins: all). The
// caller probes the first candidate with a usable stored key; the answer then
// means "this pair can serve with that agent's stored key" — the exact-pair
// preference keeps it faithful to what the plugin just reconciled.
func (s *Server) llmCheckCandidates(r *http.Request, provider, model string) ([]*agentv1alpha1.Agent, error) {
	var list agentv1alpha1.AgentList
	if err := s.client.List(r.Context(), &list, client.InNamespace(s.ns)); err != nil {
		return nil, err
	}
	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].Name < list.Items[j].Name
	})

	visible := make(map[string]bool, len(list.Items))
	scoped := s.authzStore != nil && !s.callerIsAdmin(r)
	if scoped {
		// Public agents are visible to every caller, so they are in scope:
		// their stored key can answer the question for anyone.
		groupIDs := s.callerGroupIDs(r)
		names, err := s.authzStore.ListResourcesByGroups(r.Context(), "agent", groupIDs, true)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			visible[n] = true
		}
	}

	var exact, fallback []*agentv1alpha1.Agent
	for i := range list.Items {
		a := &list.Items[i]
		if scoped && !visible[a.Name] {
			continue
		}
		if a.Spec.LLM.Provider != provider {
			continue
		}
		if a.Spec.LLM.Model == model {
			exact = append(exact, a)
		} else {
			fallback = append(fallback, a)
		}
	}
	return append(exact, fallback...), nil
}

// llmCheckAgentTarget derives the probe target from the agent's own spec and
// the llmprovider registry. Every check speaks OpenAI chat completions, so
// the target is the provider's OpenAI-compatible base (CheckBaseURL). For a
// provider whose agents run on that same protocol (openai-completions), the
// block's baseUrl override wins, so the probe hits the endpoint the agent
// actually uses; for native-API providers (OpenAI Responses, Anthropic,
// Gemini) the override points at the native API and cannot be probed this
// way: without an override the vendor's compatible endpoint is used, with
// one the agent is not checkable. ok is false when the provider cannot be
// checked or the agent has no provider block or no key selector — a pair the
// hub cannot tie to a stored key cannot be checked.
func llmCheckAgentTarget(a *agentv1alpha1.Agent) (string, *corev1.SecretKeySelector, bool) {
	p, known := llmprovider.Lookup(a.Spec.LLM.Provider)
	if !known || p.CheckBaseURL == "" {
		return "", nil, false
	}
	cred, present := p.Credential(&a.Spec)
	if !present || cred.APIKeySecretRef == nil {
		return "", nil, false
	}
	if cred.BaseURL == "" {
		return p.CheckBaseURL, cred.APIKeySecretRef, true
	}
	if p.PiAPI == llmprovider.PiAPIOpenAICompletions {
		return cred.BaseURL, cred.APIKeySecretRef, true
	}
	// A native-API provider behind its own endpoint (gateway, proxy): the
	// vendor's compatible endpoint would answer for a different service, and
	// the key must not travel to an endpoint the admin did not configure.
	return "", nil, false
}

// errLLMCheckKeyMissing marks "the selector resolved but there is no usable
// key behind it" — a 404 (nothing to check with), distinct from a store
// failure, which is a 500.
var errLLMCheckKeyMissing = errors.New("llm check: no usable api-key in secret")

// readLLMCheckKey reads the probe key through the agent's own
// APIKeySecretRef — the selector is the source of truth, so the secret name
// is never recomputed.
func (s *Server) readLLMCheckKey(ctx context.Context, ref *corev1.SecretKeySelector) (string, error) {
	var sec corev1.Secret
	if err := s.client.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: s.ns}, &sec); err != nil {
		if apierrors.IsNotFound(err) {
			return "", errLLMCheckKeyMissing
		}
		return "", err
	}
	// The API server converts StringData into Data at rest; a fake client
	// stores the object as written, so honor both here.
	if v := string(sec.Data[ref.Key]); v != "" {
		return v, nil
	}
	if v := sec.StringData[ref.Key]; v != "" {
		return v, nil
	}
	return "", errLLMCheckKeyMissing
}

// probeLLMCheck sends the cheapest possible completion to the provider and
// reports whether the pair can serve. reason is a sanitized sentence safe to
// return to the plugin: a cause class plus status text, never the key, never
// an upstream response body. Base URLs may appear — the console already
// shows agent baseUrl overrides.
func (s *Server) probeLLMCheck(ctx context.Context, baseURL, provider, model, key string) (reason string, err error) {
	endpoint, err := llmCheckEndpoint(baseURL)
	if err != nil {
		return llmCheckSanitize(fmt.Sprintf("provider %q has an invalid base URL %q", provider, baseURL)), err
	}

	limitField := "max_tokens"
	if p, ok := llmprovider.Lookup(provider); ok && p.CheckTokenLimitField != "" {
		limitField = p.CheckTokenLimitField
	}
	payload, err := json.Marshal(map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": "ping"}},
		limitField: 1,
	})
	if err != nil {
		return "check failed", err
	}

	timeout := s.llmCheckTimeout
	if timeout <= 0 {
		timeout = llmCheckDefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return llmCheckSanitize(fmt.Sprintf("provider %q has an invalid base URL %q", provider, baseURL)), err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+key)

	resp, err := llmCheckHTTPClient.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return llmCheckSanitize(fmt.Sprintf("provider %q did not respond within %s", provider, timeout)), err
		}
		// Transport class only. Go's *url.Error embeds the URL, which is
		// allowed in the sentence; it never carries the Authorization header.
		return llmCheckSanitize(fmt.Sprintf("provider %q is unreachable: %v", provider, err)), err
	}
	defer func() {
		// Drain a bounded slice so the connection can be reused; the body
		// itself is never read into the answer.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode/100 == 2 {
		return "", nil
	}
	return llmCheckSanitize(llmCheckUpstreamReason(provider, model, resp.StatusCode)),
		fmt.Errorf("upstream status %d", resp.StatusCode)
}

// llmCheckUpstreamReason phrases an upstream status as a cause sentence.
// The statuses a check caller can act on get their own wording; the rest
// report the status class only.
func llmCheckUpstreamReason(provider, model string, status int) string {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Sprintf("provider %q rejected the stored API key", provider)
	case http.StatusNotFound:
		return fmt.Sprintf("provider %q does not serve model %q", provider, model)
	case http.StatusTooManyRequests:
		return fmt.Sprintf("provider %q is rate limiting the check", provider)
	default:
		return fmt.Sprintf("provider %q returned HTTP %d", provider, status)
	}
}

// llmCheckEndpoint builds the probe URL from a stored base URL. Only http(s)
// is accepted: the base URL comes from admin-authored agent specs, and the
// scheme guard keeps a malformed stored value from aiming the hub at, say, a
// file path or an internal scheme.
func llmCheckEndpoint(baseURL string) (string, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("invalid base URL %q", baseURL)
	}
	return u.String() + "/chat/completions", nil
}

// llmCheckSanitize caps a reason sentence at the contract length, rune-safe
// so a truncation cannot split a multi-byte character.
func llmCheckSanitize(s string) string {
	if len(s) <= llmCheckReasonMaxLen {
		return s
	}
	runes := []rune(s)
	if len(runes) > llmCheckReasonMaxLen {
		runes = runes[:llmCheckReasonMaxLen]
	}
	return string(runes)
}
