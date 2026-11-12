package skills

import (
	"context"
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// newUnstructuredAgent builds an unstructured Agent CR with the given name,
// namespace, and spec.skills. A nil skillList leaves spec.skills unset, which
// means the agent selects no skills.
func newUnstructuredAgent(name, namespace string, skillList []string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "ainsel.dev", Version: "v1alpha1", Kind: "Agent",
	})
	obj.SetName(name)
	obj.SetNamespace(namespace)
	obj.SetResourceVersion("1")
	if skillList != nil {
		_ = unstructured.SetNestedStringSlice(obj.Object, skillList, "spec", "skills")
	}
	return obj
}

// getAgent reads back an Agent CR by name.
func getAgent(t *testing.T, kc ctrlclient.Client, ns, name string) *unstructured.Unstructured {
	t.Helper()
	after := &unstructured.Unstructured{}
	after.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "ainsel.dev", Version: "v1alpha1", Kind: "Agent",
	})
	if err := kc.Get(context.Background(), types.NamespacedName{Name: name, Namespace: ns}, after); err != nil {
		t.Fatalf("Get agent %q: %v", name, err)
	}
	return after
}

func TestKubeAgentLister_ListReferrersCarriesID(t *testing.T) {
	ctx := context.Background()
	ns := "test-ns"

	ref := newUnstructuredAgent("agent-ref", ns, []string{"my-skill", "other"})
	other := newUnstructuredAgent("agent-other", ns, []string{"different"})

	scheme := runtime.NewScheme()
	client := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(ref, other).Build()
	lister := &kubeAgentLister{client: client, namespace: ns}

	refs, err := lister.ListReferrers(ctx, "my-skill")
	if err != nil {
		t.Fatalf("ListReferrers: %v", err)
	}
	if len(refs) != 1 || refs[0].AgentName != "agent-ref" {
		t.Fatalf("expected one referrer agent-ref, got %+v", refs)
	}
	// The referrer carries the agent's API id (its resource name) so the
	// console can link the delete-409 referrer to the agent detail page.
	if refs[0].AgentID != "agent-ref" {
		t.Errorf("expected referrer agentId agent-ref, got %q", refs[0].AgentID)
	}
}

// TestKubeAgentLister_ListReferrersIsPerAgent pins the point of agent-scoped
// skills: a skill selected by one agent is referrered by that agent alone, even
// though both agents run on the same image.
func TestKubeAgentLister_ListReferrersIsPerAgent(t *testing.T) {
	ctx := context.Background()
	ns := "test-ns"

	a1 := newUnstructuredAgent("agent-a", ns, []string{"my-skill"})
	a2 := newUnstructuredAgent("agent-b", ns, []string{"other"})

	scheme := runtime.NewScheme()
	client := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(a1, a2).Build()
	lister := &kubeAgentLister{client: client, namespace: ns}

	refs, err := lister.ListReferrers(ctx, "my-skill")
	if err != nil {
		t.Fatalf("ListReferrers: %v", err)
	}
	if len(refs) != 1 || refs[0].AgentName != "agent-a" {
		t.Fatalf("expected only agent-a to refer, got %+v", refs)
	}
}

func TestKubeAgentLister_AssignIdempotent(t *testing.T) {
	ctx := context.Background()
	ns := "test-ns"

	// Agent already has "my-skill" in spec.skills.
	existing := newUnstructuredAgent("agent-already", ns, []string{"my-skill", "other-skill"})

	scheme := runtime.NewScheme()
	client := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(existing).Build()

	lister := &kubeAgentLister{client: client, namespace: ns}

	// Assign should be a no-op (skill already present).
	if err := lister.Assign(ctx, "my-skill", "agent-already"); err != nil {
		t.Fatalf("Assign: %v", err)
	}

	// Verify the CR was NOT updated (resourceVersion unchanged).
	after := getAgent(t, client, ns, "agent-already")
	if after.GetResourceVersion() != "1" {
		t.Errorf("expected resourceVersion unchanged (no Update call), got %q", after.GetResourceVersion())
	}

	// Verify skills are unchanged.
	skills, _, _ := unstructured.NestedStringSlice(after.Object, "spec", "skills")
	if len(skills) != 2 || skills[0] != "my-skill" || skills[1] != "other-skill" {
		t.Errorf("expected skills unchanged, got %v", skills)
	}
}

func TestKubeAgentLister_AssignAddsNewSkill(t *testing.T) {
	ctx := context.Background()
	ns := "test-ns"

	existing := newUnstructuredAgent("agent-add", ns, []string{"other-skill"})

	scheme := runtime.NewScheme()
	client := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(existing).Build()

	lister := &kubeAgentLister{client: client, namespace: ns}

	if err := lister.Assign(ctx, "new-skill", "agent-add"); err != nil {
		t.Fatalf("Assign: %v", err)
	}

	after := getAgent(t, client, ns, "agent-add")
	skills, _, _ := unstructured.NestedStringSlice(after.Object, "spec", "skills")
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %v", skills)
	}
	found := false
	for _, s := range skills {
		if s == "new-skill" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'new-skill' in skills, got %v", skills)
	}
}

