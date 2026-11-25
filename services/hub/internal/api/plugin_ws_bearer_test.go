package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func wsRequest(path string, protocols ...string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	for _, p := range protocols {
		r.Header.Add("Sec-WebSocket-Protocol", p)
	}
	return r
}

func TestPromoteWebSocketBearer(t *testing.T) {
	const apiPath = "/api/v1/plugins/jane/api/voice"
	tests := []struct {
		name      string
		req       func() *http.Request
		wantAuth  string
		wantProto []string
	}{
		{
			name:      "token moves to Authorization and the rest of the offer stays",
			req:       func() *http.Request { return wsRequest(apiPath, "ainsel.bearer.tok.en-1_x, voice.v1") },
			wantAuth:  "Bearer tok.en-1_x",
			wantProto: []string{"voice.v1"},
		},
		{
			name:      "offer made only of the token leaves no header behind",
			req:       func() *http.Request { return wsRequest(apiPath, "ainsel.bearer.tok") },
			wantAuth:  "Bearer tok",
			wantProto: nil,
		},
		{
			name:      "token on a second header line is found",
			req:       func() *http.Request { return wsRequest(apiPath, "voice.v1", "ainsel.bearer.tok") },
			wantAuth:  "Bearer tok",
			wantProto: []string{"voice.v1"},
		},
		{
			name: "an Authorization header already present wins, the token entry is still dropped",
			req: func() *http.Request {
				r := wsRequest(apiPath, "ainsel.bearer.tok")
				r.Header.Set("Authorization", "Bearer real")
				return r
			},
			wantAuth:  "Bearer real",
			wantProto: nil,
		},
		{
			name:      "empty token is ignored",
			req:       func() *http.Request { return wsRequest(apiPath, "ainsel.bearer.") },
			wantAuth:  "",
			wantProto: nil,
		},
		{
			name: "a plain GET is untouched",
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, apiPath, nil)
				r.Header.Set("Sec-WebSocket-Protocol", "ainsel.bearer.tok")
				return r
			},
			wantAuth:  "",
			wantProto: []string{"ainsel.bearer.tok"},
		},
		{
			name:      "the ui surface is untouched",
			req:       func() *http.Request { return wsRequest("/api/v1/plugins/jane/ui/x", "ainsel.bearer.tok") },
			wantAuth:  "",
			wantProto: []string{"ainsel.bearer.tok"},
		},
		{
			name:      "the mcp surface is untouched",
			req:       func() *http.Request { return wsRequest("/api/v1/plugins/jane/mcp", "ainsel.bearer.tok") },
			wantAuth:  "",
			wantProto: []string{"ainsel.bearer.tok"},
		},
		{
			name:      "hub's own API is untouched",
			req:       func() *http.Request { return wsRequest("/api/v1/agents", "ainsel.bearer.tok") },
			wantAuth:  "",
			wantProto: []string{"ainsel.bearer.tok"},
		},
		{
			name:      "a token with a space or comma cannot smuggle a second entry",
			req:       func() *http.Request { return wsRequest(apiPath, "ainsel.bearer.a b") },
			wantAuth:  "",
			wantProto: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.req()
			promoteWebSocketBearer(r)
			if got := r.Header.Get("Authorization"); got != tc.wantAuth {
				t.Errorf("Authorization = %q, want %q", got, tc.wantAuth)
			}
			got := r.Header.Values("Sec-WebSocket-Protocol")
			if len(got) != len(tc.wantProto) {
				t.Fatalf("Sec-WebSocket-Protocol = %q, want %q", got, tc.wantProto)
			}
			for i := range got {
				if got[i] != tc.wantProto[i] {
					t.Errorf("Sec-WebSocket-Protocol[%d] = %q, want %q", i, got[i], tc.wantProto[i])
				}
			}
		})
	}
}

// The promotion has to happen before the auth chain reads the request, and the
// token must not travel on to the plugin.
func TestServeHTTP_WebSocketBearerReachesAuthChain(t *testing.T) {
	s := testServer(t)
	registerPluginRoutes(s)
	var seenAuth string
	var seenProto []string
	s.authMW = func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seenAuth = r.Header.Get("Authorization")
			seenProto = r.Header.Values("Sec-WebSocket-Protocol")
			w.WriteHeader(http.StatusTeapot)
		})
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, wsRequest("/api/v1/plugins/jane/api/voice", "ainsel.bearer.abc"))
	if seenAuth != "Bearer abc" {
		t.Errorf("auth chain saw Authorization %q, want %q", seenAuth, "Bearer abc")
	}
	if len(seenProto) != 0 {
		t.Errorf("auth chain still sees the token entry: %q", seenProto)
	}
}
