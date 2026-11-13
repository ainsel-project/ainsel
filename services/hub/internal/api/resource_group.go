package api

import (
	"context"
	"log/slog"

	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
)

// assignGroup files a freshly created resource under its group: the
// requested one, or authz.DefaultGroupID when the request named none — the
// hub-wide "ungrouped resources belong to the legacy group" invariant.
//
// A nil store means authz is not configured, in which case there is nothing
// to write. A failed write is logged and swallowed: the resource itself was
// already created, and failing the create would lose it — the same
// semantics every create handler has always had.
//
// This is the shared core; the *Server method of the same name is the form
// the server-backed handlers call. The persona and skill handlers hold
// their own authzStore reference rather than a *Server, so they call this
// function directly.
func assignGroup(ctx context.Context, store authzStore, rt authz.ResourceType, id, requested string) {
	if store == nil {
		return
	}
	groupID := requested
	if groupID == "" {
		groupID = authz.DefaultGroupID
	}
	if err := store.SetResourceGroup(ctx, string(rt), id, groupID, false); err != nil {
		slog.Error("set resource group on create", "error", err, "resource", id)
	}
}

// assignGroup records the group of a resource s just created (see the
// package-level assignGroup for semantics).
func (s *Server) assignGroup(ctx context.Context, rt authz.ResourceType, id, requested string) {
	assignGroup(ctx, s.authzStore, rt, id, requested)
}
