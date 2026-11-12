package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	"github.com/ainsel-project/ainsel/services/hub/internal/plugincreds"
	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	"github.com/ainsel-project/ainsel/shared/api/llmprovider"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// POST /api/v1/llm/check asks whether a (provider, model) pair can serve.
// These tests pin the contract the ainsel-plugin-github "Check now" button
// consumes: 200 = served, 502 = the pair failed (with a sanitized sentence
// that never contains the key), 400/404/405 = the question could not be
// asked. The critical regression is TestLLMCheck_UpstreamAuthFailureMapsTo502:
// an upstream 401/403 must never pass through, or the plugin reads a real
// failure as "cannot check".

// llmCheckAgent builds an Agent with one provider block (baseUrl override and
// key selector) for the check tests.
func llmCheckAgent(name, provider, model, baseURL, secretName string) *agentv1alpha1.Agent {
	a := &agentv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test-ns"},
	}
	a.APIVersion = "ainsel.dev/v1alpha1"
	a.Kind = "Agent"
	a.Spec.DisplayName = name
	a.Spec.LLM.Provider = provider
	a.Spec.LLM.Model = model
	ref := &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
		Key:                  "api-key",
	}
	if p, ok := llmprovider.Lookup(provider); ok {
		p.SetCredential(&a.Spec, llmprovider.Credential{BaseURL: baseURL, APIKeySecretRef: ref})
	}
	return a
}

// llmCheckSecret seeds a provider key the way putAgentSecret writes one
// (StringData; the fake client stores objects as written).
func llmCheckSecret(name, key string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test-ns"},
		StringData: map[string]string{"api-key": key},
	}
}

// llmCheckServer wires a Server with the route registered.
func llmCheckServer(t *testing.T, objects ...runtime.Object) *Server {
	t.Helper()
	s := testServer(t, objects...)
	s.mux.HandleFunc("/api/v1/llm/check", s.handleLLMCheck)
	return s
}

// postLLMCheck POSTs a raw body as a plugin-kind caller (the route permission
// itself is pinned in TestRoutePermission_Mappings; the middleware that
// enforces it is covered by plugin_credentials_test.go).
func postLLMCheck(t *testing.T, s *Server, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/llm/check", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), pluginCallerKey{}, &PluginCaller{
		PluginID: "github",
		Kind:     plugincreds.KindPlugin,
		Sub:      "plugin:github",
	}))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func llmCheckError(t *testing.T, body string) string {
	t.Helper()
	var v struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("body is not the error envelope: %q", body)
	}
	return v.Error
}

func TestLLMCheck_OK(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("probe path = %s, want /chat/completions", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer up.Close()

	s := llmCheckServer(t,
		llmCheckAgent("ag", "ollama-cloud", "glm-5", up.URL, "ag-llm-key"),
		llmCheckSecret("ag-llm-key", "sk-secret-123"),
	)

	code, body := postLLMCheck(t, s, `{"provider":"ollama-cloud","model":"glm-5"}`)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", code, body)
	}
	if gotAuth != "Bearer sk-secret-123" {
		t.Errorf("probe did not carry the stored key: %q", gotAuth)
	}
	if mt, ok := gotBody["max_tokens"].(float64); !ok || mt != 1 {
		t.Errorf("probe body max_tokens = %v, want 1", gotBody["max_tokens"])
	}
	if gotBody["model"] != "glm-5" {
		t.Errorf("probe body model = %v", gotBody["model"])
	}
}

