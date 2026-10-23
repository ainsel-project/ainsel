package pluginsync_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	"github.com/ainsel-project/ainsel/services/hub/internal/mcpservers"
	"github.com/ainsel-project/ainsel/services/hub/internal/pluginsync"
	connectorv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testNS      = "ainsel"
	testSelfURL = "http://hub-backend.ainsel.svc.cluster.local:8080/"
	// testSelfURLEffective is testSelfURL with its trailing slash removed: the
	// URL the registry row is expected to carry.
	testSelfURLEffective = "http://hub-backend.ainsel.svc.cluster.local:8080"
)

// fakeMCPRegistry is an in-memory stand-in for mcpservers.Service, recording
// every write so a test can assert on the exact reconcile a run produced.
type fakeMCPRegistry struct {
	rows      map[string]*mcpservers.MCPServer
	upserted  []*mcpservers.MCPServer
	deleted   []string
	listErr   error
	upsertErr error
	// upsertFailFor fails only the upsert of this row name, so a test can
	// watch the other rows go through.
	upsertFailFor string
	deleteErr     error
}

func newFakeMCPRegistry(rows ...*mcpservers.MCPServer) *fakeMCPRegistry {
	f := &fakeMCPRegistry{rows: map[string]*mcpservers.MCPServer{}}
	for _, r := range rows {
		f.rows[r.Name] = r
	}
	return f
}

func (f *fakeMCPRegistry) List(_ context.Context) ([]*mcpservers.MCPServer, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]*mcpservers.MCPServer, 0, len(f.rows))
	for _, r := range f.rows {
		cp := *r
		out = append(out, &cp)
	}
	return out, nil
}

func (f *fakeMCPRegistry) Upsert(_ context.Context, m *mcpservers.MCPServer) error {
	if f.upsertErr != nil || f.upsertFailFor == m.Name {
		return errors.New("upsert failed")
	}
	f.upserted = append(f.upserted, m)
	cp := *m
	f.rows[m.Name] = &cp
	return nil
}

func (f *fakeMCPRegistry) DeleteManaged(_ context.Context, name string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if _, ok := f.rows[name]; !ok {
		return mcpservers.ErrNotFound
	}
	f.deleted = append(f.deleted, name)
	delete(f.rows, name)
	return nil
}

// publicCall and groupCall record the visibility writes the syncer makes so
// tests can assert on the exact arguments.
type publicCall struct {
	resourceType string
	name         string
	public       bool
}

type groupCall struct {
	resourceType string
	name         string
	groupID      string
	public       bool
}

// fakeVisibility stands in for the authz store. publicErr lets a test
// reproduce the real store's "no ownership row yet" answer.
type fakeVisibility struct {
	publicCalls []publicCall
	groupCalls  []groupCall
	publicErr   error
	groupErr    error
}

func (f *fakeVisibility) SetResourcePublic(_ context.Context, resourceType, name string, public bool) error {
	if f.publicErr != nil {
		return f.publicErr
	}
	f.publicCalls = append(f.publicCalls, publicCall{resourceType, name, public})
	return nil
}

func (f *fakeVisibility) SetResourceGroup(_ context.Context, resourceType, name, groupID string, public bool) error {
	if f.groupErr != nil {
		return f.groupErr
	}
	f.groupCalls = append(f.groupCalls, groupCall{resourceType, name, groupID, public})
	return nil
}

func pluginWithManifest(t *testing.T, name, manifest string, disabled bool) client.Object {
	t.Helper()
	p := &connectorv1alpha1.Plugin{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec: connectorv1alpha1.PluginSpec{
			Service:  connectorv1alpha1.PluginServiceRef{Name: name, Port: 8080},
			Disabled: disabled,
		},
	}
	if manifest != "" {
		p.Status.Manifest = &runtime.RawExtension{Raw: []byte(manifest)}
	}
	return p
}

func syncClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := connectorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

// mcpManifest is the smallest manifest that declares an MCP endpoint.
const mcpManifest = `{"id":"mem0","displayName":"Mem0","version":"1.0.0","pluginApi":"1.1","mcp":{"path":"/mcp"}}`

func desiredRow(name, displayName string) *mcpservers.MCPServer {
	return &mcpservers.MCPServer{
		Name:        name,
		DisplayName: displayName,
		Description: "Tools of the " + displayName + " plugin",
		URL: testSelfURLEffective + "/api/internal/plugins/" + mcpservers.PluginIDFromName(name) +
			"/agent-mcp",
		TokenFromEnv: pluginsync.MCPServerTokenFromEnv,
		ManagedBy:    mcpservers.ManagedByPlugin,
	}
}

