package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	connectorv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

// The plugin admin API gates every write behind admin and exposes a reduced
// public shape to non-admin callers. These tests pin the route table, the
// validation rules from the global constraints (id regex, digest regex, port
// range), the set-equality check on approve-permissions, the group existence
// check on group-access, and the audit row written for each successful write.

// A syntactically valid digest for the pattern ^sha256:[a-f0-9]{64}$.
const pluginTestDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// pluginCR builds a Plugin in the test namespace for seeding the fake client.
func pluginCR(id string, disabled bool) *connectorv1alpha1.Plugin {
	p := &connectorv1alpha1.Plugin{}
	p.APIVersion = "ainsel.dev/v1alpha1"
	p.Kind = "Plugin"
	p.Name = id
	p.Namespace = "test-ns"
	p.Spec.DisplayName = "Plugin " + id
	p.Spec.Service = connectorv1alpha1.PluginServiceRef{Name: id + "-svc", Port: 8080}
	p.Spec.Image = connectorv1alpha1.PluginImage{Repository: "registry.test/" + id, Digest: pluginTestDigest}
	p.Spec.Disabled = disabled
	return p
}

// pluginCRReady seeds the Ready condition and declared permissions the
// reconciler would have written.
func pluginCRReady(p *connectorv1alpha1.Plugin, ready bool, reason, message string, declared []string) *connectorv1alpha1.Plugin {
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	p.Status.Conditions = []metav1.Condition{{
		Type:               connectorv1alpha1.PluginConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	}}
	p.Status.DeclaredPermissions = declared
	p.Status.Version = "1.2.3"
	p.Status.Manifest = &runtime.RawExtension{Raw: []byte(`{"id":"` + p.Name + `","pluginApi":"1.0","version":"1.2.3"}`)}
	return p
}

// pluginTestServer wires the plugin routes on a dev-mode server (no authz
// configured, so every caller counts as admin) with the audit store attached.
func pluginTestServer(t *testing.T, objects ...runtime.Object) (*Server, *fakeAuditStore) {
	t.Helper()
	s := testServer(t, objects...)
	registerPluginRoutes(s)
	audit := &fakeAuditStore{}
	s.SetAuditEventStore(audit)
	return s, audit
}

// pluginAuthzStore adds group-existence answers to the shared fake: GetGroup
// resolves only the ids in `groups`, like the real store's ErrNotFound for
// everything else.
type pluginAuthzStore struct {
	*fakeAuthzStore
	groups   map[string]struct{}
	groupErr error
}

func (p *pluginAuthzStore) GetGroup(_ context.Context, id string) (*authz.Group, error) {
	// groupErr simulates a broken store (a Postgres hiccup): every lookup fails
	// with a non-ErrNotFound error, which the API must report as 500 rather
	// than telling the admin their group does not exist.
	if p.groupErr != nil {
		return nil, p.groupErr
	}
	if _, ok := p.groups[id]; ok {
		return &authz.Group{ID: id, Name: id}, nil
	}
	return nil, authz.ErrNotFound
}

// pluginBrokenGroupStoreServer is pluginAuthzTestServer with a store whose
// GetGroup always fails with a non-ErrNotFound error.
func pluginBrokenGroupStoreServer(t *testing.T, objects ...runtime.Object) *Server {
	t.Helper()
	s, _ := pluginAuthzTestServer(t, map[string]struct{}{"g1": {}}, objects...)
	s.authzStore.(*pluginAuthzStore).groupErr = errors.New("connection reset by peer")
	return s
}

// pluginAuthzTestServer wires the plugin routes with a real checker backed by
// seeded users "admin" and "member"; requests authenticate through asUser.
func pluginAuthzTestServer(t *testing.T, groups map[string]struct{}, objects ...runtime.Object) (*Server, *fakeAuditStore) {
	t.Helper()
	s := testServer(t, objects...)
	store := &pluginAuthzStore{fakeAuthzStore: newFakeAuthzStore(), groups: groups}
	store.users["admin"] = authz.User{ID: "admin", Username: "admin", IsAdmin: true}
	store.users["member"] = authz.User{ID: "member", Username: "member"}
	s.authzStore = store
	s.authzChecker = authz.NewChecker(store, authz.NewGroupCache(func(string) (map[string]authz.GroupRole, error) {
		return nil, nil
	}, time.Minute))
	registerPluginRoutes(s)
	audit := &fakeAuditStore{}
	s.SetAuditEventStore(audit)
	return s, audit
}

