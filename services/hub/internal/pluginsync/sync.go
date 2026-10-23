// Package pluginsync keeps the MCP server registry and the skill registry in
// step with the Plugin resources the platform runs.
//
// A plugin that declares an MCP endpoint in its manifest serves agent-facing
// tools at a hub endpoint. Agents reach those tools the same way they reach any
// other MCP server: through a registry row their AgentImage attaches. Rather
// than ask an operator to write that row by hand — and delete it again when the
// plugin goes away — the hub derives it from the Plugin CRs it can already see.
//
// The rows are the syncer's alone: it writes the ones its plugins declare,
// removes the ones they no longer declare, and never touches a row owned by a
// user or by the chart. The registry refuses user mutations of these rows, so
// the mapping from a plugin to its agent endpoint has exactly one author.
//
// Ownership is read from the Plugin CR, not from a Ready condition: a disabled
// plugin keeps its row, because disabling a plugin is meant to stop its tools
// answering, not to reconfigure every agent image that attached them.
package pluginsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	"github.com/ainsel-project/ainsel/services/hub/internal/mcpservers"
	"github.com/ainsel-project/ainsel/services/hub/internal/pluginproxy"
	connectorv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	pluginpkg "github.com/ainsel-project/ainsel/shared/api/plugin"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// MCPServerTokenFromEnv names the env var whose value on the agent pod is
	// sent as the bearer token of a plugin agent MCP call. The hub serves this
	// endpoint itself, so the credential is the hub's own cluster-internal one
	// — the same secret every other internal route checks.
	MCPServerTokenFromEnv = "HUB_INTERNAL_VALIDATE_SECRET" // #nosec G101 -- env var name, not a credential

	// mcpServerResourceType is the authz resource type of a registry row.
	mcpServerResourceType = "mcp-server"

	// skillResourceType is the authz resource type of a plugin-owned skill
	// row, the same type the console files user skills under.
	skillResourceType = "skill"

	// defaultResourceGroup is the group a registry row is filed under when it
	// has no ownership entry at all. It is the same group the startup group
	// backfill assigns, so the syncer introduces no second convention for
	// "resource with no explicit owner".
	defaultResourceGroup = "legacy"
)

// agentMCPEndpoint is the hub path that serves one plugin's tools to agent
// runtimes, relative to the hub's own address.
func agentMCPEndpoint(pluginID string) string {
	return "/api/internal/plugins/" + pluginID + "/agent-mcp"
}

// MCPRegistry is the slice of the MCP server registry the syncer drives: the
// rows to compare against, the write that claims a row for a plugin, and the
// delete that skips the ownership check the user-facing path enforces.
type MCPRegistry interface {
	List(ctx context.Context) ([]*mcpservers.MCPServer, error)
	Upsert(ctx context.Context, m *mcpservers.MCPServer) error
	DeleteManaged(ctx context.Context, name string) error
}

// ManagedSkill is a skill row a plugin owns, in the shape the syncer hands it
// over: enough to store the row without importing the registry's own type.
type ManagedSkill struct {
	ID          string
	Name        string
	Description string
	Body        string
	Tags        []string
}

// SkillRegistry is the slice of the platform skill registry the plugin skill
// pass writes through. ListManaged returns only plugin-owned rows, which is
// what makes the reconcile safe to run against a registry full of user skills.
type SkillRegistry interface {
	ListManaged(ctx context.Context) ([]ManagedSkill, error)
	Upsert(ctx context.Context, s ManagedSkill) error
	Delete(ctx context.Context, id string) error
}

// SkillFetcher reads one skill document from the plugin that declares it: the
// verified endpoint of that plugin ("ip:port") and the path its manifest gave,
// returning the document body.
type SkillFetcher func(ctx context.Context, endpoint, path string) (string, error)

// ResourceVisibility is the slice of the authz store the syncer needs to open
// its rows. The store's SetResourcePublic only toggles an existing entry, and
// an entry is created for console resources at creation time — a row with no
// entry is invisible to everyone but an admin, so the syncer creates one.
type ResourceVisibility interface {
	SetResourcePublic(ctx context.Context, resourceType, resourceName string, public bool) error
	SetResourceGroup(ctx context.Context, resourceType, resourceName, groupID string, public bool) error
}

