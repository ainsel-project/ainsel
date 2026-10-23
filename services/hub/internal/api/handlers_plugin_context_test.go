package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	"github.com/ainsel-project/ainsel/services/hub/internal/plugincreds"
	connectorv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"
	"k8s.io/apimachinery/pkg/types"
)

func TestPluginContext_PluginCaller(t *testing.T) {
	s, _, issuer, _ := pluginCredentialTestServer(t)
	store := s.authzStore

	ctx := context.Background()

	// Create groups
	g1, err := store.CreateGroup(ctx, "g1", "Team A")
	if err != nil {
		t.Fatalf("create g1: %v", err)
	}

	// Update plugin to have groupAccess and approved permissions
	var p connectorv1alpha1.Plugin
	if err := s.client.Get(ctx, types.NamespacedName{Name: "mem0", Namespace: s.ns}, &p); err != nil {
		t.Fatalf("get plugin: %v", err)
	}
	p.Spec.ApprovedPermissions = []string{"agents:read"}
	p.Spec.GroupAccess = []string{g1.ID}
	if err := s.client.Update(ctx, &p); err != nil {
		t.Fatalf("update plugin: %v", err)
	}

	// Create issuer
	s.SetPluginCredentialIssuer(issuer)

	// Mint a plugin-kind token
	tok := mintPluginPluginToken(t, issuer, "mem0", []string{"agents:read"},
		[]plugincreds.GroupGrant{{ID: g1.ID, Role: "writer"}})

	// Handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pc, ok := PluginCallerFrom(r.Context())
		if !ok {
			t.Fatal("PluginCaller not found in context")
		}
		if pc.Kind != plugincreds.KindPlugin {
			t.Errorf("expected kind plugin, got %s", pc.Kind)
		}
		if pc.PluginID != "mem0" {
			t.Errorf("expected pluginId mem0, got %s", pc.PluginID)
		}
		if len(pc.Permissions) != 1 || pc.Permissions[0] != "agents:read" {
			t.Errorf("expected permissions [agents:read], got %v", pc.Permissions)
		}
		if len(pc.Groups) != 1 || pc.Groups[0].ID != g1.ID || pc.Groups[0].Role != "writer" {
			t.Errorf("expected one group with writer role, got %v", pc.Groups)
		}

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})

	disabledGuard := authz.NewDisabledGuard(s.authzStore)
	fallbackChain := func(next http.Handler) http.Handler { return next }
	wrappedHandler := s.PluginCredentialMiddleware(disabledGuard, fallbackChain)(testHandler)

	req := httptest.NewRequest("GET", "/api/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPluginContext_RegularUser(t *testing.T) {
	s := testServer(t, pluginCRReady(pluginCR("mem0", false), true, "", "", []string{"agents:read"}))
	s.authzStore = newFakeAuthzStore()
	store := s.authzStore
	ctx := context.Background()

	// Create group and user
	g2, err := store.CreateGroup(ctx, "g2", "Team B")
	if err != nil {
		t.Fatalf("create g2: %v", err)
	}

	user, err := store.UpsertUser(ctx, "user1", "user1@example.com", "user1")
	if err != nil {
		t.Fatalf("upsert user: %v", err)
	}

	// Add user to group as owner
	if err := store.AddGroupMember(ctx, g2.ID, user.ID, authz.RoleOwner); err != nil {
		t.Fatalf("add group member: %v", err)
	}

	s.authzChecker = authz.NewChecker(s.authzStore, authz.NewGroupCache(func(uid string) (map[string]authz.GroupRole, error) {
		return nil, nil
	}, time.Minute))

	// Create session for regular user
	sessionUser := &oidc.User{Sub: user.ID, Username: user.Username}
	ctx = oidc.ContextWithUser(ctx, sessionUser)

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, ok := PluginCallerFrom(r.Context())
		if ok {
			t.Error("PluginCaller should not be set for regular user")
		}

		// For regular users, they should have their own group context
		u, ok := oidc.FromContext(r.Context())
		if !ok {
			t.Fatal("User not found in context")
		}

		if u.Sub != user.ID {
			t.Errorf("expected sub %s, got %s", user.ID, u.Sub)
		}

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})

	req := httptest.NewRequest("GET", "/api/v1/agents", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	testHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPluginContext_GET(t *testing.T) {
	s, _, issuer, _ := pluginCredentialTestServer(t)
	store := s.authzStore

	ctx := context.Background()

	// Create group
	g1, err := store.CreateGroup(ctx, "g1", "Team A")
	if err != nil {
		t.Fatalf("create g1: %v", err)
	}

	// Update plugin groupAccess
	var p connectorv1alpha1.Plugin
	if err := s.client.Get(ctx, types.NamespacedName{Name: "mem0", Namespace: s.ns}, &p); err != nil {
		t.Fatalf("get plugin: %v", err)
	}
	p.Spec.ApprovedPermissions = []string{"agents:read"}
	p.Spec.GroupAccess = []string{g1.ID}
	if err := s.client.Update(ctx, &p); err != nil {
		t.Fatalf("update plugin: %v", err)
	}

	s.SetPluginCredentialIssuer(issuer)

	// Register the handler
	s.mux.HandleFunc("/api/v1/plugin-context", s.handlePluginContext)

	// Mint plugin-kind token
	tok := mintPluginPluginToken(t, issuer, "mem0", []string{"agents:read"},
		[]plugincreds.GroupGrant{{ID: g1.ID, Role: "writer"}})

	req := httptest.NewRequest("GET", "/api/v1/plugin-context", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()

	// Wrap with middleware
	disabledGuard := authz.NewDisabledGuard(s.authzStore)
	fallbackChain := func(next http.Handler) http.Handler { return next }
	handler := s.PluginCredentialMiddleware(disabledGuard, fallbackChain)(s.mux)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}

	if resp["kind"] != plugincreds.KindPlugin {
		t.Errorf("expected kind plugin, got %v", resp["kind"])
	}
	if resp["pluginId"] != "mem0" {
		t.Errorf("expected pluginId mem0, got %v", resp["pluginId"])
	}
	if resp["isAdmin"] != false {
		t.Errorf("expected isAdmin false for plugin kind, got %v", resp["isAdmin"])
	}
}

func TestPluginVisible_FiltersByGroups(t *testing.T) {
	s := testServer(t, pluginCRReady(pluginCR("mem0", false), true, "", "", []string{"agents:read"}))
	s.authzStore = newFakeAuthzStore()
	store := s.authzStore
	ctx := context.Background()

	// Create groups
	g1, err := store.CreateGroup(ctx, "g1", "Group 1")
	if err != nil {
		t.Fatalf("create g1: %v", err)
	}
	g2, err := store.CreateGroup(ctx, "g2", "Group 2")
	if err != nil {
		t.Fatalf("create g2: %v", err)
	}

	// Set resources to groups
	if err := store.SetResourceGroup(ctx, "agent", "a1", g1.ID, false); err != nil {
		t.Fatalf("set a1 group: %v", err)
	}
	if err := store.SetResourceGroup(ctx, "agent", "a2", g2.ID, false); err != nil {
		t.Fatalf("set a2 group: %v", err)
	}

	s.authzChecker = authz.NewChecker(store, authz.NewGroupCache(func(uid string) (map[string]authz.GroupRole, error) {
		return nil, nil
	}, time.Minute))

	// Create issuer and mint token with g1 access only
	secret := make([]byte, 32)
	issuer, _ := plugincreds.NewIssuer(secret, 5*time.Minute)
	s.SetPluginCredentialIssuer(issuer)

	var p connectorv1alpha1.Plugin
	if err := s.client.Get(ctx, types.NamespacedName{Name: "mem0", Namespace: s.ns}, &p); err != nil {
		t.Fatalf("get plugin: %v", err)
	}
	p.Spec.ApprovedPermissions = []string{"agents:read"}
	p.Spec.GroupAccess = []string{g1.ID}
	if err := s.client.Update(ctx, &p); err != nil {
		t.Fatalf("update plugin: %v", err)
	}

	tok := mintPluginPluginToken(t, issuer, "mem0", []string{"agents:read"},
		[]plugincreds.GroupGrant{{ID: g1.ID, Role: "writer"}})

	// Register handler
	s.mux.HandleFunc("/api/v1/plugin-context/visible", s.handlePluginContextVisible)

	req := httptest.NewRequest("POST", "/api/v1/plugin-context/visible",
		newJSONReader(map[string]interface{}{
			"resourceType": "agent",
			"names":        []string{"a1", "a2"},
		}))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	disabledGuard := authz.NewDisabledGuard(s.authzStore)
	fallbackChain := func(next http.Handler) http.Handler { return next }
	handler := s.PluginCredentialMiddleware(disabledGuard, fallbackChain)(s.mux)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}

	visible := resp["visible"].([]interface{})
	if len(visible) != 1 || visible[0] != "a1" {
		t.Errorf("expected visible [a1], got %v", visible)
	}
}

