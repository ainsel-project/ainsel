package main

// One-time tool grant for Agents that never had one (issue #368).
//
// An empty `Agent.spec.enabledTools` used to mean "no restriction": the runtime
// gave the agent everything its image and MCP servers provided. It now means a
// grant of nothing. This command writes each such agent's *current effective
// set* — the referenced image's capability catalog plus one `mcp__<server>`
// entry per MCP server the agent effectively connects to — so every agent keeps
// exactly the tools it has today, spelled out, before the meaning flips.
//
// Ordering is the whole point: the chart runs this as a `pre-upgrade` hook, so
// the grants exist before any pod restarts on a runtime that reads an empty
// grant as "nothing". An agent left with an empty grant after this run has no
// tools.
//
// The server-level form is what makes this possible without live MCP calls: a
// Job cannot assume a server is reachable or its credentials present at upgrade
// time, and `mcp__<server>` is exactly what the runtime's matcher already
// grants (every tool of that server, present and future). The hub accepts those
// names for servers the agent has, so the written value survives the next PUT.
//
// An Agent whose `AgentImage` cannot be read is reported and counted as an
// error: the run exits non-zero and the upgrade stops, rather than continuing
// with an agent that would wake up with no tools.
//
// Agents written by a plugin (`spec.managedBy`) are migrated like any other:
// the plugin's next reconcile either writes the same grant or a narrower one
// the plugin owner chose deliberately.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	ainselv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	ctrl "sigs.k8s.io/controller-runtime"
)

// migrateEnabledToolsCmd is the subcommand name main() dispatches on.
const migrateEnabledToolsCmd = "migrate-enabled-tools"

// agentImageGVR is the Agent resource's companion: the capability catalog and
// the inherited MCP server list live here.
var agentImageGVR = ainselv1alpha1.SchemeGroupVersion.WithResource("agentimages")

// mcpServerGrantPrefix is the runtime's server-level grant spelling: a name
// `mcp__<server>` grants every tool of that MCP server.
const mcpServerGrantPrefix = "mcp__"

// enabledToolsAction is what one Agent needs done to it.
type enabledToolsAction string

const (
	// toolsActionGranted: spec.enabledTools already lists tools — the agent
	// has an explicit grant and is never touched. This is what makes a second
	// run a no-op.
	toolsActionGranted enabledToolsAction = "already-granted"
	// toolsActionGrant: no grant (absent or empty) — write the effective set.
	toolsActionGrant enabledToolsAction = "grant"
	// toolsActionInvalid: something this command cannot interpret (a
	// non-string entry, a missing imageRef) — reported, never half-migrated.
	toolsActionInvalid enabledToolsAction = "invalid"
)

// enabledToolsResult counts what one pass did. Errors > 0 is an exit-1 run: an
// operator has to learn that an agent was left without a grant, not that
// everything is fine.
type enabledToolsResult struct {
	// Migrated counts objects actually rewritten.
	Migrated int
	// Planned counts objects a --dry-run pass would rewrite; Migrated stays 0
	// so a dry run can never be read as work that happened.
	Planned int
	Skipped int
	Errors  int
}

// runMigrateEnabledTools is the subcommand entry point: parse flags, build a
// dynamic client from the ambient kubeconfig/in-cluster config, run one pass.
// Returns the process exit code.
func runMigrateEnabledTools(args []string) int {
	opts := agentMigrationOptions{namespace: envOrDefault("HUB_NAMESPACE", "ainsel")}

	fs := flag.NewFlagSet(migrateEnabledToolsCmd, flag.ContinueOnError)
	fs.StringVar(&opts.namespace, "namespace", opts.namespace,
		"namespace to migrate (defaults to $HUB_NAMESPACE, then ainsel); ignored with --all-namespaces")
	fs.StringVar(&opts.namespace, "n", opts.namespace, "shorthand for -namespace")
	fs.BoolVar(&opts.allNamespace, "all-namespaces", false,
		"migrate Agent objects in every namespace")
	fs.BoolVar(&opts.dryRun, "dry-run", false,
		"report what would change without writing anything")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: hub %s [-n <namespace>] [--all-namespaces] [--dry-run]\n\n", migrateEnabledToolsCmd)
		fmt.Fprintf(os.Stderr, "Writes each Agent's effective tool set into spec.enabledTools, for agents that have none.\n")
		fmt.Fprintf(os.Stderr, "Run it BEFORE upgrading to a runtime where an empty grant means no tools; it is\n")
		fmt.Fprintf(os.Stderr, "idempotent, so re-running is safe.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	cfg, err := ctrl.GetConfig()
	if err != nil {
		slog.Error("no Kubernetes config, cannot migrate enabled tool grants", "error", err)
		return 1
	}
	dynClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		slog.Error("failed to create dynamic client", "error", err)
		return 1
	}

	agents := func(ns string) dynamic.ResourceInterface {
		return dynClient.Resource(agentGVR).Namespace(ns)
	}
	images := func(ns string) dynamic.ResourceInterface {
		return dynClient.Resource(agentImageGVR).Namespace(ns)
	}

	ctx := context.Background()
	result, err := migrateEnabledTools(ctx, agents, images, opts)
	if err != nil {
		slog.Error("enabled tools migration failed", "error", err)
		return 1
	}

	slog.Info("enabled tools migration complete",
		"migrated", result.Migrated,
		"planned", result.Planned,
		"skipped", result.Skipped,
		"errors", result.Errors,
		"dryRun", opts.dryRun,
		"scope", migrationScope(opts),
	)
	if result.Errors > 0 {
		return 1
	}
	return 0
}

