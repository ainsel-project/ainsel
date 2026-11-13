package main

// One-time rewrite of Agent.spec.skills from the removed `{items: […]}` wrapper
// to the plain list `530070a` introduced (issue #263).
//
// The typed decode is the failure this tool works around: `AgentSpec.Skills` is
// a `[]string`, so an object-shaped `spec.skills` makes client-go fail on the
// whole object — the hub cannot list agents, and neither can the operator. So
// this command never touches `ainselv1alpha1.Agent`: it reads `spec.skills` as
// unstructured JSON through the dynamic client, which is what lets it *see* the
// old shape instead of tripping over it.
//
// Ordering matters and the tool cannot enforce it: run it *after* the new CRDs
// are applied (the API server validates on write, so patching a list against the
// old object schema is rejected). A rejection is reported with that instruction.

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

// migrateSkillsShapeCmd is the subcommand name main() dispatches on.
const migrateSkillsShapeCmd = "migrate-skills-shape"

// skillsShapeGVR is the Agent resource, addressed as unstructured on purpose —
// see the file comment.
var skillsShapeGVR = ainselv1alpha1.SchemeGroupVersion.WithResource("agents")

// crdOrderingHint is appended to a patch rejection that mentions skills, which
// is what "the CRDs are still the old shape" looks like from here. Reading the
// CRD up front would need cluster-scoped RBAC the chart does not create.
const crdOrderingHint = "if the API server rejected a list-shaped skills value, the CRDs still expect the {items: [...]} object: run kubectl apply -f chart/templates/crds/ first, then re-run"

// skillsShapeAction is what one object needs done to it.
type skillsShapeAction string

const (
	// skillsActionAbsent: spec.skills is not set — nothing to rewrite.
	skillsActionAbsent skillsShapeAction = "absent"
	// skillsActionList: already a list (or null) — untouched, which is what
	// makes a second run a no-op and keeps an explicit [] as [].
	skillsActionList skillsShapeAction = "already-list"
	// skillsActionMigrate: the wrapper shape — rewrite to a list.
	skillsActionMigrate skillsShapeAction = "migrate"
	// skillsActionInvalid: some other shape — reported, never half-migrated.
	skillsActionInvalid skillsShapeAction = "invalid"
)

// skillsShapeOptions is the resolved command configuration.
type skillsShapeOptions struct {
	namespace    string
	allNamespace bool
	dryRun       bool
}

// skillsShapeResult counts what one pass did. Errors > 0 is an exit-1 run: an
// operator has to see that an agent was left alone, not that everything is fine.
type skillsShapeResult struct {
	// Migrated counts objects actually rewritten.
	Migrated int
	// Planned counts objects a --dry-run pass would rewrite; Migrated stays 0
	// so a dry run can never be read as work that happened.
	Planned int
	Skipped int
	Errors  int
}

// skillsShapeResource resolves the Agent resource for a namespace; "" means all
// namespaces. It is a function so tests can hand in a fake dynamic client.
type skillsShapeResource func(namespace string) dynamic.ResourceInterface