func TestPluginVisible_RequiresPermission(t *testing.T) {
	s, _, issuer, _ := pluginCredentialTestServer(t)
	store := s.authzStore
	ctx := context.Background()

	g1, _ := store.CreateGroup(ctx, "g1", "Team A")

	var p connectorv1alpha1.Plugin
	if err := s.client.Get(ctx, types.NamespacedName{Name: "mem0", Namespace: s.ns}, &p); err != nil {
		t.Fatalf("get plugin: %v", err)
	}
	p.Spec.ApprovedPermissions = []string{"agents:read"} // No personas:read
	p.Spec.GroupAccess = []string{g1.ID}
	if err := s.client.Update(ctx, &p); err != nil {
		t.Fatalf("update plugin: %v", err)
	}

	// Token without personas:read
	tok := mintPluginPluginToken(t, issuer, "mem0", []string{"agents:read"},
		[]plugincreds.GroupGrant{{ID: g1.ID, Role: "writer"}})

	s.mux.HandleFunc("/api/v1/plugin-context/visible", s.handlePluginContextVisible)

	req := httptest.NewRequest("POST", "/api/v1/plugin-context/visible",
		newJSONReader(map[string]interface{}{
			"resourceType": "persona",
			"names":        []string{"p1"},
		}))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	disabledGuard := authz.NewDisabledGuard(s.authzStore)
	fallbackChain := func(next http.Handler) http.Handler { return next }
	handler := s.PluginCredentialMiddleware(disabledGuard, fallbackChain)(s.mux)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for missing permission, got %d", rec.Code)
	}
}

