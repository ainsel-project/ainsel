// Package selfsync keeps the MCP server registry in step with the MCP endpoints
// the hub serves to agents itself.
//
// The registry is an agent-facing catalogue: every row is a tool server an agent
// image can attach, rendered by the operator into MCP_SERVERS and
// MCP_SERVER_TOKENS on the agent container. The hub is one of the servers worth
// attaching — its own read plane answers "what did my last run cost", "which of
// my triggers fired", "is my connector healthy" — but a row for it has to exist
// before an agent can ask. Rather than ask an operator to register the platform
// in its own catalogue by hand (and to hang a human's token on it while doing
// so), the hub derives the row from its own address.
//
// The row is the syncer's alone: it writes it when the endpoint is enabled and
// leaves it alone otherwise. The registry refuses user mutations of it, so an
// operator can see the platform's own server, cannot repoint it, and cannot
// delete it out from under the agents that attach it.
package selfsync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	"github.com/ainsel-project/ainsel/services/hub/internal/mcpservers"
)

const (
	// PlatformServerName is the registry name of the hub's own agent-facing
	// MCP row. It is deliberately short and unprefixed: it names the platform,
	// not an integrator, and it is the name an operator types when they look
	// for it in the console.
	PlatformServerName = "ainsel"

	// PlatformServerDisplayName and PlatformServerDescription are the console
	// strings of that row. They say what attaching it gets an agent, because
	// the row is a fact about the platform rather than a string an operator
	// chose.
	PlatformServerDisplayName = "AInsel platform"
	PlatformServerDescription = "Read-only platform tools served by the hub: agents, invocations, logs, metrics, token usage, channels, MCP servers and the skill catalogue"

	// AgentMCPEndpointPath is the hub path that serves the platform tool plane
	// to agent runtimes, relative to the hub's own address. It must match
	// api.AgentMCPPath; the two packages stay independent rather than sharing
	// the constant, and the wiring test pins them together.
	AgentMCPEndpointPath = "/api/internal/agent-mcp"

	// MCPServerTokenFromEnv names the env var whose value on the agent pod is
	// sent as the bearer token of a platform tool call. The hub serves this
	// endpoint itself and checks the same cluster-internal credential the
	// plugin agent endpoints check, so the credential is one the operator
	// already injects into every agent container — no per-agent Secret and no
	// human token involved.
	MCPServerTokenFromEnv = "HUB_INTERNAL_VALIDATE_SECRET" // #nosec G101 -- env var name, not a credential

	// mcpServerResourceType is the authz resource type of a registry row.
	mcpServerResourceType = "mcp-server"

	// defaultResourceGroup is the group a registry row is filed under when it
	// has no ownership entry at all. It is the same group the startup group
	// backfill assigns, so the syncer introduces no second convention for
	// "resource with no explicit owner".
	defaultResourceGroup = "legacy"
)

// MCPRegistry is the slice of the MCP server registry the syncer drives: the
// rows to compare against, and the write that claims a row for the hub. It is
// the same seam pluginsync uses, minus DeleteManaged: this syncer registers one
// row and never prunes, because there is no desired state it can lose track of.
type MCPRegistry interface {
	List(ctx context.Context) ([]*mcpservers.MCPServer, error)
	Upsert(ctx context.Context, m *mcpservers.MCPServer) error
}

// ResourceVisibility is the slice of the authz store the syncer needs to open
// its row. The store's SetResourcePublic only toggles an existing entry, and an
// entry is created for console resources at creation time — a row with no entry
// is invisible to everyone but an admin, so the syncer creates one.
type ResourceVisibility interface {
	SetResourcePublic(ctx context.Context, resourceType, resourceName string, public bool) error
	SetResourceGroup(ctx context.Context, resourceType, resourceName, groupID string, public bool) error
}

// Syncer reconciles the hub's own registry row against the hub's own address.
type Syncer struct {
	mcp        MCPRegistry
	selfURL    string
	visibility ResourceVisibility
}

// New wires a syncer. selfURL is the hub's own in-cluster address, the base of
// the URL the row hands out — an agent outside the cluster cannot reach it,
// which is the point: the platform tool plane is cluster-internal and
// authenticated with the hub's internal credential.
func New(mcp MCPRegistry, selfURL string) *Syncer {
	return &Syncer{mcp: mcp, selfURL: selfURL}
}