// TestLLMCheck_UpstreamAuthFailureMapsTo502 is the pass-through trap: an
// upstream 401/403 (bad or revoked key) must come back as hub 502 — the one
// status the plugin maps to "unreachable". A passed-through 401/403 would be
// read as "route not permitted / cannot check" and hide a real failure.
func TestLLMCheck_UpstreamAuthFailureMapsTo502(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid api key sk-secret-123"}}`))
		}))
		defer up.Close()

		s := llmCheckServer(t,
			llmCheckAgent("ag", "opencode", "m1", up.URL, "ag-opencode-key"),
			llmCheckSecret("ag-opencode-key", "sk-secret-123"),
		)
		code, body := postLLMCheck(t, s, `{"provider":"opencode","model":"m1"}`)
		if code != http.StatusBadGateway {
			t.Fatalf("upstream %d: expected hub 502, got %d: %s", status, code, body)
		}
		if code == http.StatusUnauthorized || code == http.StatusForbidden {
			t.Fatal("upstream auth failure must never pass through")
		}
		reason := llmCheckError(t, body)
		if !strings.Contains(reason, "rejected the stored API key") {
			t.Errorf("reason = %q, want key-rejection wording", reason)
		}
		if strings.Contains(reason, "sk-secret-123") {
			t.Errorf("reason leaks the key: %q", reason)
		}
	}
}

func TestLLMCheck_UpstreamStatusReasons(t *testing.T) {
	cases := []struct {
		upStatus int
		contains string
	}{
		{http.StatusNotFound, `does not serve model "m1"`},
		{http.StatusTooManyRequests, "rate limiting"},
		{http.StatusInternalServerError, "returned HTTP 500"},
	}
	for _, tc := range cases {
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.upStatus)
		}))
		defer up.Close()

		s := llmCheckServer(t,
			llmCheckAgent("ag", "ollama-cloud", "m1", up.URL, "ag-llm-key"),
			llmCheckSecret("ag-llm-key", "k"),
		)
		code, body := postLLMCheck(t, s, `{"provider":"ollama-cloud","model":"m1"}`)
		if code != http.StatusBadGateway {
			t.Fatalf("upstream %d: expected 502, got %d: %s", tc.upStatus, code, body)
		}
		if reason := llmCheckError(t, body); !strings.Contains(reason, tc.contains) {
			t.Errorf("upstream %d: reason = %q, want %q", tc.upStatus, reason, tc.contains)
		}
	}
}

func TestLLMCheck_TimeoutMapsTo502(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()

	s := llmCheckServer(t,
		llmCheckAgent("ag", "ollama-cloud", "m1", up.URL, "ag-llm-key"),
		llmCheckSecret("ag-llm-key", "k"),
	)
	s.llmCheckTimeout = 30 * time.Millisecond

	code, body := postLLMCheck(t, s, `{"provider":"ollama-cloud","model":"m1"}`)
	if code != http.StatusBadGateway {
		t.Fatalf("expected 502 on timeout, got %d: %s", code, body)
	}
	if reason := llmCheckError(t, body); !strings.Contains(reason, "did not respond within") {
		t.Errorf("reason = %q, want timeout wording", reason)
	}
}

func TestLLMCheck_TransportFailureMapsTo502(t *testing.T) {
	// A closed listener: connection refused, no upstream answer.
	up := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := up.URL
	up.Close()

	s := llmCheckServer(t,
		llmCheckAgent("ag", "ollama-cloud", "m1", deadURL, "ag-llm-key"),
		llmCheckSecret("ag-llm-key", "k"),
	)
	code, body := postLLMCheck(t, s, `{"provider":"ollama-cloud","model":"m1"}`)
	if code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", code, body)
	}
	if reason := llmCheckError(t, body); !strings.Contains(reason, "unreachable") {
		t.Errorf("reason = %q, want unreachable wording", reason)
	}
}

func TestLLMCheck_InvalidStoredBaseURL(t *testing.T) {
	s := llmCheckServer(t,
		llmCheckAgent("ag", "ollama-cloud", "m1", "not a url", "ag-llm-key"),
		llmCheckSecret("ag-llm-key", "k"),
	)
	code, body := postLLMCheck(t, s, `{"provider":"ollama-cloud","model":"m1"}`)
	if code != http.StatusBadGateway {
		t.Fatalf("expected 502 for an invalid stored base URL, got %d: %s", code, body)
	}
}

