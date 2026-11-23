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
	"github.com/ainsel-project/ainsel/services/hub/internal/plugincreds"
	connectorv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Issue #390: the resource-scoped authz guards (requireRead/requireWrite and
// the persona/skill twins) answer a denied non-admin caller with 404, not 403,
// so a resource that never existed, one that was deleted (its ownership row is
// removed with it), and one that lives in a foreign group are indistinguishable
// — the house pattern already used for events. Group/admin guards keep 403;
// that asymmetry is pinned by the pre-existing 403 assertions on
// requireGroupOwner/requireGroupWrite and the plugin permission gates.

// authzDenyPluginPerms are the token permissions the plugin caller carries: one
// read and one write permission per guarded route group under test, all
// approved on the seeded Plugin CR.
var authzDenyPluginPerms = []string{
	"agents:read", "agents:write",
	"triggers:read", "triggers:write",
	"skills:read", "skills:write",
	"personas:read", "personas:write",
	"mcp-servers:read", "mcp-servers:write",
	"connectors:read", "connectors:write",
}

// authzDenyEnv is a full-stack server whose authz plane is the in-memory
// fakeAuthzStore: the real route table and guards over map-backed ownership
// rows. Denied requests never reach a store, so the dead Postgres pools from
// testDeps stay untouched.
type authzDenyEnv struct {
	t     *testing.T
	s     *Server
	store *fakeAuthzStore
}

func newAuthzDenyEnv(t *testing.T) *authzDenyEnv {
	t.Helper()
	deps := testDeps(t)
	deps.Personas = &stubPersona{}
	deps.Skills = newMockSkillService()
	store := newFakeAuthzStore()
	checker := authz.NewChecker(store, authz.NewGroupCache(func(uid string) (map[string]authz.GroupRole, error) {
		return store.UserGroupRoles(context.Background(), uid)
	}, time.Minute))
	deps.AuthzStore = store
	deps.AuthzChecker = checker
	s := newTestServer(t, deps)
	s.authzStore = store
	s.authzChecker = checker

	// Plugin identity: an approved, Ready "mem0" granted g1, plus an issuer.
	p := pluginCRReady(pluginCR("mem0", false), true, "", "", authzDenyPluginPerms)
	p.Spec.ApprovedPermissions = authzDenyPluginPerms
	p.Spec.GroupAccess = []string{"g1"}
	if err := s.client.Create(context.Background(), p); err != nil {
		t.Fatalf("seed plugin CR: %v", err)
	}
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i)
	}
	issuer, err := plugincreds.NewIssuer(secret, 5*time.Minute)
	if err != nil {
		t.Fatalf("create issuer: %v", err)
	}
	s.pluginCreds = issuer
	return &authzDenyEnv{t: t, s: s, store: store}
}

// sessionUser seeds a non-plugin caller: a non-admin with roles in the named
// groups, or an admin when admin is set.
func (e *authzDenyEnv) sessionUser(sub string, admin bool, roles map[string]authz.GroupRole) {
	e.t.Helper()
	ctx := context.Background()
	if _, err := e.store.UpsertUser(ctx, sub, "", sub); err != nil {
		e.t.Fatalf("seed user %q: %v", sub, err)
	}
	if admin {
		if err := e.store.SetAdmin(ctx, sub, true); err != nil {
			e.t.Fatalf("set admin %q: %v", sub, err)
		}
	}
	for group, role := range roles {
		if err := e.store.AddGroupMember(ctx, group, sub, role); err != nil {
			e.t.Fatalf("grant %q in %q: %v", sub, group, err)
		}
	}
}

// session issues one request as a session caller with the given subject.
func (e *authzDenyEnv) session(sub, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req = req.WithContext(oidc.ContextWithUser(req.Context(), &oidc.User{Sub: sub, Username: sub}))
	rec := httptest.NewRecorder()
	e.s.routerHandler().ServeHTTP(rec, req)
	return rec
}