func TestPluginVisible_Validation(t *testing.T) {
	s, _, issuer, _ := pluginCredentialTestServer(t)

	var p connectorv1alpha1.Plugin
	ctx := context.Background()
	if err := s.client.Get(ctx, types.NamespacedName{Name: "mem0", Namespace: s.ns}, &p); err != nil {
		t.Fatalf("get plugin: %v", err)
	}
	p.Spec.ApprovedPermissions = []string{"agents:read"}
	if err := s.client.Update(ctx, &p); err != nil {
		t.Fatalf("update plugin: %v", err)
	}

	tok := mintPluginPluginToken(t, issuer, "mem0", []string{"agents:read"}, nil)

	s.mux.HandleFunc("/api/v1/plugin-context/visible", s.handlePluginContextVisible)

	tests := []struct {
		name       string
		body       map[string]interface{}
		method     string
		statusCode int
	}{
		{
			name:       "unknown resourceType",
			body:       map[string]interface{}{"resourceType": "invalid", "names": []string{"a1"}},
			method:     "POST",
			statusCode: http.StatusBadRequest,
		},
		{
			name:       "zero names",
			body:       map[string]interface{}{"resourceType": "agent", "names": []string{}},
			method:     "POST",
			statusCode: http.StatusBadRequest,
		},
		{
			name:       "too many names",
			body:       map[string]interface{}{"resourceType": "agent", "names": makeLargeSlice(501)},
			method:     "POST",
			statusCode: http.StatusBadRequest,
		},
		{
			name:       "GET method not allowed",
			body:       map[string]interface{}{},
			method:     "GET",
			statusCode: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/api/v1/plugin-context/visible", nil)
			if tt.method == "POST" {
				req = httptest.NewRequest(tt.method, "/api/v1/plugin-context/visible",
					newJSONReader(tt.body))
				req.Header.Set("Content-Type", "application/json")
			}
			req.Header.Set("Authorization", "Bearer "+tok)
			rec := httptest.NewRecorder()

			disabledGuard := authz.NewDisabledGuard(s.authzStore)
			fallbackChain := func(next http.Handler) http.Handler { return next }
			handler := s.PluginCredentialMiddleware(disabledGuard, fallbackChain)(s.mux)

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.statusCode {
				t.Errorf("expected %d, got %d: %s", tt.statusCode, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestPluginBackgroundCredential_FromVerifiedEndpoint(t *testing.T) {
	s, _, _, _ := pluginCredentialTestServer(t)
	ctx := context.Background()

	var p connectorv1alpha1.Plugin
	if err := s.client.Get(ctx, types.NamespacedName{Name: "mem0", Namespace: s.ns}, &p); err != nil {
		t.Fatalf("get plugin: %v", err)
	}
	p.Status.VerifiedEndpoints = []string{"10.0.0.5:8080"}
	if err := s.client.Update(ctx, &p); err != nil {
		t.Fatalf("update plugin: %v", err)
	}

	s.mux.HandleFunc("/api/internal/plugins/", s.handleInternalPluginCredentials)

	req := httptest.NewRequest("POST", "/api/internal/plugins/mem0/credentials", nil)
	req.RemoteAddr = "10.0.0.5:51234"
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}

	if _, ok := resp["token"]; !ok {
		t.Error("expected token in response")
	}
	if _, ok := resp["expiresAt"]; !ok {
		t.Error("expected expiresAt in response")
	}
}

func TestPluginBackgroundCredential_IgnoresForwardedFor(t *testing.T) {
	s, _, _, _ := pluginCredentialTestServer(t)
	ctx := context.Background()

	var p connectorv1alpha1.Plugin
	if err := s.client.Get(ctx, types.NamespacedName{Name: "mem0", Namespace: s.ns}, &p); err != nil {
		t.Fatalf("get plugin: %v", err)
	}
	p.Status.VerifiedEndpoints = []string{"10.0.0.5:8080"}
	if err := s.client.Update(ctx, &p); err != nil {
		t.Fatalf("update plugin: %v", err)
	}

	s.mux.HandleFunc("/api/internal/plugins/", s.handleInternalPluginCredentials)

	req := httptest.NewRequest("POST", "/api/internal/plugins/mem0/credentials", nil)
	req.RemoteAddr = "10.0.0.9:1"
	req.Header.Set("X-Forwarded-For", "10.0.0.5")
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 when X-Forwarded-For is spoofed, got %d", rec.Code)
	}
}

func TestPluginBackgroundCredential_NotReady(t *testing.T) {
	s := testServer(t, pluginCRReady(pluginCR("mem0", false), false, "", "", []string{"agents:read"}))

	s.mux.HandleFunc("/api/internal/plugins/", s.handleInternalPluginCredentials)

	req := httptest.NewRequest("POST", "/api/internal/plugins/mem0/credentials", nil)
	req.RemoteAddr = "10.0.0.5:51234"
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for not-ready plugin, got %d", rec.Code)
	}
}

// Helper functions

func newJSONReader(v interface{}) io.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

func makeLargeSlice(size int) []string {
	s := make([]string, size)
	for i := 0; i < size; i++ {
		s[i] = "a" + string(rune(i))
	}
	return s
}
