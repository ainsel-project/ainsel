package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	"github.com/ainsel-project/ainsel/services/hub/internal/channels"
	"github.com/ainsel-project/ainsel/services/hub/internal/skills"
	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// groupResourceTypes are the authz resource types a group can own. The list
// mirrors docs/access-control.md: every resource type with a resource_groups
// row. Order defines the order items are returned in; the frontend groups by
// type anyway.
//
// Note on channels: only standalone (custom) channels have resource-group
// rows. A connector's or an agent's channel is authorised through the
// referenced entity (see channelAuthzTarget), so it belongs to that
// connector's/agent's group and is surfaced on this page through the
// connector/agent panel instead — never double-counted here.
var groupResourceTypes = []string{
	"agent",
	"agent-image",
	"connector",
	"persona",
	"skill",
	"channel",
	"mcp-server",
	"trigger",
	"cron-trigger",
}

// groupResourceCap bounds the items returned per type. Well above any
// realistic group size; it keeps the fan-out across the backing stores
// bounded without a pagination round. The response shape (a flat items list)
// does not preclude adding cursor pagination later.
const groupResourceCap = 200

// GroupResourceItem is one resource owned by a group, resolved to a display
// name so the UI never renders bare ids (same principle as the list
// endpoints). ID is the resource's public identifier — for k8s-backed types
// that is metadata.name, for store-backed types the record id.
type GroupResourceItem struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Public   bool   `json:"public"`
	AgentRef string `json:"agentRef,omitempty"`
}

