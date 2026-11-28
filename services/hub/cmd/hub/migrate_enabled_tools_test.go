package main

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

var agentImageTestGVR = schema.GroupVersionResource{
	Group: "ainsel.dev", Version: "v1alpha1", Resource: "agentimages",
}

// setRaw writes `v` at `path` as raw JSON, the way an API-server round trip
// would store it — so a test can express shapes the typed client cannot.
func setRaw(obj *unstructured.Unstructured, v any, path ...string) {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		panic(err)
	}
	if err := unstructured.SetNestedField(obj.Object, decoded, path...); err != nil {
		panic(err)
	}
}

// agentNeedingGrant builds an Agent with an imageRef and, when `enabled` is set,
// spec.enabledTools exactly as given (including an explicit empty list, which is
// a second way to have no grant). `mcp` is written verbatim when non-nil, so a
// test can express an explicit empty server list.
func agentNeedingGrant(ns, name, imageName string, enabled any, set bool, mcp any) *unstructured.Unstructured {
	obj := agentWithSkills(ns, name, nil, false)
	if err := unstructured.SetNestedField(obj.Object, imageName, "spec", "imageRef", "name"); err != nil {
		panic(err)
	}
	if set {
		setRaw(obj, enabled, "spec", "enabledTools")
	}
	if mcp != nil {
		setRaw(obj, mcp, "spec", "mcp")
	}
	return obj
}

// agentImageFixture builds an AgentImage whose catalog and MCP server list hold
// the given names (each as an object, the way the CRD stores them).
func agentImageFixture(ns, name string, tools, mcpServers []string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "ainsel.dev/v1alpha1",
		"kind":       "AgentImage",
		"metadata": map[string]any{
			"name":      name,
			"namespace": ns,
		},
		"spec": map[string]any{"displayName": name},
	}}
	if tools != nil {
		setRaw(obj, namedEntries(tools), "spec", "tools")
	}
	if mcpServers != nil {
		setRaw(obj, namedEntries(mcpServers), "spec", "mcpServers")
	}
	return obj
}

func namedEntries(names []string) []any {
	out := make([]any, 0, len(names))
	for _, n := range names {
		out = append(out, map[string]any{"name": n})
	}
	return out
}

func newFakeAgentsAndImages(t *testing.T, objs ...*unstructured.Unstructured) *dynamicfake.FakeDynamicClient {
	t.Helper()
	c := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			agentTestGVR:      "AgentList",
			agentImageTestGVR: "AgentImageList",
		},
	)
	for _, o := range objs {
		gvr := agentTestGVR
		if o.GetKind() == "AgentImage" {
			gvr = agentImageTestGVR
		}
		if _, err := c.Resource(gvr).Namespace(o.GetNamespace()).
			Create(context.Background(), o, metav1.CreateOptions{}); err != nil {
			t.Fatalf("seed %s %s: %v", o.GetKind(), o.GetName(), err)
		}
	}
	return c
}

func imageResourceFor(c *dynamicfake.FakeDynamicClient) agentResource {
	return func(ns string) dynamic.ResourceInterface {
		return c.Resource(agentImageTestGVR).Namespace(ns)
	}
}

func storedEnabledTools(t *testing.T, c *dynamicfake.FakeDynamicClient, ns, name string) ([]any, bool) {
	t.Helper()
	obj, err := c.Resource(agentTestGVR).Namespace(ns).
		Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get agent %s/%s: %v", ns, name, err)
	}
	raw, found, err := unstructured.NestedFieldNoCopy(obj.Object, "spec", "enabledTools")
	if err != nil || !found {
		return nil, found
	}
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf("spec.enabledTools on %s/%s is %T, want a list", ns, name, raw)
	}
	return list, true
}

func wantGrant(t *testing.T, c *dynamicfake.FakeDynamicClient, ns, name string, want []string) {
	t.Helper()
	got, found := storedEnabledTools(t, c, ns, name)
	if !found {
		t.Fatalf("%s/%s: spec.enabledTools absent, want %v", ns, name, want)
	}
	if len(got) != len(want) {
		t.Fatalf("%s/%s: grant = %v, want %v", ns, name, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s/%s: grant = %v, want %v", ns, name, got, want)
		}
	}
}

