package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/auditevents"
	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	"github.com/ainsel-project/ainsel/services/hub/internal/plugincreds"
	connectorv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
)

// pluginCredentialTestServer wires the API server with plugin credential support
// for testing. The Plugin CR "mem0" is pre-seeded as Ready with approved permissions
// and group access.
func pluginCredentialTestServer(t *testing.T) (*Server, *fakeAuditStore, *plugincreds.Issuer, string) {
	t.Helper()

	// Create plugin CR with approved permissions and group access
	plugin := pluginCR("mem0", false)
	plugin = pluginCRReady(plugin, true, "", "", []string{"agents:read", "agents:write", "personas:read"})
	plugin.Spec.ApprovedPermissions = []string{"agents:read", "agents:write", "personas:read"}
	plugin.Spec.GroupAccess = []string{}

	s := testServer(t, plugin)
	s.authzStore = newFakeAuthzStore()
	s.authzChecker = authz.NewChecker(s.authzStore, authz.NewGroupCache(func(string) (map[string]authz.GroupRole, error) {
		return nil, nil
	}, time.Minute))

	// Wire the audit store
	audit := &fakeAuditStore{}
	s.SetAuditEventStore(audit)

	// Create plugin credentials issuer
	secret := make([]byte, 32)
	for i := 0; i < len(secret); i++ {
		secret[i] = byte(i % 256)
	}
	issuer, err := plugincreds.NewIssuer(secret, 5*time.Minute)
	if err != nil {
		t.Fatalf("create issuer: %v", err)
	}
	s.SetPluginCredentialIssuer(issuer)

	// Convert secret to token form (base64 would be normal, but any 32+ bytes works)
	return s, audit, issuer, string(secret)
}

// mintUserPluginToken creates a user-kind plugin credential token for testing
func mintUserPluginToken(t *testing.T, issuer *plugincreds.Issuer, pluginID, sub, username string, admin bool, perms []string, groups []plugincreds.GroupGrant) string {
	t.Helper()
	tok, _, err := issuer.Issue(plugincreds.Claims{
		PluginID:    pluginID,
		Kind:        plugincreds.KindUser,
		Sub:         sub,
		Username:    username,
		Admin:       admin,
		Permissions: perms,
		Groups:      groups,
	})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return tok
}

// mintAgentPluginToken creates an agent-kind plugin credential token for testing
func mintAgentPluginToken(t *testing.T, issuer *plugincreds.Issuer, pluginID, agentName, invocationID string, perms []string, groups []plugincreds.GroupGrant) string {
	t.Helper()
	tok, _, err := issuer.Issue(plugincreds.Claims{
		PluginID:     pluginID,
		Kind:         plugincreds.KindAgent,
		Sub:          "agent:" + agentName,
		Permissions:  perms,
		Groups:       groups,
		InvocationID: invocationID,
	})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return tok
}

// mintPluginPluginToken creates a plugin-kind plugin credential token for testing
func mintPluginPluginToken(t *testing.T, issuer *plugincreds.Issuer, pluginID string, perms []string, groups []plugincreds.GroupGrant) string {
	t.Helper()
	tok, _, err := issuer.Issue(plugincreds.Claims{
		PluginID:    pluginID,
		Kind:        plugincreds.KindPlugin,
		Sub:         "plugin:" + pluginID,
		Permissions: perms,
		Groups:      groups,
	})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return tok
}

