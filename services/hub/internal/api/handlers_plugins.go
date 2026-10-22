package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/ainsel-project/ainsel/services/hub/internal/auditevents"
	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	connectorv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	pluginpkg "github.com/ainsel-project/ainsel/shared/api/plugin"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The plugin admin API. Plugins are third-party code the platform routes to,
// so every write is admin-only and every successful write lands an audit row
// in auth_events. Reads are open to authenticated callers, but non-admins see
// only the public shape (enabled + Ready plugins, no permission or group
// data) — a plugin that has not passed the reconciler is invisible to them.

// Plugin id and image digest rules from the plugin system spec. The id
// becomes a URL segment and later a tool-name prefix, so it is deliberately
// strict; the digest is required because a mutable tag would let a
// compromised registry push new code into the cluster silently.
//
// The id must also end alphanumeric: a trailing '-' is a fine URL slug but not
// a valid Kubernetes object name, and the id becomes the Plugin's name, so
// letting it through would turn a syntactic input error into a 500 from the
// API server.
var (
	pluginIDPattern     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,29}[a-z0-9]$`)
	pluginDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	// pluginServiceNamePattern is a DNS-1123 label. The reconciler interpolates
	// the name into http://<name>.<namespace>.svc:<port><path>, so a name
	// carrying '.', '#' or '/' would point the operator — from inside the
	// cluster — at an attacker-chosen host.
	pluginServiceNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)

// PluginInstallRequest installs a plugin whose workload is already deployed
// by the operator/admin (the platform does not create the plugin Deployment
// in phase 1). The hub records what to route to.
type PluginInstallRequest struct {
	ID              string `json:"id"`
	DisplayName     string `json:"displayName,omitempty"`
	ServiceName     string `json:"serviceName"`
	ServicePort     int32  `json:"servicePort"`
	ImageRepository string `json:"imageRepository"`
	ImageDigest     string `json:"imageDigest"`
}

// PluginSignatureResponse represents the signature verification state of a plugin.
type PluginSignatureResponse struct {
	Verified  bool         `json:"verified"`
	Signer    string       `json:"signer,omitempty"`
	CheckedAt *metav1.Time `json:"checkedAt,omitempty"`
	Message   string       `json:"message,omitempty"`
}

// PluginResponse is the admin-facing view of a plugin: everything the CR
// carries plus the flattened Ready condition.
type PluginResponse struct {
	ID                  string                   `json:"id"`
	DisplayName         string                   `json:"displayName,omitempty"`
	Version             string                   `json:"version,omitempty"`
	ServiceName         string                   `json:"serviceName"`
	ServicePort         int32                    `json:"servicePort"`
	ImageRepository     string                   `json:"imageRepository"`
	ImageDigest         string                   `json:"imageDigest"`
	Disabled            bool                     `json:"disabled"`
	Ready               bool                     `json:"ready"`
	Reason              string                   `json:"reason,omitempty"`
	Message             string                   `json:"message,omitempty"`
	DeclaredPermissions []string                 `json:"declaredPermissions,omitempty"`
	ApprovedPermissions []string                 `json:"approvedPermissions,omitempty"`
	GroupAccess         []string                 `json:"groupAccess,omitempty"`
	Signature           *PluginSignatureResponse `json:"signature,omitempty"`
	VerifiedEndpoints   []string                 `json:"verifiedEndpoints,omitempty"`
	Manifest            json.RawMessage          `json:"manifest,omitempty"`
}

// PluginPublicResponse is what non-admin callers see: enough to know a plugin
// exists and what it offers, nothing about who may use it or what it is
// trusted with.
type PluginPublicResponse struct {
	ID          string          `json:"id"`
	DisplayName string          `json:"displayName,omitempty"`
	Version     string          `json:"version,omitempty"`
	Manifest    json.RawMessage `json:"manifest,omitempty"`
}

// registerPluginRoutes mounts the plugin admin API. New() calls it; tests
// mount the same pair through the helpers in handlers_plugins_test.go.
func registerPluginRoutes(s *Server) {
	s.mux.HandleFunc("/api/v1/plugins", s.handlePlugins)
	s.mux.HandleFunc("/api/v1/plugins/", s.handlePlugin)
}

// requirePluginAdmin gates the plugin write surface. With no authz configured
// (the dev-mode escape hatch) the caller counts as admin, matching
// requireRead/requireWrite — deliberately different from the users handler's
// requireAdmin, which fails closed in dev mode: the user-management surface
// predates that convention and must keep its behaviour.
func (s *Server) requirePluginAdmin(w http.ResponseWriter, r *http.Request) bool {
	if s.authzChecker == nil {
		return true
	}
	u, ok := oidc.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	admin, _ := s.authzChecker.IsAdmin(r.Context(), u.Sub)
	if !admin {
		writeError(w, http.StatusForbidden, "admin access required")
		return false
	}
	return true
}

// pluginsCallerIsAdmin decides which read shape the caller gets. Same rule as
// requireAdmin: admin, or anyone at all when authz is not configured.
func (s *Server) pluginsCallerIsAdmin(r *http.Request) bool {
	if s.authzChecker == nil {
		return true
	}
	return s.callerIsAdmin(r)
}

// requirePluginReader accepts a reader: any caller when authz is off, any
// authenticated caller otherwise. The admin/public *shape* is decided later
// by pluginsCallerIsAdmin.
func (s *Server) requirePluginReader(w http.ResponseWriter, r *http.Request) bool {
	if s.authzChecker == nil {
		return true
	}
	if _, ok := oidc.FromContext(r.Context()); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	return true
}

func (s *Server) handlePlugins(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	switch r.Method {
	case http.MethodGet:
		if !s.requirePluginReader(w, r) {
			return
		}
		s.listPlugins(ctx, w, r)
	case http.MethodPost:
		if !s.requirePluginAdmin(w, r) {
			return
		}
		s.installPlugin(ctx, w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handlePlugin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/plugins/")
	if path == "" {
		writeError(w, http.StatusBadRequest, "missing plugin id")
		return
	}

	if parts := strings.SplitN(path, "/", 2); len(parts) == 2 {
		name, action := parts[0], parts[1]
		switch {
		case action == "enable" && r.Method == http.MethodPost:
			if !s.requirePluginAdmin(w, r) {
				return
			}
			s.setPluginDisabled(ctx, w, r, name, false, auditevents.TypePluginEnabled)
		case action == "disable" && r.Method == http.MethodPost:
			if !s.requirePluginAdmin(w, r) {
				return
			}
			s.setPluginDisabled(ctx, w, r, name, true, auditevents.TypePluginDisabled)
		case action == "approve-permissions" && r.Method == http.MethodPost:
			if !s.requirePluginAdmin(w, r) {
				return
			}
			s.approvePluginPermissions(ctx, w, r, name)
		case action == "group-access" && r.Method == http.MethodPut:
			if !s.requirePluginAdmin(w, r) {
				return
			}
			s.setPluginGroupAccess(ctx, w, r, name)
		default:
			writeError(w, http.StatusNotFound, "not found")
		}
		return
	}

	name := path
	switch r.Method {
	case http.MethodGet:
		if !s.requirePluginReader(w, r) {
			return
		}
		s.getPlugin(ctx, w, r, name)
	case http.MethodDelete:
		if !s.requirePluginAdmin(w, r) {
			return
		}
		s.deletePlugin(ctx, w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) listPlugins(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	page, err := ParsePageParams(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var list connectorv1alpha1.PluginList
	if err := s.client.List(ctx, &list, client.InNamespace(s.ns)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	sort.SliceStable(list.Items, func(i, j int) bool { return list.Items[i].Name < list.Items[j].Name })

	admin := s.pluginsCallerIsAdmin(r)
	results := make([]any, 0, len(list.Items))
	for _, p := range list.Items {
		if admin {
			results = append(results, toPluginResponse(p))
			continue
		}
		// Non-admins only see plugins that are usable: enabled and Ready.
		if p.Spec.Disabled || !pluginIsReady(p) {
			continue
		}
		results = append(results, toPluginPublicResponse(p))
	}

	total := len(results)
	lo, hi := page.Slice(total)
	items := results[lo:hi]
	if items == nil {
		items = []any{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items":      items,
		"total":      total,
		"page":       page.Page,
		"pageSize":   page.PageSize,
		"totalPages": page.TotalPages(total),
	})
}

func (s *Server) getPlugin(ctx context.Context, w http.ResponseWriter, r *http.Request, name string) {
	nn := types.NamespacedName{Name: name, Namespace: s.ns}
	var p connectorv1alpha1.Plugin
	if err := s.client.Get(ctx, nn, &p); err != nil {
		writeError(w, http.StatusNotFound, "plugin not found")
		return
	}
	if !s.pluginsCallerIsAdmin(r) {
		if p.Spec.Disabled || !pluginIsReady(p) {
			// Same 404 as "does not exist": the existence of a pending or
			// disabled plugin is not public information.
			writeError(w, http.StatusNotFound, "plugin not found")
			return
		}
		writeJSON(w, http.StatusOK, toPluginPublicResponse(p))
		return
	}
	writeJSON(w, http.StatusOK, toPluginResponse(p))
}

func (s *Server) installPlugin(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	var req PluginInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if !pluginIDPattern.MatchString(req.ID) {
		writeError(w, http.StatusBadRequest, "id must match "+pluginIDPattern.String())
		return
	}
	if req.ServiceName == "" {
		writeError(w, http.StatusBadRequest, "serviceName is required")
		return
	}
	if len(req.ServiceName) > 63 || !pluginServiceNamePattern.MatchString(req.ServiceName) {
		writeError(w, http.StatusBadRequest, "serviceName must be a DNS-1123 label ("+pluginServiceNamePattern.String()+", max 63 chars)")
		return
	}
	if req.ServicePort < 1 || req.ServicePort > 65535 {
		writeError(w, http.StatusBadRequest, "servicePort must be between 1 and 65535")
		return
	}
	if req.ImageRepository == "" {
		writeError(w, http.StatusBadRequest, "imageRepository is required")
		return
	}
	if !pluginDigestPattern.MatchString(req.ImageDigest) {
		writeError(w, http.StatusBadRequest, "imageDigest must match ^sha256:[a-f0-9]{64}$")
		return
	}

	p := &connectorv1alpha1.Plugin{}
	p.APIVersion = "ainsel.dev/v1alpha1"
	p.Kind = "Plugin"
	p.Name = req.ID
	p.Namespace = s.ns
	p.Spec.DisplayName = req.DisplayName
	p.Spec.Service = connectorv1alpha1.PluginServiceRef{Name: req.ServiceName, Port: req.ServicePort}
	p.Spec.Image = connectorv1alpha1.PluginImage{Repository: req.ImageRepository, Digest: req.ImageDigest}
	// ApprovedPermissions stays empty on purpose: an install records what the
	// plugin *is*, not what it may do. Approvals are their own admin action
	// against the declared set the manifest turns out to ask for.

	if err := s.client.Create(ctx, p); err != nil {
		if apierrors.IsAlreadyExists(err) {
			writeError(w, http.StatusConflict, "plugin "+req.ID+" already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create plugin: "+err.Error())
		return
	}

	s.recordAuthEvent(r, auditevents.TypePluginInstalled, callerSub(r), req.ID, auditevents.OutcomeSuccess)
	writeJSON(w, http.StatusCreated, toPluginResponse(*p))
}

func (s *Server) deletePlugin(ctx context.Context, w http.ResponseWriter, r *http.Request, name string) {
	nn := types.NamespacedName{Name: name, Namespace: s.ns}
	var p connectorv1alpha1.Plugin
	if err := s.client.Get(ctx, nn, &p); err != nil {
		writeError(w, http.StatusNotFound, "plugin not found")
		return
	}
	if err := s.client.Delete(ctx, &p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAuthEvent(r, auditevents.TypePluginUninstalled, callerSub(r), name, auditevents.OutcomeSuccess)
	w.WriteHeader(http.StatusNoContent)
}

// setPluginDisabled toggles spec.disabled; enable passes disabled=false. The
// reconciler reacts by flipping the Ready condition to Disabled and stops
// fetching the manifest; routing respects the flag. The hub only records
// intent here.
func (s *Server) setPluginDisabled(ctx context.Context, w http.ResponseWriter, r *http.Request, name string, disabled bool, eventType string) {
	nn := types.NamespacedName{Name: name, Namespace: s.ns}
	var p connectorv1alpha1.Plugin
	if err := s.client.Get(ctx, nn, &p); err != nil {
		writeError(w, http.StatusNotFound, "plugin not found")
		return
	}
	p.Spec.Disabled = disabled
	if err := s.client.Update(ctx, &p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAuthEvent(r, eventType, callerSub(r), name, auditevents.OutcomeSuccess)
	writeJSON(w, http.StatusOK, toPluginResponse(p))
}

// approvePluginPermissions copies the declared set into spec.approvedPermissions,
// but only when the request names exactly that set. The comparison is by set:
// order and duplicates are noise. Approving anything else — a subset, or a
// permission the manifest never declared — is a 409 listing the current
// declared set, so an admin working from a stale console view learns the
// truth from the error instead of approving the wrong thing.
func (s *Server) approvePluginPermissions(ctx context.Context, w http.ResponseWriter, r *http.Request, name string) {
	var req struct {
		Permissions []string `json:"permissions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	nn := types.NamespacedName{Name: name, Namespace: s.ns}
	var p connectorv1alpha1.Plugin
	if err := s.client.Get(ctx, nn, &p); err != nil {
		writeError(w, http.StatusNotFound, "plugin not found")
		return
	}

	declared := p.Status.DeclaredPermissions
	// Set equality in both directions: what the request fails to approve, and
	// what it approves without being declared.
	if len(pluginpkg.MissingApprovals(declared, req.Permissions)) > 0 ||
		len(pluginpkg.MissingApprovals(req.Permissions, declared)) > 0 {
		writeError(w, http.StatusConflict, fmt.Sprintf(
			"permissions must match the plugin's declared set exactly (declared: [%s])", strings.Join(declared, ", ")))
		return
	}

	approved := append([]string(nil), declared...)
	sort.Strings(approved)
	p.Spec.ApprovedPermissions = approved
	if err := s.client.Update(ctx, &p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAuthEvent(r, auditevents.TypePluginPermissionsApproved, callerSub(r), name, auditevents.OutcomeSuccess)
	writeJSON(w, http.StatusOK, toPluginResponse(p))
}

