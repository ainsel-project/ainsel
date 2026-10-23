package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// PluginServiceRef identifies the Kubernetes Service a plugin is reached over.
type PluginServiceRef struct {
	// Name is the Service name, in the Plugin's namespace. It must be a
	// DNS-1123 label: the reconciler interpolates it into
	// http://<name>.<namespace>.svc:<port>, so a name carrying '.', '#' or '/'
	// would point the operator — from inside the cluster — at an attacker-chosen
	// host.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name"`
	// Port is the port the plugin listens on.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`
}

// PluginImage specifies the container image a plugin Deployment runs.
type PluginImage struct {
	// Repository is the image repository (e.g. "ghcr.io/acme/plugin").
	Repository string `json:"repository"`
	// Digest pins the plugin to an exact build. Plugins are third-party code,
	// so this is required rather than optional: a mutable tag would let a
	// compromised registry push new code into the cluster silently.
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	Digest string `json:"digest"`
}

// PluginSpec defines the desired state of a Plugin.
type PluginSpec struct {
	// DisplayName is the user-facing name.
	// +optional
	DisplayName string `json:"displayName,omitempty"`
	// Service is the in-cluster address the hub proxies tool calls to.
	Service PluginServiceRef `json:"service"`
	// Image for the plugin container.
	Image PluginImage `json:"image"`
	// Disabled stops the plugin from being routed to when true.
	// +optional
	Disabled bool `json:"disabled,omitempty"`
	// ApprovedPermissions are the declared permissions an operator has
	// granted. A declared permission that is not listed here is not
	// effective — the plugin runs without it.
	// +optional
	ApprovedPermissions []string `json:"approvedPermissions,omitempty"`
	// GroupAccess lists the group ids the plugin's background identity may
	// act for (phase 3).
	// +optional
	GroupAccess []string `json:"groupAccess,omitempty"`
}

// PluginSignatureStatus describes the signature verification state of a plugin image.
type PluginSignatureStatus struct {
	// Verified indicates whether the plugin image signature was successfully verified.
	Verified bool `json:"verified"`
	// Signer is the identity that signed the image (from the signing certificate or keyless identity).
	// +optional
	Signer string `json:"signer,omitempty"`
	// Digest is the image digest that was verified.
	// +optional
	Digest string `json:"digest,omitempty"`
	// PolicyHash is the hash of the signing policy that was used for verification.
	// +optional
	PolicyHash string `json:"policyHash,omitempty"`
	// CheckedAt is when the signature was last verified.
	// +optional
	CheckedAt metav1.Time `json:"checkedAt,omitempty"`
	// Message is the last verification error, if any.
	// +optional
	Message string `json:"message,omitempty"`
}

// PluginStatus defines the observed state of a Plugin.
type PluginStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Manifest is the validated manifest JSON the plugin served, kept as the
	// raw document so the console and hub can render it without the operator
	// having to model every manifest field in Go.
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	Manifest *runtime.RawExtension `json:"manifest,omitempty"`
	// DeclaredPermissions are the permissions the manifest asks for, whether
	// or not they have been approved.
	// +optional
	DeclaredPermissions []string `json:"declaredPermissions,omitempty"`
	// Version is the plugin version reported by the manifest.
	// +optional
	Version string `json:"version,omitempty"`
	// Signature tracks the image signature verification state.
	// +optional
	Signature *PluginSignatureStatus `json:"signature,omitempty"`
	// VerifiedEndpoints is a sorted list of "ip:port" endpoints that have been
	// verified to be running the exact image digest specified in spec.image.digest.
	// +optional
	VerifiedEndpoints []string `json:"verifiedEndpoints,omitempty"`
}

const (
	PluginConditionReady = "Ready"

	PluginReasonReady               = "Ready"
	PluginReasonDisabled            = "Disabled"
	PluginReasonSignatureRejected   = "SignatureRejected"
	PluginReasonNoVerifiedEndpoints = "NoVerifiedEndpoints"
	PluginReasonUnreachable         = "Unreachable"
	PluginReasonInvalidManifest     = "InvalidManifest"
	PluginReasonIncompatible        = "Incompatible"
	PluginReasonPermissionsPending  = "PermissionsPending"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.status.version`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
// +kubebuilder:printcolumn:name="Signed",type=boolean,JSONPath=`.status.signature.verified`
// +kubebuilder:printcolumn:name="Disabled",type=boolean,JSONPath=`.spec.disabled`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Plugin is the Schema for the plugins API.
type Plugin struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PluginSpec   `json:"spec,omitempty"`
	Status PluginStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PluginList contains a list of Plugin.
type PluginList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Plugin `json:"items"`
}
