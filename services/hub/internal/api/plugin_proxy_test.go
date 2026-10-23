package api

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/plugincreds"
	pluginpkg "github.com/ainsel-project/ainsel/shared/api/plugin"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"
	"k8s.io/apimachinery/pkg/runtime"
)

// TestPluginProxy_UIServedWithoutAuth tests that UI requests are auth-exempt
// and forwarded to the upstream without credentials.
func TestPluginProxy_UIServedWithoutAuth(t *testing.T) {
	// Set up upstream server that records the request
	var upstreamReq *http.Request
	upstreamSvc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamReq = r
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("console.log('ok')"))
	}))
	defer upstreamSvc.Close()

	// Create plugin with UI and verified endpoint
	plugin := pluginCR("mem0", false)
	plugin = pluginCRReady(plugin, true, "", "", nil)

	// Set manifest after pluginCRReady (which overwrites it)
	manifest := &pluginpkg.Manifest{
		ID:        "mem0",
		PluginAPI: "1.0",
		Version:   "1.0.0",
		UI:        &pluginpkg.UISpec{Entry: "/static/ui/index.js"},
	}
	manifestBytes, _ := json.Marshal(manifest)
	plugin.Status.Manifest = &runtime.RawExtension{Raw: manifestBytes}

	endpoint := net.JoinHostPort("127.0.0.1", strings.Split(upstreamSvc.Listener.Addr().String(), ":")[1])
	plugin.Status.VerifiedEndpoints = []string{endpoint}

	// Create server with auth middleware that rejects everything
	s := testServer(t, plugin)
	registerPluginRoutes(s)
	s.SetAuthMiddleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusUnauthorized, "always rejects")
		})
	})

	// Test GET succeeds with auth-exempt
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/plugins/mem0/ui/index.js", nil)
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/plugins/mem0/ui/index.js: got %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); body != "console.log('ok')" {
		t.Fatalf("response body: got %q, want \"console.log('ok')\"", body)
	}

	// Verify upstream received the correct path
	if upstreamReq.URL.Path != "/static/ui/index.js" {
		t.Fatalf("upstream path: got %q, want \"/static/ui/index.js\"", upstreamReq.URL.Path)
	}

	// Verify no credentials sent to upstream
	if cred := upstreamReq.Header.Get("X-Ainsel-Credential"); cred != "" {
		t.Fatalf("upstream X-Ainsel-Credential: got %q, want empty", cred)
	}

	// Test POST is rejected (405)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/v1/plugins/mem0/ui/index.js", nil)
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/v1/plugins/mem0/ui/index.js: got %d, want 405", rec.Code)
	}
}

// TestPluginProxy_UIEncodedTraversal tests that percent-encoded traversal in UI paths is rejected.
func TestPluginProxy_UIEncodedTraversal(t *testing.T) {
	plugin := pluginCR("mem0", false)
	plugin = pluginCRReady(plugin, true, "", "", nil)

	manifest := &pluginpkg.Manifest{
		ID:        "mem0",
		PluginAPI: "1.0",
		Version:   "1.0.0",
		UI:        &pluginpkg.UISpec{Entry: "/static/ui/index.js"},
	}
	manifestBytes, _ := json.Marshal(manifest)
	plugin.Status.Manifest = &runtime.RawExtension{Raw: manifestBytes}

	plugin.Status.VerifiedEndpoints = []string{"127.0.0.1:9999"} // non-existent, shouldn't be called

	s := testServer(t, plugin)
	registerPluginRoutes(s)

	// Test traversal with percent encoding
	tests := []string{
		"/api/v1/plugins/mem0/ui/%2e%2e/%2e%2e/api/secret",
		"/api/v1/plugins/mem0/ui/..%2fapi",
	}

	for _, path := range tests {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		s.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET %s: got %d, want 400", path, rec.Code)
		}

		var errResp map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&errResp); err == nil {
			if errResp["error"] != "invalid plugin path" {
				t.Fatalf("error message: got %q, want \"invalid plugin path\"", errResp["error"])
			}
		}
	}
}

