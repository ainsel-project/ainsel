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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	"github.com/ainsel-project/ainsel/services/hub/internal/signin"
	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"
)

const testInternalToken = "internal-secret"

func signInTestAgent(name string, method agentv1alpha1.OpenAIAuthMethod) *agentv1alpha1.Agent {
	return &agentv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test-ns"},
		Spec: agentv1alpha1.AgentSpec{
			DisplayName: name,
			ImageRef:    agentv1alpha1.AgentImageRef{Name: "img-1"},
			LLM:         agentv1alpha1.AgentLLM{Provider: agentv1alpha1.AgentLLMProviderOpenAI, Model: "gpt-5"},
			OpenAI:      &agentv1alpha1.AgentOpenAI{AuthMethod: method},
		},
	}
}

// signInServer wires the agent routes, the internal routes and a MemStore on
// a clock the test controls.
func signInServer(t *testing.T) (*Server, *time.Time) {
	t.Helper()
	s := testServer(t, testAgentImage("img-1"),
		signInTestAgent("signer", agentv1alpha1.OpenAIAuthMethodChatGPTSignIn),
		signInTestAgent("keyed", agentv1alpha1.OpenAIAuthMethodAPIKey))
	now := time.Now()
	s.SetSignInStore(signin.NewMemStore(func() time.Time { return now }))
	s.internalValidateSecret = testInternalToken
	s.mux.HandleFunc("/api/v1/agents", s.handleAgents)
	s.mux.HandleFunc("/api/v1/agents/", s.handleAgent)
	s.mux.HandleFunc("/api/internal/agents/", s.handleInternalAgent)
	return s, &now
}

// call sends a request and returns the status code and body.
func call(t *testing.T, s *Server, method, path string, body any, internal bool) (int, []byte) {
	t.Helper()
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, path, nil)
	} else {
		b, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
	}
	if internal {
		r.Header.Set("X-Internal-Token", testInternalToken)
		// The sign-in pod also carries its session's token, from the Secret
		// the hub wrote for it.
		if agent, _, ok := strings.Cut(strings.TrimPrefix(path, "/api/internal/agents/"), "/"); ok {
			if tok := podToken(s, agent); tok != "" {
				r.Header.Set("X-Sign-In-Token", tok)
			}
		}
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, r)
	return rec.Code, rec.Body.Bytes()
}

func startSignIn(t *testing.T, s *Server, agent string) signInStatusResponse {
	t.Helper()
	code, body := call(t, s, http.MethodPost, "/api/v1/agents/"+agent+"/chatgpt-sign-in", nil, false)
	if code != http.StatusCreated {
		t.Fatalf("start sign-in: %d %s", code, body)
	}
	var resp signInStatusResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func sessionAnnotation(t *testing.T, s *Server, agent string) string {
	t.Helper()
	var a agentv1alpha1.Agent
	if err := s.client.Get(context.Background(), types.NamespacedName{Namespace: "test-ns", Name: agent}, &a); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	return a.Annotations[signInSessionAnnotation]
}

func TestSignInStartRequiresWrite(t *testing.T) {
	s, _ := signInServer(t)
	checker := authz.NewChecker(groupAuthzStore{roles: map[string]authz.GroupRole{"g1": authz.RoleReader}},
		authz.NewGroupCache(func(string) (map[string]authz.GroupRole, error) {
			return map[string]authz.GroupRole{"g1": authz.RoleReader}, nil
		}, time.Minute))
	s.authzChecker = checker

	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents/signer/chatgpt-sign-in", nil)
	r = r.WithContext(oidc.ContextWithUser(r.Context(), &oidc.User{Sub: "u1", Username: "u1"}))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("reader starting a sign-in: %d, want 403", rec.Code)
	}
}

func TestSignInStartRejectsAPIKeyAgent(t *testing.T) {
	s, _ := signInServer(t)
	code, body := call(t, s, http.MethodPost, "/api/v1/agents/keyed/chatgpt-sign-in", nil, false)
	if code != http.StatusBadRequest || !strings.Contains(string(body), "ChatGPT sign-in") {
		t.Fatalf("start on an API-key agent: %d %s, want 400 naming ChatGPT sign-in", code, body)
	}
}