// pluginInstallBody is a valid PluginInstallRequest payload as a map so tests
// can override single fields.
func pluginInstallBody(overrides map[string]any) map[string]any {
	body := map[string]any{
		"id":              "mem0",
		"displayName":     "Mem0",
		"serviceName":     "mem0-svc",
		"servicePort":     8081,
		"imageRepository": "registry.test/mem0",
		"imageDigest":     pluginTestDigest,
	}
	for k, v := range overrides {
		body[k] = v
	}
	return body
}

// getPluginCR reads a Plugin straight from the fake client, failing the test
// when it is absent.
func getPluginCR(t *testing.T, s *Server, id string) *connectorv1alpha1.Plugin {
	t.Helper()
	var p connectorv1alpha1.Plugin
	if err := s.client.Get(context.Background(), types.NamespacedName{Name: id, Namespace: s.ns}, &p); err != nil {
		t.Fatalf("get plugin %q: %v", id, err)
	}
	return &p
}

// decodeJSON decodes a response body into v.
func decodeJSON(t *testing.T, body []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("decode body %s: %v", body, err)
	}
}

// --- install ---

func TestInstallPlugin_AdminCreatesCR(t *testing.T) {
	s, audit := pluginTestServer(t)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/plugins", pluginInstallBody(nil), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	p := getPluginCR(t, s, "mem0")
	if p.Spec.Image.Digest != pluginTestDigest {
		t.Errorf("spec.image.digest = %q, want %q", p.Spec.Image.Digest, pluginTestDigest)
	}
	if p.Spec.Image.Repository != "registry.test/mem0" {
		t.Errorf("spec.image.repository = %q, want registry.test/mem0", p.Spec.Image.Repository)
	}
	if p.Spec.Service.Name != "mem0-svc" || p.Spec.Service.Port != 8081 {
		t.Errorf("spec.service = %+v, want mem0-svc:8081", p.Spec.Service)
	}
	if len(p.Spec.ApprovedPermissions) != 0 {
		t.Errorf("spec.approvedPermissions = %v, want empty on install", p.Spec.ApprovedPermissions)
	}

	if len(audit.inserted) != 1 {
		t.Fatalf("inserted = %d audit rows, want 1", len(audit.inserted))
	}
	e := audit.inserted[0]
	if e.EventType != "plugin_installed" || e.Target != "mem0" || e.Outcome != "success" {
		t.Errorf("audit row = %+v, want plugin_installed/mem0/success", e)
	}
}

func TestInstallPlugin_Validation(t *testing.T) {
	tests := []struct {
		name      string
		overrides map[string]any
		want      int
	}{
		{"uppercase id", map[string]any{"id": "Mem0"}, http.StatusBadRequest},
		{"short digest", map[string]any{"imageDigest": "sha256:abc"}, http.StatusBadRequest},
		{"zero port", map[string]any{"servicePort": 0}, http.StatusBadRequest},
		{"port above range", map[string]any{"servicePort": 65536}, http.StatusBadRequest},
		{"missing service name", map[string]any{"serviceName": ""}, http.StatusBadRequest},
		{"missing image repository", map[string]any{"imageRepository": ""}, http.StatusBadRequest},
		// The service name is interpolated into an in-cluster URL by the
		// operator, so anything that is not a DNS label could redirect the
		// operator at an attacker-chosen host.
		{"service name with dot and hash", map[string]any{"serviceName": "evil.com#"}, http.StatusBadRequest},
		{"service name with uppercase and underscore", map[string]any{"serviceName": "Bad_Name"}, http.StatusBadRequest},
		{"service name with slash", map[string]any{"serviceName": "a/b"}, http.StatusBadRequest},
		{"service name too long", map[string]any{"serviceName": strings.Repeat("a", 64)}, http.StatusBadRequest},
		// A trailing '-' is a valid slug for a URL segment but not a valid
		// Kubernetes object name, so the API server would reject it and the
		// install would surface as a 500.
		{"id with trailing dash", map[string]any{"id": "a-"}, http.StatusBadRequest},
		{"id too short", map[string]any{"id": "a"}, http.StatusBadRequest},
		{"id too long", map[string]any{"id": "a" + strings.Repeat("b", 31)}, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, audit := pluginTestServer(t)
			rec := doJSON(t, s, http.MethodPost, "/api/v1/plugins", pluginInstallBody(tc.overrides), nil)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tc.want, rec.Body.String())
			}
			if len(audit.inserted) != 0 {
				t.Errorf("audit rows = %d, want 0 for a rejected install", len(audit.inserted))
			}
		})
	}

	t.Run("duplicate id", func(t *testing.T) {
		s, _ := pluginTestServer(t)
		if rec := doJSON(t, s, http.MethodPost, "/api/v1/plugins", pluginInstallBody(nil), nil); rec.Code != http.StatusCreated {
			t.Fatalf("first install: status = %d, body = %s", rec.Code, rec.Body.String())
		}
		rec := doJSON(t, s, http.MethodPost, "/api/v1/plugins", pluginInstallBody(nil), nil)
		if rec.Code != http.StatusConflict {
			t.Fatalf("second install: status = %d, want 409, body = %s", rec.Code, rec.Body.String())
		}
	})
}