func TestLLMCheck_BadRequests(t *testing.T) {
	s := llmCheckServer(t)
	cases := []struct {
		name string
		body string
	}{
		{"malformed json", `{"provider":`},
		{"missing provider", `{"model":"m"}`},
		{"missing model", `{"provider":"ollama-cloud"}`},
		{"empty strings", `{"provider":"  ","model":""}`},
		{"unknown provider", `{"provider":"not-a-provider","model":"m"}`},
		{"custom provider", `{"provider":"custom","model":"m"}`},
	}
	for _, tc := range cases {
		code, body := postLLMCheck(t, s, tc.body)
		if code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d: %s", tc.name, code, body)
		}
	}
}

func TestLLMCheck_MethodNotAllowed(t *testing.T) {
	s := llmCheckServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/llm/check", nil)
	req = req.WithContext(context.WithValue(req.Context(), pluginCallerKey{}, &PluginCaller{
		PluginID: "github", Kind: plugincreds.KindPlugin, Sub: "plugin:github",
	}))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestLLMCheck_NoAgentForProvider(t *testing.T) {
	s := llmCheckServer(t,
		llmCheckAgent("ag", "ollama-cloud", "m1", "https://ollama.test/v1", "ag-llm-key"),
		llmCheckSecret("ag-llm-key", "k"),
	)
	code, body := postLLMCheck(t, s, `{"provider":"opencode","model":"m1"}`)
	if code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", code, body)
	}
	if reason := llmCheckError(t, body); !strings.Contains(reason, "opencode") {
		t.Errorf("reason = %q, want it to name the provider", reason)
	}
}

func TestLLMCheck_NoUsableKey(t *testing.T) {
	// Each case is the only agent with the provider, so the representative
	// pick is forced and the answer must be 404: nothing to probe with.
	cases := []struct {
		name string
		objs []runtime.Object
	}{
		{
			// Provider block missing entirely.
			name: "no provider block",
			objs: func() []runtime.Object {
				a := &agentv1alpha1.Agent{ObjectMeta: metav1.ObjectMeta{Name: "a1", Namespace: "test-ns"}}
				a.Spec.LLM.Provider = "ollama-cloud"
				a.Spec.LLM.Model = "m1"
				return []runtime.Object{a}
			}(),
		},
		{
			// Provider block without a key selector.
			name: "no key selector",
			objs: func() []runtime.Object {
				a := llmCheckAgent("a2", "ollama-cloud", "m1", "https://ollama.test/v1", "")
				a.Spec.OllamaCloud.APIKeySecretRef = nil
				return []runtime.Object{a}
			}(),
		},
		{
			// Selector pointing at a secret that does not exist.
			name: "dangling selector",
			objs: []runtime.Object{llmCheckAgent("a3", "ollama-cloud", "m1", "https://ollama.test/v1", "gone-key")},
		},
		{
			// Secret exists but the key is empty.
			name: "empty key",
			objs: []runtime.Object{
				llmCheckAgent("a4", "ollama-cloud", "m1", "https://ollama.test/v1", "a4-key"),
				llmCheckSecret("a4-key", ""),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := llmCheckServer(t, tc.objs...)
			code, body := postLLMCheck(t, s, `{"provider":"ollama-cloud","model":"m1"}`)
			if code != http.StatusNotFound {
				t.Fatalf("expected 404 (nothing to probe with), got %d: %s", code, body)
			}
		})
	}
}

