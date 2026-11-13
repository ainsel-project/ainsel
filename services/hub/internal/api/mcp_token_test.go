package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// fakeTokenMCPServer is the shape of an MCP server that requires a bearer
// token — the forgejo-mcp `passthrough` handshake from issue #251: an
// anonymous POST /mcp answers 401, one with the sentinel bearer succeeds.
// The 401 body echoes the Authorization header back (as some servers do), so
// tests can prove the credential never reaches a user-visible error string.
// The returned accessor yields the Authorization headers the server saw.
func fakeTokenMCPServer(t *testing.T, sentinel string, rejectEvenWithToken bool) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	record := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		mu.Lock()
		seen = append(seen, auth)
		mu.Unlock()
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if auth != "Bearer "+sentinel || rejectEvenWithToken {
			w.Header().Set("WWW-Authenticate", `Bearer realm="fake-mcp"`)
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, "401 Unauthorized: this server requires a per-request token (saw %q)\n", auth)
			return
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-03-26","capabilities":{"tools":{}},"serverInfo":{"name":"fake-mcp","version":"0.1.0"}}}`, req.ID)
		case "tools/list":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"echo","description":"Echo a message"}]}}`, req.ID)
		default:
			if strings.HasPrefix(req.Method, "notifications/") {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			// server/discover and anything else: JSON-RPC method-not-found,
			// which makes the client fall back to the legacy initialize handshake.
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"method not found"}}`, req.ID)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, record
}

