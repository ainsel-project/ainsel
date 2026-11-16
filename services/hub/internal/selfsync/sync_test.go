package selfsync_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	"github.com/ainsel-project/ainsel/services/hub/internal/mcpservers"
	"github.com/ainsel-project/ainsel/services/hub/internal/selfsync"
)

const (
	// testSelfURL has a trailing slash on purpose: the row must not carry a
	// doubled one.
	testSelfURL        = "http://hub-backend.ainsel.svc.cluster.local:8080/"
	testSelfURLEffecti = "http://hub-backend.ainsel.svc.cluster.local:8080"
	testRowName        = selfsync.PlatformServerName
)

// fakeMCPRegistry is an in-memory stand-in for mcpservers.Service, recording
// every write so a test can assert on the exact reconcile a run produced.
type fakeMCPRegistry struct {
	rows      map[string]*mcpservers.MCPServer
	upserted  []*mcpservers.MCPServer
	listErr   error
	upsertErr error
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
	if f.upsertErr != nil {
		return errors.New("upsert failed")
	}
	f.upserted = append(f.upserted, m)
	cp := *m
	f.rows[m.Name] = &cp
	return nil
}

// fakeVisibility records the visibility writes the syncer makes.
type fakeVisibility struct {
	publicCalls []string
	groupCalls  []string
	// publicErr is returned by SetResourcePublic; authz.ErrNotFound models the
	// "no ownership entry yet" case that makes the syncer create one.
	publicErr error
}

func (f *fakeVisibility) SetResourcePublic(_ context.Context, resourceType, name string, _ bool) error {
	if f.publicErr != nil {
		return f.publicErr
	}
	f.publicCalls = append(f.publicCalls, resourceType+"/"+name)
	return nil
}

func (f *fakeVisibility) SetResourceGroup(_ context.Context, resourceType, name, _ string, _ bool) error {
	f.groupCalls = append(f.groupCalls, resourceType+"/"+name)
	return nil
}

func hubRow(url string) *mcpservers.MCPServer {
	return &mcpservers.MCPServer{
		Name:         testRowName,
		DisplayName:  selfsync.PlatformServerDisplayName,
		Description:  selfsync.PlatformServerDescription,
		URL:          url,
		TokenFromEnv: selfsync.MCPServerTokenFromEnv,
		ManagedBy:    mcpservers.ManagedByHub,
	}
}

func TestSyncOnceRegistersTheRowWhenAbsent(t *testing.T) {
	reg := newFakeMCPRegistry()
	vis := &fakeVisibility{}
	s := selfsync.New(reg, testSelfURL)
	s.SetResourceVisibility(vis)

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if len(reg.upserted) != 1 {
		t.Fatalf("want one upsert, got %d", len(reg.upserted))
	}
	got := reg.upserted[0]
	if got.Name != testRowName || got.ManagedBy != mcpservers.ManagedByHub {
		t.Errorf("row = %q managedBy %q, want %q/%q", got.Name, got.ManagedBy, testRowName, mcpservers.ManagedByHub)
	}
	if got.URL != testSelfURLEffecti+selfsync.AgentMCPEndpointPath {
		t.Errorf("url = %q, want %q", got.URL, testSelfURLEffecti+selfsync.AgentMCPEndpointPath)
	}
	// The credential must be the one the operator already injects into every
	// agent container, or the row is unusable without an operator change.
	if got.TokenFromEnv != "HUB_INTERNAL_VALIDATE_SECRET" {
		t.Errorf("tokenFromEnv = %q, want the platform-injected internal secret", got.TokenFromEnv)
	}
	if len(vis.publicCalls) != 1 || vis.publicCalls[0] != "mcp-server/"+testRowName {
		t.Errorf("visibility writes = %v, want the row opened publicly", vis.publicCalls)
	}
}

func TestSyncOnceIsIdempotent(t *testing.T) {
	reg := newFakeMCPRegistry(hubRow(testSelfURLEffecti + selfsync.AgentMCPEndpointPath))
	vis := &fakeVisibility{}
	s := selfsync.New(reg, testSelfURL)
	s.SetResourceVisibility(vis)

	for i := 0; i < 3; i++ {
		if err := s.SyncOnce(context.Background()); err != nil {
			t.Fatalf("SyncOnce pass %d: %v", i, err)
		}
	}
	if len(reg.upserted) != 0 {
		t.Fatalf("an unchanged row must cost no write, got %d upserts", len(reg.upserted))
	}
	if len(vis.publicCalls) != 0 {
		t.Fatalf("an unchanged row must not be re-opened, got %v", vis.publicCalls)
	}
}

