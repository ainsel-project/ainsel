package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ainselv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

// A registry that answers 401 to the anonymous manifest request is what every
// non-Docker-Hub registry does today: resolveImageDigest gives up, and the
// restart-on-rebuild annotation is skipped. Reconciliation must still succeed,
// but the lost restart trigger has to be visible somewhere — hence the event.
func TestReconcileDeployment_DigestFailureEmitsWarningEvent(t *testing.T) {
	withTestClient(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	img := imageObject()
	img.Spec.ImageURL = strings.TrimPrefix(srv.URL, "http://") + "/ainsel/pi:dev"
	agent := scalingAgent(nil)

	scheme := scalingFixtures()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(img, agent).
		WithStatusSubresource(&ainselv1alpha1.Agent{}).
		Build()

	recorder := record.NewFakeRecorder(10)
	r := &AgentReconciler{Client: c, Scheme: scheme, Recorder: recorder}

	// The image ref has to come from the object we built, not imageObject()'s
	// digest-pinned default, or no registry is contacted at all.
	loaded := &ainselv1alpha1.AgentImage{}
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "img"}, loaded); err != nil {
		t.Fatalf("load agent image: %v", err)
	}

	deploy, _, err := r.reconcileDeployment(context.Background(), agent, "agent-worker", loaded, loaded.Spec.ImageURL)
	if err != nil {
		t.Fatalf("reconcileDeployment must not fail on an unresolvable digest: %v", err)
	}
	if _, ok := deploy.Spec.Template.Annotations[imageDigestAnnotation]; ok {
		t.Errorf("%s must be skipped when the digest cannot be resolved", imageDigestAnnotation)
	}

	select {
	case ev := <-recorder.Events:
		if !strings.Contains(ev, "Warning") || !strings.Contains(ev, "ImageDigestResolutionFailed") {
			t.Errorf("expected a Warning ImageDigestResolutionFailed event, got %q", ev)
		}
		if !strings.Contains(ev, loaded.Spec.ImageURL) {
			t.Errorf("event should name the image %q, got %q", loaded.Spec.ImageURL, ev)
		}
	default:
		t.Fatal("expected an ImageDigestResolutionFailed event, got none")
	}
}

// The nil-Recorder guard matters for callers that build a bare reconciler (tests,
// out-of-cluster runs): a missing recorder must not panic the reconcile.
func TestReconcileDeployment_DigestFailureWithoutRecorder(t *testing.T) {
	withTestClient(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	img := imageObject()
	img.Spec.ImageURL = strings.TrimPrefix(srv.URL, "http://") + "/ainsel/pi:dev"
	agent := scalingAgent(nil)

	scheme := scalingFixtures()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(img, agent).
		WithStatusSubresource(&ainselv1alpha1.Agent{}).
		Build()

	r := &AgentReconciler{Client: c, Scheme: scheme}
	if _, _, err := r.reconcileDeployment(context.Background(), agent, "agent-worker", img, img.Spec.ImageURL); err != nil {
		t.Fatalf("reconcileDeployment with a nil Recorder: %v", err)
	}
}
