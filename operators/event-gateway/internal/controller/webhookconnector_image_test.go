package controller

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	ainselv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

// webhookConnectorScheme builds a scheme holding the types reconcile() touches,
// so the tests exercise the real client paths rather than a mock of them.
func webhookConnectorScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := ainselv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("adding ainsel types to scheme: %v", err)
	}
	if err := appsv1.AddToScheme(s); err != nil {
		t.Fatalf("adding app types to scheme: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("adding core types to scheme: %v", err)
	}
	if err := networkingv1.AddToScheme(s); err != nil {
		t.Fatalf("adding networking types to scheme: %v", err)
	}
	return s
}

func newConnector(image ainselv1alpha1.ConnectorImage) *ainselv1alpha1.WebhookConnector {
	return &ainselv1alpha1.WebhookConnector{
		ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: "ainsel-dev"},
		Spec: ainselv1alpha1.WebhookConnectorSpec{
			DisplayName:     "Acme",
			WebhookEndpoint: "https://hub.example/hooks/webhook/conn-1",
			SignatureHeader: "X-Hub-Signature-256",
			WebhookSecret: ainselv1alpha1.SecretKeyRef{
				SecretRef: ainselv1alpha1.SecretRef{Name: "conn-1-hmac", Key: "secret"},
			},
			Image: image,
		},
	}
}

// reconcileOnce runs one reconcile against a fake client and returns the client,
// so a caller can read back what was written.
func reconcileOnce(t *testing.T, conn *ainselv1alpha1.WebhookConnector) (client.WithWatch, error) {
	t.Helper()
	// The reconcile reads the HMAC secret before it renders the Deployment, so it
	// has to exist for the image path to be reached.
	hmac := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "conn-1-hmac", Namespace: conn.Namespace},
		Data:       map[string][]byte{"secret": []byte("s3cr3t")},
	}
	cl := fakeclient.NewClientBuilder().
		WithScheme(webhookConnectorScheme(t)).
		WithObjects(conn, hmac).
		WithStatusSubresource(&ainselv1alpha1.WebhookConnector{}).
		Build()
	r := &WebhookConnectorReconciler{Client: cl, Scheme: webhookConnectorScheme(t), HubURL: "https://hub.example"}
	_, err := r.Reconcile(context.Background(), reqFor(conn))
	return cl, err
}

func reqFor(conn *ainselv1alpha1.WebhookConnector) ctrl.Request {
	return ctrl.Request{NamespacedName: client.ObjectKeyFromObject(conn)}
}

// containerImage reads back the image on the Deployment the reconcile produced.
func containerImage(t *testing.T, cl client.WithWatch, conn *ainselv1alpha1.WebhookConnector) string {
	t.Helper()
	var dep appsv1.Deployment
	key := client.ObjectKey{Namespace: conn.Namespace, Name: webhookConnectorResourceName(conn)}
	if err := cl.Get(context.Background(), key, &dep); err != nil {
		t.Fatalf("getting deployment: %v", err)
	}
	containers := dep.Spec.Template.Spec.Containers
	if len(containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(containers))
	}
	return containers[0].Image
}

