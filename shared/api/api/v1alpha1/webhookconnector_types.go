package v1alpha1

import (
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SecretKeyRef references a key in a Secret.
type SecretKeyRef struct {
	SecretRef SecretRef `json:"secretRef"`
}

// SecretRef identifies a Secret and key.
type SecretRef struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// ConnectorImage specifies the container image.
type ConnectorImage struct {
	Repository string `json:"repository"`
	// Tag is the mutable tag the receiver follows. Ignored when Digest is set,
	// which pins the receiver to one build.
	// +optional
	Tag string `json:"tag,omitempty"`
	// Digest pins the receiver to an exact build (e.g. "sha256:9f3a…"). Set it
	// when the image must not move; leave it empty to follow Tag.
	// +optional
	Digest string `json:"digest,omitempty"`
}

// SplitEmbeddedDigest moves a digest written into Tag into Digest, where it
// belongs, and reports whether it changed anything.
//
// The deploy pipeline used to pin digests by appending one to the chart's tag
// value, and the hub copied the resulting string into Tag verbatim, so existing
// connectors hold "dev@sha256:…". Both halves are kept: the tag the image was
// built from, and the digest it is pinned to. Reference composes the same
// reference either way, so the running receiver does not move.
//
// An existing Digest wins: a tag carrying a second digest is a malformed value,
// and quietly overwriting a deliberate pin with another one is worse than
// leaving it and letting Reference's composed string speak for the spec.
func (i *ConnectorImage) SplitEmbeddedDigest() bool {
	base, digest, found := strings.Cut(i.Tag, "@")
	if !found {
		return false
	}
	i.Tag = base
	if i.Digest == "" {
		i.Digest = digest
	}
	return true
}

// Reference composes the container image reference for the receiver.
//
// A digest is honoured when there is one: the reference names that build, and the
// tag stays on it for provenance, which is how a registry reports a pinned image
// and what a pinned deployment is asking for. Without a digest the reference is
// the tag, which is what lets a rebuild reach the receiver.
//
// It errors when neither is set: "repository:" fails at pull time with a message
// about an invalid reference, and guessing :latest would pull something nobody
// chose. Both are worth a reconcile error instead.
func (i ConnectorImage) Reference() (string, error) {
	if i.Repository == "" {
		return "", fmt.Errorf("connector image has no repository (tag=%q digest=%q)", i.Tag, i.Digest)
	}
	switch {
	case i.Digest != "" && i.Tag != "":
		return i.Repository + ":" + i.Tag + "@" + i.Digest, nil
	case i.Digest != "":
		return i.Repository + "@" + i.Digest, nil
	case i.Tag != "":
		return i.Repository + ":" + i.Tag, nil
	default:
		return "", fmt.Errorf("connector image %q has neither a tag nor a digest: set one, or the receiver has nothing to pull", i.Repository)
	}
}

const (
	ConnectorConditionReady    = "Ready"
	ConnectorConditionDisabled = "Disabled"
)

// WebhookConnectorSpec defines the desired state of a WebhookConnector.
type WebhookConnectorSpec struct {
	// DisplayName is the user-facing name.
	DisplayName string `json:"displayName,omitempty"`
	// WebhookEndpoint is the URL to paste into GitHub/Forgejo webhook settings.
	// Set by the hub when creating the connector; read-only thereafter.
	WebhookEndpoint string `json:"webhookEndpoint"`
	// SignatureHeader is the HTTP header the sender puts the HMAC-SHA256
	// signature in. E.g. "X-Hub-Signature-256" for GitHub,
	// "X-Forgejo-Signature" for Forgejo.
	SignatureHeader string `json:"signatureHeader"`
	// WebhookSecret references the K8s Secret holding the HMAC key (key: "secret").
	WebhookSecret SecretKeyRef `json:"webhookSecret"`
	// Image for the webhook-receiver container.
	Image ConnectorImage `json:"image"`
	// Disabled scales the Deployment to zero when true.
	// +optional
	Disabled bool `json:"disabled,omitempty"`
	// ManagedBy names the external system that authors this resource, e.g.
	// "helm", "gitops", or "plugin". Any non-empty value other than "user"
	// marks the resource externally managed: the hub REST API refuses every
	// mutation with 409 Conflict and the console renders it read-only, so a
	// resource has exactly one authoring source. Empty means console-managed
	// (equivalent to "user"); no default is set so existing CRs keep their
	// meaning. Only the external manager (kubectl/GitOps/Helm) sets or clears
	// it — the hub API never accepts a client-supplied value.
	// +optional
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9.-]{0,62}$`
	ManagedBy string `json:"managedBy,omitempty"`
}

// WebhookConnectorStatus defines the observed state of a WebhookConnector.
type WebhookConnectorStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=`.spec.webhookEndpoint`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Disabled",type=boolean,JSONPath=`.spec.disabled`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// WebhookConnector is the Schema for the webhookconnectors API.
type WebhookConnector struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WebhookConnectorSpec   `json:"spec,omitempty"`
	Status WebhookConnectorStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WebhookConnectorList contains a list of WebhookConnector.
type WebhookConnectorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WebhookConnector `json:"items"`
}