func TestPluginCredential_UserAllowedRoute(t *testing.T) {
	s, _, issuer, _ := pluginCredentialTestServer(t)

	// Wire the middleware: a simple handler that returns the current auth info
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	disabledGuard := authz.NewDisabledGuard(s.authzStore)
	fallbackChain := func(next http.Handler) http.Handler { return next }
	wrappedHandler := s.PluginCredentialMiddleware(disabledGuard, fallbackChain)(testHandler)

	// Create request with user-kind token for agents:read (GET allowed)
	tok := mintUserPluginToken(t, issuer, "mem0", "user1", "user1", false, []string{"agents:read"}, nil)
	req := httptest.NewRequest("GET", "/api/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPluginCredential_RouteNotGranted(t *testing.T) {
	s, _, issuer, _ := pluginCredentialTestServer(t)

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	disabledGuard := authz.NewDisabledGuard(s.authzStore)
	fallbackChain := func(next http.Handler) http.Handler { return next }
	wrappedHandler := s.PluginCredentialMiddleware(disabledGuard, fallbackChain)(testHandler)

	// Token only has agents:read, try personas:read
	tok := mintUserPluginToken(t, issuer, "mem0", "user1", "user1", false, []string{"agents:read"}, nil)

	req := httptest.NewRequest("GET", "/api/v1/personas", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for personas:read, got %d", rec.Code)
	}
	var errResp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &errResp)
	if msg, ok := errResp["error"].(string); !ok || msg != "plugin lacks permission personas:read" {
		t.Errorf("expected permission error, got: %v", errResp)
	}

	// Try a route that's not available to plugins
	req = httptest.NewRequest("GET", "/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec = httptest.NewRecorder()

	wrappedHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for /api/v1/users, got %d", rec.Code)
	}
	errResp = make(map[string]interface{})
	json.Unmarshal(rec.Body.Bytes(), &errResp)
	if msg, ok := errResp["error"].(string); !ok || msg != "route not available to plugin credentials" {
		t.Errorf("expected route unavailable error, got: %v", errResp)
	}
}

func TestPluginCredential_NarrowedApprovalsApplyImmediately(t *testing.T) {
	s, _, issuer, _ := pluginCredentialTestServer(t)

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	disabledGuard := authz.NewDisabledGuard(s.authzStore)
	fallbackChain := func(next http.Handler) http.Handler { return next }
	wrappedHandler := s.PluginCredentialMiddleware(disabledGuard, fallbackChain)(testHandler)

	// Mint token with both permissions
	tok := mintUserPluginToken(t, issuer, "mem0", "user1", "user1", false,
		[]string{"agents:read", "personas:read"}, nil)

	// Request works before narrowing
	req := httptest.NewRequest("GET", "/api/v1/personas", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	wrappedHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("before narrowing: expected 200, got %d", rec.Code)
	}

	// Narrow the plugin's approved permissions
	var p connectorv1alpha1.Plugin
	if err := s.client.Get(context.Background(), types.NamespacedName{Name: "mem0", Namespace: s.ns}, &p); err != nil {
		t.Fatalf("get plugin: %v", err)
	}
	p.Spec.ApprovedPermissions = []string{"agents:read"}
	if err := s.client.Update(context.Background(), &p); err != nil {
		t.Fatalf("update plugin: %v", err)
	}

	// Same request should now fail
	req = httptest.NewRequest("GET", "/api/v1/personas", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec = httptest.NewRecorder()
	wrappedHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("after narrowing: expected 403, got %d", rec.Code)
	}
}

func TestPluginCredential_DisabledPluginRejected(t *testing.T) {
	s, _, issuer, _ := pluginCredentialTestServer(t)

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	disabledGuard := authz.NewDisabledGuard(s.authzStore)
	fallbackChain := func(next http.Handler) http.Handler { return next }
	wrappedHandler := s.PluginCredentialMiddleware(disabledGuard, fallbackChain)(testHandler)

	tok := mintUserPluginToken(t, issuer, "mem0", "user1", "user1", false, []string{"agents:read"}, nil)

	// First verify it works
	req := httptest.NewRequest("GET", "/api/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	wrappedHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("working token: expected 200, got %d", rec.Code)
	}

	// Disable the plugin
	var p connectorv1alpha1.Plugin
	if err := s.client.Get(context.Background(), types.NamespacedName{Name: "mem0", Namespace: s.ns}, &p); err != nil {
		t.Fatalf("get plugin: %v", err)
	}
	p.Spec.Disabled = true
	if err := s.client.Update(context.Background(), &p); err != nil {
		t.Fatalf("update plugin: %v", err)
	}

	// Should now be rejected
	req = httptest.NewRequest("GET", "/api/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec = httptest.NewRecorder()
	wrappedHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("disabled plugin: expected 401, got %d", rec.Code)
	}

	// Also test missing plugin
	tok2 := mintUserPluginToken(t, issuer, "nonexistent", "user1", "user1", false, []string{"agents:read"}, nil)
	req = httptest.NewRequest("GET", "/api/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer "+tok2)
	rec = httptest.NewRecorder()
	wrappedHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("missing plugin: expected 401, got %d", rec.Code)
	}
}

