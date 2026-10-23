package mcpservers_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/mcpservers"
)

func TestServiceCreateAndGet(t *testing.T) {
	st := setup(t)
	svc := mcpservers.NewService(st)

	in := &mcpservers.MCPServer{Name: "s1", DisplayName: "S1", URL: "http://s1"}
	if err := svc.Create(context.Background(), in); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := svc.Get(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.URL != "http://s1" {
		t.Errorf("URL: %s", got.URL)
	}
}

func TestServiceDeleteNotFound(t *testing.T) {
	st := setup(t)
	svc := mcpservers.NewService(st)
	err := svc.Delete(context.Background(), "ghost")
	if !errors.Is(err, mcpservers.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestServiceDeleteRefusesHelmManaged(t *testing.T) {
	st := setup(t)
	svc := mcpservers.NewService(st)
	ctx := context.Background()
	_ = svc.Upsert(ctx, &mcpservers.MCPServer{Name: "example-mcp", DisplayName: "Example MCP", URL: "http://example-mcp", ManagedBy: "helm"})
	err := svc.Delete(ctx, "example-mcp")
	if !errors.Is(err, mcpservers.ErrManaged) {
		t.Errorf("expected ErrManaged, got %v", err)
	}
}

// TestDelete_PluginManagedRejected: a plugin-owned row cannot be deleted by a
// user, and the error names the owning plugin so the handler can answer with
// it verbatim.
func TestDelete_PluginManagedRejected(t *testing.T) {
	st := setup(t)
	svc := mcpservers.NewService(st)
	ctx := context.Background()
	_ = svc.Upsert(ctx, &mcpservers.MCPServer{
		Name: "plugin-mem0", DisplayName: "Mem0", URL: "http://hub/api/internal/plugins/mem0/agent-mcp",
		TokenFromEnv: "HUB_INTERNAL_VALIDATE_SECRET", ManagedBy: "plugin", // #nosec G101 -- env var name, not a credential
	})

	err := svc.Delete(ctx, "plugin-mem0")
	if !errors.Is(err, mcpservers.ErrPluginManaged) {
		t.Fatalf("expected ErrPluginManaged, got %v", err)
	}
	if err.Error() != "managed by plugin mem0" {
		t.Errorf("message: %q", err.Error())
	}
	if got, err := svc.Get(ctx, "plugin-mem0"); err != nil || got == nil {
		t.Errorf("row must still exist after a refused delete: %v", err)
	}
}

func TestUpdate_PluginManagedRejected(t *testing.T) {
	st := setup(t)
	svc := mcpservers.NewService(st)
	ctx := context.Background()
	_ = svc.Upsert(ctx, &mcpservers.MCPServer{
		Name: "plugin-mem0", DisplayName: "Mem0", URL: "http://hub/api/internal/plugins/mem0/agent-mcp",
		ManagedBy: "plugin",
	})

	in := &mcpservers.MCPServer{Name: "plugin-mem0", DisplayName: "Mine", URL: "http://evil"}
	err := svc.Update(ctx, in)
	if !errors.Is(err, mcpservers.ErrPluginManaged) {
		t.Fatalf("expected ErrPluginManaged, got %v", err)
	}
	if err.Error() != "managed by plugin mem0" {
		t.Errorf("message: %q", err.Error())
	}
	got, err := svc.Get(ctx, "plugin-mem0")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.URL != "http://hub/api/internal/plugins/mem0/agent-mcp" {
		t.Errorf("URL changed despite the refused update: %s", got.URL)
	}
}

func TestUpdate_AllowsHelmManaged(t *testing.T) {
	st := setup(t)
	svc := mcpservers.NewService(st)
	ctx := context.Background()
	_ = svc.Upsert(ctx, &mcpservers.MCPServer{Name: "example-mcp", DisplayName: "Example", URL: "http://example", ManagedBy: "helm"})

	if err := svc.Update(ctx, &mcpservers.MCPServer{Name: "example-mcp", DisplayName: "Example 2", URL: "http://example2", ManagedBy: "helm"}); err != nil {
		t.Fatalf("Update on a helm row: %v", err)
	}
	got, err := svc.Get(ctx, "example-mcp")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.DisplayName != "Example 2" || got.URL != "http://example2" {
		t.Errorf("helm row not updated: %+v", got)
	}
	if got.ManagedBy != "helm" {
		t.Errorf("ManagedBy: %s", got.ManagedBy)
	}
}

// TestDeleteManaged_RemovesPluginRow: the syncer's delete path skips the
// ownership check, which is the whole reason it exists.
func TestDeleteManaged_RemovesPluginRow(t *testing.T) {
	st := setup(t)
	svc := mcpservers.NewService(st)
	ctx := context.Background()
	_ = svc.Upsert(ctx, &mcpservers.MCPServer{Name: "plugin-mem0", DisplayName: "Mem0", URL: "http://hub", ManagedBy: "plugin"})

	if err := svc.DeleteManaged(ctx, "plugin-mem0"); err != nil {
		t.Fatalf("DeleteManaged: %v", err)
	}
	if _, err := svc.Get(ctx, "plugin-mem0"); !errors.Is(err, mcpservers.ErrNotFound) {
		t.Errorf("expected the row to be gone, got %v", err)
	}
	if err := svc.DeleteManaged(ctx, "plugin-mem0"); !errors.Is(err, mcpservers.ErrNotFound) {
		t.Errorf("expected ErrNotFound for a missing row, got %v", err)
	}
}
