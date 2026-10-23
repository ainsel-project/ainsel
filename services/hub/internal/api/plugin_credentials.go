package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/auditevents"
	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	"github.com/ainsel-project/ainsel/services/hub/internal/plugincreds"
	connectorv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
)

// pluginCallerKey is the context key for PluginCaller
type pluginCallerKey struct{}

// statusCaptureWriter wraps an http.ResponseWriter to capture the status code
type statusCaptureWriter struct {
	http.ResponseWriter
	status int
}

// WriteHeader captures the status code
func (w *statusCaptureWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// PluginCaller describes the authenticated plugin caller and their effective permissions/groups.
type PluginCaller struct {
	PluginID    string
	Kind        string
	Sub         string
	Permissions []string
	Groups      []plugincreds.GroupGrant
	InvocationID string
}

// PluginCallerFrom retrieves the PluginCaller from the request context
func PluginCallerFrom(ctx context.Context) (*PluginCaller, bool) {
	pc, ok := ctx.Value(pluginCallerKey{}).(*PluginCaller)
	return pc, ok
}

// SetPluginCredentialIssuer wires the plugin credential issuer for minting and verifying tokens.
func (s *Server) SetPluginCredentialIssuer(issuer *plugincreds.Issuer) {
	s.pluginCreds = issuer
}

// PluginCredentialMiddleware wraps an existing auth chain. Requests with a bearer
// token that looks like a plugin credential are verified and handled here; all
// others pass through to the fallback chain unchanged. The middleware enforces:
// - Token verification (401 if invalid)
// - Plugin CR exists and is Ready (401 if missing/disabled/not ready, 503 on other errors)
// - Effective permissions = token permissions ∩ current spec.approvedPermissions
// - Effective groups (for plugin kind) = token groups ∩ current spec.groupAccess
// - Route gate (403 if not available or permission lacks)
// - User kind only: disabledGuard
// - Audit write requests
func (s *Server) PluginCredentialMiddleware(disabledGuard, fallback func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check for plugin credential bearer token
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
				fallback(next).ServeHTTP(w, r)
				return
			}

			token := strings.TrimPrefix(authHeader, "Bearer ")
			if !plugincreds.LooksLike(token) {
				fallback(next).ServeHTTP(w, r)
				return
			}

			// Plugin credential: handle it here and never reach fallback
			s.handlePluginCredential(w, r, next, disabledGuard, token)
		})
	}
}