func TestPluginCredential_PluginKindUsesGroups(t *testing.T) {
	// Create server with agents in different groups
	s := testServer(t, pluginCRReady(pluginCR("mem0", false), true, "", "", []string{"agents:read"}))
	s.authzStore = newFakeAuthzStore()
	store := s.authzStore
	ctx := context.Background()

	// Create groups and add agents
	if _, err := store.CreateGroup(ctx, "g1", "Group 1"); err != nil {
		t.Fatalf("create g1: %v", err)
	}
	if _, err := store.CreateGroup(ctx, "g2", "Group 2"); err != nil {
		t.Fatalf("create g2: %v", err)
	}

	// Set agent a1 in g1, a2 in g2
	if err := store.SetResourceGroup(ctx, "agent", "a1", "g1", false); err != nil {
		t.Fatalf("set a1 group: %v", err)
	}
	if err := store.SetResourceGroup(ctx, "agent", "a2", "g2", false); err != nil {
		t.Fatalf("set a2 group: %v", err)
	}

	// Wire with plugin credential middleware
	s.authzChecker = authz.NewChecker(store, authz.NewGroupCache(func(uid string) (map[string]authz.GroupRole, error) {
		return nil, nil
	}, time.Minute))

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	disabledGuard := authz.NewDisabledGuard(s.authzStore)
	fallbackChain := func(next http.Handler) http.Handler { return next }
	wrappedHandler := s.PluginCredentialMiddleware(disabledGuard, fallbackChain)(testHandler)

	// Update plugin to have groupAccess for g1 only and approved permissions
	var p connectorv1alpha1.Plugin
	if err := s.client.Get(ctx, types.NamespacedName{Name: "mem0", Namespace: s.ns}, &p); err != nil {
		t.Fatalf("get plugin: %v", err)
	}
	p.Spec.ApprovedPermissions = []string{"agents:read"}
	p.Spec.GroupAccess = []string{"g1"}
	if err := s.client.Update(ctx, &p); err != nil {
		t.Fatalf("update plugin: %v", err)
	}

	// Create issuer
	secret := make([]byte, 32)
	issuer, _ := plugincreds.NewIssuer(secret, 5*time.Minute)
	s.SetPluginCredentialIssuer(issuer)

	// Token has groups from g1
	tok := mintPluginPluginToken(t, issuer, "mem0", []string{"agents:read"},
		[]plugincreds.GroupGrant{{ID: "g1", Role: "writer"}})

	// Request for a1 (in g1) should work - middleware doesn't enforce group access,
	// it just passes the groups to the context for the handler to check
	req := httptest.NewRequest("GET", "/api/v1/agents/a1", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	wrappedHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("agent in group: expected 200, got %d", rec.Code)
	}

	// Request for a2 (in g2) also reaches the handler at the middleware level
	// (the handler would check group access if needed, but our test handler doesn't)
	req = httptest.NewRequest("GET", "/api/v1/agents/a2", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec = httptest.NewRecorder()
	wrappedHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("middleware doesn't enforce group access: expected 200, got %d", rec.Code)
	}
}