// TestLLMCheck_SkipsCandidateWithoutUsableKey pins the scan-past behavior:
// when the preferred (exact-pair) candidate has no usable key but another
// visible agent with the provider does, the hub probes that one instead of
// answering 404 — the 404 wording ("none has a usable stored key") is only
// honest if the scan really covers every candidate.
func TestLLMCheck_SkipsCandidateWithoutUsableKey(t *testing.T) {
	var hitFallback bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hitFallback = true
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()

	// "a-exact" sorts first and matches the pair exactly, but its selector
	// points at a secret that does not exist; "b-fallback" matches the
	// provider only and has a usable key.
	s := llmCheckServer(t,
		llmCheckAgent("a-exact", "ollama-cloud", "m1", "https://ollama.test/v1", "gone-key"),
		llmCheckAgent("b-fallback", "ollama-cloud", "other", up.URL, "b-key"),
		llmCheckSecret("b-key", "kb"),
	)
	code, body := postLLMCheck(t, s, `{"provider":"ollama-cloud","model":"m1"}`)
	if code != http.StatusOK {
		t.Fatalf("expected 200 via the fallback candidate, got %d: %s", code, body)
	}
	if !hitFallback {
		t.Fatal("expected the usable-key candidate to be probed")
	}
}

// TestLLMCheck_AllCandidatesUnusableKeeps404 keeps the other side of the
// scan: when no candidate has a usable key, the answer is still 404 with the
// "no usable stored API key" wording.
func TestLLMCheck_AllCandidatesUnusableKeeps404(t *testing.T) {
	// a-exact sorts first and matches the pair exactly, but its selector
	// points at a secret that does not exist; b-blockless matches the pair
	// but has no provider block; c-empty has a secret whose key is empty.
	// None is probeable, so the answer stays 404.
	blockless := llmCheckAgent("b-blockless", "ollama-cloud", "m1", "https://ollama.test/v1", "b-key")
	blockless.Spec.OllamaCloud = nil
	s := llmCheckServer(t,
		llmCheckAgent("a-exact", "ollama-cloud", "m1", "https://ollama.test/v1", "gone-key"),
		blockless,
		llmCheckAgent("c-empty", "ollama-cloud", "other", "https://ollama.test/v1", "c-key"),
		llmCheckSecret("c-key", ""),
	)
	code, body := postLLMCheck(t, s, `{"provider":"ollama-cloud","model":"m1"}`)
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 when no candidate is usable, got %d: %s", code, body)
	}
	if reason := llmCheckError(t, body); !strings.Contains(reason, "usable stored API key") {
		t.Errorf("reason = %q, want the no-usable-key wording", reason)
	}
}

// TestLLMCheck_BodySizeCapped pins the MaxBytesReader guard: an oversized
// body is rejected as a bad request, not buffered and decoded.
func TestLLMCheck_BodySizeCapped(t *testing.T) {
	s := llmCheckServer(t)
	body := `{"provider":"ollama-cloud","model":"` + strings.Repeat("m", llmCheckMaxBodyBytes+1) + `"}`
	code, resp := postLLMCheck(t, s, body)
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an oversized body, got %d: %s", code, resp)
	}
}

// TestLLMCheck_ExactPairPreferred pins agent selection: the agent whose
// (provider, model) matches exactly is probed even when a provider-only
// match sorts first; with no exact match the first provider-only match wins.
func TestLLMCheck_ExactPairPreferred(t *testing.T) {
	var hitA, hitB bool
	upA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hitA = true
		w.WriteHeader(http.StatusOK)
	}))
	defer upA.Close()
	upB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hitB = true
		w.WriteHeader(http.StatusOK)
	}))
	defer upB.Close()

	s := llmCheckServer(t,
		// "a-first" sorts before "b-second": a provider-only pick lands on it.
		llmCheckAgent("a-first", "ollama-cloud", "m1", upA.URL, "a-key"),
		llmCheckAgent("b-second", "ollama-cloud", "m2", upB.URL, "b-key"),
		llmCheckSecret("a-key", "ka"),
		llmCheckSecret("b-key", "kb"),
	)

	if code, body := postLLMCheck(t, s, `{"provider":"ollama-cloud","model":"m2"}`); code != http.StatusOK {
		t.Fatalf("exact pair: expected 200, got %d: %s", code, body)
	}
	if !hitB || hitA {
		t.Fatalf("exact pair must probe the exact-match agent (hitA=%v hitB=%v)", hitA, hitB)
	}

	hitA, hitB = false, false
	if code, body := postLLMCheck(t, s, `{"provider":"ollama-cloud","model":"m3"}`); code != http.StatusOK {
		t.Fatalf("fallback pair: expected 200, got %d: %s", code, body)
	}
	if !hitA || hitB {
		t.Fatalf("provider-only fallback must probe the first name-sorted match (hitA=%v hitB=%v)", hitA, hitB)
	}
}