func TestSync_CreatesPluginMCPServer(t *testing.T) {
	c := syncClient(t, pluginWithManifest(t, "mem0", mcpManifest, false))
	reg := newFakeMCPRegistry()
	vis := &fakeVisibility{}
	s := pluginsync.New(c, testNS, reg, nil, testSelfURL, nil)
	s.SetResourceVisibility(vis)

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	if len(reg.rows) != 1 {
		t.Fatalf("expected exactly 1 registry row, got %d (%+v)", len(reg.rows), reg.rows)
	}
	got := reg.rows["plugin-mem0"]
	if got == nil {
		t.Fatalf("expected row plugin-mem0, got rows %v", reg.rows)
	}
	want := desiredRow("plugin-mem0", "Mem0")
	if *got != *want {
		t.Errorf("row mismatch:\n got %+v\nwant %+v", *got, *want)
	}
	if len(vis.publicCalls) != 1 {
		t.Fatalf("expected 1 SetResourcePublic call, got %v", vis.publicCalls)
	}
	if vis.publicCalls[0] != (publicCall{"mcp-server", "plugin-mem0", true}) {
		t.Errorf("visibility call: %+v", vis.publicCalls[0])
	}
}

// TestSync_DisplayNameFallsBackToPluginID covers a manifest without
// displayName: both the display name and the description use the plugin id.
func TestSync_DisplayNameFallsBackToPluginID(t *testing.T) {
	c := syncClient(t, pluginWithManifest(t, "mem0",
		`{"id":"mem0","version":"1.0.0","pluginApi":"1.1","mcp":{"path":"/mcp"}}`, false))
	reg := newFakeMCPRegistry()
	s := pluginsync.New(c, testNS, reg, nil, testSelfURL, nil)

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	got := reg.rows["plugin-mem0"]
	if got == nil {
		t.Fatalf("expected row plugin-mem0, got %v", reg.rows)
	}
	if got.DisplayName != "mem0" {
		t.Errorf("DisplayName: want mem0, got %q", got.DisplayName)
	}
	if got.Description != "Tools of the mem0 plugin" {
		t.Errorf("Description: %q", got.Description)
	}
}

// TestSync_KeepsRowForDisabledPlugin is ruling R2: disabling a plugin stops
// its tools, not its registry row. A matching row means no write at all.
func TestSync_KeepsRowForDisabledPlugin(t *testing.T) {
	c := syncClient(t, pluginWithManifest(t, "mem0", mcpManifest, true))
	reg := newFakeMCPRegistry(desiredRow("plugin-mem0", "Mem0"))
	s := pluginsync.New(c, testNS, reg, nil, testSelfURL, nil)

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if _, ok := reg.rows["plugin-mem0"]; !ok {
		t.Fatalf("row for disabled plugin was removed: %v", reg.rows)
	}
	if len(reg.deleted) != 0 {
		t.Errorf("expected no deletes, got %v", reg.deleted)
	}
	if len(reg.upserted) != 0 {
		t.Errorf("expected no upsert for an already-matching row, got %v", reg.upserted)
	}
}

func TestSync_RemovesRowWhenPluginDeleted(t *testing.T) {
	c := syncClient(t)
	reg := newFakeMCPRegistry(desiredRow("plugin-mem0", "Mem0"))
	s := pluginsync.New(c, testNS, reg, nil, testSelfURL, nil)

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if len(reg.deleted) != 1 || reg.deleted[0] != "plugin-mem0" {
		t.Errorf("expected delete of plugin-mem0, got %v", reg.deleted)
	}
	if len(reg.rows) != 0 {
		t.Errorf("expected no rows left, got %v", reg.rows)
	}
}

// TestSync_RemovesRowWhenManifestDropsMCP: the CR stays, but the plugin no
// longer declares an MCP endpoint, so its row goes.
func TestSync_RemovesRowWhenManifestDropsMCP(t *testing.T) {
	c := syncClient(t, pluginWithManifest(t, "mem0",
		`{"id":"mem0","displayName":"Mem0","version":"1.0.0","pluginApi":"1.1"}`, false))
	reg := newFakeMCPRegistry(desiredRow("plugin-mem0", "Mem0"))
	s := pluginsync.New(c, testNS, reg, nil, testSelfURL, nil)

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if len(reg.deleted) != 1 || reg.deleted[0] != "plugin-mem0" {
		t.Errorf("expected delete of plugin-mem0, got %v", reg.deleted)
	}
}