func TestMigrateEnabledTools_WritesEffectiveSet(t *testing.T) {
	c := newFakeAgentsAndImages(t,
		agentImageFixture("ainsel", "std", []string{"bash", "read_file"}, []string{"github", "linear"}),
		agentNeedingGrant("ainsel", "no-grant", "std", nil, false, nil),
		agentNeedingGrant("ainsel", "empty-grant", "std", []string{}, true, nil),
		agentNeedingGrant("ainsel", "pinned-mcp", "std", nil, false, map[string]any{"servers": []any{map[string]any{"name": "linear"}}}),
		agentNeedingGrant("ainsel", "no-mcp", "std", nil, false, map[string]any{"servers": []any{}}),
		agentNeedingGrant("ainsel", "granted", "std", []string{"bash"}, true, nil),
	)

	got, err := migrateEnabledTools(context.Background(), resourceFor(c), imageResourceFor(c),
		agentMigrationOptions{namespace: "ainsel"})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if want := (enabledToolsResult{Migrated: 4, Skipped: 1}); got != want {
		t.Errorf("result = %+v, want %+v", got, want)
	}

	full := []string{"bash", "read_file", "mcp__github", "mcp__linear"}
	wantGrant(t, c, "ainsel", "no-grant", full)
	wantGrant(t, c, "ainsel", "empty-grant", full)
	wantGrant(t, c, "ainsel", "pinned-mcp", []string{"bash", "read_file", "mcp__linear"})
	wantGrant(t, c, "ainsel", "no-mcp", []string{"bash", "read_file"})
	wantGrant(t, c, "ainsel", "granted", []string{"bash"})
}

func TestMigrateEnabledTools_SecondRunIsANoOp(t *testing.T) {
	c := newFakeAgentsAndImages(t,
		agentImageFixture("ainsel", "std", []string{"bash"}, []string{"github"}),
		agentNeedingGrant("ainsel", "a", "std", nil, false, nil),
	)
	opts := agentMigrationOptions{namespace: "ainsel"}

	if _, err := migrateEnabledTools(context.Background(), resourceFor(c), imageResourceFor(c), opts); err != nil {
		t.Fatalf("first run: %v", err)
	}
	got, err := migrateEnabledTools(context.Background(), resourceFor(c), imageResourceFor(c), opts)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if want := (enabledToolsResult{Skipped: 1}); got != want {
		t.Errorf("second run = %+v, want %+v", got, want)
	}
}

func TestMigrateEnabledTools_DryRunWritesNothing(t *testing.T) {
	c := newFakeAgentsAndImages(t,
		agentImageFixture("ainsel", "std", []string{"bash"}, nil),
		agentNeedingGrant("ainsel", "a", "std", nil, false, nil),
	)

	got, err := migrateEnabledTools(context.Background(), resourceFor(c), imageResourceFor(c),
		agentMigrationOptions{namespace: "ainsel", dryRun: true})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if want := (enabledToolsResult{Planned: 1}); got != want {
		t.Errorf("result = %+v, want %+v", got, want)
	}
	if _, found := storedEnabledTools(t, c, "ainsel", "a"); found {
		t.Error("dry run wrote spec.enabledTools")
	}
}

func TestMigrateEnabledTools_MissingImageIsAnError(t *testing.T) {
	c := newFakeAgentsAndImages(t,
		agentImageFixture("ainsel", "std", []string{"bash"}, nil),
		agentNeedingGrant("ainsel", "orphan", "gone", nil, false, nil),
		agentNeedingGrant("ainsel", "ok", "std", nil, false, nil),
	)

	got, err := migrateEnabledTools(context.Background(), resourceFor(c), imageResourceFor(c),
		agentMigrationOptions{namespace: "ainsel"})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if want := (enabledToolsResult{Migrated: 1, Errors: 1}); got != want {
		t.Errorf("result = %+v, want %+v", got, want)
	}
	if _, found := storedEnabledTools(t, c, "ainsel", "orphan"); found {
		t.Error("an agent with an unreadable image was given a grant anyway")
	}
}

