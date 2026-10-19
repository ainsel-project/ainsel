package api

// Tests for the forced password change (issue #113): the must_change_password
// flag lifecycle in the handlers, and the DB-backed 403 gate that keeps a
// flagged local user away from the console until they change their password.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	"github.com/ainsel-project/ainsel/services/hub/internal/localauth"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"
)

// gatedTestServer is localAuthTestServer plus an auth middleware, since the
// gate deliberately runs inside the auth chain. The middleware is a
// pass-through: the tests place the context user with asUser(). A stub
// /api/v1/agents route gives the gate something to block — unregistered paths
// would answer 404 and hide whether the gate ran at all.
func gatedTestServer(t *testing.T) (*Server, *fakeAuthzStore) {
	t.Helper()
	s, store := localAuthTestServer(t)
	s.SetAuthMiddleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r) })
	})
	s.mux.HandleFunc("/api/v1/agents", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "handler reached"})
	})
	return s, store
}

// seedLocalUser creates a local account with an explicit flag state.
func seedLocalUser(t *testing.T, store *fakeAuthzStore, username string, isAdmin, mustChange bool) {
	t.Helper()
	hash, err := localauth.HashPassword("user-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateLocalUser(context.Background(), authz.LocalUserIDPrefix+username, "", username, hash, isAdmin, mustChange); err != nil {
		t.Fatal(err)
	}
}

// --- the gate ---

func TestPasswordGateBlocksFlaggedLocalUser(t *testing.T) {
	s, store := gatedTestServer(t)
	seedLocalUser(t, store, "alice", false, true)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents", nil, asUser("local:alice", "alice"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	// The stable code, not the message text, is the contract with the frontend.
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != CodePasswordChangeRequired {
		t.Fatalf("body = %v, want code %q", body, CodePasswordChangeRequired)
	}
	if body["message"] == "" {
		t.Fatal("403 must carry a human-readable message")
	}
}

func TestPasswordGateAllowedEndpoints(t *testing.T) {
	s, store := gatedTestServer(t)
	seedLocalUser(t, store, "alice", false, true)
	alice := asUser("local:alice", "alice")

	// Identity: reachable, and it reports the flag so the frontend can render
	// the forced screen after a mid-session reset.
	rec := doJSON(t, s, http.MethodGet, "/api/v1/auth/me", nil, alice)
	if rec.Code != http.StatusOK {
		t.Fatalf("/auth/me: status = %d, body %s", rec.Code, rec.Body.String())
	}
	var me map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&me); err != nil {
		t.Fatal(err)
	}
	if me["mustChangePassword"] != true {
		t.Fatalf("/auth/me must report the flag: %v", me)
	}

	// Logout must stay reachable: a user who does not want the change has to be
	// able to leave.
	rec = doJSON(t, s, http.MethodPost, "/api/v1/auth/logout", nil, alice)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("/auth/logout: status = %d, want 204", rec.Code)
	}

	// The password endpoint is reached: 401 here is the handler rejecting the
	// wrong current password, not the gate.
	rec = doJSON(t, s, http.MethodPut, "/api/v1/auth/password", map[string]string{
		"current": "wrong", "new": "brand-new-pass",
	}, alice)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("/auth/password: status = %d, want 401 from the handler", rec.Code)
	}
}

func TestPasswordGateSkipsEveryoneElse(t *testing.T) {
	s, store := gatedTestServer(t)
	seedLocalUser(t, store, "alice", false, true) // flagged
	seedLocalUser(t, store, "bob", false, false)  // chose their own password
	if _, err := store.UpsertUser(context.Background(), "oidc-sub-1", "e@x", "oidcuser"); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		sub  string
	}{
		{"unflagged local user", "local:bob"},
		{"oidc subject", "oidc-sub-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, s, http.MethodGet, "/api/v1/agents", nil, asUser(tc.sub, "u"))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
			}
		})
	}
}

