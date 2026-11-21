package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	"github.com/ainsel-project/ainsel/services/hub/internal/channels"
	"github.com/ainsel-project/ainsel/services/hub/internal/dbtest"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"
)

// #390: a resource the caller cannot read/write/manage answers an opaque 404 —
// never a 403 — so a deleted resource (its resource_groups row removed by the
// delete handler) and a resource in a foreign group are indistinguishable to a
// non-admin caller, plugin credentials included. These tests pin the status for
// every guarded resource type, across the three shapes of "not yours" and the
// three item methods, for both a session caller and a plugin-principal caller.

// guardAuthzStore is the checker store behind the matrix below: GetUser answers
// every subject as a plain (non-admin) user, and GetResourceGroup answers from
// the row the test placed, where ok=false models the row being absent — the
// state a deleted resource leaves behind, indistinguishable from never existed.
type guardAuthzStore struct {
	// The full registry surface comes from the permissive store; the matrix
	// never calls it. GetUser and GetResourceGroup below are the answers the
	// checker actually reads.
	*authz.PermissiveStore
	group    authz.ResourceGroup
	hasGroup bool
}

func (guardAuthzStore) GetUser(_ context.Context, id string) (*authz.User, error) {
	// Every subject is a plain user: no admin short-circuit for session or
	// plugin callers.
	return &authz.User{ID: id, Username: id}, nil
}

func (s guardAuthzStore) GetResourceGroup(_ context.Context, resourceType, resourceName string) (*authz.ResourceGroup, error) {
	if !s.hasGroup || resourceName != guardResourceName {
		// Absent mapping row: the state of a never-created *and* of a deleted
		// resource — the checker reads both as default-closed.
		return nil, authz.ErrNotFound
	}
	return &authz.ResourceGroup{
		ResourceType: resourceType,
		ResourceName: resourceName,
		GroupID:      s.group.GroupID,
		Public:       s.group.Public,
	}, nil
}

// guardResourceName is the {name} every matrix path carries, so one store row
// covers every resource type.
const guardResourceName = "res-1"

// guardMatrixServer is a fully-routed server whose checker denies by default:
// no caller holds a role in any group, and the resource's group row is whatever
// the case placed.
func guardMatrixServer(t *testing.T, store guardAuthzStore) *Server {
	t.Helper()
	guard := store
	guard.PermissiveStore = authz.NewPermissiveStore()
	checker := authz.NewChecker(store, authz.NewGroupCache(func(string) (map[string]authz.GroupRole, error) {
		return nil, nil
	}, time.Minute))
	deps := testDeps(t)
	deps.Personas = &stubPersona{}
	deps.Skills = newMockSkillService()
	deps.AgentMCPEnabled = true
	// The checker must be in Deps, not swapped in afterwards: the persona and
	// skill registrars capture it at registration time.
	deps.AuthzStore = guard
	deps.AuthzChecker = checker
	s := newTestServer(t, deps)
	s.authMW = nil // identity comes from the request context, as callers below assert
	return s
}

func TestResourceAuthzDeniesAnswerNotFound(t *testing.T) {
	cases := []struct {
		name         string
		resourceType string
		path         string
		// wantMsg is the guard's opaque not-found body, shaped exactly like the
		// handler's own 404 so the two are interchangeable to clients.
		wantMsg string
	}{
		{name: "agents", resourceType: "agent", path: "/api/v1/agents/res-1", wantMsg: "agent not found"},
		{name: "cron triggers", resourceType: "cron-trigger", path: "/api/v1/cron-triggers/res-1", wantMsg: "cron-trigger not found"},
		{name: "triggers", resourceType: "trigger", path: "/api/v1/triggers/res-1", wantMsg: "trigger not found"},
		{name: "skills", resourceType: "skill", path: "/api/v1/skills/res-1", wantMsg: "skill not found"},
		{name: "personas", resourceType: "persona", path: "/api/v1/personas/res-1", wantMsg: "persona not found"},
		{name: "mcp-servers", resourceType: "mcp-server", path: "/api/v1/mcp-servers/res-1", wantMsg: "mcp-server not found"},
		{name: "connectors", resourceType: "connector", path: "/api/v1/connectors/res-1", wantMsg: "connector not found"},
	}
	callers := []struct {
		name string
		sub  string
	}{
		{name: "non-admin session", sub: "u1"},
		{name: "plugin principal", sub: "plugin:mem0"},
	}
	scenarios := []struct {
		name  string
		store guardAuthzStore
	}{
		// Both gone states resolve to the same absent resource_groups row —
		// that is the point of the checker's default-closed read.
		{name: "never existed", store: guardAuthzStore{}},
		{name: "deleted", store: guardAuthzStore{}},
		{name: "foreign group", store: guardAuthzStore{
			hasGroup: true,
			group:    authz.ResourceGroup{GroupID: "g2"},
		}},
	}
	methods := []string{http.MethodGet, http.MethodPut, http.MethodDelete}

	doAs := func(s *Server, method, path, sub string) (int, string) {
		req := httptest.NewRequest(method, path, nil)
		if sub != "" {
			req = req.WithContext(oidc.ContextWithUser(req.Context(), &oidc.User{Sub: sub, Username: sub}))
		}
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		var body struct {
			Error string `json:"error"`
		}
		if rec.Body.Len() > 0 {
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("%s %s: body %q is not the documented error JSON", method, path, rec.Body.String())
			}
		}
		return rec.Code, body.Error
	}

	for _, tc := range cases {
		for _, caller := range callers {
			for _, sc := range scenarios {
				s := guardMatrixServer(t, sc.store)
				for _, method := range methods {
					code, msg := doAs(s, method, tc.path, caller.sub)
					if code != http.StatusNotFound {
						t.Errorf("%s (%s): %s %s = %d, want 404 (%q)", tc.name, caller.name, method, tc.path, code, msg)
					}
					if msg != tc.wantMsg {
						t.Errorf("%s (%s): %s %s body = %q, want %q", tc.name, caller.name, method, tc.path, msg, tc.wantMsg)
					}
				}
			}
		}
	}
}