func TestMigrateEnabledTools_MissingImageRefIsAnError(t *testing.T) {
	obj := agentWithSkills("ainsel", "no-image", nil, false)
	c := newFakeAgentsAndImages(t, obj)

	got, err := migrateEnabledTools(context.Background(), resourceFor(c), imageResourceFor(c),
		agentMigrationOptions{namespace: "ainsel"})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if want := (enabledToolsResult{Errors: 1}); got != want {
		t.Errorf("result = %+v, want %+v", got, want)
	}
}

func TestMigrateEnabledTools_NonStringEntryIsAnError(t *testing.T) {
	c := newFakeAgentsAndImages(t,
		agentImageFixture("ainsel", "std", []string{"bash"}, nil),
		agentNeedingGrant("ainsel", "bad", "std", []any{"bash", int64(3)}, true, nil),
	)

	got, err := migrateEnabledTools(context.Background(), resourceFor(c), imageResourceFor(c),
		agentMigrationOptions{namespace: "ainsel"})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if want := (enabledToolsResult{Errors: 1}); got != want {
		t.Errorf("result = %+v, want %+v", got, want)
	}
}

// An agent whose image ships nothing and has no MCP servers has no tools today
// either, so there is nothing to spell out — and writing an empty list would
// only make the next run ask the same question.
func TestMigrateEnabledTools_NothingToGrantIsSkipped(t *testing.T) {
	c := newFakeAgentsAndImages(t,
		agentImageFixture("ainsel", "empty", nil, nil),
		agentNeedingGrant("ainsel", "a", "empty", nil, false, nil),
	)

	got, err := migrateEnabledTools(context.Background(), resourceFor(c), imageResourceFor(c),
		agentMigrationOptions{namespace: "ainsel"})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if want := (enabledToolsResult{Skipped: 1}); got != want {
		t.Errorf("result = %+v, want %+v", got, want)
	}
	if _, found := storedEnabledTools(t, c, "ainsel", "a"); found {
		t.Error("wrote an empty grant, which a later run would re-read as unmigrated")
	}
}

func TestMigrateEnabledTools_AllNamespaces(t *testing.T) {
	c := newFakeAgentsAndImages(t,
		agentImageFixture("a", "std", []string{"bash"}, nil),
		agentImageFixture("b", "std", []string{"read_file"}, nil),
		agentNeedingGrant("a", "one", "std", nil, false, nil),
		agentNeedingGrant("b", "two", "std", nil, false, nil),
	)

	got, err := migrateEnabledTools(context.Background(), resourceFor(c), imageResourceFor(c),
		agentMigrationOptions{allNamespace: true})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if got.Migrated != 2 {
		t.Errorf("migrated = %d, want 2 across namespaces", got.Migrated)
	}
}

func TestPlanEnabledToolsGrant(t *testing.T) {
	tests := []struct {
		name       string
		enabled    any
		set        bool
		wantAction enabledToolsAction
	}{
		{"absent", nil, false, toolsActionGrant},
		{"empty list", []string{}, true, toolsActionGrant},
		{"null", nil, true, toolsActionGrant},
		{"one tool", []string{"bash"}, true, toolsActionGranted},
		{"non-string entry", []any{3}, true, toolsActionInvalid},
		{"not a list", "bash", true, toolsActionInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			obj := agentNeedingGrant("ainsel", "a", "std", tc.enabled, tc.set, nil)
			action, image, _ := planEnabledToolsGrant(obj)
			if action != tc.wantAction {
				t.Errorf("action = %q, want %q", action, tc.wantAction)
			}
			if image != "std" {
				t.Errorf("image = %q, want std", image)
			}
		})
	}
}

func TestEnabledToolsPatchBody(t *testing.T) {
	body, err := enabledToolsPatchBody([]string{"bash", "mcp__github"})
	if err != nil {
		t.Fatalf("patch body: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	spec, ok := got["spec"].(map[string]any)
	if !ok {
		t.Fatalf("patch has no spec object: %s", body)
	}
	if want := []any{"bash", "mcp__github"}; !reflect.DeepEqual(spec["enabledTools"], want) {
		t.Errorf("patch enabledTools = %v, want %v", spec["enabledTools"], want)
	}
}
