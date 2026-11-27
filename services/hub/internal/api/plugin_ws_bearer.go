package api

import (
	"net/http"
	"strings"
)

// wsBearerPrefix marks the Sec-WebSocket-Protocol entry that carries a bearer
// token: "ainsel.bearer.<token>".
const wsBearerPrefix = "ainsel.bearer." // #nosec G101 -- a protocol label, not a credential

// promoteWebSocketBearer lets a browser authenticate a WebSocket to a plugin's
// api surface. Browsers cannot set an Authorization header on a WebSocket, and
// the surface requires one, so the client offers the token as a subprotocol
// instead. This moves it into Authorization, where the auth chain reads it, and
// removes the entry so the token is not forwarded to the plugin or echoed back.
//
// It acts only on WebSocket upgrades of /api/v1/plugins/<id>/api/..., and an
// Authorization header that is already present wins.
func promoteWebSocketBearer(r *http.Request) {
	if !isPluginAPIWebSocket(r) {
		return
	}
	var token string
	var kept []string
	for _, line := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, entry := range strings.Split(line, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			if !strings.HasPrefix(entry, wsBearerPrefix) {
				kept = append(kept, entry)
				continue
			}
			if t := strings.TrimPrefix(entry, wsBearerPrefix); token == "" && isHeaderToken(t) {
				token = t
			}
		}
	}
	r.Header.Del("Sec-WebSocket-Protocol")
	if len(kept) > 0 {
		r.Header.Set("Sec-WebSocket-Protocol", strings.Join(kept, ", "))
	}
	if token != "" && r.Header.Get("Authorization") == "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
}

func isPluginAPIWebSocket(r *http.Request) bool {
	if r.Method != http.MethodGet || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	connection := false
	for _, v := range strings.Split(r.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(v), "upgrade") {
			connection = true
		}
	}
	if !connection {
		return false
	}
	rest, ok := strings.CutPrefix(r.URL.EscapedPath(), "/api/v1/plugins/")
	if !ok {
		return false
	}
	parts := strings.SplitN(rest, "/", 3)
	return len(parts) >= 2 && parts[1] == "api"
}

// isHeaderToken reports whether s is a non-empty RFC 7230 token, the only
// characters a subprotocol may contain. JWTs (base64url and dots) qualify.
func isHeaderToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}