// runMigrateSkillsShape is the subcommand entry point: parse flags, build a
// dynamic client from the ambient kubeconfig/in-cluster config, run one pass.
// Returns the process exit code.
func runMigrateSkillsShape(args []string) int {
	opts := skillsShapeOptions{namespace: envOrDefault("HUB_NAMESPACE", "ainsel")}

	fs := flag.NewFlagSet(migrateSkillsShapeCmd, flag.ContinueOnError)
	fs.StringVar(&opts.namespace, "namespace", opts.namespace,
		"namespace to migrate (defaults to $HUB_NAMESPACE, then ainsel); ignored with --all-namespaces")
	fs.StringVar(&opts.namespace, "n", opts.namespace, "shorthand for -namespace")
	fs.BoolVar(&opts.allNamespace, "all-namespaces", false,
		"migrate Agent objects in every namespace")
	fs.BoolVar(&opts.dryRun, "dry-run", false,
		"report what would change without writing anything")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: hub %s [-n <namespace>] [--all-namespaces] [--dry-run]\n\n", migrateSkillsShapeCmd)
		fmt.Fprintf(os.Stderr, "Rewrites Agent.spec.skills from the removed {items: [...]} wrapper to a plain list.\n")
		fmt.Fprintf(os.Stderr, "Run it after applying the new CRDs; it is idempotent, so re-running is safe.\n\n")
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
		slog.Error("no Kubernetes config, cannot migrate skills shape", "error", err)
		return 1
	}
	dynClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		slog.Error("failed to create dynamic client", "error", err)
		return 1
	}

	res := func(ns string) dynamic.ResourceInterface {
		return dynClient.Resource(skillsShapeGVR).Namespace(ns)
	}

	ctx := context.Background()
	result, err := migrateSkillsShape(ctx, res, opts)
	if err != nil {
		slog.Error("skills shape migration failed", "error", err)
		return 1
	}

	slog.Info("skills shape migration complete",
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

func migrationScope(opts skillsShapeOptions) string {
	if opts.allNamespace {
		return "all-namespaces"
	}
	return opts.namespace
}

// migrateSkillsShape lists Agent objects unstructured and applies the shape
// table to each. A List failure is returned (nothing could be done); per-object
// problems are logged and counted.
func migrateSkillsShape(ctx context.Context, res skillsShapeResource, opts skillsShapeOptions) (skillsShapeResult, error) {
	var out skillsShapeResult

	list, err := res(migrationListNamespace(opts)).List(ctx, metav1.ListOptions{})
	if err != nil {
		return out, fmt.Errorf("list agents: %w", err)
	}

	for i := range list.Items {
		obj := &list.Items[i]
		action, skills, reason := planSkillsShape(obj)

		attrs := []any{
			"namespace", obj.GetNamespace(),
			"agent", obj.GetName(),
			"found", reason,
			"action", string(action),
		}

		switch action {
		case skillsActionAbsent, skillsActionList:
			out.Skipped++
			slog.Info("skills shape left as is", attrs...)
		case skillsActionInvalid:
			out.Errors++
			slog.Error("skills shape not recognised, object untouched", attrs...)
		case skillsActionMigrate:
			if opts.dryRun {
				out.Planned++
				slog.Info("would rewrite spec.skills to a list", append(attrs, "skills", skills)...)
				continue
			}
			if err := patchSkillsShape(ctx, res(obj.GetNamespace()), obj.GetName(), skills); err != nil {
				out.Errors++
				slog.Error("failed to patch agent skills", append(attrs, "error", err)...)
				continue
			}
			out.Migrated++
			slog.Info("rewrote spec.skills to a list", append(attrs, "skills", skills)...)
		}
	}
	return out, nil
}

func migrationListNamespace(opts skillsShapeOptions) string {
	if opts.allNamespace {
		return ""
	}
	return opts.namespace
}

// planSkillsShape applies the shape table to one object's raw spec.skills.
//
//	absent                    -> skip
//	[] / [ids…] (any array)   -> skip, never patched
//	null                      -> skip (a typed decode reads it as no skills)
//	{} / {items: null|[]}     -> patch to an explicit [] (never null, never removed)
//	{items: [ids…]}           -> patch to the same list
//	anything else             -> report, count as an error
func planSkillsShape(obj *unstructured.Unstructured) (skillsShapeAction, []any, string) {
	raw, found, err := unstructured.NestedFieldNoCopy(obj.Object, "spec", "skills")
	if err != nil {
		return skillsActionInvalid, nil, "spec.skills is not readable: " + err.Error()
	}
	if !found {
		return skillsActionAbsent, nil, "spec.skills absent"
	}

	switch v := raw.(type) {
	case nil:
		return skillsActionList, nil, "spec.skills is null"
	case []any:
		return skillsActionList, nil, "spec.skills is already a list"
	case map[string]any:
		items, ok := v["items"]
		if !ok {
			if len(v) == 0 {
				return skillsActionMigrate, []any{}, "spec.skills is the empty wrapper {}"
			}
			return skillsActionInvalid, nil, "spec.skills is an object without an items field: " + jsonSnippet(raw)
		}
		switch it := items.(type) {
		case nil:
			return skillsActionMigrate, []any{}, "spec.skills is {items: null}"
		case []any:
			out := make([]any, 0, len(it))
			for _, e := range it {
				s, ok := e.(string)
				if !ok {
					return skillsActionInvalid, nil, "spec.skills.items holds a non-string entry: " + jsonSnippet(items)
				}
				out = append(out, s)
			}
			return skillsActionMigrate, out, "spec.skills is the {items: [...]} wrapper"
		default:
			return skillsActionInvalid, nil, "spec.skills.items is not a list: " + jsonSnippet(items)
		}
	default:
		return skillsActionInvalid, nil, "spec.skills is not a list: " + jsonSnippet(raw)
	}
}

// patchSkillsShape writes the plain list with a merge patch. An empty list is
// sent as [] — a merge patch deletes with null, and the acceptance rule is that
// an empty selection stays an empty array rather than going absent.
func patchSkillsShape(ctx context.Context, res dynamic.ResourceInterface, name string, skills []any) error {
	body, err := skillsShapePatchBody(skills)
	if err != nil {
		return err
	}
	if _, err := res.Patch(ctx, name, types.MergePatchType, body, metav1.PatchOptions{}); err != nil {
		if strings.Contains(err.Error(), "skills") {
			return fmt.Errorf("%w (%s)", err, crdOrderingHint)
		}
		return err
	}
	return nil
}

// skillsShapePatchBody builds the merge patch for one agent. An empty selection
// is encoded as [] — a merge patch deletes with null, and the stored value must
// stay an empty array rather than going absent.
func skillsShapePatchBody(skills []any) ([]byte, error) {
	if skills == nil {
		skills = []any{}
	}
	body, err := json.Marshal(map[string]any{"spec": map[string]any{"skills": skills}})
	if err != nil {
		return nil, fmt.Errorf("encode patch: %w", err)
	}
	return body, nil
}

// jsonSnippet renders a value for a log line, capped so a large unexpected
// spec.skills cannot flood the output.
func jsonSnippet(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%T", v)
	}
	s := string(b)
	const max = 80
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}