func TestSignInStartWritesAnnotation(t *testing.T) {
	s, _ := signInServer(t)
	resp := startSignIn(t, s, "signer")
	if resp.State != string(signin.StateWaitingForPod) || resp.ID == "" {
		t.Fatalf("start response = %+v", resp)
	}
	if got := sessionAnnotation(t, s, "signer"); got != resp.ID {
		t.Fatalf("annotation = %q, want the session id %q", got, resp.ID)
	}
}

func TestSignInSecondStartConflicts(t *testing.T) {
	s, _ := signInServer(t)
	startSignIn(t, s, "signer")
	if code, _ := call(t, s, http.MethodPost, "/api/v1/agents/signer/chatgpt-sign-in", nil, false); code != http.StatusConflict {
		t.Fatalf("second start: %d, want 409", code)
	}
}

// postAuthURL plays the sign-in pod posting the authorization link.
func postAuthURL(t *testing.T, s *Server, agent, id string) {
	t.Helper()
	code, body := call(t, s, http.MethodPost, "/api/internal/agents/"+agent+"/chatgpt-sign-in/"+id+"/auth-url",
		map[string]string{"url": "https://auth.openai.com/api/accounts/authorize?client_id=dynamic_agent_client&state=st8"}, true)
	if code != http.StatusNoContent {
		t.Fatalf("post auth url: %d %s", code, body)
	}
}

func TestSignInRedirectValidatesPrefix(t *testing.T) {
	s, _ := signInServer(t)
	sess := startSignIn(t, s, "signer")
	postAuthURL(t, s, "signer", sess.ID)
	for _, bad := range []string{"https://chatgpt.com/", "", "http://127.0.0.1:1456/auth/callback?code=x"} {
		code, body := call(t, s, http.MethodPost, "/api/v1/agents/signer/chatgpt-sign-in/redirect",
			signInRedirectRequest{RedirectURL: bad}, false)
		if code != http.StatusBadRequest || !strings.Contains(string(body), "must start with http://127.0.0.1:1455/auth/callback") {
			t.Errorf("paste %q: %d %s, want 400 naming the callback prefix", bad, code, body)
		}
	}
	code, body := call(t, s, http.MethodGet, "/api/v1/agents/signer/chatgpt-sign-in", nil, false)
	if code != http.StatusOK || !strings.Contains(string(body), `"state":"awaiting_redirect"`) {
		t.Fatalf("after bad pastes: %d %s, want the session still awaiting_redirect", code, body)
	}
}

func TestSignInRedirectBeforeLinkIsConflict(t *testing.T) {
	s, _ := signInServer(t)
	startSignIn(t, s, "signer")
	code, _ := call(t, s, http.MethodPost, "/api/v1/agents/signer/chatgpt-sign-in/redirect",
		signInRedirectRequest{RedirectURL: "http://127.0.0.1:1455/auth/callback?code=c&state=s"}, false)
	if code != http.StatusConflict {
		t.Fatalf("paste before the pod posted the link: %d, want 409", code)
	}
}

func TestSignInRedirectHandedOnce(t *testing.T) {
	s, _ := signInServer(t)
	sess := startSignIn(t, s, "signer")
	postAuthURL(t, s, "signer", sess.ID)
	path := "/api/internal/agents/signer/chatgpt-sign-in/" + sess.ID + "/redirect"
	if code, _ := call(t, s, http.MethodGet, path, nil, true); code != http.StatusNoContent {
		t.Fatalf("redirect before paste: %d, want 204", code)
	}
	const pasted = "http://127.0.0.1:1455/auth/callback?code=c&state=st8&client_id=app_x"
	if code, body := call(t, s, http.MethodPost, "/api/v1/agents/signer/chatgpt-sign-in/redirect",
		signInRedirectRequest{RedirectURL: pasted}, false); code != http.StatusAccepted {
		t.Fatalf("paste: %d %s, want 202", code, body)
	}
	code, body := call(t, s, http.MethodGet, path, nil, true)
	if code != http.StatusOK || !strings.Contains(string(body), "code=c") {
		t.Fatalf("first take: %d %s, want 200 with the URL", code, body)
	}
	if code, _ := call(t, s, http.MethodGet, path, nil, true); code != http.StatusNoContent {
		t.Fatalf("second take: %d, want 204: the URL is handed out once", code)
	}
	code, body = call(t, s, http.MethodGet, "/api/v1/agents/signer/chatgpt-sign-in", nil, false)
	if code != http.StatusOK || strings.Contains(string(body), "code=c") {
		t.Fatalf("console status %d leaks the pasted URL: %s", code, body)
	}
}