// setPluginGroupAccess records which groups' background identity the plugin
// may act for. Group ids are checked against the authz store when one is
// wired; with no store (dev mode) the check is skipped, like elsewhere.
func (s *Server) setPluginGroupAccess(ctx context.Context, w http.ResponseWriter, r *http.Request, name string) {
	var req struct {
		Groups []string `json:"groups"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	nn := types.NamespacedName{Name: name, Namespace: s.ns}
	var p connectorv1alpha1.Plugin
	if err := s.client.Get(ctx, nn, &p); err != nil {
		writeError(w, http.StatusNotFound, "plugin not found")
		return
	}

	if s.authzStore != nil {
		for _, g := range req.Groups {
			grp, err := s.authzStore.GetGroup(ctx, g)
			if errors.Is(err, authz.ErrNotFound) || (err == nil && grp == nil) {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("group %q does not exist", g))
				return
			}
			if err != nil {
				// A broken store is not a missing group — reporting it as one
				// would send the admin to re-create a group that still exists.
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	}

	p.Spec.GroupAccess = req.Groups
	if err := s.client.Update(ctx, &p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAuthEvent(r, auditevents.TypePluginGroupAccessChanged, callerSub(r), name, auditevents.OutcomeSuccess)
	writeJSON(w, http.StatusOK, toPluginResponse(p))
}

// --- response mapping ---

// pluginReadyCondition returns the Ready condition, or nil when the
// reconciler has not written one yet.
func pluginReadyCondition(p connectorv1alpha1.Plugin) *metav1.Condition {
	for i := range p.Status.Conditions {
		if p.Status.Conditions[i].Type == connectorv1alpha1.PluginConditionReady {
			return &p.Status.Conditions[i]
		}
	}
	return nil
}

func pluginIsReady(p connectorv1alpha1.Plugin) bool {
	c := pluginReadyCondition(p)
	return c != nil && c.Status == metav1.ConditionTrue
}

func toPluginResponse(p connectorv1alpha1.Plugin) PluginResponse {
	resp := PluginResponse{
		ID:                  p.Name,
		DisplayName:         p.Spec.DisplayName,
		Version:             p.Status.Version,
		ServiceName:         p.Spec.Service.Name,
		ServicePort:         p.Spec.Service.Port,
		ImageRepository:     p.Spec.Image.Repository,
		ImageDigest:         p.Spec.Image.Digest,
		Disabled:            p.Spec.Disabled,
		DeclaredPermissions: p.Status.DeclaredPermissions,
		ApprovedPermissions: p.Spec.ApprovedPermissions,
		GroupAccess:         p.Spec.GroupAccess,
		VerifiedEndpoints:   p.Status.VerifiedEndpoints,
	}
	// Absent condition means "not ready, no reason yet" rather than a lie.
	if c := pluginReadyCondition(p); c != nil {
		resp.Ready = c.Status == metav1.ConditionTrue
		resp.Reason = c.Reason
		resp.Message = c.Message
	}
	// Map signature status to the response struct.
	if p.Status.Signature != nil {
		resp.Signature = &PluginSignatureResponse{
			Verified:  p.Status.Signature.Verified,
			Signer:    p.Status.Signature.Signer,
			CheckedAt: &p.Status.Signature.CheckedAt,
			Message:   p.Status.Signature.Message,
		}
	}
	if p.Status.Manifest != nil && len(p.Status.Manifest.Raw) > 0 {
		resp.Manifest = json.RawMessage(p.Status.Manifest.Raw)
	}
	return resp
}

func toPluginPublicResponse(p connectorv1alpha1.Plugin) PluginPublicResponse {
	resp := PluginPublicResponse{
		ID:          p.Name,
		DisplayName: p.Spec.DisplayName,
		Version:     p.Status.Version,
	}
	if p.Status.Manifest != nil && len(p.Status.Manifest.Raw) > 0 {
		resp.Manifest = json.RawMessage(p.Status.Manifest.Raw)
	}
	return resp
}
