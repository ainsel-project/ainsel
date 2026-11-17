package main

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// agentWithLLM builds an Agent whose spec.llm is exactly `llm`, or has no llm
// at all when `llm` is nil.
func agentWithLLM(ns, name string, llm map[string]any) *unstructured.Unstructured {
	obj := agentWithSkills(ns, name, nil, false)
	if llm != nil {
		if err := unstructured.SetNestedMap(obj.Object, llm, "spec", "llm"); err != nil {
			panic(err)
		}
	}
	return obj
}

func storedLLM(t *testing.T, c *dynamicfake.FakeDynamicClient, ns, name string) map[string]any {
	t.Helper()
	obj, err := c.Resource(agentTestGVR).Namespace(ns).
		Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get agent %s/%s: %v", ns, name, err)
	}
	llm, _, err := unstructured.NestedMap(obj.Object, "spec", "llm")
	if err != nil {
		t.Fatalf("read spec.llm of %s/%s: %v", ns, name, err)
	}
	return llm
}

func TestMigrateLLMProvider_SetsLegacyDefaultOnlyWhereEmpty(t *testing.T) {
	c := newFakeAgents(t,
		agentWithLLM("ainsel", "absent", map[string]any{"model": "gpt-oss:120b"}),
		agentWithLLM("ainsel", "empty", map[string]any{"model": "m", "provider": ""}),
		agentWithLLM("ainsel", "set", map[string]any{"model": "m", "provider": "anthropic"}),
		agentWithLLM("other", "elsewhere", map[string]any{"model": "m"}),
	)

	got, err := migrateLLMProvider(context.Background(), resourceFor(c), agentMigrationOptions{namespace: "ainsel"})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if want := (llmProviderResult{Migrated: 2, Skipped: 1}); got != want {
		t.Errorf("result = %+v, want %+v", got, want)
	}

	for _, name := range []string{"absent", "empty"} {
		llm := storedLLM(t, c, "ainsel", name)
		if llm["provider"] != "ollama-cloud" {
			t.Errorf("%s: provider = %v, want ollama-cloud", name, llm["provider"])
		}
		if llm["model"] == nil {
			t.Errorf("%s: the merge patch dropped spec.llm.model", name)
		}
	}
	if p := storedLLM(t, c, "ainsel", "set")["provider"]; p != "anthropic" {
		t.Errorf("set: provider = %v, want anthropic left alone", p)
	}
	if p := storedLLM(t, c, "other", "elsewhere")["provider"]; p != nil {
		t.Errorf("an agent outside the namespace was patched: provider = %v", p)
	}
}

func TestMigrateLLMProvider_SecondRunIsANoOp(t *testing.T) {
	c := newFakeAgents(t, agentWithLLM("ainsel", "a", map[string]any{"model": "m"}))
	opts := agentMigrationOptions{namespace: "ainsel"}

	if _, err := migrateLLMProvider(context.Background(), resourceFor(c), opts); err != nil {
		t.Fatalf("first run: %v", err)
	}
	got, err := migrateLLMProvider(context.Background(), resourceFor(c), opts)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if want := (llmProviderResult{Skipped: 1}); got != want {
		t.Errorf("second run = %+v, want %+v", got, want)
	}
}

func TestMigrateLLMProvider_DryRunWritesNothing(t *testing.T) {
	c := newFakeAgents(t, agentWithLLM("ainsel", "a", map[string]any{"model": "m"}))

	got, err := migrateLLMProvider(context.Background(), resourceFor(c),
		agentMigrationOptions{namespace: "ainsel", dryRun: true})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if want := (llmProviderResult{Planned: 1}); got != want {
		t.Errorf("result = %+v, want %+v", got, want)
	}
	if p := storedLLM(t, c, "ainsel", "a")["provider"]; p != nil {
		t.Errorf("dry run wrote provider = %v", p)
	}
}

func TestMigrateLLMProvider_NonStringProviderIsAnError(t *testing.T) {
	c := newFakeAgents(t,
		agentWithLLM("ainsel", "bad", map[string]any{"model": "m", "provider": int64(3)}),
		agentWithLLM("ainsel", "good", map[string]any{"model": "m"}),
	)

	got, err := migrateLLMProvider(context.Background(), resourceFor(c), agentMigrationOptions{namespace: "ainsel"})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if want := (llmProviderResult{Migrated: 1, Errors: 1}); got != want {
		t.Errorf("result = %+v, want %+v", got, want)
	}
}

func TestMigrateLLMProvider_AllNamespaces(t *testing.T) {
	c := newFakeAgents(t,
		agentWithLLM("a", "one", map[string]any{"model": "m"}),
		agentWithLLM("b", "two", map[string]any{"model": "m"}),
	)

	got, err := migrateLLMProvider(context.Background(), resourceFor(c),
		agentMigrationOptions{allNamespace: true})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if got.Migrated != 2 {
		t.Errorf("migrated = %d, want 2 across namespaces", got.Migrated)
	}
}