func TestSignInResultSucceeds(t *testing.T) {
	s, _ := signInServer(t)
	sess := startSignIn(t, s, "signer")
	code, _ := call(t, s, http.MethodPost, "/api/internal/agents/signer/chatgpt-sign-in/"+sess.ID+"/result",
		signInResultRequest{OK: true}, true)
	if code != http.StatusNoContent {
		t.Fatalf("post result: %d, want 204", code)
	}
	_, body := call(t, s, http.MethodGet, "/api/v1/agents/signer/chatgpt-sign-in", nil, false)
	if !strings.Contains(string(body), `"state":"succeeded"`) {
		t.Fatalf("status after success: %s", body)
	}
}

func TestSignInExpires(t *testing.T) {
	s, now := signInServer(t)
	sess := startSignIn(t, s, "signer")
	*now = now.Add(signin.TTL + time.Second)
	_, body := call(t, s, http.MethodGet, "/api/v1/agents/signer/chatgpt-sign-in", nil, false)
	if !strings.Contains(string(body), `"state":"expired"`) {
		t.Fatalf("status after the TTL: %s, want expired", body)
	}
	if code, _ := call(t, s, http.MethodGet, "/api/internal/agents/signer/chatgpt-sign-in/"+sess.ID+"/redirect", nil, true); code != http.StatusGone {
		t.Fatalf("pod polling an expired session: %d, want 410", code)
	}
	startSignIn(t, s, "signer") // a new sign-in can start
}

func TestSignInCancel(t *testing.T) {
	s, _ := signInServer(t)
	sess := startSignIn(t, s, "signer")
	if code, _ := call(t, s, http.MethodDelete, "/api/v1/agents/signer/chatgpt-sign-in", nil, false); code != http.StatusNoContent {
		t.Fatalf("cancel: %d, want 204", code)
	}
	if code, _ := call(t, s, http.MethodGet, "/api/internal/agents/signer/chatgpt-sign-in/"+sess.ID+"/redirect", nil, true); code != http.StatusGone {
		t.Fatalf("pod polling a cancelled session: %d, want 410", code)
	}
}

func TestInternalSignInRequiresToken(t *testing.T) {
	s, _ := signInServer(t)
	sess := startSignIn(t, s, "signer")
	code, _ := call(t, s, http.MethodGet, "/api/internal/agents/signer/chatgpt-sign-in/"+sess.ID+"/redirect", nil, false)
	if code != http.StatusUnauthorized {
		t.Fatalf("internal call without token: %d, want 401", code)
	}
}

func TestCreateAgentRejectsSignInWithReplicas2(t *testing.T) {
	s, _ := signInServer(t)
	code, body := call(t, s, http.MethodPost, "/api/v1/agents", SimpleAgentCreateRequest{
		Name: "multi", ImageRef: AgentImageRefInfo{Name: "img-1"},
		LLM:      AgentLLMInfo{Model: "gpt-5", Provider: agentv1alpha1.AgentLLMProviderOpenAI},
		OpenAI:   &AgentAPIKeyInfo{AuthMethod: string(agentv1alpha1.OpenAIAuthMethodChatGPTSignIn)},
		Replicas: two(),
	}, false)
	if code != http.StatusBadRequest || !strings.Contains(string(body), "set replicas to 1") {
		t.Fatalf("create sign-in agent with replicas 2: %d %s", code, body)
	}
}

func TestCreateAgentRejectsSignInWithAPIKey(t *testing.T) {
	s, _ := signInServer(t)
	code, body := call(t, s, http.MethodPost, "/api/v1/agents", SimpleAgentCreateRequest{
		Name: "both", ImageRef: AgentImageRefInfo{Name: "img-1"},
		LLM:    AgentLLMInfo{Model: "gpt-5", Provider: agentv1alpha1.AgentLLMProviderOpenAI},
		OpenAI: &AgentAPIKeyInfo{AuthMethod: string(agentv1alpha1.OpenAIAuthMethodChatGPTSignIn), APIKey: "sk-x"},
	}, false)
	if code != http.StatusBadRequest {
		t.Fatalf("create with sign-in and an API key: %d %s, want 400", code, body)
	}
}

