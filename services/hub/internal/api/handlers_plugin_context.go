package api

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/plugincreds"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"
)

// handlePluginContext returns the authenticated caller's context (kind, permissions, groups)
// GET /api/v1/plugin-context
func (s *Server) handlePluginContext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ctx := r.Context()
	resp := pluginContextResponse{Kind: plugincreds.KindUser, Groups: []pluginContextGroup{}}

	if pc, ok := PluginCallerFrom(ctx); ok {
		resp.PluginID = pc.PluginID
		resp.Kind = pc.Kind
		resp.Sub = pc.Sub
		resp.Username = pc.Username
		resp.Permissions = pc.Permissions
		if resp.Permissions == nil {
			resp.Permissions = []string{}
		}
		resp.InvocationID = pc.InvocationID
		if pc.Kind == plugincreds.KindUser {
			resp.IsAdmin = s.callerIsAdmin(r)
		}
		for _, g := range pc.Groups {
			resp.Groups = append(resp.Groups, pluginContextGroup{ID: g.ID, Name: s.groupName(r, g.ID), Role: g.Role})
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Regular user (session or ainsel_ token)
	u, ok := oidc.FromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	resp, err := s.userPluginContext(r, u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// pluginContextResponse is the body of GET /api/v1/plugin-context.
// Permissions is null for callers that are not plugin credentials.
type pluginContextResponse struct {
	PluginID     string               `json:"pluginId"`
	Kind         string               `json:"kind"`
	Sub          string               `json:"sub"`
	Username     string               `json:"username"`
	IsAdmin      bool                 `json:"isAdmin"`
	Permissions  []string             `json:"permissions"`
	Groups       []pluginContextGroup `json:"groups"`
	InvocationID string               `json:"invocationId"`
}

type pluginContextGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// groupName resolves a group's display name; unknown groups get "".
func (s *Server) groupName(r *http.Request, id string) string {
	if s.authzStore == nil {
		return ""
	}
	g, err := s.authzStore.GetGroup(r.Context(), id)
	if err != nil || g == nil {
		return ""
	}
	return g.Name
}

// handlePluginContextVisible checks which resources are visible to the caller
// POST /api/v1/plugin-context/visible
func (s *Server) handlePluginContextVisible(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		ResourceType string   `json:"resourceType"`
		Names        []string `json:"names"`
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate resourceType
	resourceTypeMap := map[string]string{
		"agent":       "agents:read",
		"agent-image": "agent-images:read",
		"connector":   "connectors:read",
		"trigger":     "triggers:read",
		"mcp-server":  "mcp-servers:read",
		"persona":     "personas:read",
		"skill":       "skills:read",
	}

	requiredPerm, ok := resourceTypeMap[req.ResourceType]
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown resourceType")
		return
	}

	// Validate names length
	if len(req.Names) < 1 || len(req.Names) > 500 {
		writeError(w, http.StatusBadRequest, "names length must be 1-500")
		return
	}

	ctx := r.Context()
	pc, isPlugin := PluginCallerFrom(ctx)

	// For plugin credentials, check permission
	if isPlugin && pc.Kind != "" {
		if !contains(pc.Permissions, requiredPerm) {
			writeError(w, http.StatusForbidden, fmt.Sprintf("plugin lacks permission %s", requiredPerm))
			return
		}
	}

	// Deduplicate and check visibility
	visible := []string{}
	seen := make(map[string]bool)

	for _, name := range req.Names {
		if seen[name] {
			continue
		}
		seen[name] = true

		// Check if the user can read this resource
		canRead := true
		if s.authzChecker != nil {
			var sub string
			if isPlugin && pc.Kind != "" {
				sub = pc.Sub
			} else if u, ok := oidc.FromContext(ctx); ok {
				sub = u.Sub
			} else {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}

			ok, err := s.authzChecker.CanRead(ctx, sub, req.ResourceType, name)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			canRead = ok
		}
		// else: no checker (dev mode) → all visible

		if canRead {
			visible = append(visible, name)
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"visible": visible,
	})
}

// handleInternalPluginCredentials issues a background credential token for a plugin
// POST /api/internal/plugins/<id>/credentials
// Requires the request to come from a verified endpoint
func (s *Server) handleInternalPluginCredentials(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Parse plugin ID from path: /api/internal/plugins/<id>/credentials
	path := strings.TrimPrefix(r.URL.Path, "/api/internal/plugins/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[1] != "credentials" || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	pluginID := parts[0]

	// Check plugin is available and ready
	ctx := r.Context()
	plugin, err := s.availablePlugin(ctx, pluginID)
	if err != nil {
		if err == errPluginUnavailable {
			writeError(w, http.StatusNotFound, "not found")
		} else {
			writeError(w, http.StatusInternalServerError, "internal error")
		}
		return
	}

	// Check: issuer is configured
	if s.pluginCreds == nil {
		writeError(w, http.StatusServiceUnavailable, "service unavailable")
		return
	}

	// Verify request source IP matches a verified endpoint
	remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	foundMatch := false
	for _, endpoint := range plugin.Status.VerifiedEndpoints {
		endpointHost, _, err := net.SplitHostPort(endpoint)
		if err == nil && endpointHost == remoteHost {
			foundMatch = true
			break
		}
	}

	if !foundMatch {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	// Mint the credential
	tok, exp, err := s.MintPluginBackgroundCredential(ctx, pluginID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "service unavailable")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":     tok,
		"expiresAt": exp.UTC().Format(time.RFC3339),
	})
}