func TestListMCPTools_AnonymousRejectedBearerAccepted(t *testing.T) {
	const sentinel = "tok-list-sentinel" // #nosec G101 -- test fixture
	srv, _ := fakeTokenMCPServer(t, sentinel, false)

	if _, err := listMCPTools(t.Context(), srv.URL, ""); err == nil {
		t.Error("anonymous listing should fail against a token-required server")
	}

	tools, err := listMCPTools(t.Context(), srv.URL, sentinel)
	if err != nil {
		t.Fatalf("listing with the bearer should succeed: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Errorf("expected the echo tool, got %+v", tools)
	}
}

func mcpImage(name, serverName, url, tokenFromEnv string, env ...agentv1alpha1.AgentImageEnvVar) *agentv1alpha1.AgentImage {
	return &agentv1alpha1.AgentImage{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test-ns"},
		Spec: agentv1alpha1.AgentImageSpec{
			DisplayName: name,
			ImageURL:    "registry.test/" + name + ":latest",
			MCPServers: []agentv1alpha1.AgentImageMCPServer{
				{Name: serverName, URL: url, TokenFromEnv: tokenFromEnv},
			},
			Env: env,
		},
	}
}

func TestResolveMCPServerToken_ImageEnvResolution(t *testing.T) {
	imgEnv := []agentv1alpha1.AgentImageEnvVar{
		{Name: "OTHER_VAR", Value: "unrelated"},
		{Name: "FORGEJO_PAT", Value: "pat-value", Secret: true},
	}
	cases := []struct {
		name         string
		tokenFromEnv string
		env          []agentv1alpha1.AgentImageEnvVar
		want         string
	}{
		{"named var present (secret)", "FORGEJO_PAT", imgEnv, "pat-value"},
		{"var absent", "MISSING_VAR", imgEnv, ""},
		{"tokenFromEnv empty never searches env", "", imgEnv, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := testServer(t)
			got, err := s.resolveMCPServerToken(t.Context(), "forgejo", tc.tokenFromEnv, "https://mcp.example/mcp", tc.env, false)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveMCPServerToken_EmptyValueStaysAnonymous(t *testing.T) {
	s := testServer(t)
	env := []agentv1alpha1.AgentImageEnvVar{{Name: "FORGEJO_PAT", Value: ""}}
	got, err := s.resolveMCPServerToken(t.Context(), "forgejo", "FORGEJO_PAT", "https://mcp.example/mcp", env, false)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "" {
		t.Errorf("an empty env value must resolve to anonymous, got %q", got)
	}
}

func TestResolveMCPServerToken_FallbackFirstReferencingImageByName(t *testing.T) {
	// Two images reference the server; both define the var. The one that sorts
	// first by name wins, deterministically.
	s := testServer(t,
		mcpImage("zeta-image", "forgejo", "https://mcp.example/mcp", "FORGEJO_PAT",
			agentv1alpha1.AgentImageEnvVar{Name: "FORGEJO_PAT", Value: "zeta-token", Secret: true}),
		mcpImage("alpha-image", "forgejo", "https://mcp.example/mcp", "FORGEJO_PAT",
			agentv1alpha1.AgentImageEnvVar{Name: "FORGEJO_PAT", Value: "alpha-token", Secret: true}),
		// An image that does not reference the server must not contribute.
		mcpImage("other-image", "memory", "https://mcp.example/mcp", "FORGEJO_PAT",
			agentv1alpha1.AgentImageEnvVar{Name: "FORGEJO_PAT", Value: "not-this", Secret: true}),
	)
	got, err := s.resolveMCPServerToken(t.Context(), "forgejo", "FORGEJO_PAT", "https://mcp.example/mcp", nil, true)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "alpha-token" {
		t.Errorf("first referencing image by name should win, got %q", got)
	}
}

func TestResolveMCPServerToken_FallbackSkipsEmptyAndMissing(t *testing.T) {
	// The first image by name defines the var with an empty value; the second
	// defines it for real and wins.
	s := testServer(t,
		mcpImage("alpha-image", "forgejo", "https://mcp.example/mcp", "FORGEJO_PAT",
			agentv1alpha1.AgentImageEnvVar{Name: "FORGEJO_PAT", Value: ""}),
		mcpImage("zeta-image", "forgejo", "https://mcp.example/mcp", "FORGEJO_PAT",
			agentv1alpha1.AgentImageEnvVar{Name: "FORGEJO_PAT", Value: "zeta-token", Secret: true}),
	)
	got, err := s.resolveMCPServerToken(t.Context(), "forgejo", "FORGEJO_PAT", "https://mcp.example/mcp", nil, true)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "zeta-token" {
		t.Errorf("empty definition must be skipped, got %q", got)
	}

	// Nothing references the server → anonymous.
	s2 := testServer(t)
	got, err = s2.resolveMCPServerToken(t.Context(), "forgejo", "FORGEJO_PAT", "https://mcp.example/mcp", nil, true)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "" {
		t.Errorf("no referencing image must stay anonymous, got %q", got)
	}
}

func TestResolveMCPServerToken_PlatformExceptionHubPluginURL(t *testing.T) {
	s := testServer(t)
	s.hubSelfURL = "https://hub.ainsel.svc:8443"
	s.internalValidateSecret = "platform-secret" // #nosec G101 -- test fixture
	url := "https://hub.ainsel.svc:8443/api/internal/plugins/memory/agent-mcp"
	got, err := s.resolveMCPServerToken(t.Context(), "memory", "HUB_INTERNAL_VALIDATE_SECRET", url, nil, false)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "platform-secret" {
		t.Errorf("hub plugin endpoint should get the platform secret, got %q", got)
	}
}

func TestResolveMCPServerToken_PlatformSecretNeverForUserURLs(t *testing.T) {
	// A user-registered row naming the platform var must never collect the
	// platform secret — the operator's guard, mirrored (env_test.go cases).
	s := testServer(t)
	s.hubSelfURL = "https://hub.ainsel.svc:8443"
	s.internalValidateSecret = "platform-secret" // #nosec G101 -- test fixture
	got, err := s.resolveMCPServerToken(t.Context(), "evil", "HUB_INTERNAL_VALIDATE_SECRET", "https://mcp.example.com/mcp", nil, false)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "" {
		t.Errorf("user URL must not receive the platform secret, got %q", got)
	}

	// Same row, but the image really defines the var itself: the image's own
	// value is used (exactly what the operator injects), never the platform one.
	env := []agentv1alpha1.AgentImageEnvVar{
		{Name: "HUB_INTERNAL_VALIDATE_SECRET", Value: "image-own-value"},
	}
	got, err = s.resolveMCPServerToken(t.Context(), "evil", "HUB_INTERNAL_VALIDATE_SECRET", "https://mcp.example.com/mcp", env, false)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "image-own-value" {
		t.Errorf("expected the image's own value, got %q", got)
	}
}

func TestResolveMCPServerToken_PlatformExceptionDisabledWithoutConfig(t *testing.T) {
	// No HUB_SELF_URL / no internal-validate secret: the exception is inert
	// and the row stays anonymous rather than erroring.
	s := testServer(t)
	url := "https://hub.ainsel.svc:8443/api/internal/plugins/memory/agent-mcp"
	got, err := s.resolveMCPServerToken(t.Context(), "memory", "HUB_INTERNAL_VALIDATE_SECRET", url, nil, false)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "" {
		t.Errorf("unset platform config must stay anonymous, got %q", got)
	}
}

func TestRedactToken(t *testing.T) {
	const token = "super-secret-token" // #nosec G101 -- test fixture
	msg := `list tools: server replied "saw \"Bearer super-secret-token\""`
	got := redactToken(msg, token)
	if strings.Contains(got, token) {
		t.Errorf("token leaked: %s", got)
	}
	if !strings.Contains(got, "[redacted]") {
		t.Errorf("expected redaction marker, got %s", got)
	}
	if redactToken("plain message", "") != "plain message" {
		t.Error("empty token must leave the message untouched")
	}
}