func TestReconcileComposesConnectorImage(t *testing.T) {
	t.Run("a pin written into the tag is moved to the digest field", func(t *testing.T) {
		// What a pinned deploy left behind: the tag holds a whole reference. The
		// digest is real intent and stays - in the field that means it - and the
		// tag is what is left of the tag field. The composed reference is the same
		// string the receiver already ran, so nothing restarts.
		conn := newConnector(ainselv1alpha1.ConnectorImage{
			Repository: "dpinsel/ainsel-webhook-receiver",
			Tag:        "dev@sha256:521f386b0f6f",
		})
		cl, err := reconcileOnce(t, conn)
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if got, want := containerImage(t, cl, conn), "dpinsel/ainsel-webhook-receiver:dev@sha256:521f386b0f6f"; got != want {
			t.Fatalf("container image = %q, want %q", got, want)
		}
		var got ainselv1alpha1.WebhookConnector
		if err := cl.Get(context.Background(), client.ObjectKeyFromObject(conn), &got); err != nil {
			t.Fatalf("getting connector: %v", err)
		}
		if got.Spec.Image.Tag != "dev" {
			t.Fatalf("CR spec.image.tag = %q, want %q", got.Spec.Image.Tag, "dev")
		}
		if got.Spec.Image.Digest != "sha256:521f386b0f6f" {
			t.Fatalf("CR spec.image.digest = %q, want it to carry the pin", got.Spec.Image.Digest)
		}
	})

	t.Run("a plain tag is left untouched and stays following the tag", func(t *testing.T) {
		conn := newConnector(ainselv1alpha1.ConnectorImage{
			Repository: "dpinsel/ainsel-webhook-receiver",
			Tag:        "dev",
		})
		cl, err := reconcileOnce(t, conn)
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if got, want := containerImage(t, cl, conn), "dpinsel/ainsel-webhook-receiver:dev"; got != want {
			t.Fatalf("container image = %q, want %q", got, want)
		}
		var got ainselv1alpha1.WebhookConnector
		if err := cl.Get(context.Background(), client.ObjectKeyFromObject(conn), &got); err != nil {
			t.Fatalf("getting connector: %v", err)
		}
		if got.Spec.Image.Tag != "dev" || got.Spec.Image.Digest != "" {
			t.Fatalf("CR image was modified: %+v", got.Spec.Image)
		}
	})

	t.Run("an explicit digest pins the receiver", func(t *testing.T) {
		conn := newConnector(ainselv1alpha1.ConnectorImage{
			Repository: "r",
			Tag:        "dev",
			Digest:     "sha256:9f3a",
		})
		cl, err := reconcileOnce(t, conn)
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if got, want := containerImage(t, cl, conn), "r:dev@sha256:9f3a"; got != want {
			t.Fatalf("container image = %q, want %q", got, want)
		}
	})

	t.Run("a digest with no tag still has an image to run", func(t *testing.T) {
		conn := newConnector(ainselv1alpha1.ConnectorImage{Repository: "r", Digest: "sha256:9f3a"})
		cl, err := reconcileOnce(t, conn)
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if got, want := containerImage(t, cl, conn), "r@sha256:9f3a"; got != want {
			t.Fatalf("container image = %q, want %q", got, want)
		}
	})

	t.Run("neither tag nor digest is an error, not a guessed image", func(t *testing.T) {
		conn := newConnector(ainselv1alpha1.ConnectorImage{Repository: "r"})
		_, err := reconcileOnce(t, conn)
		if err == nil {
			t.Fatal("Reconcile succeeded, want an error naming the missing tag or digest")
		}
		if !strings.Contains(err.Error(), "neither a tag nor a digest") {
			t.Fatalf("error = %q, want it to mention the missing tag or digest", err)
		}
	})

	t.Run("reconciling twice is stable", func(t *testing.T) {
		conn := newConnector(ainselv1alpha1.ConnectorImage{
			Repository: "r",
			Tag:        "dev@sha256:521f386b0f6f",
		})
		cl, err := reconcileOnce(t, conn)
		if err != nil {
			t.Fatalf("first Reconcile: %v", err)
		}
		first := containerImage(t, cl, conn)

		var got ainselv1alpha1.WebhookConnector
		if err := cl.Get(context.Background(), client.ObjectKeyFromObject(conn), &got); err != nil {
			t.Fatalf("getting connector: %v", err)
		}
		if _, err := r2(t, cl, &got).Reconcile(context.Background(), reqFor(&got)); err != nil {
			t.Fatalf("second Reconcile: %v", err)
		}
		if again := containerImage(t, cl, conn); again != first {
			t.Fatalf("second reconcile changed the image to %q, want the stable %q", again, first)
		}
		var after ainselv1alpha1.WebhookConnector
		if err := cl.Get(context.Background(), client.ObjectKeyFromObject(conn), &after); err != nil {
			t.Fatalf("getting connector: %v", err)
		}
		if after.Spec.Image.Tag != "dev" || after.Spec.Image.Digest != "sha256:521f386b0f6f" {
			t.Fatalf("second reconcile changed the CR image: %+v", after.Spec.Image)
		}
	})
}

// r2 builds a reconciler over an existing client, for a second pass.
func r2(t *testing.T, cl client.WithWatch, _ *ainselv1alpha1.WebhookConnector) *WebhookConnectorReconciler {
	t.Helper()
	return &WebhookConnectorReconciler{Client: cl, Scheme: webhookConnectorScheme(t), HubURL: "https://hub.example"}
}
