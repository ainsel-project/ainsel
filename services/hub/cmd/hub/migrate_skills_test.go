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

var skillsShapeTestGVR = schema.GroupVersionResource{
	Group: "ainsel.dev", Version: "v1alpha1", Resource: "agents",
}

// agentWithSkills builds an Agent whose spec.skills is exactly `skills` — or is
// left unset when `set` is false. The value goes in as raw JSON on purpose: the
// old wrapper shape cannot be expressed through the typed Agent at all, which is
// the whole reason this migration exists.
func agentWithSkills(ns, name string, skills any, set bool) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "ainsel.dev/v1alpha1",
		"kind":       "Agent",
		"metadata": map[string]any{
			"name":      name,
			"namespace": ns,
		},
		"spec": map[string]any{"displayName": name},
	}}
	if set {
		raw, err := json.Marshal(skills)
		if err != nil {
			panic(err)
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			panic(err)
		}
		if err := unstructured.SetNestedField(obj.Object, v, "spec", "skills"); err != nil {
			panic(err)
		}
	}
	return obj
}

func newFakeAgents(t *testing.T, objs ...*unstructured.Unstructured) *dynamicfake.FakeDynamicClient {
	t.Helper()
	c := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{skillsShapeTestGVR: "AgentList"},
	)
	for _, o := range objs {
		if _, err := c.Resource(skillsShapeTestGVR).Namespace(o.GetNamespace()).
			Create(context.Background(), o, metav1.CreateOptions{}); err != nil {
			t.Fatalf("seed agent %s: %v", o.GetName(), err)
		}
	}
	return c
}

func resourceFor(c *dynamicfake.FakeDynamicClient) skillsShapeResource {
	return func(ns string) dynamic.ResourceInterface {
		return c.Resource(skillsShapeTestGVR).Namespace(ns)
	}
}

func storedSkills(t *testing.T, c *dynamicfake.FakeDynamicClient, ns, name string) (any, bool) {
	t.Helper()
	obj, err := c.Resource(skillsShapeTestGVR).Namespace(ns).
		Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get agent %s/%s: %v", ns, name, err)
	}
	v, found, err := unstructured.NestedFieldNoCopy(obj.Object, "spec", "skills")
	if err != nil {
		t.Fatalf("read spec.skills on %s/%s: %v", ns, name, err)
	}
	return v, found
}

func TestPlanSkillsShape(t *testing.T) {
	tests := []struct {
		name    string
		skills  any
		set     bool
		want    skillsShapeAction
		wantVal []any
	}{
		{name: "absent", set: false, want: skillsActionAbsent},
		{name: "empty list", skills: []any{}, set: true, want: skillsActionList},
		{name: "list of ids", skills: []any{"a", "b"}, set: true, want: skillsActionList},
		{name: "null", skills: nil, set: true, want: skillsActionList},
		{name: "wrapper with ids", skills: map[string]any{"items": []any{"a", "b"}}, set: true,
			want: skillsActionMigrate, wantVal: []any{"a", "b"}},
		{name: "wrapper with empty items", skills: map[string]any{"items": []any{}}, set: true,
			want: skillsActionMigrate, wantVal: []any{}},
		{name: "wrapper with null items", skills: map[string]any{"items": nil}, set: true,
			want: skillsActionMigrate, wantVal: []any{}},
		{name: "empty wrapper", skills: map[string]any{}, set: true,
			want: skillsActionMigrate, wantVal: []any{}},
		{name: "wrapper with non-string item", skills: map[string]any{"items": []any{"a", 3}}, set: true,
			want: skillsActionInvalid},
		{name: "wrapper with scalar items", skills: map[string]any{"items": "a"}, set: true,
			want: skillsActionInvalid},
		{name: "object without items", skills: map[string]any{"foo": "bar"}, set: true,
			want: skillsActionInvalid},
		{name: "string", skills: "a,b", set: true, want: skillsActionInvalid},
		{name: "number", skills: 42, set: true, want: skillsActionInvalid},
		{name: "bool", skills: true, set: true, want: skillsActionInvalid},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			action, val, reason := planSkillsShape(agentWithSkills("ainsel", "a1", tc.skills, tc.set))
			if action != tc.want {
				t.Fatalf("action = %q (reason %q), want %q", action, reason, tc.want)
			}
			if tc.wantVal != nil && !reflect.DeepEqual(val, tc.wantVal) {
				t.Fatalf("value = %#v, want %#v", val, tc.wantVal)
			}
			if reason == "" {
				t.Error("every shape needs a reason for the log line")
			}
		})
	}
}

