package v1alpha1

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
)

func TestPluginTypesRegistered(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() returned error: %v", err)
	}

	kinds, _, err := scheme.ObjectKinds(&Plugin{})
	if err != nil {
		t.Fatalf("ObjectKinds(&Plugin{}) returned error: %v", err)
	}
	var found bool
	for _, k := range kinds {
		if k.Kind == "Plugin" && k.Group == "ainsel.dev" && k.Version == "v1alpha1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ObjectKinds(&Plugin{}) = %+v, want kind Plugin in ainsel.dev/v1alpha1", kinds)
	}

	// The list type must be registered too, or informers and `kubectl get
	// plugins` cannot decode the collection.
	listKinds, _, err := scheme.ObjectKinds(&PluginList{})
	if err != nil {
		t.Fatalf("ObjectKinds(&PluginList{}) returned error: %v", err)
	}
	var foundList bool
	for _, k := range listKinds {
		if k.Kind == "PluginList" {
			foundList = true
		}
	}
	if !foundList {
		t.Fatalf("ObjectKinds(&PluginList{}) = %+v, want kind PluginList", listKinds)
	}
}

func TestPluginDeepCopyIndependence(t *testing.T) {
	original := &Plugin{
		Spec: PluginSpec{
			ApprovedPermissions: []string{"agents:read"},
			GroupAccess:         []string{"staff"},
		},
	}

	deepCopy := original.DeepCopy()
	if len(deepCopy.Spec.ApprovedPermissions) != 1 || deepCopy.Spec.ApprovedPermissions[0] != "agents:read" {
		t.Fatalf("DeepCopy lost Spec.ApprovedPermissions: %+v", deepCopy.Spec.ApprovedPermissions)
	}

	// Mutating the copy must not reach back into the original — the slice has
	// to be a real copy, not a shared backing array.
	deepCopy.Spec.ApprovedPermissions[0] = "agents:write"
	deepCopy.Spec.GroupAccess[0] = "admin"
	if original.Spec.ApprovedPermissions[0] != "agents:read" {
		t.Fatalf("mutating the copy changed the original: ApprovedPermissions[0] = %q, want %q",
			original.Spec.ApprovedPermissions[0], "agents:read")
	}
	if original.Spec.GroupAccess[0] != "staff" {
		t.Fatalf("mutating the copy changed the original: GroupAccess[0] = %q, want %q",
			original.Spec.GroupAccess[0], "staff")
	}
}

func TestPluginStatusDeepCopyIndependence(t *testing.T) {
	original := &Plugin{
		Status: PluginStatus{
			VerifiedEndpoints: []string{"192.168.1.1:8080", "192.168.1.2:8080"},
			Signature: &PluginSignatureStatus{
				Verified: true,
				Signer:   "test-signer",
				Digest:   "sha256:abcdef1234567890",
				Message:  "signature verified",
			},
		},
	}

	deepCopy := original.DeepCopy()
	if len(deepCopy.Status.VerifiedEndpoints) != 2 {
		t.Fatalf("DeepCopy lost Status.VerifiedEndpoints: %+v", deepCopy.Status.VerifiedEndpoints)
	}
	if deepCopy.Status.Signature == nil {
		t.Fatalf("DeepCopy lost Status.Signature")
	}

	// Mutating the copy's VerifiedEndpoints must not reach back into the original
	deepCopy.Status.VerifiedEndpoints[0] = "192.168.2.1:8080"
	if original.Status.VerifiedEndpoints[0] != "192.168.1.1:8080" {
		t.Fatalf("mutating the copy changed the original: VerifiedEndpoints[0] = %q, want %q",
			original.Status.VerifiedEndpoints[0], "192.168.1.1:8080")
	}

	// Mutating the copy's Signature must not reach back into the original
	deepCopy.Status.Signature.Signer = "modified-signer"
	deepCopy.Status.Signature.Verified = false
	if original.Status.Signature.Signer != "test-signer" {
		t.Fatalf("mutating the copy changed the original: Signature.Signer = %q, want %q",
			original.Status.Signature.Signer, "test-signer")
	}
	if !original.Status.Signature.Verified {
		t.Fatalf("mutating the copy changed the original: Signature.Verified = %v, want %v",
			original.Status.Signature.Verified, true)
	}
}