// SetResourceVisibility attaches the authz store that decides who can read a
// registry row. Without it the syncer still writes the row; it just cannot open
// it to non-admin users.
func (s *Syncer) SetResourceVisibility(v ResourceVisibility) {
	s.visibility = v
}

// row is the registry row the hub's own endpoint asks for.
func (s *Syncer) row() *mcpservers.MCPServer {
	return &mcpservers.MCPServer{
		Name:         PlatformServerName,
		DisplayName:  PlatformServerDisplayName,
		Description:  PlatformServerDescription,
		URL:          strings.TrimSuffix(s.selfURL, "/") + AgentMCPEndpointPath,
		TokenFromEnv: MCPServerTokenFromEnv,
		ManagedBy:    mcpservers.ManagedByHub,
	}
}

// SyncOnce reconciles one pass: the hub's row exists and matches the hub's
// current address, or it is written.
//
// Three rules carry the safety of the loop, both inherited from how the plugin
// syncer reconciles its own rows. A row is written only when a field actually
// differs, so an idle cluster costs one list per pass rather than a write. And a
// row the hub does not own is never taken over: an operator who registered
// "ainsel" by hand keeps theirs, with a warning naming the collision rather
// than a silent overwrite.
func (s *Syncer) SyncOnce(ctx context.Context) error {
	want := s.row()

	existing, err := s.mcp.List(ctx)
	if err != nil {
		return fmt.Errorf("selfsync: list mcp servers: %w", err)
	}
	for _, cur := range existing {
		if cur.Name != want.Name {
			continue
		}
		if cur.ManagedBy != mcpservers.ManagedByHub {
			// Never take over a row a user, helm or a plugin owns, even if its
			// name matches: the platform's server is simply not registered, and
			// the operator's row is the one agents get.
			slog.Warn("platform mcp server name taken by a row the hub does not own; not registering",
				"name", want.Name, "managedBy", cur.ManagedBy)
			return nil
		}
		if sameRegistration(cur, want) {
			return nil
		}
	}

	if err := s.mcp.Upsert(ctx, want); err != nil {
		return fmt.Errorf("selfsync: upsert %s: %w", want.Name, err)
	}
	s.makeReadable(ctx, mcpServerResourceType, want.Name)
	return nil
}

// Run reconciles until ctx is cancelled: one pass immediately, so a hub that
// starts with the endpoint already enabled registers itself without waiting a
// tick, then one pass every interval. Failures are logged and the loop
// continues — a registry that could not be written this round is a degraded
// feature, not a reason to stop the hub.
func (s *Syncer) Run(ctx context.Context, interval time.Duration) {
	if err := s.SyncOnce(ctx); err != nil {
		slog.Warn("platform registry sync failed", "error", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.SyncOnce(ctx); err != nil {
				slog.Warn("platform registry sync failed", "error", err)
			}
		}
	}
}

// sameRegistration compares the fields the syncer owns. Timestamps are not
// registration state; managed_by is, so a row whose owner changed is rewritten
// back rather than left half-owned.
func sameRegistration(cur, want *mcpservers.MCPServer) bool {
	return cur.DisplayName == want.DisplayName &&
		cur.Description == want.Description &&
		cur.URL == want.URL &&
		cur.TokenFromEnv == want.TokenFromEnv &&
		cur.ManagedBy == want.ManagedBy
}

// makeReadable opens a freshly written resource to every user, which is what
// lets any agent image attach it: the endpoint is the hub's own and
// authenticates with the hub's internal credential, so it grants nothing the
// reader does not already have.
//
// It runs after an upsert only. A row that was already registered keeps the
// visibility it was given, and a row the syncer did not write this pass is not
// its to re-open.
func (s *Syncer) makeReadable(ctx context.Context, resourceType, name string) {
	if s.visibility == nil {
		return
	}
	err := s.visibility.SetResourcePublic(ctx, resourceType, name, true)
	if err == nil {
		return
	}
	// No ownership entry yet — the console creates one at creation time and
	// this row has no creator. Create it open, in the group the hub uses for
	// resources with no explicit owner.
	if errors.Is(err, authz.ErrNotFound) {
		if cerr := s.visibility.SetResourceGroup(ctx, resourceType, name, defaultResourceGroup, true); cerr != nil {
			slog.Warn("could not open platform resource to all users",
				"resource_type", resourceType, "name", name, "error", cerr)
		}
		return
	}
	slog.Warn("could not make platform resource public",
		"resource_type", resourceType, "name", name, "error", err)
}