// Syncer reconciles plugin-owned MCP server rows against the Plugin resources
// in one namespace.
type Syncer struct {
	client client.Client
	ns     string
	mcp    MCPRegistry
	// skills and fetch are the skill-registry seam. A syncer built without
	// either of them reconciles MCP server rows only; see New.
	skills     SkillRegistry
	fetch      SkillFetcher
	selfURL    string
	visibility ResourceVisibility
}

// New wires a syncer. selfURL is the hub's own in-cluster address, the base of
// every URL the registry rows hand out — an agent outside the cluster cannot
// reach it, which is the point: the agent endpoint is cluster-internal and
// authenticated with the hub's internal credential.
//
// skills and fetch are the two halves of the plugin skill pass and are
// optional: pass nil for either (or both) and the syncer manages MCP server
// rows only. Both are needed together — the registry to write rows to and the
// fetcher to read a document off the plugin — and wiring them is the caller's
// decision, not something a half-configured syncer should guess at.
func New(c client.Client, ns string, mcp MCPRegistry, skills SkillRegistry, selfURL string, fetch SkillFetcher) *Syncer {
	return &Syncer{
		client:  c,
		ns:      ns,
		mcp:     mcp,
		skills:  skills,
		fetch:   fetch,
		selfURL: selfURL,
	}
}

// SetResourceVisibility attaches the authz store that decides who can read a
// registry row. Without it the syncer still writes rows; it just cannot open
// them to non-admin users.
func (s *Syncer) SetResourceVisibility(v ResourceVisibility) {
	s.visibility = v
}