func TestPluginCredential_DoesNotPersistIdentity(t *testing.T) {
	s, _, issuer, _ := pluginCredentialTestServer(t)
	store := s.authzStore

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	disabledGuard := authz.NewDisabledGuard(s.authzStore)
	fallbackChain := func(next http.Handler) http.Handler { return next }
	wrappedHandler := s.PluginCredentialMiddleware(disabledGuard, fallbackChain)(testHandler)

	tok := mintPluginPluginToken(t, issuer, "mem0", []string{"agents:read"}, nil)

	req := httptest.NewRequest("GET", "/api/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	wrappedHandler.ServeHTTP(rec, req)

	// Check that plugin:mem0 was NOT persisted to the authz store
	ctx := context.Background()
	_, err := store.GetUser(ctx, "plugin:mem0")
	if err != authz.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestPluginCredential_WriteAudited(t *testing.T) {
	s, audit, issuer, _ := pluginCredentialTestServer(t)

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	disabledGuard := authz.NewDisabledGuard(s.authzStore)
	fallbackChain := func(next http.Handler) http.Handler { return next }
	wrappedHandler := s.PluginCredentialMiddleware(disabledGuard, fallbackChain)(testHandler)

	tok := mintUserPluginToken(t, issuer, "mem0", "user1", "user1", false, []string{"agents:write"}, nil)

	req := httptest.NewRequest("PATCH", "/api/v1/agents/a1", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	wrappedHandler.ServeHTTP(rec, req)

	// Check audit event was recorded
	if len(audit.inserted) != 1 {
		t.Errorf("expected 1 audit event, got %d", len(audit.inserted))
		return
	}
	e := audit.inserted[0]
	if e.EventType != auditevents.TypePluginAPIWrite {
		t.Errorf("expected TypePluginAPIWrite, got %s", e.EventType)
	}
	if e.ActorSub != "user1" {
		t.Errorf("expected actor user1, got %s", e.ActorSub)
	}
	// Target format: <pluginId> <METHOD> <path>
	expectedTarget := "mem0 PATCH /api/v1/agents/a1"
	if e.Target != expectedTarget {
		t.Errorf("expected target %q, got %q", expectedTarget, e.Target)
	}
	if e.Outcome != auditevents.OutcomeSuccess {
		t.Errorf("expected success outcome, got %s", e.Outcome)
	}
}

func TestPluginCredential_FallbackUntouched(t *testing.T) {
	s, _, _, _ := pluginCredentialTestServer(t)

	handlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	fallbackCalled := false
	fallbackChain := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fallbackCalled = true
			next.ServeHTTP(w, r)
		})
	}

	disabledGuard := authz.NewDisabledGuard(s.authzStore)
	wrappedHandler := s.PluginCredentialMiddleware(disabledGuard, fallbackChain)(testHandler)

	// Non-plugin bearer token should go through fallback
	req := httptest.NewRequest("GET", "/api/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer some-other-token")
	rec := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(rec, req)

	if !fallbackCalled {
		t.Error("fallback not called for non-plugin token")
	}
	if !handlerCalled {
		t.Error("handler not called when fallback is used")
	}
}

