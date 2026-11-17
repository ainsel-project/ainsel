package main

// One-time rewrite of Agent.spec.llm.provider from empty to ollama-cloud.
//
// v0.2.0's operator ran an agent without a provider as ollama-cloud (the
// default branch of resolvePiProvider). The operator now refuses such an agent
// (Ready=False, InvalidLLMProvider), so without this pass every one of them
// stops on upgrade. Writing the provider v0.2.0 was already using keeps each
// agent on the backend it ran on and makes the strict check safe.
//
// Unlike migrate-skills-shape there is no ordering constraint: provider is a
// string in both the old and the new CRD schema, so the patch is valid before
// and after the chart applies its CRDs.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"

	ainselv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	ctrl "sigs.k8s.io/controller-runtime"
)

// migrateLLMProviderCmd is the subcommand name main() dispatches on.
const migrateLLMProviderCmd = "migrate-llm-provider"

// legacyDefaultLLMProvider is what v0.2.0 ran an empty provider as.
const legacyDefaultLLMProvider = ainselv1alpha1.AgentLLMProviderOllamaCloud

// llmProviderResult counts what one pass did. Errors > 0 is an exit-1 run.
type llmProviderResult struct {
	// Migrated counts objects actually rewritten.
	Migrated int
	// Planned counts objects a --dry-run pass would rewrite.
	Planned int
	Skipped int
	Errors  int
}

// runMigrateLLMProvider is the subcommand entry point: parse flags, build a
// dynamic client from the ambient kubeconfig/in-cluster config, run one pass.
// Returns the process exit code.
func runMigrateLLMProvider(args []string) int {
	opts := agentMigrationOptions{namespace: envOrDefault("HUB_NAMESPACE", "ainsel")}

	fs := flag.NewFlagSet(migrateLLMProviderCmd, flag.ContinueOnError)
	fs.StringVar(&opts.namespace, "namespace", opts.namespace,
		"namespace to migrate (defaults to $HUB_NAMESPACE, then ainsel); ignored with --all-namespaces")
	fs.StringVar(&opts.namespace, "n", opts.namespace, "shorthand for -namespace")
	fs.BoolVar(&opts.allNamespace, "all-namespaces", false,
		"migrate Agent objects in every namespace")
	fs.BoolVar(&opts.dryRun, "dry-run", false,
		"report what would change without writing anything")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: hub %s [-n <namespace>] [--all-namespaces] [--dry-run]\n\n", migrateLLMProviderCmd)
		fmt.Fprintf(os.Stderr, "Sets Agent.spec.llm.provider to %s where it is empty - the provider v0.2.0 ran those agents with.\n", legacyDefaultLLMProvider)
		fmt.Fprintf(os.Stderr, "It is idempotent, so re-running is safe.\n\n")
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
		slog.Error("no Kubernetes config, cannot migrate LLM providers", "error", err)
		return 1
	}
	dynClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		slog.Error("failed to create dynamic client", "error", err)
		return 1
	}

	res := func(ns string) dynamic.ResourceInterface {
		return dynClient.Resource(agentGVR).Namespace(ns)
	}

	result, err := migrateLLMProvider(context.Background(), res, opts)
	if err != nil {
		slog.Error("LLM provider migration failed", "error", err)
		return 1
	}

	slog.Info("LLM provider migration complete",
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

// migrateLLMProvider lists Agent objects unstructured and sets the legacy
// default provider on each one without a provider. A List failure is returned
// (nothing could be done); per-object problems are logged and counted.
func migrateLLMProvider(ctx context.Context, res agentResource, opts agentMigrationOptions) (llmProviderResult, error) {
	var out llmProviderResult

	list, err := res(migrationListNamespace(opts)).List(ctx, metav1.ListOptions{})
	if err != nil {
		return out, fmt.Errorf("list agents: %w", err)
	}

	for i := range list.Items {
		obj := &list.Items[i]
		attrs := []any{"namespace", obj.GetNamespace(), "agent", obj.GetName()}

		provider, _, err := unstructured.NestedString(obj.Object, "spec", "llm", "provider")
		if err != nil {
			out.Errors++
			slog.Error("spec.llm.provider is not a string, object untouched", append(attrs, "error", err)...)
			continue
		}
		if provider != "" {
			out.Skipped++
			slog.Info("LLM provider left as is", append(attrs, "provider", provider)...)
			continue
		}
		if opts.dryRun {
			out.Planned++
			slog.Info("would set spec.llm.provider", append(attrs, "provider", legacyDefaultLLMProvider)...)
			continue
		}
		if err := patchLLMProvider(ctx, res(obj.GetNamespace()), obj.GetName()); err != nil {
			out.Errors++
			slog.Error("failed to patch agent LLM provider", append(attrs, "error", err)...)
			continue
		}
		out.Migrated++
		slog.Info("set spec.llm.provider", append(attrs, "provider", legacyDefaultLLMProvider)...)
	}
	return out, nil
}

// patchLLMProvider writes the legacy default provider with a merge patch,
// which leaves every other spec.llm field as stored.
func patchLLMProvider(ctx context.Context, res dynamic.ResourceInterface, name string) error {
	body, err := json.Marshal(map[string]any{
		"spec": map[string]any{"llm": map[string]any{"provider": legacyDefaultLLMProvider}},
	})
	if err != nil {
		return fmt.Errorf("encode patch: %w", err)
	}
	_, err = res.Patch(ctx, name, types.MergePatchType, body, metav1.PatchOptions{})
	return err
}