// --- authorization ---

func TestPluginWrites_NonAdminForbidden(t *testing.T) {
	s, audit := pluginAuthzTestServer(t, map[string]struct{}{"g1": {}}, pluginCRReady(pluginCR("mem0", false), true, "Ready", "ok", []string{"agents:read"}))
	member := asUser("member", "member")

	writes := []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/api/v1/plugins", pluginInstallBody(map[string]any{"id": "other"})},
		{http.MethodDelete, "/api/v1/plugins/mem0", nil},
		{http.MethodPost, "/api/v1/plugins/mem0/enable", nil},
		{http.MethodPost, "/api/v1/plugins/mem0/disable", nil},
		{http.MethodPost, "/api/v1/plugins/mem0/approve-permissions", map[string]any{"permissions": []string{"agents:read"}}},
		{http.MethodPut, "/api/v1/plugins/mem0/group-access", map[string]any{"groups": []string{"g1"}}},
	}
	for _, w := range writes {
		rec := doJSON(t, s, w.method, w.path, w.body, member)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want 403, body = %s", w.method, w.path, rec.Code, rec.Body.String())
		}
	}

	// The existing CR is untouched and nothing was created or audited.
	p := getPluginCR(t, s, "mem0")
	if p.Spec.Disabled || len(p.Spec.ApprovedPermissions) != 0 || len(p.Spec.GroupAccess) != 0 {
		t.Errorf("plugin mutated by forbidden writes: %+v", p.Spec)
	}
	var other connectorv1alpha1.Plugin
	if err := s.client.Get(context.Background(), types.NamespacedName{Name: "other", Namespace: s.ns}, &other); err == nil {
		t.Errorf("forbidden install created plugin %q", other.Name)
	}
	if len(audit.inserted) != 0 {
		t.Errorf("audit rows = %d, want 0 for forbidden writes", len(audit.inserted))
	}
}

// --- list / get shapes ---