func TestMintCredentials(t *testing.T) {
	t.Run("user kind", func(t *testing.T) {
		s, _, issuer, _ := pluginCredentialTestServer(t)
		store := s.authzStore

		// Create and add a test user
		ctx := context.Background()
		user, err := store.UpsertUser(ctx, "user1", "user1@example.com", "user1")
		if err != nil {
			t.Fatalf("upsert user: %v", err)
		}

		// Mark as admin
		if err := store.SetAdmin(ctx, user.ID, true); err != nil {
			t.Fatalf("set admin: %v", err)
		}

		// Mint credential
		tok, exp, err := s.MintUserPluginCredential(ctx, "mem0", user)
		if err != nil {
			t.Fatalf("mint user credential: %v", err)
		}

		// Verify the token
		claims, err := issuer.Verify(tok)
		if err != nil {
			t.Fatalf("verify token: %v", err)
		}

		if claims.PluginID != "mem0" {
			t.Errorf("expected plugin mem0, got %s", claims.PluginID)
		}
		if claims.Kind != plugincreds.KindUser {
			t.Errorf("expected kind user, got %s", claims.Kind)
		}
		if claims.Sub != "user1" {
			t.Errorf("expected sub user1, got %s", claims.Sub)
		}
		if claims.Username != "user1" {
			t.Errorf("expected username user1, got %s", claims.Username)
		}
		if !claims.Admin {
			t.Error("expected admin=true")
		}
		// Permissions should be from plugin spec
		if !slices.Contains(claims.Permissions, "agents:read") {
			t.Errorf("expected agents:read permission, got %v", claims.Permissions)
		}
		if !exp.After(time.Now()) {
			t.Error("expiration should be in the future")
		}
	})

	t.Run("agent kind", func(t *testing.T) {
		s, _, issuer, _ := pluginCredentialTestServer(t)
		store := s.authzStore

		// Create a group and set the agent's resource group
		ctx := context.Background()
		g, err := store.CreateGroup(ctx, "agents-group", "Agents Group")
		if err != nil {
			t.Fatalf("create group: %v", err)
		}

		if err := store.SetResourceGroup(ctx, "agent", "a1", g.ID, false); err != nil {
			t.Fatalf("set agent group: %v", err)
		}

		// Mint credential
		tok, _, err := s.MintAgentPluginCredential(ctx, "mem0", "a1", "inv-123")
		if err != nil {
			t.Fatalf("mint agent credential: %v", err)
		}

		// Verify the token
		claims, err := issuer.Verify(tok)
		if err != nil {
			t.Fatalf("verify token: %v", err)
		}

		if claims.Kind != plugincreds.KindAgent {
			t.Errorf("expected kind agent, got %s", claims.Kind)
		}
		if claims.Sub != "agent:a1" {
			t.Errorf("expected sub agent:a1, got %s", claims.Sub)
		}
		if claims.InvocationID != "inv-123" {
			t.Errorf("expected invocation inv-123, got %s", claims.InvocationID)
		}
		// Agent should have reader role on its group
		if len(claims.Groups) != 1 || claims.Groups[0].ID != g.ID || claims.Groups[0].Role != "reader" {
			t.Errorf("expected reader role on group, got %v", claims.Groups)
		}
	})

	t.Run("plugin kind", func(t *testing.T) {
		s, _, issuer, _ := pluginCredentialTestServer(t)

		ctx := context.Background()

		// Mint credential
		tok, _, err := s.MintPluginBackgroundCredential(ctx, "mem0")
		if err != nil {
			t.Fatalf("mint plugin credential: %v", err)
		}

		// Verify the token
		claims, err := issuer.Verify(tok)
		if err != nil {
			t.Fatalf("verify token: %v", err)
		}

		if claims.Kind != plugincreds.KindPlugin {
			t.Errorf("expected kind plugin, got %s", claims.Kind)
		}
		if claims.Sub != "plugin:mem0" {
			t.Errorf("expected sub plugin:mem0, got %s", claims.Sub)
		}
		// Plugin should have writer role on its group accesses
		if len(claims.Groups) != 0 {
			t.Errorf("expected no groups (mem0 has no groupAccess), got %v", claims.Groups)
		}
	})

	t.Run("disabled plugin returns error", func(t *testing.T) {
		s, _, _, _ := pluginCredentialTestServer(t)
		ctx := context.Background()

		// Disable the plugin
		var p connectorv1alpha1.Plugin
		if err := s.client.Get(ctx, types.NamespacedName{Name: "mem0", Namespace: s.ns}, &p); err != nil {
			t.Fatalf("get plugin: %v", err)
		}
		p.Spec.Disabled = true
		if err := s.client.Update(ctx, &p); err != nil {
			t.Fatalf("update plugin: %v", err)
		}

		user, err := s.authzStore.UpsertUser(ctx, "user1", "user1@example.com", "user1")
		if err != nil {
			t.Fatalf("upsert user: %v", err)
		}

		_, _, err = s.MintUserPluginCredential(ctx, "mem0", user)
		if err == nil {
			t.Error("expected error for disabled plugin")
		}
	})

	t.Run("no issuer returns ErrDisabled", func(t *testing.T) {
		// Server without issuer
		s := testServer(t)
		s.authzStore = newFakeAuthzStore()
		ctx := context.Background()

		user, err := s.authzStore.UpsertUser(ctx, "user1", "user1@example.com", "user1")
		if err != nil {
			t.Fatalf("upsert user: %v", err)
		}

		_, _, err = s.MintUserPluginCredential(ctx, "mem0", user)
		if err != plugincreds.ErrDisabled {
			t.Errorf("expected ErrDisabled, got %v", err)
		}
	})
}