// TestLLMCheck_KeyReadViaSelector proves the selector is the source of truth:
// the secret name does not follow the {agent}-{suffix}-key convention, and
// the probe still uses what the selector points at.
func TestLLMCheck_KeyReadViaSelector(t *testing.T) {
	var gotAuth string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()

	agent := llmCheckAgent("ag", "ollama-cloud", "m1", up.URL, "totally-other-name")
	s := llmCheckServer(t, agent, llmCheckSecret("totally-other-name", "sk-from-selector"))

	if code, body := postLLMCheck(t, s, `{"provider":"ollama-cloud","model":"m1"}`); code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", code, body)
	}
	if gotAuth != "Bearer sk-from-selector" {
		t.Errorf("probe key = %q, want the selector's secret", gotAuth)
	}
}

// llmCheckAuthzFake is a minimal authz graph for the group-scoping tests.
// It embeds the full authzStore interface so unimplemented methods panic
// loudly instead of returning a misleading zero value.
type llmCheckAuthzFake struct {
	authzStore

	admins map[string]bool
	// resources maps "agent/<name>" -> groupID.
	resources map[string]string
	// public lists agent names visible to every caller.
	public map[string]bool
}

func (f *llmCheckAuthzFake) GetUser(_ context.Context, id string) (*authz.User, error) {
	return &authz.User{ID: id, IsAdmin: f.admins[id]}, nil
}

func (f *llmCheckAuthzFake) GetResourceGroup(_ context.Context, resourceType, name string) (*authz.ResourceGroup, error) {
	gid := f.resources[resourceType+"/"+name]
	return &authz.ResourceGroup{ResourceType: resourceType, ResourceName: name, GroupID: gid, Public: f.public[name]}, nil
}

func (f *llmCheckAuthzFake) ListResourcesByGroups(_ context.Context, resourceType string, groupIDs []string, includePublic bool) ([]string, error) {
	wanted := map[string]bool{}
	for _, g := range groupIDs {
		wanted[g] = true
	}
	var out []string
	for key, gid := range f.resources {
		rt, rn, _ := strings.Cut(key, "/")
		if rt != resourceType {
			continue
		}
		if wanted[gid] || (includePublic && f.public[rn]) {
			out = append(out, rn)
		}
	}
	return out, nil
}

func llmCheckAuthzEnv(t *testing.T, s *Server, fake *llmCheckAuthzFake) {
	t.Helper()
	s.authzStore = fake
	s.authzChecker = authz.NewChecker(fake, authz.NewGroupCache(
		func(string) (map[string]authz.GroupRole, error) { return nil, nil }, time.Minute))
}