func TestKubeAgentLister_UnassignIdempotent(t *testing.T) {
	ctx := context.Background()
	ns := "test-ns"

	// Agent does NOT have "absent-skill" in spec.skills.
	existing := newUnstructuredAgent("agent-no-skill", ns, []string{"other-skill"})

	scheme := runtime.NewScheme()
	client := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(existing).Build()

	lister := &kubeAgentLister{client: client, namespace: ns}

	// Unassign should be a no-op (skill not present).
	if err := lister.Unassign(ctx, "absent-skill", "agent-no-skill"); err != nil {
		t.Fatalf("Unassign: %v", err)
	}

	// Verify the CR was NOT updated.
	after := getAgent(t, client, ns, "agent-no-skill")
	if after.GetResourceVersion() != "1" {
		t.Errorf("expected resourceVersion unchanged (no Update call), got %q", after.GetResourceVersion())
	}
}

func TestKubeAgentLister_UnassignRemovesSkill(t *testing.T) {
	ctx := context.Background()
	ns := "test-ns"

	existing := newUnstructuredAgent("agent-rm", ns, []string{"my-skill", "other-skill"})

	scheme := runtime.NewScheme()
	client := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(existing).Build()

	lister := &kubeAgentLister{client: client, namespace: ns}

	if err := lister.Unassign(ctx, "my-skill", "agent-rm"); err != nil {
		t.Fatalf("Unassign: %v", err)
	}

	after := getAgent(t, client, ns, "agent-rm")
	skills, _, _ := unstructured.NestedStringSlice(after.Object, "spec", "skills")
	if len(skills) != 1 || skills[0] != "other-skill" {
		t.Errorf("expected [other-skill], got %v", skills)
	}
}

func TestKubeAgentLister_AssignToEmptySkills(t *testing.T) {
	ctx := context.Background()
	ns := "test-ns"

	// Agent has no spec.skills field at all.
	existing := newUnstructuredAgent("agent-empty", ns, nil)

	scheme := runtime.NewScheme()
	client := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(existing).Build()

	lister := &kubeAgentLister{client: client, namespace: ns}

	if err := lister.Assign(ctx, "new-skill", "agent-empty"); err != nil {
		t.Fatalf("Assign: %v", err)
	}

	after := getAgent(t, client, ns, "agent-empty")
	skills, _, _ := unstructured.NestedStringSlice(after.Object, "spec", "skills")
	if len(skills) != 1 || skills[0] != "new-skill" {
		t.Errorf("expected [new-skill], got %v", skills)
	}
}

// TestKubeAgentLister_AssignMissingAgentIsNotFound pins the error an assignment
// to a nonexistent agent returns: ErrNotFound (handler → 404), not an opaque
// internal error.
func TestKubeAgentLister_AssignMissingAgentIsNotFound(t *testing.T) {
	ctx := context.Background()
	ns := "test-ns"

	kc := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()
	lister := &kubeAgentLister{client: kc, namespace: ns}

	if err := lister.Assign(ctx, "my-skill", "ghost-agent"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Assign missing agent = %v, want ErrNotFound", err)
	}
	if err := lister.Unassign(ctx, "my-skill", "ghost-agent"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Unassign missing agent = %v, want ErrNotFound", err)
	}
}

// TestUsageCountsIsTheAgentSelection pins the single tally: UsageCounts is what
// the shared ConfigMap is sized to, so every skill some agent selects must be in
// it — a skill no agent selects must not be.
func TestUsageCountsIsTheAgentSelection(t *testing.T) {
	ctx := context.Background()
	ns := "test-ns"

	agentA := newUnstructuredAgent("agent-a", ns, []string{"skill-a", "shared-skill"})
	agentB := newUnstructuredAgent("agent-b", ns, []string{"shared-skill"})
	agentNone := newUnstructuredAgent("agent-none", ns, nil)

	scheme := runtime.NewScheme()
	kc := fake.NewClientBuilder().WithScheme(scheme).
		WithRuntimeObjects(agentA, agentB, agentNone).Build()
	lister := &kubeAgentLister{client: kc, namespace: ns}

	counts, err := lister.UsageCounts(ctx)
	if err != nil {
		t.Fatalf("UsageCounts: %v", err)
	}
	if counts["skill-a"] != 1 {
		t.Errorf("skill-a = %d, want 1", counts["skill-a"])
	}
	if counts["shared-skill"] != 2 {
		t.Errorf("shared-skill = %d, want 2 (both agents)", counts["shared-skill"])
	}
}

// TestUsageCountsDeduplicatesWithinAnObject checks the per-object dedupe: an
// agent that repeats an id must not inflate its count.
func TestUsageCountsDeduplicatesWithinAnObject(t *testing.T) {
	ctx := context.Background()
	ns := "test-ns"
	agent := newUnstructuredAgent("agent-dup", ns, []string{"s", "s", "s"})
	kc := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithRuntimeObjects(agent).Build()
	lister := &kubeAgentLister{client: kc, namespace: ns}

	counts, err := lister.UsageCounts(ctx)
	if err != nil {
		t.Fatalf("UsageCounts: %v", err)
	}
	if counts["s"] != 1 {
		t.Errorf("s = %d, want 1 for a single agent listing it repeatedly", counts["s"])
	}
}