// plugin issues one request with a plugin-kind credential granted g1:writer.
func (e *authzDenyEnv) plugin(method, path string) *httptest.ResponseRecorder {
	tok := mintPluginPluginToken(e.t, e.s.pluginCreds, "mem0", authzDenyPluginPerms,
		[]plugincreds.GroupGrant{{ID: "g1", Role: "writer"}})
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	chain := e.s.PluginCredentialMiddleware(
		authz.NewDisabledGuard(e.store),
		func(next http.Handler) http.Handler { return next },
	)
	chain(e.s.routerHandler()).ServeHTTP(rec, req)
	return rec
}

// wantOpaqueNotFound asserts the guard's answer: 404 with the standard error
// body, identical in shape to the handlers' own not-found answers.
func wantOpaqueNotFound(t *testing.T, rec *httptest.ResponseRecorder, name string) {
	t.Helper()
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q is not the documented error JSON: %v", rec.Body.String(), err)
	}
	if want := name + " not found"; body.Error != want {
		t.Errorf("error = %q, want %q", body.Error, want)
	}
}

// TestResourceAuthzDenyAnswersNotFound pins the issue #390 matrix: every
// guarded resource route × GET/PUT/DELETE × {never existed, deleted, foreign
// group} answers 404 for both a non-admin session caller and a plugin
// credential. (Channels address their guard behind a channel load, so they are
// pinned by TestChannelAuthzDenyAnswersNotFound against the guard itself.)
func TestResourceAuthzDenyAnswersNotFound(t *testing.T) {
	kinds := []struct {
		label     string
		authzType string
		prefix    string
	}{
		{"agents", "agent", "/api/v1/agents/"},
		{"cron triggers", "cron-trigger", "/api/v1/cron-triggers/"},
		{"triggers", "trigger", "/api/v1/triggers/"},
		{"skills", "skill", "/api/v1/skills/"},
		{"personas", "persona", "/api/v1/personas/"},
		{"mcp-servers", "mcp-server", "/api/v1/mcp-servers/"},
		{"connectors", "connector", "/api/v1/connectors/"},
	}
	methods := []string{http.MethodGet, http.MethodPut, http.MethodDelete}
	const name = "denied-res"

	scenarios := []struct {
		label string
		apply func(e *authzDenyEnv, kind, name string)
	}{
		// Nothing was ever filed: no ownership row at all.
		{"never existed", func(e *authzDenyEnv, kind, name string) {}},
		// The delete handlers remove the resource and its ownership row
		// together, so the authz layer sees exactly this state afterwards.
		{"deleted", func(e *authzDenyEnv, kind, name string) {
			ctx := context.Background()
			if kind == "agent" {
				agent := &connectorv1alpha1.Agent{
					ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test-ns"},
				}
				if err := e.s.client.Create(ctx, agent); err != nil {
					e.t.Fatalf("create agent: %v", err)
				}
				if err := e.s.client.Delete(ctx, agent); err != nil {
					e.t.Fatalf("delete agent: %v", err)
				}
			}
			if err := e.store.SetResourceGroup(ctx, kind, name, "g1", false); err != nil {
				e.t.Fatalf("file resource: %v", err)
			}
			if err := e.store.SetResourceGroup(ctx, kind, name, "", false); err != nil {
				e.t.Fatalf("delete resource: %v", err)
			}
		}},
		// The resource exists and belongs to a group the caller holds nothing in.
		{"exists in a foreign group", func(e *authzDenyEnv, kind, name string) {
			ctx := context.Background()
			if kind == "agent" {
				agent := &connectorv1alpha1.Agent{
					ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test-ns"},
				}
				if err := e.s.client.Create(ctx, agent); err != nil {
					e.t.Fatalf("create agent: %v", err)
				}
			}
			if err := e.store.SetResourceGroup(ctx, kind, name, "g2", false); err != nil {
				e.t.Fatalf("file resource: %v", err)
			}
		}},
	}

	for _, kind := range kinds {
		for _, scenario := range scenarios {
			for _, method := range methods {
				for _, caller := range []string{"session", "plugin"} {
					t.Run(kind.label+"/"+scenario.label+"/"+method+"/"+caller+"-caller", func(t *testing.T) {
						env := newAuthzDenyEnv(t)
						// The caller owns g1 — everything above is outside it,
						// except the deleted case, whose row was just removed.
						env.sessionUser("u1", false, map[string]authz.GroupRole{"g1": authz.RoleWriter})
						scenario.apply(env, kind.authzType, name)

						path := kind.prefix + name
						var rec *httptest.ResponseRecorder
						if caller == "session" {
							rec = env.session("u1", method, path)
						} else {
							rec = env.plugin(method, path)
						}
						wantOpaqueNotFound(t, rec, name)
					})
				}
			}
		}
	}
}