// handlePluginCredential verifies and processes a plugin credential request
func (s *Server) handlePluginCredential(w http.ResponseWriter, r *http.Request, next http.Handler, disabledGuard func(http.Handler) http.Handler, token string) {
	// Verify token
	if s.pluginCreds == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	claims, err := s.pluginCreds.Verify(token)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Load Plugin CR
	ctx := r.Context()
	var plugin connectorv1alpha1.Plugin
	pluginKey := types.NamespacedName{Name: claims.PluginID, Namespace: s.ns}
	if err := s.client.Get(ctx, pluginKey, &plugin); err != nil {
		// Plugin not found, disabled, or other issue
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Check: disabled
	if plugin.Spec.Disabled {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Check: Ready condition
	if !meta.IsStatusConditionTrue(plugin.Status.Conditions, connectorv1alpha1.PluginConditionReady) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Compute effective permissions: token ∩ current approvals
	effectivePerms := intersect(claims.Permissions, plugin.Spec.ApprovedPermissions)

	// Compute effective groups (for plugin kind)
	var effectiveGroups []plugincreds.GroupGrant
	if claims.Kind == plugincreds.KindPlugin {
		effectiveGroups = intersectGroups(claims.Groups, plugin.Spec.GroupAccess)
	} else if claims.Kind == plugincreds.KindAgent {
		// Agent: filter groups to only those in spec.groupAccess
		effectiveGroups = intersectGroups(claims.Groups, plugin.Spec.GroupAccess)
	} else {
		effectiveGroups = claims.Groups
	}

	// Set up request context
	ctx = r.Context()

	// Create oidc.User for context
	oidcUser := &oidc.User{
		Sub:      claims.Sub,
		Username: claims.Username,
		Roles:    []string{},
	}
	if claims.Kind == plugincreds.KindUser && claims.Admin {
		oidcUser.Roles = []string{"admin"}
	}
	ctx = oidc.ContextWithUser(ctx, oidcUser)
	ctx = oidc.ContextWithToken(ctx, token)

	// Set PluginCaller
	pc := &PluginCaller{
		PluginID:     claims.PluginID,
		Kind:         claims.Kind,
		Sub:          claims.Sub,
		Permissions:  effectivePerms,
		Groups:       effectiveGroups,
		InvocationID: claims.InvocationID,
	}
	ctx = context.WithValue(ctx, pluginCallerKey{}, pc)

	// For agent and plugin kinds, set the principal (no groups for agent, groups from spec for plugin/agent)
	if claims.Kind == plugincreds.KindAgent || claims.Kind == plugincreds.KindPlugin {
		groups := make(map[string]authz.GroupRole)
		for _, g := range effectiveGroups {
			groups[g.ID] = authz.GroupRole(g.Role)
		}
		principal := &authz.Principal{
			Sub:    claims.Sub,
			Kind:   claims.Kind,
			Groups: groups,
		}
		ctx = authz.WithPrincipal(ctx, principal)
	}

	r = r.WithContext(ctx)

	// Route gate: check if this route is available
	perm, always, ok := RoutePermission(r.Method, r.URL.Path)
	if !ok {
		writeError(w, http.StatusForbidden, "route not available to plugin credentials")
		return
	}

	// Check permission (unless always allowed)
	if !always && !contains(effectivePerms, perm) {
		writeError(w, http.StatusForbidden, fmt.Sprintf("plugin lacks permission %s", perm))
		return
	}

	// Wrap response writer to capture status code for audit logging
	wrappedWriter := &statusCaptureWriter{ResponseWriter: w}

	// For user kind, apply disabled guard
	if claims.Kind == plugincreds.KindUser {
		disabledGuard(next).ServeHTTP(wrappedWriter, r)
	} else {
		// For agent/plugin kinds, call next directly (they skip passwordChangeGate anyway)
		next.ServeHTTP(wrappedWriter, r)
	}

	// Audit non-GET/HEAD writes
	if s.authEvents != nil && !isReadMethod(r.Method) {
		target := fmt.Sprintf("%s %s %s", claims.PluginID, r.Method, r.URL.Path)
		outcome := auditevents.OutcomeSuccess
		if wrappedWriter.status >= 400 {
			outcome = auditevents.OutcomeFailure
		}
		s.recordAuthEvent(r, auditevents.TypePluginAPIWrite, claims.Sub, target, outcome)
	}
}

// MintUserPluginCredential mints a user-kind plugin credential.
// Returns token, expiration, error. Checks that the plugin is enabled and Ready.
func (s *Server) MintUserPluginCredential(ctx context.Context, pluginID string, u *authz.User) (string, time.Time, error) {
	if s.pluginCreds == nil {
		return "", time.Time{}, plugincreds.ErrDisabled
	}

	// Load and validate plugin
	var plugin connectorv1alpha1.Plugin
	if err := s.client.Get(ctx, types.NamespacedName{Name: pluginID, Namespace: s.ns}, &plugin); err != nil {
		return "", time.Time{}, plugincreds.ErrDisabled
	}
	if plugin.Spec.Disabled || !meta.IsStatusConditionTrue(plugin.Status.Conditions, connectorv1alpha1.PluginConditionReady) {
		return "", time.Time{}, errors.New("plugin not available")
	}

	// Look up current user state to get admin flag
	admin := false
	if u != nil {
		currentUser, err := s.authzStore.GetUser(ctx, u.ID)
		if err == nil && currentUser != nil {
			admin = currentUser.IsAdmin
		}
	}

	claims := plugincreds.Claims{
		PluginID:    pluginID,
		Kind:        plugincreds.KindUser,
		Sub:         u.ID,
		Username:    u.Username,
		Admin:       admin,
		Permissions: plugin.Spec.ApprovedPermissions,
		Groups:      nil,
	}

	return s.pluginCreds.Issue(claims)
}

// MintAgentPluginCredential mints an agent-kind plugin credential.
// Returns token, expiration, error. Checks that the plugin is enabled and Ready.
func (s *Server) MintAgentPluginCredential(ctx context.Context, pluginID, agentName, invocationID string) (string, time.Time, error) {
	if s.pluginCreds == nil {
		return "", time.Time{}, plugincreds.ErrDisabled
	}

	// Load and validate plugin
	var plugin connectorv1alpha1.Plugin
	if err := s.client.Get(ctx, types.NamespacedName{Name: pluginID, Namespace: s.ns}, &plugin); err != nil {
		return "", time.Time{}, plugincreds.ErrDisabled
	}
	if plugin.Spec.Disabled || !meta.IsStatusConditionTrue(plugin.Status.Conditions, connectorv1alpha1.PluginConditionReady) {
		return "", time.Time{}, errors.New("plugin not available")
	}

	// Get agent's owning group
	var groups []plugincreds.GroupGrant
	rg, err := s.authzStore.GetResourceGroup(ctx, "agent", agentName)
	if err == nil && rg != nil && rg.GroupID != "" {
		groups = []plugincreds.GroupGrant{{ID: rg.GroupID, Role: "reader"}}
	}

	claims := plugincreds.Claims{
		PluginID:     pluginID,
		Kind:         plugincreds.KindAgent,
		Sub:          "agent:" + agentName,
		Permissions:  plugin.Spec.ApprovedPermissions,
		Groups:       groups,
		InvocationID: invocationID,
	}

	return s.pluginCreds.Issue(claims)
}

// MintPluginBackgroundCredential mints a plugin-kind plugin credential for background operations.
// Returns token, expiration, error. Checks that the plugin is enabled and Ready.
func (s *Server) MintPluginBackgroundCredential(ctx context.Context, pluginID string) (string, time.Time, error) {
	if s.pluginCreds == nil {
		return "", time.Time{}, plugincreds.ErrDisabled
	}

	// Load and validate plugin
	var plugin connectorv1alpha1.Plugin
	if err := s.client.Get(ctx, types.NamespacedName{Name: pluginID, Namespace: s.ns}, &plugin); err != nil {
		return "", time.Time{}, plugincreds.ErrDisabled
	}
	if plugin.Spec.Disabled || !meta.IsStatusConditionTrue(plugin.Status.Conditions, connectorv1alpha1.PluginConditionReady) {
		return "", time.Time{}, errors.New("plugin not available")
	}

	// Build groups from spec.groupAccess with writer role
	var groups []plugincreds.GroupGrant
	for _, gid := range plugin.Spec.GroupAccess {
		groups = append(groups, plugincreds.GroupGrant{ID: gid, Role: "writer"})
	}

	claims := plugincreds.Claims{
		PluginID:    pluginID,
		Kind:        plugincreds.KindPlugin,
		Sub:         "plugin:" + pluginID,
		Permissions: plugin.Spec.ApprovedPermissions,
		Groups:      groups,
	}

	return s.pluginCreds.Issue(claims)
}

// Helper functions

// intersect returns the intersection of two string slices
func intersect(a, b []string) []string {
	var result []string
	for _, s := range a {
		for _, t := range b {
			if s == t {
				result = append(result, s)
				break
			}
		}
	}
	return result
}

// intersectGroups returns groups that appear in both the token groups and the allowed list
func intersectGroups(tokenGroups []plugincreds.GroupGrant, allowedGroupIDs []string) []plugincreds.GroupGrant {
	var result []plugincreds.GroupGrant
	for _, tg := range tokenGroups {
		for _, ag := range allowedGroupIDs {
			if tg.ID == ag {
				result = append(result, tg)
				break
			}
		}
	}
	return result
}

// contains checks if a string is in a slice
func contains(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// isReadMethod returns true for GET/HEAD requests
func isReadMethod(method string) bool {
	return method == "GET" || method == "HEAD"
}
