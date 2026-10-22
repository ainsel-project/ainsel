package controller

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"

	"github.com/ainsel-project/ainsel/operators/event-gateway/internal/signing"
	commonv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	"github.com/ainsel-project/ainsel/shared/api/plugin"
)

const (
	// defaultPluginResync is how often a Plugin is re-fetched even without an
	// event, so a plugin that changes its served manifest is picked up and a
	// plugin that went away is noticed.
	defaultPluginResync = 60 * time.Second

	// defaultPluginFetchTimeout bounds a single manifest fetch. The plugin
	// service is in-cluster, so anything slower than this counts as failure
	// rather than slowness to wait out.
	defaultPluginFetchTimeout = 5 * time.Second
)

// PluginReconciler reconciles a Plugin object: it fetches the manifest the
// plugin serves at its well-known path, validates it against the platform's
// manifest rules, and records what it learned in the Plugin's status.
//
// The reconciler is read-only with respect to the plugin's workload — phase 1
// does not create the plugin's Deployment; it only observes and reports. A
// manifest that fails any check leaves Ready=False with the first failing
// reason, and the last good manifest stays in status so the console can keep
// rendering what worked.
type PluginReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// HTTPClient is used to fetch manifests. Defaults to a client with a
	// 5s timeout.
	HTTPClient *http.Client

	// ManifestURL resolves the URL a plugin's manifest is served at, given the
	// plugin and endpoint address. Defaults to http://<endpoint>/ainsel-plugin.json.
	// Tests point this at a local stub server.
	ManifestURL func(p *commonv1alpha1.Plugin, endpoint string) string

	// Resync is the period between manifest re-fetches. Defaults to 60s.
	Resync time.Duration

	// SigningPolicy is the operator-wide trust material used to verify a
	// plugin image's signature. The reconciler treats it as read-only. When
	// main could not load the policy it passes a policy that requires a
	// signature and trusts nothing, alongside SigningPolicyErr, so the failure
	// is visible in plugin status instead of looking like "unsigned is fine".
	SigningPolicy *signing.Policy

	// SigningPolicyErr is the error from loading SigningPolicy at startup, if
	// any. Nil in the normal case.
	SigningPolicyErr error

	// Verifier is used to verify plugin image signatures. If nil, no signature
	// verification is performed.
	Verifier signing.Verifier

	// Now returns the current time, used for cache expiration checks.
	// Defaults to time.Now.
	Now func() time.Time
}

//+kubebuilder:rbac:groups=ainsel.dev,resources=plugins,verbs=get;list;watch
//+kubebuilder:rbac:groups=ainsel.dev,resources=plugins/status,verbs=get;update;patch