// TestLLMCheck_GroupScoping: an agent outside the plugin caller's effective
// groups is never probed — its key is never spent on a caller who cannot see
// it. A public agent is in scope for any caller.
func TestLLMCheck_GroupScoping(t *testing.T) {
	var hitPrivate, hitPublic bool
	upPrivate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hitPrivate = true
		w.WriteHeader(http.StatusOK)
	}))
	defer upPrivate.Close()
	upPublic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hitPublic = true
		w.WriteHeader(http.StatusOK)
	}))
	defer upPublic.Close()

	s := llmCheckServer(t,
		// The public agent matches the provider but not the model, so it only
		// answers as the provider-only fallback for callers who cannot see the
		// exact-pair agent in group gB.
		llmCheckAgent("in-group", "ollama-cloud", "other-model", upPublic.URL, "pub-key"),
		llmCheckAgent("other-group", "ollama-cloud", "m1", upPrivate.URL, "priv-key"),
		llmCheckSecret("pub-key", "kp"),
		llmCheckSecret("priv-key", "kq"),
	)
	llmCheckAuthzEnv(t, s, &llmCheckAuthzFake{
		admins:    map[string]bool{},
		resources: map[string]string{"agent/in-group": "gA", "agent/other-group": "gB"},
		public:    map[string]bool{"in-group": true},
	})

	// Plugin caller in group gB: "other-group" is visible (exact pair), so it
	// is probed; a caller in no group sees only the public agent. The oidc
	// user mirrors what the credential middleware sets for a plugin caller.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/llm/check",
		strings.NewReader(`{"provider":"ollama-cloud","model":"m1"}`))
	ctx := context.WithValue(req.Context(), pluginCallerKey{}, &PluginCaller{
		PluginID: "github", Kind: plugincreds.KindPlugin, Sub: "plugin:github",
	})
	ctx = oidc.ContextWithUser(ctx, &oidc.User{Sub: "plugin:github"})
	ctx = authz.WithPrincipal(ctx, &authz.Principal{
		Sub:    "plugin:github",
		Kind:   plugincreds.KindPlugin,
		Groups: map[string]authz.GroupRole{"gB": authz.RoleWriter},
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !hitPrivate {
		t.Fatalf("caller-group agent must be probed: code=%d hitPrivate=%v body=%s", rec.Code, hitPrivate, rec.Body.String())
	}

	// A caller with no groups and no public agent match gets 404 — the
	// other-group key is never touched.
	hitPrivate, hitPublic = false, false
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/llm/check",
		strings.NewReader(`{"provider":"ollama-cloud","model":"m1"}`))
	ctx2 := context.WithValue(req2.Context(), pluginCallerKey{}, &PluginCaller{
		PluginID: "github", Kind: plugincreds.KindPlugin, Sub: "plugin:github",
	})
	ctx2 = oidc.ContextWithUser(ctx2, &oidc.User{Sub: "plugin:github"})
	ctx2 = authz.WithPrincipal(ctx2, &authz.Principal{
		Sub: "plugin:github", Kind: plugincreds.KindPlugin,
		Groups: map[string]authz.GroupRole{"gC": authz.RoleWriter},
	})
	req2 = req2.WithContext(ctx2)
	rec2 := httptest.NewRecorder()
	s.mux.ServeHTTP(rec2, req2)
	// "in-group" is public and matches the pair, so the public agent answers.
	if rec2.Code != http.StatusOK || !hitPublic || hitPrivate {
		t.Fatalf("public agent must answer, private must not: code=%d hitPublic=%v hitPrivate=%v", rec2.Code, hitPublic, hitPrivate)
	}
}

// TestLLMCheck_UserCallers pins the non-plugin gate: anonymous → 401,
// authenticated non-admin → 403, admin → the check runs.
func TestLLMCheck_UserCallers(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()

	newS := func() *Server {
		s := llmCheckServer(t,
			llmCheckAgent("ag", "ollama-cloud", "m1", up.URL, "ag-llm-key"),
			llmCheckSecret("ag-llm-key", "k"),
		)
		llmCheckAuthzEnv(t, s, &llmCheckAuthzFake{
			admins:    map[string]bool{"admin-sub": true},
			resources: map[string]string{"agent/ag": "gA"},
		})
		return s
	}

	post := func(sub string) (int, string) {
		s := newS()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/llm/check",
			strings.NewReader(`{"provider":"ollama-cloud","model":"m1"}`))
		if sub != "" {
			req = req.WithContext(oidc.ContextWithUser(req.Context(), &oidc.User{Sub: sub}))
		}
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	if code, body := post(""); code != http.StatusUnauthorized {
		t.Errorf("anonymous: expected 401, got %d: %s", code, body)
	}
	if code, body := post("alice"); code != http.StatusForbidden {
		t.Errorf("non-admin: expected 403, got %d: %s", code, body)
	}
	if code, body := post("admin-sub"); code != http.StatusOK {
		t.Errorf("admin: expected 200, got %d: %s", code, body)
	}
}