// migrateEnabledTools lists Agent objects unstructured and grants each one with
// nothing the set it effectively has today. A List failure is returned (nothing
// could be done); per-object problems are logged and counted, because one bad
// Agent must not hide the agents after it.
func migrateEnabledTools(ctx context.Context, agents, images agentResource, opts agentMigrationOptions) (enabledToolsResult, error) {
	var out enabledToolsResult

	list, err := agents(migrationListNamespace(opts)).List(ctx, metav1.ListOptions{})
	if err != nil {
		return out, fmt.Errorf("list agents: %w", err)
	}

	for i := range list.Items {
		obj := &list.Items[i]
		action, imageName, reason := planEnabledToolsGrant(obj)

		attrs := []any{
			"namespace", obj.GetNamespace(),
			"agent", obj.GetName(),
			"image", imageName,
			"found", reason,
			"action", string(action),
		}

		switch action {
		case toolsActionGranted:
			out.Skipped++
			slog.Info("tool grant left as is", attrs...)
		case toolsActionInvalid:
			out.Errors++
			slog.Error("tool grant not understood, object untouched", attrs...)
		case toolsActionGrant:
			grants, err := effectiveToolGrant(ctx, images(obj.GetNamespace()), obj, imageName)
			if err != nil {
				out.Errors++
				slog.Error("cannot compute the agent's effective tool set", append(attrs, "error", err)...)
				continue
			}
			if len(grants) == 0 {
				// The image ships no catalog tools and the agent has no MCP
				// servers: it has nothing today either, so there is nothing to
				// spell out. An empty grant stays empty, correctly.
				out.Skipped++
				slog.Info("nothing to grant, the agent has no tools either way", attrs...)
				continue
			}
			if opts.dryRun {
				out.Planned++
				slog.Info("would write the agent's effective tool grant",
					append(attrs, "enabledTools", grants)...)
				continue
			}
			if err := patchEnabledTools(ctx, agents(obj.GetNamespace()), obj.GetName(), grants); err != nil {
				out.Errors++
				slog.Error("failed to patch agent enabledTools", append(attrs, "error", err)...)
				continue
			}
			out.Migrated++
			slog.Info("wrote the agent's effective tool grant",
				append(attrs, "enabledTools", grants)...)
		}
	}
	return out, nil
}

// planEnabledToolsGrant decides whether one Agent needs a grant. Both states
// that read as "no grant" today are migrated — an absent field and an explicit
// empty list reached the pod as the same empty AGENT_TOOLS.
//
//	absent / null / []        -> grant (write the effective set)
//	["read", …]               -> skip, never patched
//	anything unreadable       -> report, count as an error
func planEnabledToolsGrant(obj *unstructured.Unstructured) (enabledToolsAction, string, string) {
	imageName, found, err := unstructured.NestedString(obj.Object, "spec", "imageRef", "name")
	if err != nil || !found || imageName == "" {
		return toolsActionInvalid, imageName, "spec.imageRef.name is missing, the effective tool set cannot be resolved"
	}

	raw, found, err := unstructured.NestedFieldNoCopy(obj.Object, "spec", "enabledTools")
	if err != nil {
		return toolsActionInvalid, imageName, "spec.enabledTools is not readable: " + err.Error()
	}
	if !found || raw == nil {
		return toolsActionGrant, imageName, "spec.enabledTools absent"
	}
	list, ok := raw.([]any)
	if !ok {
		return toolsActionInvalid, imageName, "spec.enabledTools is not a list: " + jsonSnippet(raw)
	}
	if len(list) == 0 {
		return toolsActionGrant, imageName, "spec.enabledTools is empty"
	}
	for _, e := range list {
		if _, ok := e.(string); !ok {
			return toolsActionInvalid, imageName, "spec.enabledTools holds a non-string entry: " + jsonSnippet(raw)
		}
	}
	return toolsActionGranted, imageName, fmt.Sprintf("spec.enabledTools already grants %d tool(s)", len(list))
}