// Reconcile fetches and validates the plugin's manifest and updates the
// Plugin's status with the result. Checks are evaluated in order and the
// first failure sets the Ready condition's reason:
//
//	Disabled → Unreachable → InvalidManifest (parse) → InvalidManifest (id
//	mismatch) → Incompatible → PermissionsPending → Ready
func (r *PluginReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var p commonv1alpha1.Plugin
	if err := r.Get(ctx, req.NamespacedName, &p); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !p.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	result := ctrl.Result{RequeueAfter: r.resync()}
	before := p.Status.DeepCopy()

	if p.Spec.Disabled {
		r.setReady(&p, metav1.ConditionFalse, commonv1alpha1.PluginReasonDisabled,
			"Plugin is disabled; manifest not fetched")
		return result, r.updateStatus(ctx, &p, before)
	}

	// Ensure we have a default policy
	policy := r.SigningPolicy
	if policy == nil {
		policy = &signing.Policy{}
	}

	// Step 1: Verify signature if needed
	sigResult, err := r.verifySignature(ctx, &p)
	if err != nil {
		// Signature verification error when required means fail closed
		if policy.RequireSignature {
			r.setReady(&p, metav1.ConditionFalse, commonv1alpha1.PluginReasonSignatureRejected,
				fmt.Sprintf("Signature verification failed: %v", err))
			return result, r.updateStatus(ctx, &p, before)
		}
		// Non-fatal error for optional signatures, continue
		logf.FromContext(ctx).Info("plugin signature verification failed (non-fatal)", "plugin", p.Name, "error", err.Error())
	}

	// Record signature result
	if sigResult != nil {
		var message string
		if sigResult.Verified {
			message = "Signature verified by " + sigResult.Signer
		} else if !policy.HasTrust() {
			message = "no signing trust configured"
		} else {
			message = "signature verification failed"
		}

		p.Status.Signature = &commonv1alpha1.PluginSignatureStatus{
			Verified:   sigResult.Verified,
			Signer:     sigResult.Signer,
			Digest:     p.Spec.Image.Digest,
			PolicyHash: policy.Hash(),
			CheckedAt:  metav1.NewTime(r.now()),
			Message:    message,
		}
		// If not verified and required, reject
		if !sigResult.Verified && policy.RequireSignature {
			r.setReady(&p, metav1.ConditionFalse, commonv1alpha1.PluginReasonSignatureRejected,
				"Plugin image signature verification failed")
			return result, r.updateStatus(ctx, &p, before)
		}
	} else if p.Status.Signature != nil {
		// Update the CheckedAt time even if reusing cached result
		p.Status.Signature.CheckedAt = metav1.NewTime(r.now())
	}

	// Step 2: Resolve verified endpoints (optional - fallback to default if service not found)
	endpoints, err := r.resolveVerifiedEndpoints(ctx, &p)

	// Check if service was found but no endpoints verified
	var noVerifiedEndpoints bool
	if epErr, ok := err.(*endpointResolutionError); ok {
		noVerifiedEndpoints = epErr.serviceFound
		err = epErr.cause // Extract the underlying error if any
	}

	if err != nil {
		logf.FromContext(ctx).Info("endpoint resolution failed", "plugin", p.Name, "error", err.Error())
		endpoints = []string{}
	}
	p.Status.VerifiedEndpoints = endpoints

	// Step 3: Use verified endpoints or fail with NoVerifiedEndpoints if service exists but no endpoints
	var endpoint string
	if len(endpoints) > 0 {
		endpoint = endpoints[0]
	} else if noVerifiedEndpoints {
		// Service exists and signature was checked, but no endpoints running the verified digest
		r.setReady(&p, metav1.ConditionFalse, commonv1alpha1.PluginReasonNoVerifiedEndpoints,
			"No endpoints are running the verified image digest")
		return result, r.updateStatus(ctx, &p, before)
	}
	// else: use empty endpoint for default service DNS (service not found or signature not checked)

	// Fetch manifest
	body, err := r.fetchManifest(ctx, &p, endpoint)
	if err != nil {
		logf.FromContext(ctx).Info("plugin manifest unreachable", "plugin", p.Name, "error", err.Error())
		r.setReady(&p, metav1.ConditionFalse, commonv1alpha1.PluginReasonUnreachable,
			fmt.Sprintf("Cannot fetch plugin manifest: %v", err))
		return result, r.updateStatus(ctx, &p, before)
	}

	manifest, err := plugin.Parse(body)
	if err != nil {
		r.setReady(&p, metav1.ConditionFalse, commonv1alpha1.PluginReasonInvalidManifest,
			fmt.Sprintf("Invalid plugin manifest: %v", err))
		return result, r.updateStatus(ctx, &p, before)
	}

	// The manifest declares its own identity; trusting it would let one
	// plugin's endpoint impersonate another in the console and in later
	// permission checks.
	if manifest.ID != p.Name {
		r.setReady(&p, metav1.ConditionFalse, commonv1alpha1.PluginReasonInvalidManifest,
			fmt.Sprintf("manifest id %q does not match plugin name %q", manifest.ID, p.Name))
		return result, r.updateStatus(ctx, &p, before)
	}

	// A successfully parsed, correctly identified manifest is recorded even
	// when a later check fails, so the console shows what the plugin asked
	// for and waits for an operator instead of showing nothing.
	declared := append([]string(nil), manifest.Permissions...)
	sort.Strings(declared)
	p.Status.Manifest = &runtime.RawExtension{Raw: body}
	p.Status.DeclaredPermissions = declared
	p.Status.Version = manifest.Version

	compatible, err := plugin.Compatible(manifest.PluginAPI, plugin.HostAPIVersion)
	if err != nil {
		compatible = false
	}
	if !compatible {
		r.setReady(&p, metav1.ConditionFalse, commonv1alpha1.PluginReasonIncompatible,
			fmt.Sprintf("Manifest pluginApi %q is not compatible with host plugin api %q",
				manifest.PluginAPI, plugin.HostAPIVersion))
		return result, r.updateStatus(ctx, &p, before)
	}

	if missing := plugin.MissingApprovals(declared, p.Spec.ApprovedPermissions); len(missing) > 0 {
		r.setReady(&p, metav1.ConditionFalse, commonv1alpha1.PluginReasonPermissionsPending,
			fmt.Sprintf("Declared permissions awaiting approval: %s", strings.Join(missing, ", ")))
		return result, r.updateStatus(ctx, &p, before)
	}

	r.setReady(&p, metav1.ConditionTrue, commonv1alpha1.PluginReasonReady,
		fmt.Sprintf("Manifest version %s accepted", manifest.Version))
	return result, r.updateStatus(ctx, &p, before)
}