// The gate must be inert where local auth is not wired, so `oidc` and `none`
// deployments see no extra store read and no new failure mode.
func TestPasswordGateInertWithoutLocalAuth(t *testing.T) {
	t.Run("no authz store", func(t *testing.T) {
		s, store := gatedTestServer(t)
		seedLocalUser(t, store, "alice", false, true)
		s.authzStore = nil
		rec := doJSON(t, s, http.MethodGet, "/api/v1/agents", nil, asUser("local:alice", "alice"))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("no auth middleware", func(t *testing.T) {
		s, store := gatedTestServer(t)
		seedLocalUser(t, store, "alice", false, true)
		s.SetAuthMiddleware(nil)
		rec := doJSON(t, s, http.MethodGet, "/api/v1/agents", nil, asUser("local:alice", "alice"))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
	})
}

// A local JWT whose registry row is gone has no flag to enforce. The handlers
// answered such a request on their own before the gate existed; the gate must
// not turn that into a "change your password" dead end.
func TestPasswordGateUnknownUserPassesThrough(t *testing.T) {
	s, _ := gatedTestServer(t)
	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents", nil, asUser("local:ghost", "ghost"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

// --- flag lifecycle through the handlers ---

func TestLoginResponseCarriesMustChangePassword(t *testing.T) {
	s, store := localAuthTestServer(t)
	seedLocalUser(t, store, "admin", true, true)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": "admin", "password": "user-password",
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("a flagged user must still be able to log in: status = %d", rec.Code)
	}
	var resp struct {
		User struct {
			Sub                string `json:"sub"`
			Username           string `json:"username"`
			IsAdmin            bool   `json:"isAdmin"`
			MustChangePassword bool   `json:"mustChangePassword"`
		} `json:"user"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.User.MustChangePassword {
		t.Fatalf("login response must carry the flag: %+v", resp.User)
	}
}

func TestChangePasswordClearsMustChangeFlag(t *testing.T) {
	s, store := localAuthTestServer(t)
	seedLocalUser(t, store, "alice", false, true)

	rec := doJSON(t, s, http.MethodPut, "/api/v1/auth/password", map[string]string{
		"current": "user-password", "new": "self-chosen-pass",
	}, asUser("local:alice", "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	u, err := store.GetUser(context.Background(), "local:alice")
	if err != nil {
		t.Fatal(err)
	}
	if u.MustChangePassword {
		t.Fatal("a self-service change must clear the flag")
	}
}

func TestCreateUserArmsMustChangeFlag(t *testing.T) {
	s, store := localAuthTestServer(t)
	seedAdmin(t, store)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/users", map[string]any{
		"username": "alice", "password": "alice-password",
	}, asUser("local:admin", "admin"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var u authz.User
	if err := json.NewDecoder(rec.Body).Decode(&u); err != nil {
		t.Fatal(err)
	}
	if !u.MustChangePassword {
		t.Fatal("an admin-created user must be forced to change the password they were handed")
	}
}

func TestAdminResetArmsMustChangeFlag(t *testing.T) {
	s, store := localAuthTestServer(t)
	seedAdmin(t, store)
	seedLocalUser(t, store, "bob", false, false)

	rec := doJSON(t, s, http.MethodPatch, "/api/v1/users/local:bob", map[string]any{
		"password": "reset-password-1",
	}, asUser("local:admin", "admin"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var u authz.User
	if err := json.NewDecoder(rec.Body).Decode(&u); err != nil {
		t.Fatal(err)
	}
	if !u.MustChangePassword {
		t.Fatal("an admin reset must re-arm the forced change")
	}

	// An isAdmin-only patch must not disturb a pending change.
	if err := store.SetPasswordWithFlag(context.Background(), "local:bob", "hash", true); err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, s, http.MethodPatch, "/api/v1/users/local:bob", map[string]any{
		"isAdmin": true,
	}, asUser("local:admin", "admin"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&u); err != nil {
		t.Fatal(err)
	}
	if !u.MustChangePassword {
		t.Fatal("a role change must leave a pending password change pending")
	}
}

// --- /auth/me ---

// The hub-side handler replaces the shared oidc.MeHandler mount. The existing
// fields are a frozen contract; only mustChangePassword is added.
func TestAuthMeShapeStable(t *testing.T) {
	s, store := gatedTestServer(t)
	seedLocalUser(t, store, "alice", false, true)
	if _, err := store.UpsertUser(context.Background(), "oidc-sub-1", "oidc@example.com", "oidcuser"); err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, s, http.MethodGet, "/api/v1/auth/me", nil, func(ctx context.Context) context.Context {
		return oidc.ContextWithUser(ctx, &oidc.User{Sub: "oidc-sub-1", Email: "oidc@example.com", Username: "oidcuser"})
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var raw map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"sub", "email", "username", "roles"} {
		if _, ok := raw[k]; !ok {
			t.Fatalf("field %q missing from /auth/me: %v", k, raw)
		}
	}
	if raw["mustChangePassword"] != false {
		t.Fatalf("an OIDC subject has no password to change: %v", raw)
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/auth/me", nil, asUser("local:alice", "alice"))
	raw = map[string]any{}
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if raw["mustChangePassword"] != true {
		t.Fatalf("a flagged local user must see the flag: %v", raw)
	}
}

func TestAuthMeUnauthorized(t *testing.T) {
	s, _ := localAuthTestServer(t)
	// No context user and no auth middleware: the handler must not answer 200
	// with an empty body, matching the shared MeHandler's defensive behaviour.
	rec := httptest.NewRecorder()
	s.handleAuthMe(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// --- end to end: login → blocked → change → console ---

func TestForcedChangeEndToEnd(t *testing.T) {
	s, store := localAuthTestServer(t)
	seedLocalUser(t, store, "admin", true, true)
	s.SetAuthMiddleware(func(next http.Handler) http.Handler {
		return localauth.NewMiddleware(testLocalSecret)(s.IdentityPersistMiddleware(next))
	})
	s.mux.HandleFunc("/api/v1/agents", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "handler reached"})
	})

	rec := doJSON(t, s, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": "admin", "password": "user-password",
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d", rec.Code)
	}
	var login struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&login); err != nil {
		t.Fatal(err)
	}

	call := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+login.Token)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		return rec
	}

	if rec := call("/api/v1/agents"); rec.Code != http.StatusForbidden {
		t.Fatalf("console call while flagged: status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}

	// The change itself goes through with the same token.
	req := httptest.NewRequest(http.MethodPut, "/api/v1/auth/password",
		strings.NewReader(`{"current":"user-password","new":"self-chosen-pass"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+login.Token)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("password change: status = %d, body %s", rec.Code, rec.Body.String())
	}

	// Same token, no re-login: the gate reads the flag from the database, so
	// clearing it takes effect on the very next request.
	if rec := call("/api/v1/agents"); rec.Code != http.StatusOK {
		t.Fatalf("after the change: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}
