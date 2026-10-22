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

	// ManifestURL resolves the URL a plugin's manifest is served at. Defaults
	// to http://<svc>.<ns>.svc:<port>/ainsel-plugin.json. Tests point this at
	// a local stub server.
	ManifestURL func(p *commonv1alpha1.Plugin) string

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

	body, err := r.fetchManifest(ctx, &p)
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

func (r *PluginReconciler) fetchManifest(ctx context.Context, p *commonv1alpha1.Plugin) ([]byte, error) {
	url := r.manifestURL(p)

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

func (r *PluginReconciler) manifestURL(p *commonv1alpha1.Plugin) string {
	if r.ManifestURL != nil {
		return r.ManifestURL(p)
	}
	return r.defaultManifestURL(p)
}

// defaultManifestURL returns the in-cluster address of the plugin's manifest:
// the well-known path on the plugin's own Service, in the Plugin's namespace.
func (r *PluginReconciler) defaultManifestURL(p *commonv1alpha1.Plugin) string {
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

// SetupWithManager sets up the controller with the Manager.
func (r *PluginReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&commonv1alpha1.Plugin{}).
		Named("plugin").
		Complete(r)
}
