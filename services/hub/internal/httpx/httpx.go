// Package httpx holds the small HTTP helpers the hub's handlers repeat: the
// JSON response envelope and bearer-token extraction.
//
// It depends on the standard library plus slog only, so any hub package can
// use it without pulling in API, authz or database types.
package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
)

// ErrorBody is the wire shape of an API error: {"error": "..."}. Hub plugin
// clients parse this field, so it is a contract — do not rename or extend it.
type ErrorBody struct {
	Error string `json:"error"`
}

// WriteJSON writes v as a JSON response with the given status. A failed
// encode is logged, not returned: the status line is already on the wire.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to encode JSON response", "error", err)
	}
}

// WriteError answers with the {"error": msg} envelope the hub's clients
// expect.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, ErrorBody{Error: msg})
}

// BearerToken returns the token from an Authorization header value, or ""
// when the header is absent, shorter than the prefix, or not a Bearer scheme
// (the scheme match is case-insensitive).
func BearerToken(header string) string {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}