func TestMigrateSkillsShape_RewritesWrapper(t *testing.T) {
	c := newFakeAgents(t,
		agentWithSkills("ainsel", "with-ids", map[string]any{"items": []any{"sk-a", "sk-b"}}, true),
		agentWithSkills("ainsel", "empty-wrapper", map[string]any{}, true),
	)

	res, err := migrateSkillsShape(context.Background(), resourceFor(c), skillsShapeOptions{namespace: "ainsel"})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if res.Migrated != 2 || res.Skipped != 0 || res.Errors != 0 {
		t.Fatalf("counts = %+v, want 2 migrated / 0 skipped / 0 errors", res)
	}

	if got, _ := storedSkills(t, c, "ainsel", "with-ids"); !reflect.DeepEqual(got, []any{"sk-a", "sk-b"}) {
		t.Fatalf("with-ids skills = %#v, want [sk-a sk-b]", got)
	}
	got, found := storedSkills(t, c, "ainsel", "empty-wrapper")
	if !found {
		t.Fatal("empty-wrapper lost spec.skills entirely; it must stay present as []")
	}
	if v, ok := got.([]any); !ok || len(v) != 0 {
		t.Fatalf("empty-wrapper skills = %#v (%T), want an empty array", got, got)
	}
}

// The acceptance rule: an object that already holds a list is never touched —
// including the explicit empty list, which must stay [] rather than becoming
// null or absent.
func TestMigrateSkillsShape_LeavesListsAlone(t *testing.T) {
	c := newFakeAgents(t,
		agentWithSkills("ainsel", "empty-list", []any{}, true),
		agentWithSkills("ainsel", "with-ids", []any{"sk-a"}, true),
		agentWithSkills("ainsel", "absent", nil, false),
	)

	res, err := migrateSkillsShape(context.Background(), resourceFor(c), skillsShapeOptions{namespace: "ainsel"})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if res.Migrated != 0 || res.Skipped != 3 || res.Errors != 0 {
		t.Fatalf("counts = %+v, want 0 migrated / 3 skipped / 0 errors", res)
	}
	for _, a := range c.Actions() {
		if a.GetVerb() == "patch" {
			t.Fatalf("unexpected write to %v", a)
		}
	}
	if got, found := storedSkills(t, c, "ainsel", "empty-list"); !found {
		t.Fatal("empty-list: spec.skills became absent")
	} else if v, ok := got.([]any); !ok || len(v) != 0 {
		t.Fatalf("empty-list skills = %#v (%T), want an empty array", got, got)
	}
}

// A second run over an already-migrated cluster is a no-op, which is what makes
// the chart hook Job safe to leave enabled.
func TestMigrateSkillsShape_Idempotent(t *testing.T) {
	c := newFakeAgents(t,
		agentWithSkills("ainsel", "a", map[string]any{"items": []any{"sk-a"}}, true),
		agentWithSkills("ainsel", "b", map[string]any{"items": nil}, true),
		agentWithSkills("ainsel", "c", []any{}, true),
	)
	opts := skillsShapeOptions{namespace: "ainsel"}

	first, err := migrateSkillsShape(context.Background(), resourceFor(c), opts)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if first.Migrated != 2 || first.Errors != 0 {
		t.Fatalf("first run = %+v, want 2 migrated / 0 errors", first)
	}

	before := map[string]any{}
	for _, name := range []string{"a", "b", "c"} {
		before[name], _ = storedSkills(t, c, "ainsel", name)
	}

	second, err := migrateSkillsShape(context.Background(), resourceFor(c), opts)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.Migrated != 0 || second.Skipped != 3 || second.Errors != 0 {
		t.Fatalf("second run = %+v, want 0 migrated / 3 skipped / 0 errors", second)
	}
	for _, name := range []string{"a", "b", "c"} {
		after, _ := storedSkills(t, c, "ainsel", name)
		if !reflect.DeepEqual(before[name], after) {
			t.Fatalf("agent %s changed on the second run: %#v -> %#v", name, before[name], after)
		}
	}
}