// SyncOnce reconciles one pass: every plugin whose manifest declares an MCP
// endpoint gets a row matching its current manifest, and every plugin-owned row
// no plugin declares is removed. Rows owned by a user or by the chart are never
// read for deletion.
//
// A row is written only when a field actually differs, so an idle cluster costs
// one list per pass rather than a write per plugin per tick.
func (s *Syncer) SyncOnce(ctx context.Context) error {
	var list connectorv1alpha1.PluginList
	if err := s.client.List(ctx, &list, client.InNamespace(s.ns)); err != nil {
		return fmt.Errorf("pluginsync: list plugins: %w", err)
	}

	desired := make(map[string]*mcpservers.MCPServer, len(list.Items))
	for i := range list.Items {
		plugin := &list.Items[i]
		manifest, ok := manifestOf(plugin)
		if !ok || manifest.MCP == nil {
			continue
		}
		row := s.mcpServerRow(plugin, manifest)
		desired[row.Name] = row
	}

	existing, err := s.mcp.List(ctx)
	if err != nil {
		return fmt.Errorf("pluginsync: list mcp servers: %w", err)
	}
	current := make(map[string]*mcpservers.MCPServer, len(existing))
	for _, m := range existing {
		current[m.Name] = m
	}

	var errs []error
	// Sorted so a cluster with several plugins writes and logs in a stable
	// order instead of a new one every pass.
	for _, name := range slices.Sorted(maps.Keys(desired)) {
		row := desired[name]
		cur, ok := current[name]
		if ok && cur.ManagedBy != mcpservers.ManagedByPlugin {
			// Never take over a row a user or helm owns, even if its name
			// matches: the plugin's server is simply not registered.
			slog.Warn("plugin mcp server name taken by a row the plugin does not own; not registering",
				"name", name, "managedBy", cur.ManagedBy)
			continue
		}
		if ok && sameRegistration(cur, row) {
			continue
		}
		if err := s.mcp.Upsert(ctx, row); err != nil {
			errs = append(errs, fmt.Errorf("pluginsync: upsert %s: %w", name, err))
			continue
		}
		s.makeRowReadable(ctx, name)
	}
	for _, name := range slices.Sorted(maps.Keys(current)) {
		if _, wanted := desired[name]; wanted {
			continue
		}
		if current[name].ManagedBy != mcpservers.ManagedByPlugin {
			continue
		}
		if err := s.mcp.DeleteManaged(ctx, name); err != nil && !errors.Is(err, mcpservers.ErrNotFound) {
			errs = append(errs, fmt.Errorf("pluginsync: delete %s: %w", name, err))
		}
	}
	if err := s.reconcileSkills(ctx, list.Items); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// desiredSkill is one skill a plugin declares: the row to write (minus the
// body, which only a fetch can produce) and where to fetch the body from.
type desiredSkill struct {
	owner     string // plugin id that declares the skill
	row       ManagedSkill
	endpoint  string
	path      string
	hasTarget bool
}

// reconcileSkills is the skill half of the pass: every skill declared by a
// plugin manifest gets a managed row whose body was read from that plugin, and
// every managed row no longer declared is removed.
//
// A declared skill whose body cannot be read right now — the plugin has no
// verified endpoint yet, or the fetch failed — keeps whatever row it already
// has and is never deleted for it: the declaration still stands, and a pod
// that is restarting or a network that blinks must not empty the registry.
func (s *Syncer) reconcileSkills(ctx context.Context, plugins []connectorv1alpha1.Plugin) error {
	if s.skills == nil || s.fetch == nil {
		return nil
	}

	desired := make(map[string]desiredSkill)
	// Ids are <plugin>-<skill> and both halves may contain dashes, so plugin
	// "a" skill "b-c" and plugin "a-b" skill "c" both produce "a-b-c". Such an
	// id is registered for neither: one plugin must never be able to replace
	// another plugin's skill text, which agents read as instructions.
	contested := make(map[string]bool)
	for i := range plugins {
		plugin := &plugins[i]
		manifest, ok := manifestOf(plugin)
		if !ok || len(manifest.Skills) == 0 {
			continue
		}
		endpoint, hasEndpoint := pluginproxy.PickEndpoint(plugin.Status.VerifiedEndpoints, nil)
		for _, spec := range manifest.Skills {
			// The id doubles as the name and carries the plugin id, so a plugin
			// skill is recognisable — and its owner readable — without a join.
			id := plugin.Name + "-" + spec.Name
			if prev, taken := desired[id]; taken && prev.owner != plugin.Name {
				contested[id] = true
				slog.Warn("plugin skill id claimed by two plugins; registering neither",
					"skill", id, "plugins", prev.owner+","+plugin.Name)
				continue
			}
			desired[id] = desiredSkill{
				owner: plugin.Name,
				row: ManagedSkill{
					ID:          id,
					Name:        id,
					Description: spec.Description,
					Tags:        []string{"plugin:" + plugin.Name},
				},
				endpoint:  endpoint,
				path:      spec.Path,
				hasTarget: hasEndpoint,
			}
		}
	}

	for id := range contested {
		delete(desired, id)
	}

	existing, err := s.skills.ListManaged(ctx)
	if err != nil {
		return fmt.Errorf("pluginsync: list managed skills: %w", err)
	}
	current := make(map[string]ManagedSkill, len(existing))
	for _, sk := range existing {
		current[sk.ID] = sk
	}

	var errs []error
	// Sorted for a stable write and log order across passes.
	for _, id := range slices.Sorted(maps.Keys(desired)) {
		want := desired[id]
		if !want.hasTarget {
			// No verified endpoint: nothing to read the body from. The row is
			// still declared, so it is kept and not deleted below.
			continue
		}
		body, err := s.fetch(ctx, want.endpoint, want.path)
		if err != nil {
			// Keep the stored body; the next pass retries.
			slog.Warn("plugin skill fetch failed, keeping the stored body",
				"skill", id, "endpoint", want.endpoint, "error", err)
			continue
		}
		want.row.Body = body
		cur, ok := current[id]
		if ok && skillOwner(cur) != want.owner {
			// The row belongs to another plugin (renamed ids can line up
			// later): leave it alone.
			slog.Warn("plugin skill id owned by another plugin; not registering",
				"skill", id, "owner", skillOwner(cur), "plugin", want.owner)
			continue
		}
		if ok && sameSkill(cur, want.row) {
			continue
		}
		if err := s.skills.Upsert(ctx, want.row); err != nil {
			errs = append(errs, fmt.Errorf("pluginsync: upsert skill %s: %w", id, err))
			continue
		}
		s.makeReadable(ctx, skillResourceType, id)
	}
	for _, id := range slices.Sorted(maps.Keys(current)) {
		if want, wanted := desired[id]; wanted && skillOwner(current[id]) == want.owner {
			continue
		}
		if contested[id] {
			// Keep whatever is stored until the conflict is resolved.
			continue
		}
		if _, wanted := desired[id]; wanted {
			continue // owned by another plugin; see above
		}
		if err := s.skills.Delete(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("pluginsync: delete skill %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// sameSkill compares the fields the syncer owns for a skill row. Timestamps
// are not registration state; managed_by is not compared either because
// ListManaged only returns plugin-owned rows.
func sameSkill(cur, want ManagedSkill) bool {
	return cur.Name == want.Name &&
		cur.Description == want.Description &&
		cur.Body == want.Body &&
		slices.Equal(cur.Tags, want.Tags)
}

// Run reconciles until ctx is cancelled: one pass immediately, so a hub that
// starts with plugins already installed registers them without waiting a tick,
// then one pass every interval. Failures are logged and the loop continues —
// a registry that could not be written this round is a degraded feature, not a
// reason to stop the hub.
func (s *Syncer) Run(ctx context.Context, interval time.Duration) {
	if err := s.SyncOnce(ctx); err != nil {
		slog.Warn("plugin registry sync failed", "error", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.SyncOnce(ctx); err != nil {
				slog.Warn("plugin registry sync failed", "error", err)
			}
		}
	}
}

// mcpServerRow is the registry row a plugin's manifest asks for. The display
// name falls back to the plugin id, and the description is derived rather than
// taken from the manifest: it says what the row is for, which is a fact about
// the platform, not a string the plugin chose.
func (s *Syncer) mcpServerRow(plugin *connectorv1alpha1.Plugin, manifest *pluginpkg.Manifest) *mcpservers.MCPServer {
	displayName := manifest.DisplayName
	if displayName == "" {
		displayName = plugin.Name
	}
	return &mcpservers.MCPServer{
		Name:         mcpservers.PluginNamePrefix + plugin.Name,
		DisplayName:  displayName,
		Description:  "Tools of the " + displayName + " plugin",
		URL:          strings.TrimSuffix(s.selfURL, "/") + agentMCPEndpoint(plugin.Name),
		TokenFromEnv: MCPServerTokenFromEnv,
		ManagedBy:    mcpservers.ManagedByPlugin,
	}
}

// skillOwner reads the owning plugin id from a managed skill's plugin:<id> tag.
func skillOwner(sk ManagedSkill) string {
	for _, tag := range sk.Tags {
		if id, ok := strings.CutPrefix(tag, "plugin:"); ok {
			return id
		}
	}
	return ""
}

// sameRegistration compares the fields the syncer owns. Timestamps are not
// registration state; managed_by is. A row a user happened to name plugin-<id>
// is therefore re-claimed for the plugin here rather than left half-owned — the
// plugin id names the row, and the plugin is its only author.
func sameRegistration(cur, want *mcpservers.MCPServer) bool {
	return cur.DisplayName == want.DisplayName &&
		cur.Description == want.Description &&
		cur.URL == want.URL &&
		cur.TokenFromEnv == want.TokenFromEnv &&
		cur.ManagedBy == want.ManagedBy
}

// makeRowReadable opens a freshly written row to every user, which is what lets
// any agent image attach it: the endpoint is the hub's own and authenticates
// with the hub's internal credential, so it grants nothing the reader does not
// already have.
//
// It runs after an upsert only. Rows that were already registered keep the
// visibility they were given, and a row the syncer did not write this pass is
// not its to re-open.
func (s *Syncer) makeRowReadable(ctx context.Context, name string) {
	s.makeReadable(ctx, mcpServerResourceType, name)
}

// makeReadable opens a freshly written resource to every user, which is what
// lets any agent image attach an MCP row and any user see a plugin skill: the
// plugin shipped it for the whole platform, not for one owner.
//
// It runs after an upsert only. Resources that were already registered keep
// the visibility they were given, and a resource the syncer did not write this
// pass is not its to re-open.
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
			slog.Warn("could not open plugin resource to all users",
				"resource_type", resourceType, "name", name, "error", cerr)
		}
		return
	}
	slog.Warn("could not make plugin resource public",
		"resource_type", resourceType, "name", name, "error", err)
}

// manifestOf parses the manifest the operator's reconciler published into
// status.manifest. Absent or unparsable means the plugin declares no MCP
// endpoint, so it owns no registry row; the warn is there because the
// alternative is an operator wondering why a plugin has no tools listed.
func manifestOf(plugin *connectorv1alpha1.Plugin) (*pluginpkg.Manifest, bool) {
	if plugin.Status.Manifest == nil || len(plugin.Status.Manifest.Raw) == 0 {
		return nil, false
	}
	var manifest pluginpkg.Manifest
	if err := json.Unmarshal(plugin.Status.Manifest.Raw, &manifest); err != nil {
		slog.Warn("plugin manifest is unparsable, treating it as declaring nothing",
			"plugin", plugin.Name, "error", err)
		return nil, false
	}
	return &manifest, true
}
