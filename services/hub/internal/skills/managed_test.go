package skills

import (
	"context"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/plugincreds"
)

func managedCtxWithCaller(id string) context.Context {
	return plugincreds.WithCaller(context.Background(), &plugincreds.Caller{
		PluginID: id,
		Kind:     plugincreds.KindPlugin,
	})
}

// RefusesWrite is the guard Update and Delete run on the stored row. The named
// manager (issue #190) is the only caller that passes on a managed row; every
// other caller keeps the #146 refusal — including for the bare legacy "plugin"
// marker the syncer writes, whose owner lives in the row's tags and whose 409
// wording #189 pinned.
func TestRefusesWrite(t *testing.T) {
	tests := []struct {
		name      string
		ctx       context.Context
		managedBy string
		want      bool
	}{
		{"console caller, console row", context.Background(), ManagedByUser, false},
		{"console caller, unmanaged row", context.Background(), "", false},
		{"console caller, managed row", context.Background(), "plugin-forgejo", true},
		{"named plugin, own row", managedCtxWithCaller("forgejo"), "plugin-forgejo", false},
		{"other plugin, managed row", managedCtxWithCaller("github"), "plugin-forgejo", true},
		{"named plugin, free-form manager row", managedCtxWithCaller("forgejo"), "gitops", true},
		{"named plugin, legacy bare marker", managedCtxWithCaller("forgejo"), ManagedByPlugin, true},
		{"named plugin, unmanaged row", managedCtxWithCaller("forgejo"), "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row := &Skill{ID: "s", ManagedBy: tc.managedBy}
			if got := RefusesWrite(tc.ctx, row); got != tc.want {
				t.Errorf("RefusesWrite(%q) = %v, want %v", tc.managedBy, got, tc.want)
			}
		})
	}
}
