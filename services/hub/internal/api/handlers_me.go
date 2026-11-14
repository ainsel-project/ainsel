package api

import (
	"net/http"

	"github.com/ainsel-project/ainsel/services/hub/internal/httpx"
)

// handleMe returns the authenticated user from Authelia-forwarded headers.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	user := r.Header.Get("Remote-User")
	if user == "" {
		user = "anonymous"
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"username": user,
		"name":     r.Header.Get("Remote-Name"),
		"email":    r.Header.Get("Remote-Email"),
		"groups":   r.Header.Get("Remote-Groups"),
	})
}