// handleGroupResources serves GET /api/v1/groups/{id}/resources: every
// resource whose resource-group row points at this group, across all group
// resource types, resolved to display names.
//
// Access control is exactly the read-scoping the per-type list endpoints use:
// the group's names for a type are intersected with filterByAccess (member of
// the resource's group, public when ?includePublic=true, or admin) *before*
// any display name is resolved, so a name the caller may not read never
// reaches the response. A non-member therefore sees the same set the
// corresponding list endpoints would show.
//
// Partial failure semantics: if one type's backing store fails, that type is
// omitted (with a warning) rather than failing the whole summary — this page
// is a convenience view, not an authority.
func (s *Server) handleGroupResources(w http.ResponseWriter, r *http.Request, groupID string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if _, err := s.authzStore.GetGroup(r.Context(), groupID); err != nil {
		if errors.Is(err, authz.ErrNotFound) {
			writeError(w, http.StatusNotFound, "group not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	items := make([]GroupResourceItem, 0)
	for _, resourceType := range groupResourceTypes {
		items = append(items, s.groupResourcesOfType(r, groupID, resourceType)...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// groupResourcesOfType returns the caller-visible resources of one type in the
// group: group membership first (cheap, indexed), then the access filter, then
// — and only then — display-name resolution from the backing store.
func (s *Server) groupResourcesOfType(r *http.Request, groupID, resourceType string) []GroupResourceItem {
	names, err := s.authzStore.ListResourcesByGroups(r.Context(), resourceType, []string{groupID}, false)
	if err != nil {
		slog.Warn("group resources: list failed", "group", groupID, "type", resourceType, "error", err)
		return nil
	}
	// Filter before resolving: resolving first would put names the caller
	// cannot read into the response on any code-path slip.
	names = s.filterByAccess(r, resourceType, names)
	if n := len(names); n > groupResourceCap {
		names = names[:groupResourceCap]
	}
	if len(names) == 0 {
		return nil
	}

	resolved, err := s.resolveGroupResourceNames(r.Context(), resourceType, names)
	if err != nil {
		slog.Warn("group resources: resolution failed", "group", groupID, "type", resourceType, "error", err)
		return nil
	}

	out := make([]GroupResourceItem, 0, len(names))
	for _, name := range names {
		info, ok := resolved[name]
		if !ok {
			// Stale resource_groups row: the object was deleted out-of-band
			// (e.g. kubectl). A row pointing at nothing is not a resource the
			// caller can read — drop it rather than render a bare id.
			continue
		}
		public := false
		if rg, err := s.authzStore.GetResourceGroup(r.Context(), resourceType, name); err == nil && rg != nil {
			public = rg.Public
		}
		out = append(out, GroupResourceItem{
			Type:     resourceType,
			ID:       name,
			Name:     info.displayName,
			Public:   public,
			AgentRef: info.agentRef,
		})
	}
	return out
}

// groupResourceDisplay is the resolved display data for one resource.
type groupResourceDisplay struct {
	displayName string
	agentRef    string // trigger and cron-trigger only
}

// resolveGroupResourceNames batch-resolves display names for one type with a
// single store List filtered to the wanted set — the same pattern the list
// endpoints use, never an N+1 of Gets. Unknown ids (stale rows) are simply
// absent from the result, which the caller treats as "drop".
//
// Agent-owned personas (OwnerAgent set) are not group resources — they are
// addressed through their owning agent and excluded by the persona store's
// List — so they resolve to nothing here and are dropped, which keeps them
// from double-counting against the agent panel.
func (s *Server) resolveGroupResourceNames(ctx context.Context, resourceType string, names []string) (map[string]groupResourceDisplay, error) {
	want := toAccessSet(names)
	out := make(map[string]groupResourceDisplay, len(names))

	switch resourceType {
	case "agent":
		var list agentv1alpha1.AgentList
		if err := s.client.List(ctx, &list, client.InNamespace(s.ns)); err != nil {
			return nil, err
		}
		for _, a := range list.Items {
			if want[a.Name] {
				out[a.Name] = groupResourceDisplay{displayName: displayOr(a.Spec.DisplayName, a.Name)}
			}
		}
	case "agent-image":
		var list agentv1alpha1.AgentImageList
		if err := s.client.List(ctx, &list, client.InNamespace(s.ns)); err != nil {
			return nil, err
		}
		for _, img := range list.Items {
			if want[img.Name] {
				out[img.Name] = groupResourceDisplay{displayName: displayOr(img.Spec.DisplayName, img.Name)}
			}
		}
	case "connector":
		var list agentv1alpha1.WebhookConnectorList
		if err := s.client.List(ctx, &list, client.InNamespace(s.ns)); err != nil {
			return nil, err
		}
		for _, c := range list.Items {
			if want[c.Name] {
				out[c.Name] = groupResourceDisplay{displayName: displayOr(c.Spec.DisplayName, c.Name)}
			}
		}
	case "persona":
		if s.personas == nil {
			return out, nil
		}
		list, err := s.personas.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range list {
			if want[p.ID] {
				out[p.ID] = groupResourceDisplay{displayName: displayOr(p.Name, p.ID)}
			}
		}
	case "skill":
		if s.skills == nil {
			return out, nil
		}
		list, err := s.skills.List(ctx, skills.ListFilter{})
		if err != nil {
			return nil, err
		}
		for _, sk := range list {
			if want[sk.ID] {
				out[sk.ID] = groupResourceDisplay{displayName: displayOr(sk.Name, sk.ID)}
			}
		}
	case "mcp-server":
		if s.mcp == nil {
			return out, nil
		}
		list, err := s.mcp.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, m := range list {
			if want[m.Name] {
				out[m.Name] = groupResourceDisplay{displayName: displayOr(m.DisplayName, m.Name)}
			}
		}
	case "channel":
		if s.channelSvc == nil {
			return out, nil
		}
		// Standalone (custom) channels only: derived channels have no
		// resource-group rows of their own (see groupResourceTypes).
		views, err := s.channelSvc.List(ctx, channels.KindCustom, time.Time{})
		if err != nil {
			return nil, err
		}
		for _, v := range views {
			if want[v.ID] {
				out[v.ID] = groupResourceDisplay{displayName: displayOr(v.Name, v.ID)}
			}
		}
	case "trigger":
		if s.triggerStore == nil {
			return out, nil
		}
		all, err := s.triggerStore.ListTriggers(ctx, "", "")
		if err != nil {
			return nil, err
		}
		for _, t := range all {
			if want[t.ID] {
				out[t.ID] = groupResourceDisplay{displayName: displayOr(t.DisplayName, t.ID), agentRef: t.AgentRef}
			}
		}
	case "cron-trigger":
		if s.triggerStore == nil {
			return out, nil
		}
		all, err := s.triggerStore.ListCronTriggers(ctx, "")
		if err != nil {
			return nil, err
		}
		for _, t := range all {
			if want[t.ID] {
				out[t.ID] = groupResourceDisplay{displayName: displayOr(t.DisplayName, t.ID), agentRef: t.AgentRef}
			}
		}
	}
	return out, nil
}

// displayOr falls back to the identifier when no display name is set — the
// same fallback rule the list endpoints apply.
func displayOr(displayName, fallback string) string {
	if displayName == "" {
		return fallback
	}
	return displayName
}
