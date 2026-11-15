package personas

import (
	"context"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/plugincreds"
)

func ctxWithCaller(id string) context.Context {
	return plugincreds.WithCaller(context.Background(), &plugincreds.Caller{
		PluginID: id,
		Kind:     plugincreds.KindPlugin,
	})
}

// managedRefusal is the single guard every persona write path runs. The named
// manager (issue #190) is the only caller that passes on a managed row; every
// other caller keeps the #146 refusal, including for the bare legacy "plugin"
// marker whose owner is encoded in the row name rather than the value.
func TestManagedRefusal(t *testing.T) {
	tests := []struct {
		name      string
		ctx       context.Context
		managedBy string
		want      bool
	}{
		{"console caller, console row", context.Background(), ManagedByUser, false},
		{"console caller, managed row", context.Background(), "plugin-forgejo", true},
		{"named plugin, own row", ctxWithCaller("forgejo"), "plugin-forgejo", false},
		{"other plugin, managed row", ctxWithCaller("github"), "plugin-forgejo", true},
		{"named plugin, free-form manager row", ctxWithCaller("forgejo"), "gitops", true},
		{"named plugin, legacy bare marker", ctxWithCaller("forgejo"), "plugin", true},
		{"named plugin, unmanaged row", ctxWithCaller("forgejo"), "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := managedRefusal(tc.ctx, tc.managedBy); got != tc.want {
				t.Errorf("managedRefusal(%q) = %v, want %v", tc.managedBy, got, tc.want)
			}
		})
	}
}
