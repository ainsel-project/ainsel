package skills

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// kubeAgentLister implements AgentLister by reading Agent CRs from Kubernetes
// via the controller-runtime client. It reads spec.skills from each CR and
// matches against the requested skill ID.
//
// Selection lives on the agent, never on the AgentImage: the image is a runtime
// profile and carries no skill list, so the agent CR is the only place a skill
// can be selected — and assigning a skill to an agent touches that agent alone,
// not every other agent sharing its runtime.
type kubeAgentLister struct {
	client    ctrlclient.Client
	namespace string
}

// NewAgentLister wires a production AgentLister against the hub's K8s client
// and namespace.
func NewAgentLister(c ctrlclient.Client, namespace string) AgentLister {
	return &kubeAgentLister{client: c, namespace: namespace}
}

// listAgents lists every Agent CR in the lister's namespace.
func (l *kubeAgentLister) listAgents(ctx context.Context) (*unstructured.UnstructuredList, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "ainsel.dev", Version: "v1alpha1", Kind: "AgentList",
	})
	if err := l.client.List(ctx, list, ctrlclient.InNamespace(l.namespace)); err != nil {
		return nil, err
	}
	return list, nil
}

// skillIDsOf reads an agent CR's spec.skills.
func skillIDsOf(item unstructured.Unstructured) []string {
	ids, found, err := unstructured.NestedStringSlice(item.Object, "spec", "skills")
	if err != nil || !found {
		return nil
	}
	return ids
}

func (l *kubeAgentLister) ListReferrers(ctx context.Context, skillID string) ([]Referrer, error) {
	list, err := l.listAgents(ctx)
	if err != nil {
		return nil, err
	}
	var refs []Referrer
	for _, item := range list.Items {
		for _, s := range skillIDsOf(item) {
			if s == skillID {
				refs = append(refs, Referrer{AgentName: item.GetName(), AgentID: item.GetName()})
				break
			}
		}
	}
	return refs, nil
}

// UsageCounts lists all Agent CRs in the namespace once and tallies, per skill
// ID, how many agents select it. A CR that lists the same skill ID more than
// once counts once for that skill.
//
// This is also the enabled set the shared ConfigMap is sized to: a skill with
// no count is selected by nobody, so nothing mounts it.
func (l *kubeAgentLister) UsageCounts(ctx context.Context) (map[string]int, error) {
	list, err := l.listAgents(ctx)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int)
	for _, item := range list.Items {
		tallyUnique(counts, skillIDsOf(item))
	}
	return counts, nil
}

// Assign adds skillID to the agent CR's spec.skills list.
// Idempotent: no-op if the skill is already present.
func (l *kubeAgentLister) Assign(ctx context.Context, skillID, agentName string) error {
	return l.mutateSkills(ctx, agentName, func(skills []string) []string {
		for _, s := range skills {
			if s == skillID {
				return skills // already present
			}
		}
		return append(skills, skillID)
	})
}

// Unassign removes skillID from the agent CR's spec.skills list.
// Idempotent: no-op if the skill is not present.
func (l *kubeAgentLister) Unassign(ctx context.Context, skillID, agentName string) error {
	return l.mutateSkills(ctx, agentName, func(skills []string) []string {
		out := skills[:0]
		for _, s := range skills {
			if s != skillID {
				out = append(out, s)
			}
		}
		return out
	})
}

// mutateSkills fetches the named Agent CR, applies the mutation function to
// spec.skills, and updates the CR if changed. The get-mutate-update cycle is
// wrapped in retry.RetryOnConflict so that concurrent assignments to the same
// CR are retried automatically.
func (l *kubeAgentLister) mutateSkills(ctx context.Context, agentName string, mutate func([]string) []string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group: "ainsel.dev", Version: "v1alpha1", Kind: "Agent",
		})
		if err := l.client.Get(ctx, types.NamespacedName{Name: agentName, Namespace: l.namespace}, obj); err != nil {
			// A missing agent is the caller's problem, not an internal error:
			// the handler maps ErrNotFound to 404.
			if apierrors.IsNotFound(err) {
				return fmt.Errorf("agent %q: %w", agentName, ErrNotFound)
			}
			return fmt.Errorf("get agent %q: %w", agentName, err)
		}

		existing := skillIDsOf(*obj)
		updated := mutate(existing)

		// Compare lengths as a quick check; if same length and same content, skip update.
		if len(updated) == len(existing) {
			same := true
			for i := range updated {
				if updated[i] != existing[i] {
					same = false
					break
				}
			}
			if same {
				return nil // no change needed
			}
		}

		if err := unstructured.SetNestedStringSlice(obj.Object, updated, "spec", "skills"); err != nil {
			return fmt.Errorf("set skills: %w", err)
		}
		if err := l.client.Update(ctx, obj); err != nil {
			return fmt.Errorf("update agent %q: %w", agentName, err)
		}
		return nil
	})
}

// tallyUnique adds each distinct id in ids to counts once.
func tallyUnique(counts map[string]int, ids []string) {
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		counts[id]++
	}
}