// setReady sets the Ready condition, carrying the Plugin's generation into it.
// meta.SetStatusCondition keeps the LastTransitionTime stable while the
// condition is unchanged, which is what makes updateStatus a no-op on
// resyncs of a healthy plugin.
func (r *PluginReconciler) setReady(p *commonv1alpha1.Plugin, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&p.Status.Conditions, metav1.Condition{
		Type:               commonv1alpha1.PluginConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: p.Generation,
	})
}

// updateStatus writes the status only when it actually changed, so a healthy
// plugin re-checked every Resync does not get patched (and does not bump its
// resourceVersion or fire watch events) once per resync.
func (r *PluginReconciler) updateStatus(ctx context.Context, p *commonv1alpha1.Plugin, before *commonv1alpha1.PluginStatus) error {
	p.Status.ObservedGeneration = p.Generation
	if reflect.DeepEqual(before, &p.Status) {
		return nil
	}
	return r.Status().Update(ctx, p)
}

func (r *PluginReconciler) fetchManifest(ctx context.Context, p *commonv1alpha1.Plugin, endpoint string) ([]byte, error) {
	url := r.manifestURL(p, endpoint)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building request for %s: %w", url, err)
	}

	resp, err := r.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: unexpected status %s", url, resp.Status)
	}

	// Read one byte past the cap so an oversized body is reported as too
	// large by plugin.Parse instead of being silently truncated.
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(plugin.MaxManifestBytes)+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", url, err)
	}
	return body, nil
}

func (r *PluginReconciler) manifestURL(p *commonv1alpha1.Plugin, endpoint string) string {
	if r.ManifestURL != nil {
		return r.ManifestURL(p, endpoint)
	}
	return r.defaultManifestURL(p, endpoint)
}

// defaultManifestURL returns the in-cluster address of the plugin's manifest:
// the well-known path on the plugin's own Service, in the Plugin's namespace.
func (r *PluginReconciler) defaultManifestURL(p *commonv1alpha1.Plugin, endpoint string) string {
	if endpoint != "" {
		return fmt.Sprintf("http://%s%s", endpoint, plugin.ManifestPath)
	}
	return fmt.Sprintf("http://%s.%s.svc:%d%s",
		p.Spec.Service.Name, p.Namespace, p.Spec.Service.Port, plugin.ManifestPath)
}

func (r *PluginReconciler) httpClient() *http.Client {
	if r.HTTPClient != nil {
		return r.HTTPClient
	}
	return &http.Client{Timeout: defaultPluginFetchTimeout}
}

func (r *PluginReconciler) resync() time.Duration {
	if r.Resync > 0 {
		return r.Resync
	}
	return defaultPluginResync
}

