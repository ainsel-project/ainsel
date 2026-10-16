package controller

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	ainselv1alpha1 "github.com/DominikPinsel/ainsel/shared/api/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// status.paused is the display mirror of spec.pause for `kubectl get agents`
// and the console. Two failure modes matter: an active pause must read true,
// and a lifted or lapsed pause must read false — the status write is a JSON
// merge patch, so a bool that drops out of the patch at false would leave a
// resumed agent looking paused forever.
func TestUpdateStatusMirrorsPausedState(t *testing.T) {
	tests := []struct {
		name  string
		pause *ainselv1alpha1.AgentPause
		// preexisting is the status.paused value before the reconcile.
		preexisting bool
		want        bool
	}{
		{
			name:  "indefinite pause",
			pause: &ainselv1alpha1.AgentPause{},
			want:  true,
		},
		{
			name:  "future expiry",
			pause: &ainselv1alpha1.AgentPause{PausedUntil: &metav1.Time{Time: time.Now().Add(time.Hour)}},
			want:  true,
		},
		{
			name:  "lapsed expiry clears the mirror",
			pause: &ainselv1alpha1.AgentPause{PausedUntil: &metav1.Time{Time: time.Now().Add(-time.Minute)}},
			want:  false,
		},
		{
			name:        "explicit resume clears a stale true",
			pause:       nil,
			preexisting: true,
			want:        false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scheme := scalingFixtures()
			a := scalingAgent(&ainselv1alpha1.AgentScaling{Replicas: ptr.To(int32(1))})
			a.Spec.Pause = tc.pause
			a.Status.Paused = tc.preexisting

			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(a).
				WithStatusSubresource(&ainselv1alpha1.Agent{}).
				Build()
			ctx := context.Background()
			key := types.NamespacedName{Namespace: "ns", Name: "worker"}

			agent := &ainselv1alpha1.Agent{}
			if err := c.Get(ctx, key, agent); err != nil {
				t.Fatalf("operator read: %v", err)
			}
			r := &AgentReconciler{Client: c, Scheme: scheme}
			decision := ScaleDecision{Replicas: 1, Reason: ScaleReasonJobPerTask, Message: "1 task job active"}
			if err := r.updateStatus(ctx, agent, existingDeployment(1), decision); err != nil {
				t.Fatalf("updateStatus: %v", err)
			}

			var got ainselv1alpha1.Agent
			if err := c.Get(ctx, key, &got); err != nil {
				t.Fatalf("read back: %v", err)
			}
			if got.Status.Paused != tc.want {
				t.Errorf("status.paused = %v, want %v", got.Status.Paused, tc.want)
			}
		})
	}
}