// TestChannelAuthzDenyAnswersNotFound pins the eighth guarded kind. A channel
// is addressed through channelAuthzTarget, which maps it onto the resource
// whose group owns it (a custom channel owns itself; a connector's or agent's
// channel follows its entity). The load-then-guard order means a gone channel
// already answered 404 from the store; this pins that the guard behind it
// answers the same opaque 404 for foreign and deleted ownership.
func TestChannelAuthzDenyAnswersNotFound(t *testing.T) {
	env := newAuthzDenyEnv(t)
	env.sessionUser("u1", false, map[string]authz.GroupRole{"g1": authz.RoleWriter})
	ctx := context.Background()

	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/channels/ch1", nil)
		return r.WithContext(oidc.ContextWithUser(r.Context(), &oidc.User{Sub: "u1", Username: "u1"}))
	}

	t.Run("custom channel with no ownership row", func(t *testing.T) {
		rec := httptest.NewRecorder()
		if env.s.requireChannelRead(rec, request(), &channels.Channel{ID: "ch1", Kind: channels.KindCustom}) {
			t.Error("read allowed on a channel the caller has no claim to")
		}
		wantOpaqueNotFound(t, rec, "ch1")
	})

	t.Run("custom channel in a foreign group", func(t *testing.T) {
		if err := env.store.SetResourceGroup(ctx, "channel", "ch1", "g2", false); err != nil {
			t.Fatalf("file channel: %v", err)
		}
		rec := httptest.NewRecorder()
		if env.s.requireChannelRead(rec, request(), &channels.Channel{ID: "ch1", Kind: channels.KindCustom}) {
			t.Error("read allowed on a foreign-group channel")
		}
		wantOpaqueNotFound(t, rec, "ch1")
	})

	t.Run("connector channel follows its connector", func(t *testing.T) {
		if err := env.store.SetResourceGroup(ctx, "connector", "conn-9", "g2", false); err != nil {
			t.Fatalf("file connector: %v", err)
		}
		rec := httptest.NewRecorder()
		ch := &channels.Channel{ID: "ch1", Kind: channels.KindConnector, EntityRef: "conn-9"}
		if env.s.requireChannelWrite(rec, request(), ch) {
			t.Error("write allowed on a channel whose connector is in a foreign group")
		}
		wantOpaqueNotFound(t, rec, "conn-9")
	})

	t.Run("group member passes", func(t *testing.T) {
		if err := env.store.SetResourceGroup(ctx, "channel", "ch1", "g1", false); err != nil {
			t.Fatalf("file channel: %v", err)
		}
		rec := httptest.NewRecorder()
		if !env.s.requireChannelRead(rec, request(), &channels.Channel{ID: "ch1", Kind: channels.KindCustom}) {
			t.Error("read denied for a member of the channel's group")
		}
		if rec.Body.Len() != 0 {
			t.Errorf("passing guard wrote a body: %q", rec.Body.String())
		}
	})
}

// TestAdminGoneResourceGetsHandlerNotFound pins the admin path: admins
// short-circuit the guard and get the handler's own 404 — the guard flip must
// not have turned anything for them.
func TestAdminGoneResourceGetsHandlerNotFound(t *testing.T) {
	env := newAuthzDenyEnv(t)
	env.sessionUser("boss", true, nil)
	rec := env.session("boss", http.MethodGet, "/api/v1/agents/gone-agent")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q is not the documented error JSON: %v", rec.Body.String(), err)
	}
	if body.Error != "agent not found" {
		t.Errorf("error = %q, want the handler's own %q", body.Error, "agent not found")
	}
}