// TestPluginProxy_APIForwardsCredential tests that API requests forward credentials and context.
func TestPluginProxy_APIForwardsCredential(t *testing.T) {
	// Set up upstream server
	var upstreamReq *http.Request
	upstreamSvc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamReq = r
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"memories":[]}`))
	}))
	defer upstreamSvc.Close()

	// Create plugin with API
	plugin := pluginCR("mem0", false)
	plugin = pluginCRReady(plugin, true, "", "", nil)

	manifest := &pluginpkg.Manifest{
		ID:        "mem0",
		PluginAPI: "1.0",
		Version:   "1.0.0",
		API:       &pluginpkg.APISpec{BasePath: "/api"},
	}
	manifestBytes, _ := json.Marshal(manifest)
	plugin.Status.Manifest = &runtime.RawExtension{Raw: manifestBytes}

	plugin.Spec.ApprovedPermissions = []string{"agents:read"}

	endpoint := net.JoinHostPort("127.0.0.1", strings.Split(upstreamSvc.Listener.Addr().String(), ":")[1])
	plugin.Status.VerifiedEndpoints = []string{endpoint}

	// Set up server with credential issuer
	s := testServer(t, plugin)
	registerPluginRoutes(s)

	secret := make([]byte, 32)
	for i := 0; i < len(secret); i++ {
		secret[i] = byte(i % 256)
	}
	issuer, _ := plugincreds.NewIssuer(secret, 5*time.Minute)
	s.SetPluginCredentialIssuer(issuer)

	// Set up auth middleware that injects a user context
	testUser := &oidc.User{
		Sub:      "user123",
		Username: "testuser",
	}

	s.SetAuthMiddleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := oidc.ContextWithUser(r.Context(), testUser)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})

	// Make authenticated request
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/plugins/mem0/api/memories?agent=a1", nil)
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/plugins/mem0/api/memories: got %d, want 200", rec.Code)
	}

	// Verify upstream path
	if upstreamReq.URL.Path != "/api/memories" {
		t.Fatalf("upstream path: got %q, want \"/api/memories\"", upstreamReq.URL.Path)
	}

	// Verify upstream raw query
	if upstreamReq.URL.RawQuery != "agent=a1" {
		t.Fatalf("upstream raw query: got %q, want \"agent=a1\"", upstreamReq.URL.RawQuery)
	}

	// Verify X-Ainsel-Credential is set
	cred := upstreamReq.Header.Get("X-Ainsel-Credential")
	if cred == "" {
		t.Fatal("X-Ainsel-Credential not set")
	}
	if !strings.HasPrefix(cred, "ainselp_") {
		t.Fatalf("X-Ainsel-Credential: got %q, doesn't start with \"ainselp_\"", cred)
	}

	// Verify X-Ainsel-Context is set and valid
	ctxHeader := upstreamReq.Header.Get("X-Ainsel-Context")
	if ctxHeader == "" {
		t.Fatal("X-Ainsel-Context not set")
	}

	// Decode base64url (no padding)
	decoded, err := base64.RawURLEncoding.DecodeString(ctxHeader)
	if err != nil {
		t.Fatalf("decode X-Ainsel-Context: %v", err)
	}

	var ctx pluginContextResponse
	if err := json.Unmarshal(decoded, &ctx); err != nil {
		t.Fatalf("unmarshal X-Ainsel-Context: %v", err)
	}

	if ctx.PluginID != "mem0" {
		t.Fatalf("context.pluginId: got %q, want \"mem0\"", ctx.PluginID)
	}
	if ctx.Kind != plugincreds.KindUser {
		t.Fatalf("context.kind: got %q, want %q", ctx.Kind, plugincreds.KindUser)
	}
	if ctx.Sub != "user123" {
		t.Fatalf("context.sub: got %q, want \"user123\"", ctx.Sub)
	}

	// Verify X-Ainsel-Forwarded-Prefix
	prefix := upstreamReq.Header.Get("X-Ainsel-Forwarded-Prefix")
	if prefix != "/api/v1/plugins/mem0/api" {
		t.Fatalf("X-Ainsel-Forwarded-Prefix: got %q, want \"/api/v1/plugins/mem0/api\"", prefix)
	}

	// Verify X-Ainsel-Plugin-Id
	pluginID := upstreamReq.Header.Get("X-Ainsel-Plugin-Id")
	if pluginID != "mem0" {
		t.Fatalf("X-Ainsel-Plugin-Id: got %q, want \"mem0\"", pluginID)
	}
}

// TestPluginProxy_StripsSpoofedHeaders tests that spoofed headers are stripped and replaced.
func TestPluginProxy_StripsSpoofedHeaders(t *testing.T) {
	var upstreamReq *http.Request
	upstreamSvc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamReq = r
		w.Header().Set("Set-Cookie", "upstream=cookie")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstreamSvc.Close()

	plugin := pluginCR("mem0", false)
	plugin = pluginCRReady(plugin, true, "", "", nil)

	manifest := &pluginpkg.Manifest{
		ID:        "mem0",
		PluginAPI: "1.0",
		Version:   "1.0.0",
		API:       &pluginpkg.APISpec{BasePath: "/api"},
	}
	manifestBytes, _ := json.Marshal(manifest)
	plugin.Status.Manifest = &runtime.RawExtension{Raw: manifestBytes}

	plugin.Spec.ApprovedPermissions = []string{"agents:read"}

	endpoint := net.JoinHostPort("127.0.0.1", strings.Split(upstreamSvc.Listener.Addr().String(), ":")[1])
	plugin.Status.VerifiedEndpoints = []string{endpoint}

	s := testServer(t, plugin)
	registerPluginRoutes(s)

	secret := make([]byte, 32)
	for i := 0; i < len(secret); i++ {
		secret[i] = byte(i % 256)
	}
	issuer, _ := plugincreds.NewIssuer(secret, 5*time.Minute)
	s.SetPluginCredentialIssuer(issuer)

	testUser := &oidc.User{
		Sub:      "user123",
		Username: "testuser",
	}

	s.SetAuthMiddleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := oidc.ContextWithUser(r.Context(), testUser)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/plugins/mem0/api/test", nil)
	// Add spoofed headers
	req.Header.Set("Authorization", "Bearer session")
	req.Header.Set("Cookie", "a=b")
	req.Header.Set("Proxy-Authorization", "forged")
	req.Header.Set("X-Ainsel-Credential", "forged")
	req.Header.Set("X-Ainsel-Context", "forged")
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("Forwarded", "by=forged")

	s.ServeHTTP(rec, req)

	// Verify spoofed headers are not sent upstream
	if upstreamReq.Header.Get("Authorization") != "" {
		t.Fatal("Authorization header not stripped")
	}
	if upstreamReq.Header.Get("Cookie") != "" {
		t.Fatal("Cookie header not stripped")
	}
	if upstreamReq.Header.Get("X-Forwarded-For") != "" {
		t.Fatal("X-Forwarded-For header not stripped")
	}
	if upstreamReq.Header.Get("Proxy-Authorization") != "" {
		t.Fatal("Proxy-Authorization header not stripped")
	}
	if upstreamReq.Header.Get("Forwarded") != "" {
		t.Fatal("Forwarded header not stripped")
	}

	// X-Ainsel-Credential should be the hub-minted one, not "forged"
	cred := upstreamReq.Header.Get("X-Ainsel-Credential")
	if !strings.HasPrefix(cred, "ainselp_") {
		t.Fatalf("X-Ainsel-Credential: got %q, should be minted not forged", cred)
	}

	if ctxHdr := upstreamReq.Header.Get("X-Ainsel-Context"); ctxHdr == "" || ctxHdr == "forged" {
		t.Fatalf("X-Ainsel-Context: got %q, should be hub-built", ctxHdr)
	}

	// Response should not have upstream Set-Cookie
	if rec.Header().Get("Set-Cookie") != "" {
		t.Fatal("Set-Cookie from upstream not stripped")
	}

	// Response should have X-Content-Type-Options
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("X-Content-Type-Options: got %q, want \"nosniff\"", rec.Header().Get("X-Content-Type-Options"))
	}
}

// TestPluginProxy_OnlyVerifiedEndpoints tests that requests fail with 503 when no verified endpoints.
func TestPluginProxy_OnlyVerifiedEndpoints(t *testing.T) {
	plugin := pluginCR("mem0", false)

	plugin = pluginCRReady(plugin, true, "", "", nil)

	manifest := &pluginpkg.Manifest{
		ID:        "mem0",
		PluginAPI: "1.0",
		Version:   "1.0.0",
		API:       &pluginpkg.APISpec{BasePath: "/api"},
	}
	manifestBytes, _ := json.Marshal(manifest)
	plugin.Status.Manifest = &runtime.RawExtension{Raw: manifestBytes}

	plugin.Status.VerifiedEndpoints = []string{} // Empty!

	s := testServer(t, plugin)
	registerPluginRoutes(s)

	secret := make([]byte, 32)
	for i := 0; i < len(secret); i++ {
		secret[i] = byte(i % 256)
	}
	issuer, _ := plugincreds.NewIssuer(secret, 5*time.Minute)
	s.SetPluginCredentialIssuer(issuer)

	testUser := &oidc.User{
		Sub:      "user123",
		Username: "testuser",
	}

	s.SetAuthMiddleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := oidc.ContextWithUser(r.Context(), testUser)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/plugins/mem0/api/test", nil)
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rec.Code)
	}

	var errResp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if errResp["error"] != "plugin has no verified endpoints" {
		t.Fatalf("error message: got %q", errResp["error"])
	}
}

// TestPluginProxy_DisabledPlugin tests that disabled/not-ready plugins return 404.
func TestPluginProxy_DisabledPlugin(t *testing.T) {
	tests := []struct {
		name     string
		disabled bool
		ready    bool
		wantCode int
	}{
		{"disabled", true, false, http.StatusNotFound},
		{"not ready", false, false, http.StatusNotFound},
		{"enabled and ready", false, true, http.StatusServiceUnavailable}, // 503 because no endpoints
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := pluginCR("mem0", tt.disabled)
			plugin = pluginCRReady(plugin, tt.ready, "", "", nil)

			manifest := &pluginpkg.Manifest{
				ID:        "mem0",
				PluginAPI: "1.0",
				Version:   "1.0.0",
				API:       &pluginpkg.APISpec{BasePath: "/api"},
			}
			manifestBytes, _ := json.Marshal(manifest)
			plugin.Status.Manifest = &runtime.RawExtension{Raw: manifestBytes}

			s := testServer(t, plugin)
			registerPluginRoutes(s)

			secret := make([]byte, 32)
			for i := 0; i < len(secret); i++ {
				secret[i] = byte(i % 256)
			}
			issuer, _ := plugincreds.NewIssuer(secret, 5*time.Minute)
			s.SetPluginCredentialIssuer(issuer)

			testUser := &oidc.User{
				Sub:      "user123",
				Username: "testuser",
			}

			s.SetAuthMiddleware(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					ctx := oidc.ContextWithUser(r.Context(), testUser)
					next.ServeHTTP(w, r.WithContext(ctx))
				})
			})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/api/v1/plugins/mem0/api/test", nil)
			s.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("got %d, want %d", rec.Code, tt.wantCode)
			}
		})
	}
}

// TestPluginProxy_NoSurface tests that missing manifest surfaces return 404.
func TestPluginProxy_NoSurface(t *testing.T) {
	plugin := pluginCR("mem0", false)
	plugin = pluginCRReady(plugin, true, "", "", nil)

	manifest := &pluginpkg.Manifest{
		ID:        "mem0",
		PluginAPI: "1.0",
		Version:   "1.0.0",
		// No MCP!
	}
	manifestBytes, _ := json.Marshal(manifest)
	plugin.Status.Manifest = &runtime.RawExtension{Raw: manifestBytes}
	plugin.Status.VerifiedEndpoints = []string{"127.0.0.1:9999"}

	s := testServer(t, plugin)
	registerPluginRoutes(s)

	secret := make([]byte, 32)
	for i := 0; i < len(secret); i++ {
		secret[i] = byte(i % 256)
	}
	issuer, _ := plugincreds.NewIssuer(secret, 5*time.Minute)
	s.SetPluginCredentialIssuer(issuer)

	testUser := &oidc.User{
		Sub:      "user123",
		Username: "testuser",
	}

	s.SetAuthMiddleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := oidc.ContextWithUser(r.Context(), testUser)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/plugins/mem0/mcp", nil)
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", rec.Code)
	}
}

// TestPluginProxy_Unreachable tests that upstream connection failures return 502.
func TestPluginProxy_Unreachable(t *testing.T) {
	plugin := pluginCR("mem0", false)

	plugin = pluginCRReady(plugin, true, "", "", nil)

	manifest := &pluginpkg.Manifest{
		ID:        "mem0",
		PluginAPI: "1.0",
		Version:   "1.0.0",
		API:       &pluginpkg.APISpec{BasePath: "/api"},
	}
	manifestBytes, _ := json.Marshal(manifest)
	plugin.Status.Manifest = &runtime.RawExtension{Raw: manifestBytes}

	plugin.Status.VerifiedEndpoints = []string{"127.0.0.1:1"}

	s := testServer(t, plugin)
	registerPluginRoutes(s)

	secret := make([]byte, 32)
	for i := 0; i < len(secret); i++ {
		secret[i] = byte(i % 256)
	}
	issuer, _ := plugincreds.NewIssuer(secret, 5*time.Minute)
	s.SetPluginCredentialIssuer(issuer)

	testUser := &oidc.User{
		Sub:      "user123",
		Username: "testuser",
	}

	s.SetAuthMiddleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := oidc.ContextWithUser(r.Context(), testUser)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/plugins/mem0/api/test", nil)
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("got %d, want 502", rec.Code)
	}

	var errResp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err == nil {
		if !strings.Contains(errResp["error"], "unreachable") {
			t.Fatalf("error message: got %q", errResp["error"])
		}
	}
}

// TestPluginProxy_CredentialsDisabled tests that API/MCP without issuer return 503.
func TestPluginProxy_CredentialsDisabled(t *testing.T) {
	plugin := pluginCR("mem0", false)

	plugin = pluginCRReady(plugin, true, "", "", nil)

	manifest := &pluginpkg.Manifest{
		ID:        "mem0",
		PluginAPI: "1.0",
		Version:   "1.0.0",
		API:       &pluginpkg.APISpec{BasePath: "/api"},
	}
	manifestBytes, _ := json.Marshal(manifest)
	plugin.Status.Manifest = &runtime.RawExtension{Raw: manifestBytes}

	plugin.Status.VerifiedEndpoints = []string{"127.0.0.1:9999"}

	s := testServer(t, plugin)
	registerPluginRoutes(s)
	// Don't set issuer! This simulates credentials being disabled

	testUser := &oidc.User{
		Sub:      "user123",
		Username: "testuser",
	}

	s.SetAuthMiddleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := oidc.ContextWithUser(r.Context(), testUser)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/plugins/mem0/api/test", nil)
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rec.Code)
	}

	var errResp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if errResp["error"] != "plugin credentials are not configured" {
		t.Fatalf("error message: got %q", errResp["error"])
	}
}

// TestPluginProxy_ManagementRoutesUnchanged tests that management routes still work.
func TestPluginProxy_ManagementRoutesUnchanged(t *testing.T) {
	plugin := pluginCR("mem0", false)
	plugin = pluginCRReady(plugin, true, "", "", nil)

	s := testServer(t, plugin)
	registerPluginRoutes(s)

	secret := make([]byte, 32)
	for i := 0; i < len(secret); i++ {
		secret[i] = byte(i % 256)
	}
	issuer, _ := plugincreds.NewIssuer(secret, 5*time.Minute)
	s.SetPluginCredentialIssuer(issuer)

	// Admin user
	testUser := &oidc.User{
		Sub:      "admin",
		Username: "admin",
		Roles:    []string{"admin"},
	}

	s.SetAuthMiddleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := oidc.ContextWithUser(r.Context(), testUser)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})

	// GET /api/v1/plugins/mem0 should return plugin info
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/plugins/mem0", nil)
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/plugins/mem0: got %d, want 200", rec.Code)
	}

	var resp PluginResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.ID != "mem0" {
		t.Fatalf("plugin id: got %q, want \"mem0\"", resp.ID)
	}
}

// TestPluginProxy_DevModeForwardsWithoutCredential: with no auth middleware and
// no issuer there is no user, so the request is forwarded without credentials.
func TestPluginProxy_DevModeForwardsWithoutCredential(t *testing.T) {
	var gotCred, gotPath string
	called := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		gotCred = r.Header.Get("X-Ainsel-Credential")
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	plugin := pluginCRReady(pluginCR("mem0", false), true, "", "", nil)
	raw, _ := json.Marshal(&pluginpkg.Manifest{ID: "mem0", PluginAPI: "1.0", Version: "1.0.0", API: &pluginpkg.APISpec{BasePath: "/api"}})
	plugin.Status.Manifest = &runtime.RawExtension{Raw: raw}
	plugin.Status.VerifiedEndpoints = []string{upstream.Listener.Addr().String()}

	s := testServer(t, plugin)
	registerPluginRoutes(s)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/plugins/mem0/api/memories", nil))

	if rec.Code != http.StatusOK || !called {
		t.Fatalf("got %d (upstream called=%v), want 200 forwarded", rec.Code, called)
	}
	if gotPath != "/api/memories" || gotCred != "" {
		t.Fatalf("upstream saw path %q credential %q", gotPath, gotCred)
	}
}