func TestSyncOnceRewritesWhenTheURLDrifts(t *testing.T) {
	reg := newFakeMCPRegistry(hubRow("http://old-hub.ainsel.svc.cluster.local:8080/api/internal/agent-mcp"))
	s := selfsync.New(reg, testSelfURL)

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if len(reg.upserted) != 1 {
		t.Fatalf("want one upsert after a drift, got %d", len(reg.upserted))
	}
	if want := testSelfURLEffecti + selfsync.AgentMCPEndpointPath; reg.upserted[0].URL != want {
		t.Errorf("url = %q, want %q", reg.upserted[0].URL, want)
	}
}

// A hand-made row named "ainsel" is the operator's, not the hub's to fix. The
// collision has to be loud in the log and invisible in the registry.
func TestSyncOnceNeverAdoptsARowTheHubDoesNotOwn(t *testing.T) {
	for _, managedBy := range []string{"", mcpservers.ManagedByUser, mcpservers.ManagedByHelm, mcpservers.ManagedByPlugin} {
		t.Run("managedBy="+managedBy, func(t *testing.T) {
			handMade := &mcpservers.MCPServer{
				Name: testRowName,
				URL:  "https://operator-registered.example/mcp",
				// tokenFromEnv deliberately a human token: exactly the row the
				// hub must not overwrite into an agent's hands.
				TokenFromEnv: "MY_ADMIN_TOKEN",
				ManagedBy:    managedBy,
			}
			reg := newFakeMCPRegistry(handMade)
			s := selfsync.New(reg, testSelfURL)

			if err := s.SyncOnce(context.Background()); err != nil {
				t.Fatalf("SyncOnce: %v", err)
			}
			if len(reg.upserted) != 0 {
				t.Fatalf("a row the hub does not own must never be written, got %d upserts", len(reg.upserted))
			}
			if got := reg.rows[testRowName].URL; got != handMade.URL {
				t.Errorf("row url = %q, want the operator's %q", got, handMade.URL)
			}
		})
	}
}

// The syncer registers; it never prunes. A row it wrote outlives the pass that
// wrote it, and a row it did not write is not its to remove.
func TestSyncOnceNeverDeletes(t *testing.T) {
	reg := newFakeMCPRegistry(
		hubRow(testSelfURLEffecti+selfsync.AgentMCPEndpointPath),
		&mcpservers.MCPServer{Name: "someone-elses", ManagedBy: mcpservers.ManagedByUser},
	)
	s := selfsync.New(reg, testSelfURL)

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if len(reg.rows) != 2 {
		t.Fatalf("no row may be deleted, got %d rows left", len(reg.rows))
	}
}

func TestSyncOnceCreatesTheVisibilityEntryWhenNoneExists(t *testing.T) {
	reg := newFakeMCPRegistry()
	vis := &fakeVisibility{publicErr: authz.ErrNotFound}
	s := selfsync.New(reg, testSelfURL)
	s.SetResourceVisibility(vis)

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if len(vis.groupCalls) != 1 || vis.groupCalls[0] != "mcp-server/"+testRowName {
		t.Errorf("group writes = %v, want the row created open in the default group", vis.groupCalls)
	}
}

func TestSyncOnceReportsRegistryFailures(t *testing.T) {
	reg := newFakeMCPRegistry()
	reg.listErr = errors.New("db down")
	s := selfsync.New(reg, testSelfURL)
	if err := s.SyncOnce(context.Background()); err == nil {
		t.Fatal("want the list failure reported")
	}

	reg = newFakeMCPRegistry()
	reg.upsertErr = errors.New("db down")
	s = selfsync.New(reg, testSelfURL)
	if err := s.SyncOnce(context.Background()); err == nil {
		t.Fatal("want the upsert failure reported")
	}
}

// Without a visibility store the row is still written: a hub that cannot open
// it is degraded, not broken.
func TestSyncOnceWithoutVisibilityStillRegisters(t *testing.T) {
	reg := newFakeMCPRegistry()
	s := selfsync.New(reg, testSelfURL)
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if len(reg.upserted) != 1 {
		t.Fatalf("want one upsert, got %d", len(reg.upserted))
	}
}