func TestCreateAgentRejectsUnknownAuthMethod(t *testing.T) {
	s, _ := signInServer(t)
	code, body := call(t, s, http.MethodPost, "/api/v1/agents", SimpleAgentCreateRequest{
		Name: "odd", ImageRef: AgentImageRefInfo{Name: "img-1"},
		LLM:    AgentLLMInfo{Model: "gpt-5", Provider: agentv1alpha1.AgentLLMProviderOpenAI},
		OpenAI: &AgentAPIKeyInfo{AuthMethod: "Codex"},
	}, false)
	if code != http.StatusBadRequest || !strings.Contains(string(body), "APIKey") {
		t.Fatalf("create with authMethod Codex: %d %s, want 400 listing the methods", code, body)
	}
}

func TestUpdateAgentSwitchesAuthMethodAndReportsIt(t *testing.T) {
	s, _ := signInServer(t)
	code, body := call(t, s, http.MethodPut, "/api/v1/agents/keyed", SimpleAgentUpdateRequest{
		OpenAI: &AgentAPIKeyInfo{AuthMethod: string(agentv1alpha1.OpenAIAuthMethodChatGPTSignIn)},
	}, false)
	if code != http.StatusOK {
		t.Fatalf("switch to sign-in: %d %s", code, body)
	}
	var resp SimpleAgentResponse
	_ = json.Unmarshal(body, &resp)
	if resp.OpenAI == nil || resp.OpenAI.AuthMethod != string(agentv1alpha1.OpenAIAuthMethodChatGPTSignIn) {
		t.Fatalf("response openAI = %+v, want authMethod ChatGPTSignIn", resp.OpenAI)
	}
	if resp.OpenAI.KeyStored {
		t.Errorf("keyStored = true for an agent that never stored a key")
	}
}

func TestUpdateAgentRejectsReplicas2OnSignInAgent(t *testing.T) {
	s, _ := signInServer(t)
	code, body := call(t, s, http.MethodPut, "/api/v1/agents/signer", SimpleAgentUpdateRequest{Replicas: two()}, false)
	if code != http.StatusBadRequest || !strings.Contains(string(body), "set replicas to 1") {
		t.Fatalf("raise replicas on a sign-in agent: %d %s", code, body)
	}
}

func TestAgentResponseCarriesSignInRequired(t *testing.T) {
	a := signInTestAgent("signer", agentv1alpha1.OpenAIAuthMethodChatGPTSignIn)
	a.Status.Conditions = []metav1.Condition{{
		Type: agentv1alpha1.AgentConditionSignInRequired, Status: metav1.ConditionTrue, Reason: "SignInExpired",
	}}
	resp := toSimpleAgentResponse(*a, "")
	if resp.Status == nil || !resp.Status.SignInRequired || resp.Status.SignInReason != "SignInExpired" {
		t.Fatalf("status = %+v, want signInRequired with reason SignInExpired", resp.Status)
	}
}

func two() *int32 { n := int32(2); return &n }

// The agent pods share one internal token, so a pod must not be able to put
// a link to anywhere but OpenAI in front of the user.
func TestInternalSignInRejectsForeignAuthURL(t *testing.T) {
	s, _ := signInServer(t)
	sess := startSignIn(t, s, "signer")
	code, _ := call(t, s, http.MethodPost, "/api/internal/agents/signer/chatgpt-sign-in/"+sess.ID+"/auth-url",
		map[string]string{"url": "https://evil.example/login"}, true)
	if code != http.StatusBadRequest {
		t.Fatalf("foreign authorization URL: %d, want 400", code)
	}
	_, body := call(t, s, http.MethodGet, "/api/v1/agents/signer/chatgpt-sign-in", nil, false)
	if strings.Contains(string(body), "evil.example") {
		t.Fatalf("console shows the foreign link: %s", body)
	}
}