// TestResourceAuthzDenyCallerShapes pins the edges around the deny: anonymous
// stays 401 (the identity answer must not move to 404), and the deny body is the
// standard {"error": ...} JSON, not plain text.
func TestResourceAuthzDenyCallerShapes(t *testing.T) {
	s := guardMatrixServer(t, guardAuthzStore{})
	// Anonymous: 401 from the guard, unchanged by #390.
	code, _, _ := doAPI(t, s, http.MethodGet, "/api/v1/agents/a-1", nil)
	if code != http.StatusUnauthorized {
		t.Errorf("anonymous GET agent = %d, want 401", code)
	}
}

// TestChannelAuthzDenyAnswersNotFound covers the one resource type whose guard
// needs the loaded row (a channel's kind decides the authz target), so its
// foreign-group case needs the channel to exist in Postgres: skipped without
// Docker, like the rest of the store-backed tests.
func TestChannelAuthzDenyAnswersNotFound(t *testing.T) {
	s, cs, _, _ := channelTestServer(t)
	ctx := context.Background()

	pool := dbtest.Open(t)
	store := authz.NewStore(pool)
	u, err := store.UpsertUser(ctx, "u1", "u1@example.com", "u1")
	if err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	s.authzStore = store
	s.authzChecker = authz.NewChecker(store, authz.NewGroupCache(func(uid string) (map[string]authz.GroupRole, error) {
		return store.UserGroupRoles(ctx, uid)
	}, time.Minute))
	s.authMW = func(next http.Handler) http.Handler { return next }

	id := seedChannel(t, cs, channels.KindCustom, "gone-check")
	g, err := store.CreateGroup(ctx, "foreign", "a group u1 holds nothing in")
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := store.SetResourceGroup(ctx, "channel", id, g.ID, false); err != nil {
		t.Fatalf("set resource group: %v", err)
	}
	_ = u

	asU1 := func(method, path string) (int, string) {
		req := httptest.NewRequest(method, path, nil)
		req = req.WithContext(oidc.ContextWithUser(req.Context(), &oidc.User{Sub: "u1", Username: "u1"}))
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		var body struct {
			Error string `json:"error"`
		}
		if rec.Body.Len() > 0 {
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("%s %s: body %q is not the documented error JSON", method, path, rec.Body.String())
			}
		}
		return rec.Code, body.Error
	}

	// Exists, but in a group the caller holds nothing in: opaque 404.
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		code, msg := asU1(method, "/api/v1/channels/"+id)
		if code != http.StatusNotFound {
			t.Errorf("foreign-group channel: %s = %d, want 404 (%q)", method, code, msg)
		}
		if msg != "channel not found" {
			t.Errorf("foreign-group channel: %s body = %q, want %q", method, msg, "channel not found")
		}
	}
	// Absent row (never existed or deleted — the same store state): also 404.
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		code, _ := asU1(method, "/api/v1/channels/ch-does-not-exist")
		if code != http.StatusNotFound {
			t.Errorf("absent channel: %s = %d, want 404", method, code)
		}
	}
}