func (r *PluginReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// endpointResolutionError indicates whether service was found but no endpoints were verified.
type endpointResolutionError struct {
	serviceFound bool
	cause        error
}

func (e *endpointResolutionError) Error() string {
	if e.cause != nil {
		return e.cause.Error()
	}
	return "no verified endpoints"
}

// verifySignature checks the plugin's image signature against the signing policy.
// It returns nil if no verification is needed or can be skipped, otherwise returns
// the verification result or an error.
func (r *PluginReconciler) verifySignature(ctx context.Context, p *commonv1alpha1.Plugin) (*signing.Result, error) {
	// If there's a policy loading error, fail closed
	if r.SigningPolicyErr != nil {
		return nil, r.SigningPolicyErr
	}

	// Default to empty policy if none provided
	policy := r.SigningPolicy
	if policy == nil {
		policy = &signing.Policy{}
	}

	// If no trust material and signatures not required, record as verified=false
	if !policy.HasTrust() && !policy.RequireSignature {
		return &signing.Result{Verified: false}, nil
	}

	// If no verifier, can't verify
	if r.Verifier == nil {
		if policy.RequireSignature {
			return nil, fmt.Errorf("no verifier configured but signatures required")
		}
		return &signing.Result{Verified: false}, nil
	}

	// Check if we can reuse the cached signature result
	if r.canReuseSigResult(p) {
		return nil, nil // Reuse cached, no new verification needed
	}

	// Verify the signature
	result, err := r.Verifier.Verify(ctx, p.Spec.Image.Repository+"@"+p.Spec.Image.Digest)
	if err != nil {
		return nil, err
	}

	// If signature is not verified and it's required, return error
	if !result.Verified && policy.RequireSignature {
		return &result, nil
	}

	return &result, nil
}

// canReuseSigResult checks if the cached signature result in status can be reused
// without re-verification based on the image digest, policy hash, and time.
func (r *PluginReconciler) canReuseSigResult(p *commonv1alpha1.Plugin) bool {
	if p.Status.Signature == nil {
		return false
	}

	// Check if image digest changed
	if p.Status.Signature.Digest != p.Spec.Image.Digest {
		return false
	}

	// Check if policy hash changed
	if p.Status.Signature.PolicyHash != r.SigningPolicy.Hash() {
		return false
	}

	// Check if 24 hours have passed since last check
	if p.Status.Signature.CheckedAt.IsZero() {
		return false
	}

	elapsed := r.now().Sub(p.Status.Signature.CheckedAt.Time)
	if elapsed > 24*time.Hour {
		return false
	}

	return true
}

// resolveVerifiedEndpoints finds all endpoints (IP:port pairs) that are running
// the verified image digest. Returns nil error if service not found (fallback to default).
// Returns an endpointResolutionError if service found but no verified endpoints.
func (r *PluginReconciler) resolveVerifiedEndpoints(ctx context.Context, p *commonv1alpha1.Plugin) ([]string, error) {
	// Get the service to find the port
	svc := &corev1.Service{}
	svcKey := client.ObjectKey{Namespace: p.Namespace, Name: p.Spec.Service.Name}
	if err := r.Get(ctx, svcKey, svc); err != nil {
		if apierrors.IsNotFound(err) {
			return []string{}, nil // Service not found, caller will fall back to default DNS
		}
		return nil, fmt.Errorf("failed to get service: %w", err)
	}

	// Find the port matching the spec.service.port
	var portName *string
	for _, port := range svc.Spec.Ports {
		if port.Port == p.Spec.Service.Port {
			portName = &port.Name
			break
		}
	}

	// List EndpointSlices for this service
	epsList := &discoveryv1.EndpointSliceList{}
	if err := r.List(ctx, epsList, client.InNamespace(p.Namespace),
		client.MatchingLabels{"kubernetes.io/service-name": p.Spec.Service.Name}); err != nil {
		return nil, fmt.Errorf("failed to list endpoint slices: %w", err)
	}

	var verifiedEndpoints []string

	// Check each endpoint
	for _, eps := range epsList.Items {
		for _, endpoint := range eps.Endpoints {
			// Skip endpoints that are not ready
			if endpoint.Conditions.Ready != nil && !*endpoint.Conditions.Ready {
				continue
			}

			// Get the Pod referenced by this endpoint
			if endpoint.TargetRef == nil {
				continue
			}

			pod := &corev1.Pod{}
			podKey := client.ObjectKey{Namespace: endpoint.TargetRef.Namespace, Name: endpoint.TargetRef.Name}
			if err := r.Get(ctx, podKey, pod); err != nil {
				continue // Skip this endpoint if pod is not found
			}

			// Check if the pod is running the verified digest
			if !r.podHasDigest(pod, p.Spec.Image.Digest) {
				continue
			}

			// Get the port number
			var port int32 = p.Spec.Service.Port
			if portName != nil {
				for _, epPort := range eps.Ports {
					if epPort.Name != nil && *epPort.Name == *portName && epPort.Port != nil {
						port = *epPort.Port
						break
					}
				}
			}

			// Add the endpoint
			for _, addr := range endpoint.Addresses {
				verifiedEndpoints = append(verifiedEndpoints, fmt.Sprintf("%s:%d", addr, port))
			}
		}
	}

	// Sort and deduplicate
	sort.Strings(verifiedEndpoints)
	verifiedEndpoints = r.dedup(verifiedEndpoints)

	// If service exists but no verified endpoints, return an error to indicate this
	if len(verifiedEndpoints) == 0 {
		return []string{}, &endpointResolutionError{serviceFound: true}
	}

	return verifiedEndpoints, nil
}

// podHasDigest checks if a pod is running a container with the specified digest.
func (r *PluginReconciler) podHasDigest(pod *corev1.Pod, digest string) bool {
	for _, cs := range pod.Status.ContainerStatuses {
		if strings.HasSuffix(cs.ImageID, "@"+digest) {
			return true
		}
	}
	return false
}

// dedup removes consecutive duplicates from a sorted slice.
func (r *PluginReconciler) dedup(strs []string) []string {
	if len(strs) == 0 {
		return strs
	}
	var result []string
	prev := ""
	for _, s := range strs {
		if s != prev {
			result = append(result, s)
			prev = s
		}
	}
	return result
}

// SetupWithManager sets up the controller with the Manager.
func (r *PluginReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&commonv1alpha1.Plugin{}).
		Named("plugin").
		Complete(r)
}
