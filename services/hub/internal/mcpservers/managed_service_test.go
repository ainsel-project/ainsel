package mcpservers_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/mcpservers"
	"github.com/ainsel-project/ainsel/services/hub/internal/plugincreds"
)

func pluginCtx(id string) context.Context {
	return plugincreds.WithCaller(context.Background(), &plugincreds.Caller{
		PluginID: id,
		Kind:     plugincreds.KindPlugin,
	})
}

// A plugin that stamped a row at create is that row's sole writer: Update and
// Delete pass for it and keep refusing everyone else (issue #190). The stored
// marker is never changed by the update, so the row cannot be handed to a
// second author.
func TestNamedManagerWritesItsOwnRow(t *testing.T) {
	svc := mcpservers.NewService(setup(t))
	ctx := pluginCtx("forgejo")

	in := &mcpservers.MCPServer{Name: "forgejo-tools", DisplayName: "Forgejo Tools", URL: "https://forgejo.test/mcp", ManagedBy: "plugin-forgejo"}
	if err := svc.Create(ctx, in); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := svc.Get(ctx, "forgejo-tools")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ManagedBy != "plugin-forgejo" {
		t.Fatalf("stored managedBy = %q, want plugin-forgejo", got.ManagedBy)
	}

	// Everyone else keeps the #146 refusal.
	if err := svc.Update(context.Background(), &mcpservers.MCPServer{Name: "forgejo-tools", URL: "https://evil.test"}); !errors.Is(err, mcpservers.ErrManaged) {
		t.Errorf("console Update = %v, want ErrManaged", err)
	}
	if err := svc.Update(pluginCtx("github"), &mcpservers.MCPServer{Name: "forgejo-tools", URL: "https://evil.test"}); !errors.Is(err, mcpservers.ErrManaged) {
		t.Errorf("other plugin Update = %v, want ErrManaged", err)
	}
	if err := svc.Delete(pluginCtx("github"), "forgejo-tools"); !errors.Is(err, mcpservers.ErrManaged) {
		t.Errorf("other plugin Delete = %v, want ErrManaged", err)
	}

	// The named manager writes, and the marker survives.
	if err := svc.Update(ctx, &mcpservers.MCPServer{Name: "forgejo-tools", DisplayName: "Renamed", URL: "https://forgejo.test/v2/mcp"}); err != nil {
		t.Fatalf("named manager Update: %v", err)
	}
	got, err = svc.Get(ctx, "forgejo-tools")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.URL != "https://forgejo.test/v2/mcp" {
		t.Errorf("URL not written: %q", got.URL)
	}
	if got.ManagedBy != "plugin-forgejo" {
		t.Errorf("Update changed the marker: %q", got.ManagedBy)
	}
	if err := svc.Delete(ctx, "forgejo-tools"); err != nil {
		t.Errorf("named manager Delete: %v", err)
	}
}

// The exception fires only for a value that names the caller. A row under the
// bare legacy "plugin" marker — whose owner lives in the row name, not the
// value — keeps refusing every REST caller with the wording #189 pinned, and a
// console row stays writable by everyone as it was.
func TestNamedManagerExceptionDoesNotReachOtherRows(t *testing.T) {
	svc := mcpservers.NewService(setup(t))
	ctx := context.Background()

	if err := svc.Upsert(ctx, &mcpservers.MCPServer{
		Name: "plugin-mem0", DisplayName: "Mem0", URL: "http://hub/api/internal/plugins/mem0/agent-mcp",
		TokenFromEnv: "HUB_INTERNAL_VALIDATE_SECRET", ManagedBy: "plugin", // #nosec G101 -- env var name, not a credential
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// The plugin whose name the row carries still cannot write it.
	if err := svc.Delete(pluginCtx("mem0"), "plugin-mem0"); !errors.Is(err, mcpservers.ErrPluginManaged) {
		t.Errorf("Delete of a legacy plugin row = %v, want ErrPluginManaged", err)
	}

	if err := svc.Create(pluginCtx("forgejo"), &mcpservers.MCPServer{Name: "console-row", URL: "https://a.test"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got, err := svc.Get(ctx, "console-row"); err != nil {
		t.Fatalf("Get: %v", err)
	} else if got.ManagedBy != "user" {
		t.Errorf("unstamped create stored %q, want user", got.ManagedBy)
	}
	if err := svc.Update(ctx, &mcpservers.MCPServer{Name: "console-row", URL: "https://b.test"}); err != nil {
		t.Errorf("console Update of a console row: %v", err)
	}
}