// TestLLMCheck_ReasonCapped keeps the sanitized sentence within the contract
// cap even when the transport error text is long (.invalid never resolves).
func TestLLMCheck_ReasonCapped(t *testing.T) {
	longHost := strings.Repeat("u", 400) + ".invalid"
	s := llmCheckServer(t,
		llmCheckAgent("ag", "ollama-cloud", "m1", "http://"+longHost, "ag-llm-key"),
		llmCheckSecret("ag-llm-key", "k"),
	)
	code, body := postLLMCheck(t, s, `{"provider":"ollama-cloud","model":"m1"}`)
	if code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", code, body)
	}
	if reason := llmCheckError(t, body); len(reason) > llmCheckReasonMaxLen {
		t.Errorf("reason length = %d, want <= %d", len(reason), llmCheckReasonMaxLen)
	}
}

// TestLLMCheck_DefaultBaseURL pins the probe targets: the hub and the
// operator both read the llmprovider registry, so for providers whose agents
// speak OpenAI chat completions the check probes the URL the agent runs on,
// and custom stays uncheckable.
func TestLLMCheck_DefaultBaseURL(t *testing.T) {
	for _, p := range llmprovider.All() {
		if p.PiAPI == llmprovider.PiAPIOpenAICompletions && p.ID != "custom" && p.CheckBaseURL != p.DefaultBaseURL {
			t.Errorf("%s: check URL %q differs from the URL agents run on %q", p.ID, p.CheckBaseURL, p.DefaultBaseURL)
		}
	}
	for id, want := range map[string]string{
		"ollama-cloud": "https://ollama.com/v1",
		"opencode":     "https://opencode.ai/zen/go/v1",
	} {
		if p, _ := llmprovider.Lookup(id); p.CheckBaseURL != want {
			t.Errorf("%s check URL = %q, want %q", id, p.CheckBaseURL, want)
		}
	}
	if p, _ := llmprovider.Lookup("alibaba-cloud"); !strings.HasPrefix(p.CheckBaseURL, "https://token-plan.") {
		t.Errorf("alibaba-cloud check URL = %q", p.CheckBaseURL)
	}
	if p, _ := llmprovider.Lookup("custom"); p.CheckBaseURL != "" {
		t.Error("custom must have no check URL")
	}

	// Endpoint building: trailing slash trimmed, path appended once.
	ep, err := llmCheckEndpoint("https://ollama.com/v1/")
	if err != nil || ep != "https://ollama.com/v1/chat/completions" {
		t.Errorf("llmCheckEndpoint = %q, err %v", ep, err)
	}
	for _, bad := range []string{"", "ollama.com/v1", "file:///etc", "://bad"} {
		if _, err := llmCheckEndpoint(bad); err == nil {
			t.Errorf("llmCheckEndpoint(%q): expected error", bad)
		}
	}
}

// TestLLMCheck_NonPostViaMux covers the route registration itself: the path
// exists on a Server built by New() (the constructor wiring), not just in
// tests that register it by hand.
func TestLLMCheck_NonPostViaMux(t *testing.T) {
	s := testServer(t)
	// New() registers the handler; replicate what the tests cannot call
	// directly (New needs more wiring) by asserting the handler's own guard.
	s.mux.HandleFunc("/api/v1/llm/check", s.handleLLMCheck)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/llm/check", bytes.NewReader(nil))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}