// TestSync_LeavesUserAndHelmRows is the ownership boundary: the syncer only
// ever deletes rows it owns, whatever else is in the registry.
func TestSync_LeavesUserAndHelmRows(t *testing.T) {
	c := syncClient(t)
	rows := []*mcpservers.MCPServer{
		{Name: "github", DisplayName: "GitHub", URL: "https://mcp.github.com", ManagedBy: "user"},
		{Name: "example-mcp", DisplayName: "Example", URL: "http://example-mcp", ManagedBy: "helm"},
	}
	reg := newFakeMCPRegistry(rows...)
	s := pluginsync.New(c, testNS, reg, nil, testSelfURL, nil)

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if len(reg.deleted) != 0 {
		t.Errorf("expected no deletes, got %v", reg.deleted)
	}
	if len(reg.rows) != 2 {
		t.Errorf("expected both rows to survive, got %v", reg.rows)
	}
}

// TestSync_UpdatesRowWhenFieldsDiffer: a hub moved to a new address must
// rewrite the URL, and a manifest rename must follow the display name.
func TestSync_UpdatesRowWhenFieldsDiffer(t *testing.T) {
	c := syncClient(t, pluginWithManifest(t, "mem0", mcpManifest, false))
	stale := desiredRow("plugin-mem0", "Mem0")
	stale.URL = "http://old-hub:8080/api/internal/plugins/mem0/agent-mcp"
	reg := newFakeMCPRegistry(stale)
	s := pluginsync.New(c, testNS, reg, nil, testSelfURL, nil)

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if len(reg.upserted) != 1 {
		t.Fatalf("expected 1 upsert, got %d (%+v)", len(reg.upserted), reg.upserted)
	}
	if reg.upserted[0].URL != testSelfURLEffective+"/api/internal/plugins/mem0/agent-mcp" {
		t.Errorf("URL: %s", reg.upserted[0].URL)
	}
}

// TestSync_CreatesOwnershipRowWhenMissing: the authz store only toggles
// public on an existing row, and default-closed means a row with no entry at
// all is invisible. The syncer therefore creates the entry when it is missing.
func TestSync_CreatesOwnershipRowWhenMissing(t *testing.T) {
	c := syncClient(t, pluginWithManifest(t, "mem0", mcpManifest, false))
	reg := newFakeMCPRegistry()
	vis := &fakeVisibility{publicErr: authz.ErrNotFound}
	s := pluginsync.New(c, testNS, reg, nil, testSelfURL, nil)
	s.SetResourceVisibility(vis)

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if len(vis.groupCalls) != 1 {
		t.Fatalf("expected 1 SetResourceGroup call, got %v", vis.groupCalls)
	}
	if vis.groupCalls[0] != (groupCall{"mcp-server", "plugin-mem0", "legacy", true}) {
		t.Errorf("ownership call: %+v", vis.groupCalls[0])
	}
}

// TestSync_ContinuesAfterRowError: one bad row must not strand the others,
// but the run still reports that something failed.
func TestSync_ContinuesAfterRowError(t *testing.T) {
	c := syncClient(t,
		pluginWithManifest(t, "a", `{"id":"a","displayName":"A","version":"1.0.0","pluginApi":"1.1","mcp":{"path":"/mcp"}}`, false),
		pluginWithManifest(t, "b", `{"id":"b","displayName":"B","version":"1.0.0","pluginApi":"1.1","mcp":{"path":"/mcp"}}`, false),
	)
	reg := newFakeMCPRegistry()
	reg.upsertFailFor = "plugin-a"
	s := pluginsync.New(c, testNS, reg, nil, testSelfURL, nil)

	err := s.SyncOnce(context.Background())
	if err == nil {
		t.Fatal("expected SyncOnce to report the failed upsert")
	}
	if _, ok := reg.rows["plugin-b"]; !ok {
		t.Errorf("expected row plugin-b despite plugin-a failing, got %v", reg.rows)
	}
}

func TestSync_ListError(t *testing.T) {
	reg := newFakeMCPRegistry()
	reg.listErr = errors.New("db down")
	s := pluginsync.New(syncClient(t), testNS, reg, nil, testSelfURL, nil)

	if err := s.SyncOnce(context.Background()); err == nil {
		t.Fatal("expected SyncOnce to report the list failure")
	}
}

// TestSync_SkillRegistryOptional: New accepts nil for the skill registry and
// the fetcher and reconciles MCP rows alone.
func TestSync_SkillRegistryOptional(t *testing.T) {
	c := syncClient(t, pluginWithManifest(t, "mem0", mcpManifest, false))
	reg := newFakeMCPRegistry()
	s := pluginsync.New(c, testNS, reg, nil, testSelfURL, nil)

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if len(reg.rows) != 1 {
		t.Fatalf("expected the MCP row to be created, got %v", reg.rows)
	}
}