func TestListPlugins_NonAdminSeesOnlyEnabledReady(t *testing.T) {
	a := pluginCRReady(pluginCR("a", false), true, "Ready", "ok", []string{"agents:read"})
	b := pluginCRReady(pluginCR("b", false), false, "PermissionsPending", "2 permissions need approval", []string{"agents:write"})
	c := pluginCRReady(pluginCR("c", true), true, "Ready", "ok", nil)
	s, _ := pluginAuthzTestServer(t, nil, a, b, c)

	// Non-admin: only the enabled + Ready plugin, in the reduced public shape.
	rec := doJSON(t, s, http.MethodGet, "/api/v1/plugins", nil, asUser("member", "member"))
	if rec.Code != http.StatusOK {
		t.Fatalf("member list: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var pub struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	decodeJSON(t, rec.Body.Bytes(), &pub)
	if pub.Total != 1 || len(pub.Items) != 1 || pub.Items[0]["id"] != "a" {
		t.Fatalf("member items = %+v, want exactly [a]", pub.Items)
	}
	for _, key := range []string{"approvedPermissions", "groupAccess"} {
		if _, ok := pub.Items[0][key]; ok {
			t.Errorf("public list item exposes %q", key)
		}
	}
	if pub.Items[0]["version"] != "1.2.3" {
		t.Errorf("public item = %+v, want version 1.2.3", pub.Items[0])
	}

	// Admin: all three, with the full fields.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/plugins", nil, asUser("admin", "admin"))
	if rec.Code != http.StatusOK {
		t.Fatalf("admin list: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var full struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	decodeJSON(t, rec.Body.Bytes(), &full)
	if full.Total != 3 {
		t.Fatalf("admin total = %d, want 3", full.Total)
	}
	byID := map[string]map[string]any{}
	for _, item := range full.Items {
		byID[item["id"].(string)] = item
	}
	if _, ok := byID["b"]; !ok {
		t.Errorf("admin list is missing pending plugin b: %+v", full.Items)
	}
	if _, ok := byID["c"]; !ok {
		t.Errorf("admin list is missing disabled plugin c: %+v", full.Items)
	}
	if byID["b"]["reason"] != "PermissionsPending" {
		t.Errorf("plugin b reason = %v, want PermissionsPending", byID["b"]["reason"])
	}

	// Detail GET mirrors the list visibility rules.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/plugins/b", nil, asUser("member", "member"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("member GET pending plugin: status = %d, want 404", rec.Code)
	}
	rec = doJSON(t, s, http.MethodGet, "/api/v1/plugins/a", nil, asUser("member", "member"))
	if rec.Code != http.StatusOK {
		t.Fatalf("member GET ready plugin: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var item map[string]any
	decodeJSON(t, rec.Body.Bytes(), &item)
	for _, key := range []string{"approvedPermissions", "groupAccess"} {
		if _, ok := item[key]; ok {
			t.Errorf("public detail exposes %q", key)
		}
	}
}

func TestListPlugins_NonAdminStripsAdminPages(t *testing.T) {
	// Create a manifest with two pages: one public and one admin-only.
	adminPageManifest := `{
		"id":"admin-pages","pluginApi":"1.1","version":"1.0.0",
		"ui":{
			"entry":"/index.js",
			"pages":[
				{"path":"/","label":"Home"},
				{"path":"/settings","label":"Settings","visibility":"admin"}
			]
		}
	}`
	p := pluginCRReady(pluginCR("admin-pages", false), true, "Ready", "ok", nil)
	p.Status.Manifest.Raw = []byte(adminPageManifest)

	s, _ := pluginAuthzTestServer(t, nil, p)

	// Non-admin: sees only the public page
	rec := doJSON(t, s, http.MethodGet, "/api/v1/plugins", nil, asUser("member", "member"))
	if rec.Code != http.StatusOK {
		t.Fatalf("member list: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var pubList struct {
		Items []map[string]any `json:"items"`
	}
	decodeJSON(t, rec.Body.Bytes(), &pubList)
	if len(pubList.Items) != 1 {
		t.Fatalf("member items = %v, want exactly 1", pubList.Items)
	}
	var pubManifest map[string]any
	pubManifestBytes, _ := json.Marshal(pubList.Items[0]["manifest"])
	if err := json.Unmarshal(pubManifestBytes, &pubManifest); err != nil {
		t.Fatalf("decoding non-admin manifest: %v", err)
	}
	pubPages, ok := pubManifest["ui"].(map[string]any)["pages"].([]any)
	if !ok || len(pubPages) != 1 {
		t.Errorf("non-admin manifest pages = %v, want 1 public page", pubPages)
	}
	if pubPages[0].(map[string]any)["path"] != "/" {
		t.Errorf("non-admin page path = %v, want /", pubPages[0])
	}

	// Admin: sees both pages
	rec = doJSON(t, s, http.MethodGet, "/api/v1/plugins", nil, asUser("admin", "admin"))
	if rec.Code != http.StatusOK {
		t.Fatalf("admin list: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var adminList struct {
		Items []map[string]any `json:"items"`
	}
	decodeJSON(t, rec.Body.Bytes(), &adminList)
	if len(adminList.Items) != 1 {
		t.Fatalf("admin items = %v, want exactly 1", adminList.Items)
	}
	var adminManifest map[string]any
	adminManifestBytes, _ := json.Marshal(adminList.Items[0]["manifest"])
	if err := json.Unmarshal(adminManifestBytes, &adminManifest); err != nil {
		t.Fatalf("decoding admin manifest: %v", err)
	}
	adminPages, ok := adminManifest["ui"].(map[string]any)["pages"].([]any)
	if !ok || len(adminPages) != 2 {
		t.Errorf("admin manifest pages = %v, want 2 pages", adminPages)
	}
}

// --- authentication ---

// With authz configured, an anonymous caller gets 401 on every plugin route —
// reads included; the reduced public shape is for authenticated non-admins,
// not for the open internet. Nothing is mutated or audited.
func TestPluginRoutes_Unauthenticated(t *testing.T) {
	seed := pluginCRReady(pluginCR("mem0", false), true, "Ready", "ok", []string{"agents:read"})
	s, audit := pluginAuthzTestServer(t, map[string]struct{}{"g1": {}}, seed)

	calls := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/v1/plugins", nil},
		{http.MethodGet, "/api/v1/plugins/mem0", nil},
		{http.MethodPost, "/api/v1/plugins", pluginInstallBody(map[string]any{"id": "other"})},
		{http.MethodDelete, "/api/v1/plugins/mem0", nil},
		{http.MethodPost, "/api/v1/plugins/mem0/enable", nil},
		{http.MethodPost, "/api/v1/plugins/mem0/disable", nil},
		{http.MethodPost, "/api/v1/plugins/mem0/approve-permissions", map[string]any{"permissions": []string{"agents:read"}}},
		{http.MethodPut, "/api/v1/plugins/mem0/group-access", map[string]any{"groups": []string{"g1"}}},
	}
	for _, c := range calls {
		rec := doJSON(t, s, c.method, c.path, c.body, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want 401, body = %s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
	if p := getPluginCR(t, s, "mem0"); p.Spec.Disabled || len(p.Spec.GroupAccess) != 0 {
		t.Errorf("plugin mutated by anonymous calls: %+v", p.Spec)
	}
	if len(audit.inserted) != 0 {
		t.Errorf("audit rows = %d, want 0 for anonymous calls", len(audit.inserted))
	}
}

// --- response shape ---

// A freshly installed plugin has no Ready condition yet. The API reports that
// honestly (ready=false, empty reason) instead of inventing one, and a
// plugin the reconciler has not validated is invisible to non-admins.
func TestGetPlugin_ReconcilerHasNotRunYet(t *testing.T) {
	s, _ := pluginAuthzTestServer(t, nil, pluginCR("mem0", false))

	rec := doJSON(t, s, http.MethodGet, "/api/v1/plugins/mem0", nil, asUser("admin", "admin"))
	if rec.Code != http.StatusOK {
		t.Fatalf("admin GET: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp PluginResponse
	decodeJSON(t, rec.Body.Bytes(), &resp)
	if resp.Ready || resp.Reason != "" || resp.Message != "" {
		t.Errorf("absent condition mapped to ready=%v reason=%q message=%q, want false/\"\"/\"\"",
			resp.Ready, resp.Reason, resp.Message)
	}
	if resp.Version != "" || resp.Manifest != nil {
		t.Errorf("unreconciled plugin exposes version=%q manifest=%s, want empty", resp.Version, resp.Manifest)
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/plugins/mem0", nil, asUser("member", "member"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("member GET on unreconciled plugin: status = %d, want 404", rec.Code)
	}
}

// The admin detail response carries the whole record: spec, flattened status,
// both permission sets, group access, and the raw manifest.
func TestGetPlugin_AdminFullShape(t *testing.T) {
	seed := pluginCRReady(pluginCR("mem0", false), false, "PermissionsPending", "awaiting approval", []string{"agents:read"})
	seed.Spec.ApprovedPermissions = []string{"agents:read"}
	seed.Spec.GroupAccess = []string{"g1"}
	s, _ := pluginTestServer(t, seed)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/plugins/mem0", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp PluginResponse
	decodeJSON(t, rec.Body.Bytes(), &resp)
	if resp.ID != "mem0" || resp.DisplayName != "Plugin mem0" || resp.Version != "1.2.3" {
		t.Errorf("identity fields = %+v", resp)
	}
	if resp.ServiceName != "mem0-svc" || resp.ServicePort != 8080 {
		t.Errorf("service = %s:%d, want mem0-svc:8080", resp.ServiceName, resp.ServicePort)
	}
	if resp.ImageRepository != "registry.test/mem0" || resp.ImageDigest != pluginTestDigest {
		t.Errorf("image = %s@%s", resp.ImageRepository, resp.ImageDigest)
	}
	if resp.Ready || resp.Reason != "PermissionsPending" || resp.Message != "awaiting approval" {
		t.Errorf("condition fields = ready=%v reason=%q message=%q", resp.Ready, resp.Reason, resp.Message)
	}
	if strings.Join(resp.DeclaredPermissions, ",") != "agents:read" ||
		strings.Join(resp.ApprovedPermissions, ",") != "agents:read" ||
		strings.Join(resp.GroupAccess, ",") != "g1" {
		t.Errorf("permission/group fields = %v / %v / %v", resp.DeclaredPermissions, resp.ApprovedPermissions, resp.GroupAccess)
	}
	var manifest map[string]any
	if err := json.Unmarshal(resp.Manifest, &manifest); err != nil || manifest["pluginApi"] != "1.0" {
		t.Errorf("manifest = %s, want the raw served manifest with pluginApi 1.0", resp.Manifest)
	}
}

// The audit store is optional (a hub without a DB must still manage
// plugins); a nil store is a silent no-op, never a failure or a panic.
func TestPluginWrites_NilAuditStore(t *testing.T) {
	s := testServer(t)
	registerPluginRoutes(s)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/plugins", pluginInstallBody(nil), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("install without audit store: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	getPluginCR(t, s, "mem0")
}

// --- pagination ---

// The list is paged like every other collection: sorted by id (the fake client
// iterates a map, so the sort is what makes the pages stable), with the
// standard envelope, and a page past the end returns an empty array rather
// than null so the frontend can render it directly.
func TestListPlugins_Pagination(t *testing.T) {
	seed := func(id string) *connectorv1alpha1.Plugin {
		return pluginCRReady(pluginCR(id, false), true, "Ready", "ok", nil)
	}
	s, _ := pluginTestServer(t, seed("a"), seed("b"), seed("c"))

	rec := doJSON(t, s, http.MethodGet, "/api/v1/plugins?pageSize=2", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("page 1: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Items      []map[string]any `json:"items"`
		Total      int              `json:"total"`
		Page       int              `json:"page"`
		PageSize   int              `json:"pageSize"`
		TotalPages int              `json:"totalPages"`
	}
	decodeJSON(t, rec.Body.Bytes(), &page)
	if page.Total != 3 || page.Page != 1 || page.PageSize != 2 || page.TotalPages != 2 {
		t.Fatalf("page 1 meta = %+v, want total 3 page 1 size 2 pages 2", page)
	}
	if len(page.Items) != 2 || page.Items[0]["id"] != "a" || page.Items[1]["id"] != "b" {
		t.Fatalf("page 1 items = %+v, want [a b] sorted by id", page.Items)
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/plugins?pageSize=2&page=2", nil, nil)
	decodeJSON(t, rec.Body.Bytes(), &page)
	if len(page.Items) != 1 || page.Items[0]["id"] != "c" {
		t.Fatalf("page 2 items = %+v, want [c]", page.Items)
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/plugins?pageSize=2&page=9", nil, nil)
	if !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("past-the-end page body = %s, want an empty items array", rec.Body.String())
	}

	// A non-numeric page parameter is a 400, not a silent default.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/plugins?pageSize=two", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid pageSize: status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}

func TestPluginRoutes_MethodNotAllowed(t *testing.T) {
	s, _ := pluginTestServer(t, pluginCRReady(pluginCR("mem0", false), true, "Ready", "ok", nil))

	rec := doJSON(t, s, http.MethodPut, "/api/v1/plugins", nil, nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT /api/v1/plugins: status = %d, want 405, body = %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodPatch, "/api/v1/plugins/mem0", nil, nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("PATCH /api/v1/plugins/mem0: status = %d, want 405, body = %s", rec.Code, rec.Body.String())
	}
}

// --- approve-permissions ---

func TestApprovePermissions(t *testing.T) {
	declared := []string{"agents:read", "personas:write"}
	seed := pluginCRReady(pluginCR("mem0", false), false, "PermissionsPending", "declared permissions need approval", declared)
	s, audit := pluginTestServer(t, seed)

	// Mismatching subset: 409 naming the current declared set, spec untouched.
	rec := doJSON(t, s, http.MethodPost, "/api/v1/plugins/mem0/approve-permissions",
		map[string]any{"permissions": []string{"agents:read"}}, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("mismatch: status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "personas:write") {
		t.Errorf("409 body %s does not name the declared set", rec.Body.String())
	}
	if p := getPluginCR(t, s, "mem0"); len(p.Spec.ApprovedPermissions) != 0 {
		t.Errorf("spec.approvedPermissions = %v after a rejected approval", p.Spec.ApprovedPermissions)
	}

	// Matching set in a different order: 200 and the set lands in spec.
	rec = doJSON(t, s, http.MethodPost, "/api/v1/plugins/mem0/approve-permissions",
		map[string]any{"permissions": []string{"personas:write", "agents:read"}}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("match: status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	p := getPluginCR(t, s, "mem0")
	if len(p.Spec.ApprovedPermissions) != 2 ||
		p.Spec.ApprovedPermissions[0] != "agents:read" || p.Spec.ApprovedPermissions[1] != "personas:write" {
		t.Errorf("spec.approvedPermissions = %v, want [agents:read personas:write]", p.Spec.ApprovedPermissions)
	}

	if len(audit.inserted) != 1 || audit.inserted[0].EventType != "plugin_permissions_approved" || audit.inserted[0].Target != "mem0" {
		t.Errorf("audit rows = %+v, want one plugin_permissions_approved/mem0", audit.inserted)
	}
}

// --- group-access ---

func TestGroupAccess(t *testing.T) {
	seed := pluginCRReady(pluginCR("mem0", false), true, "Ready", "ok", nil)
	s, audit := pluginAuthzTestServer(t, map[string]struct{}{"g1": {}}, seed)
	admin := asUser("admin", "admin")

	// Unknown group: 400 and the spec stays as it was.
	rec := doJSON(t, s, http.MethodPut, "/api/v1/plugins/mem0/group-access", map[string]any{"groups": []string{"ghost"}}, admin)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown group: status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	if p := getPluginCR(t, s, "mem0"); len(p.Spec.GroupAccess) != 0 {
		t.Errorf("spec.groupAccess = %v after rejected update", p.Spec.GroupAccess)
	}

	// Existing group: 200 and the spec is updated.
	rec = doJSON(t, s, http.MethodPut, "/api/v1/plugins/mem0/group-access", map[string]any{"groups": []string{"g1"}}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("known group: status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	p := getPluginCR(t, s, "mem0")
	if len(p.Spec.GroupAccess) != 1 || p.Spec.GroupAccess[0] != "g1" {
		t.Errorf("spec.groupAccess = %v, want [g1]", p.Spec.GroupAccess)
	}

	if len(audit.inserted) != 1 || audit.inserted[0].EventType != "plugin_group_access_changed" || audit.inserted[0].Target != "mem0" {
		t.Errorf("audit rows = %+v, want one plugin_group_access_changed/mem0", audit.inserted)
	}
}

// A store failure must not be reported as "group does not exist": an admin whose
// database hiccupped during a group-access update would go re-create a group
// that is still there.
func TestGroupAccess_StoreErrorIs500(t *testing.T) {
	seed := pluginCRReady(pluginCR("mem0", false), true, "Ready", "ok", nil)
	s := pluginBrokenGroupStoreServer(t, seed)

	rec := doJSON(t, s, http.MethodPut, "/api/v1/plugins/mem0/group-access",
		map[string]any{"groups": []string{"g1"}}, asUser("admin", "admin"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body = %s", rec.Code, rec.Body.String())
	}
	if p := getPluginCR(t, s, "mem0"); len(p.Spec.GroupAccess) != 0 {
		t.Errorf("spec.groupAccess = %v after failed update, want unchanged", p.Spec.GroupAccess)
	}
}

// --- enable / disable / uninstall ---

func TestEnableDisable(t *testing.T) {
	seed := pluginCRReady(pluginCR("mem0", false), true, "Ready", "ok", nil)
	s, audit := pluginTestServer(t, seed)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/plugins/mem0/disable", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp PluginResponse
	decodeJSON(t, rec.Body.Bytes(), &resp)
	if !resp.Disabled {
		t.Errorf("response disabled = false, want true")
	}
	if p := getPluginCR(t, s, "mem0"); !p.Spec.Disabled {
		t.Errorf("spec.disabled = false after disable")
	}

	rec = doJSON(t, s, http.MethodPost, "/api/v1/plugins/mem0/enable", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if p := getPluginCR(t, s, "mem0"); p.Spec.Disabled {
		t.Errorf("spec.disabled = true after enable")
	}

	if len(audit.inserted) != 2 ||
		audit.inserted[0].EventType != "plugin_disabled" ||
		audit.inserted[1].EventType != "plugin_enabled" {
		t.Errorf("audit rows = %+v, want plugin_disabled then plugin_enabled", audit.inserted)
	}
	for _, e := range audit.inserted {
		if e.Target != "mem0" || e.Outcome != "success" {
			t.Errorf("audit row = %+v, want target mem0 / success", e)
		}
	}
}

func TestUninstallPlugin(t *testing.T) {
	seed := pluginCRReady(pluginCR("mem0", false), true, "Ready", "ok", nil)
	s, audit := pluginTestServer(t, seed)

	rec := doJSON(t, s, http.MethodDelete, "/api/v1/plugins/mem0", nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body = %s", rec.Code, rec.Body.String())
	}

	var p connectorv1alpha1.Plugin
	if err := s.client.Get(context.Background(), types.NamespacedName{Name: "mem0", Namespace: s.ns}, &p); err == nil {
		t.Errorf("plugin still exists after uninstall")
	}
	if len(audit.inserted) != 1 || audit.inserted[0].EventType != "plugin_uninstalled" || audit.inserted[0].Target != "mem0" {
		t.Errorf("audit rows = %+v, want one plugin_uninstalled/mem0", audit.inserted)
	}

	// Uninstalling a plugin that is not there is a 404, not a second audit row.
	rec = doJSON(t, s, http.MethodDelete, "/api/v1/plugins/mem0", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("second uninstall: status = %d, want 404", rec.Code)
	}
	if len(audit.inserted) != 1 {
		t.Errorf("audit rows = %d, want still 1", len(audit.inserted))
	}
}

// TestGetPlugin_ExposesSignatureToAdminsOnly verifies that signature state
// appears in the admin response but not in the public response.
func TestGetPlugin_ExposesSignatureToAdminsOnly(t *testing.T) {
	seed := pluginCRReady(pluginCR("mem0", false), true, "Ready", "ok", nil)
	seed.Status.Signature = &connectorv1alpha1.PluginSignatureStatus{
		Verified:  true,
		Signer:    "key:abc",
		CheckedAt: metav1.Time{Time: time.Now()},
	}
	seed.Status.VerifiedEndpoints = []string{"10.0.0.1:8080", "10.0.0.2:8080"}
	s, _ := pluginAuthzTestServer(t, nil, seed)

	// Admin: signature and verifiedEndpoints are exposed
	adminRec := doJSON(t, s, http.MethodGet, "/api/v1/plugins/mem0", nil, asUser("admin", "admin"))
	if adminRec.Code != http.StatusOK {
		t.Fatalf("admin GET: status = %d, body = %s", adminRec.Code, adminRec.Body.String())
	}
	var adminResp PluginResponse
	decodeJSON(t, adminRec.Body.Bytes(), &adminResp)
	if adminResp.Signature == nil {
		t.Errorf("admin response signature = nil, want signature data")
	} else {
		if adminResp.Signature.Verified != true {
			t.Errorf("signature.verified = %v, want true", adminResp.Signature.Verified)
		}
		if adminResp.Signature.Signer != "key:abc" {
			t.Errorf("signature.signer = %q, want key:abc", adminResp.Signature.Signer)
		}
	}
	expectedEndpoints := []string{"10.0.0.1:8080", "10.0.0.2:8080"}
	if len(adminResp.VerifiedEndpoints) != len(expectedEndpoints) {
		t.Errorf("verifiedEndpoints length = %d, want %d", len(adminResp.VerifiedEndpoints), len(expectedEndpoints))
	} else {
		for i, ep := range expectedEndpoints {
			if adminResp.VerifiedEndpoints[i] != ep {
				t.Errorf("verifiedEndpoints[%d] = %q, want %q", i, adminResp.VerifiedEndpoints[i], ep)
			}
		}
	}

	// Non-admin: signature and verifiedEndpoints are not exposed
	memberRec := doJSON(t, s, http.MethodGet, "/api/v1/plugins/mem0", nil, asUser("member", "member"))
	if memberRec.Code != http.StatusOK {
		t.Fatalf("member GET: status = %d, body = %s", memberRec.Code, memberRec.Body.String())
	}
	var memberResp PluginPublicResponse
	decodeJSON(t, memberRec.Body.Bytes(), &memberResp)
	// PluginPublicResponse should not have signature or verifiedEndpoints fields
	// We verify this by decoding into a map and checking the keys
	var memberMap map[string]any
	json.Unmarshal(memberRec.Body.Bytes(), &memberMap)
	if _, hasSignature := memberMap["signature"]; hasSignature {
		t.Errorf("public response exposes signature field")
	}
	if _, hasEndpoints := memberMap["verifiedEndpoints"]; hasEndpoints {
		t.Errorf("public response exposes verifiedEndpoints field")
	}
}