// A paste that has the right prefix but cannot complete the sign-in is a 400
// with the session left open, not a doomed exchange in the pod.
func TestSignInRedirectRejectsUnusableCallbacks(t *testing.T) {
	s, _ := signInServer(t)
	sess := startSignIn(t, s, "signer")
	postAuthURL(t, s, "signer", sess.ID)
	cases := map[string]string{
		"http://127.0.0.1:1455/auth/callback":                                    "code",
		"http://127.0.0.1:1455/auth/callbackX?code=c&state=st8&client_id=app_x":  "/auth/callback",
		"http://127.0.0.1:1455/auth/callback?error=access_denied&state=st8":      "access_denied",
		"http://127.0.0.1:1455/auth/callback?code=c&state=older&client_id=app_x": "earlier sign-in",
		"http://127.0.0.1:1455/auth/callback?code=c&state=st8":                   "client_id",
	}
	for pasted, want := range cases {
		code, body := call(t, s, http.MethodPost, "/api/v1/agents/signer/chatgpt-sign-in/redirect",
			signInRedirectRequest{RedirectURL: pasted}, false)
		if code != http.StatusBadRequest || !strings.Contains(string(body), want) {
			t.Errorf("paste %q: %d %s, want 400 mentioning %q", pasted, code, body, want)
		}
	}
	_, body := call(t, s, http.MethodGet, "/api/v1/agents/signer/chatgpt-sign-in", nil, false)
	if !strings.Contains(string(body), `"state":"awaiting_redirect"`) {
		t.Fatalf("after unusable pastes: %s, want the session still open", body)
	}
	if code, _ := call(t, s, http.MethodPost, "/api/v1/agents/signer/chatgpt-sign-in/redirect",
		signInRedirectRequest{RedirectURL: "http://127.0.0.1:1455/auth/callback?code=c&state=st8&client_id=app_x"}, false); code != http.StatusAccepted {
		t.Fatalf("the right callback afterwards: %d, want 202", code)
	}
}

func podToken(s *Server, agent string) string {
	var sec corev1.Secret
	if err := s.client.Get(context.Background(), types.NamespacedName{Namespace: "test-ns", Name: agent + "-chatgpt-sign-in"}, &sec); err != nil {
		return ""
	}
	if v := sec.StringData["token"]; v != "" {
		return v
	}
	return string(sec.Data["token"])
}

func TestSignInStartCreatesPodSecret(t *testing.T) {
	s, _ := signInServer(t)
	resp := startSignIn(t, s, "signer")
	var sec corev1.Secret
	if err := s.client.Get(context.Background(), types.NamespacedName{Namespace: "test-ns", Name: "signer-chatgpt-sign-in"}, &sec); err != nil {
		t.Fatalf("pod token secret: %v", err)
	}
	tok := podToken(s, "signer")
	if len(tok) < 32 {
		t.Fatalf("pod token %q is too short to be a secret", tok)
	}
	if len(sec.OwnerReferences) != 1 || sec.OwnerReferences[0].Name != "signer" {
		t.Errorf("owner refs = %+v, want the Agent", sec.OwnerReferences)
	}
	_, body := call(t, s, http.MethodGet, "/api/v1/agents/signer/chatgpt-sign-in", nil, false)
	if strings.Contains(string(body), tok) || strings.Contains(sessionAnnotation(t, s, "signer"), tok) {
		t.Fatal("the pod token is visible outside its Secret")
	}
	_ = resp
}

// Every agent pod has the shared internal token. Only the sign-in pod has the
// session token, so only it may post the link or take the pasted address.
func TestInternalSignInRequiresPodToken(t *testing.T) {
	s, _ := signInServer(t)
	sess := startSignIn(t, s, "signer")
	path := "/api/internal/agents/signer/chatgpt-sign-in/" + sess.ID + "/auth-url"
	for _, tok := range []string{"", "wrong-token"} {
		b, _ := json.Marshal(map[string]string{"url": "https://auth.openai.com/api/accounts/authorize?state=evil"})
		r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
		r.Header.Set("X-Internal-Token", testInternalToken)
		if tok != "" {
			r.Header.Set("X-Sign-In-Token", tok)
		}
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Errorf("pod token %q: %d, want 403", tok, rec.Code)
		}
	}
	postAuthURL(t, s, "signer", sess.ID) // with the right token it works
}