// effectiveToolGrant computes what one Agent may use today: the referenced
// image's capability catalog, plus a server-level grant for every MCP server it
// effectively connects to (its own spec.mcp.servers when set, the image's
// mcpServers otherwise). Order is stable — catalog names in catalog order, then
// server grants — and duplicates are dropped.
func effectiveToolGrant(ctx context.Context, images dynamic.ResourceInterface, obj *unstructured.Unstructured, imageName string) ([]string, error) {
	img, err := images.Get(ctx, imageName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get AgentImage %q: %w", imageName, err)
	}

	catalog, err := namesOfListEntries(img.Object, "spec", "tools")
	if err != nil {
		return nil, fmt.Errorf("AgentImage %q: %w", imageName, err)
	}

	servers, err := effectiveMCPServers(obj, img)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{}, len(catalog)+len(servers))
	out := make([]string, 0, len(catalog)+len(servers))
	add := func(name string) {
		if _, dup := seen[name]; dup {
			return
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	for _, name := range catalog {
		add(name)
	}
	for _, name := range servers {
		add(mcpServerGrantPrefix + name)
	}
	return out, nil
}

// effectiveMCPServers lists the MCP server names an Agent connects to. An
// explicit `spec.mcp.servers` replaces the image's list (it never merges), so an
// explicit empty list means no server at all; an absent one inherits.
func effectiveMCPServers(obj, img *unstructured.Unstructured) ([]string, error) {
	raw, found, err := unstructured.NestedFieldNoCopy(obj.Object, "spec", "mcp")
	if err != nil {
		return nil, fmt.Errorf("agent spec.mcp is not readable: %w", err)
	}
	if found && raw != nil {
		if _, ok := raw.(map[string]any); !ok {
			return nil, fmt.Errorf("agent spec.mcp is not an object: %s", jsonSnippet(raw))
		}
		return namesOfListEntries(obj.Object, "spec", "mcp", "servers")
	}
	return namesOfListEntries(img.Object, "spec", "mcpServers")
}

// namesOfListEntries reads the list of objects at `path` and returns each
// entry's `name`, in order. A missing path is an empty list; an entry without a
// usable name is an error, because guessing here would silently under-grant.
func namesOfListEntries(obj map[string]any, path ...string) ([]string, error) {
	where := strings.Join(path, ".")
	raw, found, err := unstructured.NestedFieldNoCopy(obj, path...)
	if err != nil {
		return nil, err
	}
	if !found || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s is not a list", where)
	}
	out := make([]string, 0, len(list))
	for _, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s holds a non-object entry: %s", where, jsonSnippet(entry))
		}
		name, ok := m["name"].(string)
		if !ok || name == "" {
			return nil, fmt.Errorf("%s holds an entry without a name: %s", where, jsonSnippet(entry))
		}
		out = append(out, name)
	}
	return out, nil
}

// patchEnabledTools writes the grant with a merge patch. The list is never
// empty here (callers skip that case), so the patch can never be read as a
// deletion.
func patchEnabledTools(ctx context.Context, res dynamic.ResourceInterface, name string, grants []string) error {
	body, err := enabledToolsPatchBody(grants)
	if err != nil {
		return err
	}
	if _, err := res.Patch(ctx, name, types.MergePatchType, body, metav1.PatchOptions{}); err != nil {
		return err
	}
	return nil
}

// enabledToolsPatchBody builds the merge patch for one agent.
func enabledToolsPatchBody(grants []string) ([]byte, error) {
	if grants == nil {
		grants = []string{}
	}
	body, err := json.Marshal(map[string]any{"spec": map[string]any{"enabledTools": grants}})
	if err != nil {
		return nil, fmt.Errorf("encode patch: %w", err)
	}
	return body, nil
}
