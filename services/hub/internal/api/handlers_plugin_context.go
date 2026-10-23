package api

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

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
	pc, ok := PluginCallerFrom(ctx)

	// Check if this is a plugin caller or a regular user
	if ok && pc.Kind != "" {
		// Plugin credentials
		resp := map[string]interface{}{
			"pluginId":    pc.PluginID,
			"kind":        pc.Kind,
			"sub":         pc.Sub,
			"username":    "",
			"isAdmin":     false,
			"permissions": pc.Permissions,
			"groups":      pc.Groups,
			"invocationId": pc.InvocationID,
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Regular user
	u, ok := oidc.FromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	isAdmin := false
	if s.authzChecker != nil {
		isAdmin, _ = s.authzChecker.IsAdmin(ctx, u.Sub)
	}

	// Get user's groups
	var groups []map[string]interface{}
	if s.authzStore != nil {
		userGroups, err := s.authzStore.UserGroupRoles(ctx, u.Sub)
		if err == nil && userGroups != nil {
			for groupID, role := range userGroups {
				g, _ := s.authzStore.GetGroup(ctx, groupID)
				groupName := ""
				if g != nil {
					groupName = g.Name
				}
				groups = append(groups, map[string]interface{}{
					"id":   groupID,
					"name": groupName,
					"role": string(role),
				})
			}
		}
	}

	resp := map[string]interface{}{
		"pluginId":    "",
		"kind":        "user",
		"sub":         u.Sub,
		"username":    u.Username,
		"isAdmin":     isAdmin,
		"permissions": nil,
		"groups":      groups,
		"invocationId": "",
	}
	writeJSON(w, http.StatusOK, resp)
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

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate resourceType
	resourceTypeMap := map[string]string{
		"agent":         "agents:read",
		"agent-image":   "agent-images:read",
		"connector":     "connectors:read",
		"trigger":       "triggers:read",
		"mcp-server":    "mcp-servers:read",
		"persona":       "personas:read",
		"skill":         "skills:read",
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
	var visible []string
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
		"expiresAt": exp.Format("2006-01-02T15:04:05Z07:00"),
	})
}
