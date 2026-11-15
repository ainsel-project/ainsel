package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	"github.com/ainsel-project/ainsel/services/hub/internal/httpx"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"
)

func (s *Server) handleGroups(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		groups, err := s.authzStore.ListGroups(r.Context())
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if groups == nil {
			groups = []authz.Group{}
		}
		httpx.WriteJSON(w, http.StatusOK, groups)
	case http.MethodPost:
		// Any authenticated user can create a group and becomes its owner.
		u, ok := oidc.FromContext(r.Context())
		if !ok {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		var body struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if body.Name == "" {
			httpx.WriteError(w, http.StatusBadRequest, "name is required")
			return
		}
		g, err := s.authzStore.CreateGroup(r.Context(), body.Name, body.Description)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Creator becomes owner.
		if err := s.authzStore.AddGroupMember(r.Context(), g.ID, u.Sub, authz.RoleOwner); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httpx.WriteJSON(w, http.StatusCreated, g)
	default:
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleGroup(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/groups/")
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	rest := ""
	if len(parts) > 1 {
		rest = parts[1]
	}

	switch {
	case rest == "" || rest == "/":
		s.handleGroupRoot(w, r, id)
	case rest == "members" || strings.HasPrefix(rest, "members"):
		s.handleGroupMembers(w, r, id, strings.TrimPrefix(rest, "members"))
	case rest == "resources":
		s.handleGroupResources(w, r, id)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleGroupRoot(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		// Any authenticated user can view a group and its members.
		g, err := s.authzStore.GetGroup(r.Context(), id)
		if writeAuthzError(w, err, "group") {
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		members, err := s.authzStore.ListGroupMembers(r.Context(), id)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if members == nil {
			members = []authz.MemberWithUser{}
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"group":   g,
			"members": members,
		})
	case http.MethodPatch:
		if !s.requireGroupOwner(w, r, id) {
			return
		}
		var body struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		g, err := s.authzStore.UpdateGroup(r.Context(), id, body.Name, body.Description)
		if writeAuthzError(w, err, "group") {
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httpx.WriteJSON(w, http.StatusOK, g)
	case http.MethodDelete:
		if !s.requireGroupOwner(w, r, id) {
			return
		}
		if err := s.authzStore.DeleteGroup(r.Context(), id); err != nil {
			if writeAuthzError(w, err, "group") {
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleGroupMembers(w http.ResponseWriter, r *http.Request, groupID, rest string) {
	if !s.requireGroupOwner(w, r, groupID) {
		return
	}
	rest = strings.TrimPrefix(rest, "/")

	switch {
	case rest == "" && r.Method == http.MethodPost:
		var body struct {
			UserIDs []string        `json:"userIds"`
			Role    authz.GroupRole `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if body.Role == "" {
			body.Role = authz.RoleReader
		}
		if body.Role != authz.RoleReader && body.Role != authz.RoleWriter && body.Role != authz.RoleOwner {
			httpx.WriteError(w, http.StatusBadRequest, "role must be reader, writer, or owner")
			return
		}
		for _, uid := range body.UserIDs {
			if err := s.authzStore.AddGroupMember(r.Context(), groupID, uid, body.Role); err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	case rest != "" && r.Method == http.MethodDelete:
		userID := rest
		if err := s.authzStore.RemoveGroupMember(r.Context(), groupID, userID); err != nil {
			if writeAuthzError(w, err, "member") {
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