func TestMigrateSkillsShape_DryRunWritesNothing(t *testing.T) {
	c := newFakeAgents(t, agentWithSkills("ainsel", "a", map[string]any{"items": []any{"sk-a"}}, true))

	res, err := migrateSkillsShape(context.Background(), resourceFor(c),
		skillsShapeOptions{namespace: "ainsel", dryRun: true})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if res.Planned != 1 || res.Migrated != 0 || res.Skipped != 0 {
		t.Fatalf("dry run counts = %+v, want 1 planned and nothing written", res)
	}
	for _, a := range c.Actions() {
		if a.GetVerb() == "patch" {
			t.Fatalf("dry run wrote: %v", a)
		}
	}
	if got, _ := storedSkills(t, c, "ainsel", "a"); !isWrapper(got) {
		t.Fatalf("dry run changed the stored value: %#v", got)
	}
}

func isWrapper(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, has := m["items"]
	return has
}

// Unknown shapes are reported and counted, never half-migrated — and the run
// reports failure so a chart hook Job cannot claim success over a broken agent.
func TestMigrateSkillsShape_ReportsUnknownShapes(t *testing.T) {
	c := newFakeAgents(t,
		agentWithSkills("ainsel", "string", "sk-a", true),
		agentWithSkills("ainsel", "number", 42, true),
		agentWithSkills("ainsel", "mixed", map[string]any{"items": []any{"sk-a", 3}}, true),
		agentWithSkills("ainsel", "fine", map[string]any{"items": []any{"sk-a"}}, true),
	)

	res, err := migrateSkillsShape(context.Background(), resourceFor(c), skillsShapeOptions{namespace: "ainsel"})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if res.Errors != 3 {
		t.Fatalf("errors = %d, want 3", res.Errors)
	}
	if res.Migrated != 1 {
		t.Fatalf("migrated = %d, want 1", res.Migrated)
	}
	for _, name := range []string{"string", "number", "mixed"} {
		got, found := storedSkills(t, c, "ainsel", name)
		if !found {
			t.Fatalf("%s: spec.skills was removed", name)
		}
		if _, ok := got.([]any); ok {
			t.Fatalf("%s: unknown shape was rewritten to %#v", name, got)
		}
	}
}

func TestMigrateSkillsShape_NamespaceScope(t *testing.T) {
	c := newFakeAgents(t,
		agentWithSkills("ainsel", "in-ns", map[string]any{"items": []any{"sk-a"}}, true),
		agentWithSkills("other", "other-ns", map[string]any{"items": []any{"sk-b"}}, true),
	)

	res, err := migrateSkillsShape(context.Background(), resourceFor(c), skillsShapeOptions{namespace: "ainsel"})
	if err != nil {
		t.Fatalf("scoped migrate: %v", err)
	}
	if res.Migrated != 1 {
		t.Fatalf("scoped migrated = %d, want 1 (other namespace must be left alone)", res.Migrated)
	}
	if got, _ := storedSkills(t, c, "other", "other-ns"); !isWrapper(got) {
		t.Fatalf("out-of-scope agent was rewritten: %#v", got)
	}

	all, err := migrateSkillsShape(context.Background(), resourceFor(c), skillsShapeOptions{allNamespace: true})
	if err != nil {
		t.Fatalf("all-namespaces migrate: %v", err)
	}
	if all.Migrated != 1 {
		t.Fatalf("all-namespaces migrated = %d, want the remaining 1", all.Migrated)
	}
	if got, _ := storedSkills(t, c, "other", "other-ns"); isWrapper(got) {
		t.Fatalf("all-namespaces run left the wrapper in place: %#v", got)
	}
}

// The patch payload is what the API server sees: a merge patch carrying a real
// array, never null (null would delete the field).
func TestSkillsShapePatchBody(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input []any
		want  string
	}{
		{"ids", []any{"a", "b"}, `{"spec":{"skills":["a","b"]}}`},
		{"empty", []any{}, `{"spec":{"skills":[]}}`},
		{"nil becomes empty", nil, `{"spec":{"skills":[]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := skillsShapePatchBody(tc.input)
			if err != nil {
				t.Fatalf("patch body: %v", err)
			}
			if string(body) != tc.want {
				t.Fatalf("patch body = %s, want %s", body, tc.want)
			}
		})
	}
}
