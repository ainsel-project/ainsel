package personas_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/personas"
	"github.com/ainsel-project/ainsel/services/hub/internal/plugincreds"
)

func managedPluginCtx(id string) context.Context {
	return plugincreds.WithCaller(context.Background(), &plugincreds.Caller{
		PluginID: id,
		Kind:     plugincreds.KindPlugin,
	})
}

func isManagedError(err error) bool {
	var mErr *personas.ManagedError
	return errors.As(err, &mErr)
}

// A plugin that stamped a persona at create remains its sole writer: Update,
// Rollback and Delete pass for it and keep refusing everyone else (issue #190).
func TestNamedManagerWritesItsOwnPersona(t *testing.T) {
	svc, cleanup := newTestService(t, nil)
	defer cleanup()

	ctx := managedPluginCtx("forgejo")
	p, err := svc.Create(ctx, personas.CreateRequest{
		Name: "forgejo-reviewer", Text: "You review forgejo PRs.", ManagedBy: "plugin-forgejo",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.ManagedBy != "plugin-forgejo" {
		t.Fatalf("stored managedBy = %q, want plugin-forgejo", p.ManagedBy)
	}

	// Everyone else keeps the #146 refusal, on every write path.
	if _, err := svc.Update(context.Background(), p.ID, personas.UpdateRequest{Text: ptr("hijacked")}); !isManagedError(err) {
		t.Errorf("console Update = %v, want ManagedError", err)
	}
	if _, err := svc.Update(managedPluginCtx("github"), p.ID, personas.UpdateRequest{Text: ptr("hijacked")}); !isManagedError(err) {
		t.Errorf("other plugin Update = %v, want ManagedError", err)
	}
	if err := svc.Delete(managedPluginCtx("github"), p.ID); !isManagedError(err) {
		t.Errorf("other plugin Delete = %v, want ManagedError", err)
	}

	// The named manager writes, and the marker survives the version bump.
	updated, err := svc.Update(ctx, p.ID, personas.UpdateRequest{Text: ptr("You review pull requests.")})
	if err != nil {
		t.Fatalf("named manager Update: %v", err)
	}
	if updated.CurrentVersion != 2 {
		t.Errorf("currentVersion = %d, want 2", updated.CurrentVersion)
	}
	if updated.ManagedBy != "plugin-forgejo" {
		t.Errorf("Update changed the marker: %q", updated.ManagedBy)
	}
	if _, err := svc.Rollback(ctx, p.ID, 1); err != nil {
		t.Errorf("named manager Rollback: %v", err)
	}
	if err := svc.Delete(ctx, p.ID); err != nil {
		t.Errorf("named manager Delete: %v", err)
	}
}

// The exception only ever fires for a managed row the caller names. A persona a
// plugin created without a stamp stays console-owned, and a console caller keeps
// writing it — the guard never narrows anyone's existing access.
func TestUnstampedPluginCreateStaysConsoleOwned(t *testing.T) {
	svc, cleanup := newTestService(t, nil)
	defer cleanup()

	p, err := svc.Create(managedPluginCtx("forgejo"), personas.CreateRequest{Name: "plain", Text: "Hello."})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.ManagedBy != personas.ManagedByUser {
		t.Fatalf("stored managedBy = %q, want %q", p.ManagedBy, personas.ManagedByUser)
	}
	if _, err := svc.Update(context.Background(), p.ID, personas.UpdateRequest{Text: ptr("Edited.")}); err != nil {
		t.Errorf("console Update of a console-owned persona: %v", err)
	}
}
