package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"
)

// These tests are the regression net for the routing refactor (issue #265).
// They cover three things the refactor depends on:
//
//  1. the router's own 404/405 answers stay inside the published JSON error
//     contract, without breaking the responses that are not errors (Hijack for
//     /api/v1/ws, Flush for streaming);
//  2. the route inventory — a snapshot of every registered pattern plus the
//     checks that keep the hand-maintained path knowledge in plugin_routes.go
//     from drifting away from the router;
//  3. the internal-token invariant: every /api/internal/* route that is opened
//     by X-Internal-Token answers 401/503 without it, so no new internal route
//     can ship unauthenticated.

// routeConversionComplete flips to true with the last route group of issue #265.
// The end-state invariants in TestRouteInventory are skipped until then, so the
// conversion can land group by group while the snapshot check applies from the
// first commit.
const routeConversionComplete = false

// stubPersona registers the persona route group; no handler under test reaches
// the service, so the embedded nil interface is enough.
type stubPersona struct{ PersonaService }

// fullRouteServer builds a server with every route group registered, the way
// production wiring does, so the inventory sees the whole surface.
func fullRouteServer(t *testing.T) *Server {
	t.Helper()
	// New takes the concrete persona service, so the persona group is mounted
	// the way the wiring does it: on the server's own registration surface.
	s := New(newFakeClient(), "test-ns", ConnectorConfig{}, nil, nil, nil, nil, newMockSkillService(), &Config{})
	RegisterPersonaRoutes(s.routes(), &stubPersona{}, nil, nil)
	// The channel routes are mounted by SetChannelService in wiring; the route
	// registration does not touch the service, so nil is enough to mount them.
	s.SetChannelService(nil, nil)
	return s
}

// goldenRoutePatterns is the route inventory of New(...) plus the routes wired
// by SetChannelService, RegisterPersonaRoutes and RegisterSkillRoutes. It is a
// deliberate diff: converting a route group changes this list, and the diff is
// the review of that group.
var goldenRoutePatterns = []string{
	"/api/internal/agents/",
	"/api/internal/chat/sessions",
	"/api/internal/chat/sessions/",
	"/api/internal/events",
	"/api/internal/plugin-tools",
	"/api/internal/plugins/",
	"/api/internal/skills/mcp",
	"/api/internal/task-logs",
	"/api/internal/task-messages",
	"/api/internal/user-tokens/validate",
	"/api/v1/agent-images",
	"/api/v1/agent-images/",
	"/api/v1/agents",
	"/api/v1/agents/",
	"/api/v1/audit/auth-events",
	"/api/v1/auth/login",
	"/api/v1/auth/logout",
	"/api/v1/auth/me",
	"/api/v1/auth/password",
	"/api/v1/auth/password-policy",
	"/api/v1/auth/sign-out-all",
	"/api/v1/channel-subscriptions",
	"/api/v1/channels",
	"/api/v1/channels/",
	"/api/v1/chat/sessions",
	"/api/v1/chat/sessions/",
	"/api/v1/connectors",
	"/api/v1/connectors/",
	"/api/v1/cron-triggers",
	"/api/v1/cron-triggers/",
	"/api/v1/errors",
	"/api/v1/events",
	"/api/v1/events/",
	"/api/v1/groups",
	"/api/v1/groups/",
	"/api/v1/invocations",
	"/api/v1/invocations/",
	"/api/v1/llm/check",
	"/api/v1/mcp-servers",
	"/api/v1/mcp-servers/",
	"/api/v1/me",
	"/api/v1/me/resources",
	"/api/v1/observability/conversations",
	"/api/v1/observability/logs",
	"/api/v1/observability/metrics/agents",
	"/api/v1/observability/metrics/query",
	"/api/v1/observability/metrics/summary",
	"/api/v1/observability/metrics/timeseries",
	"/api/v1/observability/metrics/tokens/by-event",
	"/api/v1/observability/metrics/tokens/by-subject",
	"/api/v1/observability/metrics/tokens/summary",
	"/api/v1/observability/metrics/tokens/timeseries",
	"/api/v1/personas",
	"/api/v1/personas/",
	"/api/v1/platform/health",
	"/api/v1/plugin-context",
	"/api/v1/plugin-context/visible",
	"/api/v1/plugin-tools/call",
	"/api/v1/plugins",
	"/api/v1/plugins/",
	"/api/v1/queue/info",
	"/api/v1/queue/recent",
	"/api/v1/skills",
	"/api/v1/skills/",
	"/api/v1/stats",
	"/api/v1/tokens",
	"/api/v1/triggers",
	"/api/v1/triggers/",
	"/api/v1/user-tokens",
	"/api/v1/user-tokens/",
	"/api/v1/users",
	"/api/v1/users/",
	"/api/v1/ws",
	"/health",
}

// TestRouteInventory pins the registered route surface and the rules issue #265
// sets for it. The snapshot is the reviewable diff for each conversion PR; the
// other assertions keep the two hand-maintained path tables — routeRules in
// plugin_routes.go and isAuthExempt in server.go — from drifting away from the
// router without the compiler noticing.
func TestRouteInventory(t *testing.T) {
	s := fullRouteServer(t)
	patterns := s.routePatterns()

	if got, want := strings.Join(patterns, "\n"), strings.Join(goldenRoutePatterns, "\n"); got != want {
		gotSet := map[string]bool{}
		for _, p := range patterns {
			gotSet[p] = true
		}
		wantSet := map[string]bool{}
		for _, p := range goldenRoutePatterns {
			wantSet[p] = true
		}
		var missing, extra []string
		for _, p := range goldenRoutePatterns {
			if !gotSet[p] {
				missing = append(missing, p)
			}
		}
		for _, p := range patterns {
			if !wantSet[p] {
				extra = append(extra, p)
			}
		}
		t.Errorf("route inventory drifted\nmissing (registered pattern was removed or renamed): %v\nextra (new registered pattern — update the snapshot deliberately): %v", missing, extra)
	}

	// Every routeRules prefix must still describe at least one registered
	// route. A prefix that matches nothing is dead path knowledge: the plugin
	// credential table and the router have started to disagree.
	for _, rule := range routeRules {
		if matchesRoutePrefix(patterns, rule.prefix) {
			continue
		}
		t.Errorf("routeRules prefix %q matches no registered pattern — plugin credential authz has drifted from the route table", rule.prefix)
	}

	// isAuthExempt keeps its own hand-parsed copy of the plugin-UI path shape.
	// Pin the two ends of it: the UI surface is reachable without a session,
	// and nothing else under a plugin is.
	if isAuthExempt(httptest.NewRequest(http.MethodGet, "/api/v1/plugins/github/ui/app.js", nil)) != true {
		t.Errorf("plugin UI asset must stay auth-exempt")
	}
	if isAuthExempt(httptest.NewRequest(http.MethodGet, "/api/v1/plugins/github", nil)) {
		t.Errorf("the plugin resource itself must not be auth-exempt")
	}

	if !routeConversionComplete {
		return
	}

	// --- end-state invariants for the completed conversion ---

	// Every /api/v1/* route carries a method, except the documented
	// exceptions: /health (kubelet probes any method) and /api/v1/ws (the
	// upgrade is checked in-handler).
	methodlessExceptions := map[string]bool{
		"/health":    true,
		"/api/v1/ws": true,
	}
	// The two dispatched exceptions of Decision 8: plugin-authored internal
	// endpoints and the plugin-UI proxy are not knowable at construction.
	subtreeExceptions := map[string]bool{
		"/api/internal/plugins/": true,
	}
	for _, p := range patterns {
		if !strings.HasPrefix(p, "/api/v1/") {
			continue
		}
		if !strings.Contains(p, " ") && !methodlessExceptions[p] {
			t.Errorf("pattern %q has no method — routes are declared as method patterns (issue #265)", p)
		}
		if strings.HasSuffix(p, "/") && !subtreeExceptions[p] {
			t.Errorf("pattern %q is a method-less subtree registration — it must be replaced by explicit patterns, or the mux stops answering 405", p)
		}
	}
	// A resource id is always {name}: two different placeholders in the same
	// position panic the mux at registration, and the guard helpers read one key.
	for _, p := range patterns {
		for _, seg := range strings.Split(p, "/") {
			if len(seg) > 2 && seg[0] == '{' && seg != "{name}" && !strings.HasPrefix(seg, "{path...") {
				t.Errorf("pattern %q uses placeholder %q — the route table standardizes on {name}", p, seg)
			}
		}
	}
}

// matchesRoutePrefix reports whether any registered pattern covers the given
// literal prefix, in either of the two shapes routeRules matches on: the exact
// path, or a path below it.
func matchesRoutePrefix(patterns []string, prefix string) bool {
	for _, p := range patterns {
		// Strip the method, if the pattern carries one.
		_, path, found := strings.Cut(p, " ")
		if !found {
			path = p
		}
		path = strings.TrimSuffix(path, "/")
		if path == "" {
			continue
		}
		// A pattern with wildcards covers everything its literal prefix does.
		literal := path
		if i := strings.Index(literal, "{"); i >= 0 {
			literal = strings.TrimSuffix(literal[:i], "/")
		}
		if literal == "" {
			continue
		}
		if literal == prefix || strings.HasPrefix(literal, prefix+"/") {
			return true
		}
	}
	return false
}

// --- router-generated error responses ---

// doHandlerJSON issues a request against one handler and decodes the documented
// {"error": "..."} body.
func doHandlerJSON(t *testing.T, h http.Handler, method, path string) (int, http.Header, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	var body struct {
		Error string `json:"error"`
	}
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s %s: body %q is not the documented error JSON: %v", method, path, rec.Body.String(), err)
		}
	}
	return rec.Code, rec.Header(), body.Error
}

// doAPI issues a request through the full stack (rate limiter, auth, mux) and
// decodes the documented {"error": "..."} body.
func doAPI(t *testing.T, s *Server, method, path string, headers map[string]string) (int, http.Header, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	var body struct {
		Error string `json:"error"`
	}
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s %s: body %q is not the documented error JSON: %v", method, path, rec.Body.String(), err)
		}
	}
	return rec.Code, rec.Header(), body.Error
}

// TestRouterMethodNotAllowedKeepsJSONError is the contract the hand-written 405
// branches used to provide: JSON body, and an Allow header listing what the
// path does accept.
func TestRouterMethodNotAllowedKeepsJSONError(t *testing.T) {
	// A mux in the end-state shape of issue #265 — every pattern carries a
	// method — so the mux itself is what rejects the request. On today's
	// method-less registrations the mux never answers 405 at all: the
	// catch-all pattern matches and the handler's own switch does it.
	m := http.NewServeMux()
	m.HandleFunc("GET /api/v1/agents/{name}", func(http.ResponseWriter, *http.Request) {})
	m.HandleFunc("PUT /api/v1/agents/{name}", func(http.ResponseWriter, *http.Request) {})
	m.HandleFunc("GET /api/v1/agents", func(http.ResponseWriter, *http.Request) {})
	m.HandleFunc("POST /api/v1/agents", func(http.ResponseWriter, *http.Request) {})

	code, hdr, msg := doHandlerJSON(t, jsonRouterErrors(m), http.MethodPost, "/api/v1/agents/existing")
	if code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", code)
	}
	if msg != "method not allowed" {
		t.Errorf("expected error %q, got %q", "method not allowed", msg)
	}
	if allow := hdr.Get("Allow"); !strings.Contains(allow, "GET") || !strings.Contains(allow, "PUT") {
		t.Errorf("expected Allow to list the registered methods, got %q", allow)
	}
	if ct := hdr.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected JSON content type, got %q", ct)
	}

	// A collection route answers 405 the same way, and the body is JSON.
	code, _, msg = doHandlerJSON(t, jsonRouterErrors(m), http.MethodDelete, "/api/v1/agents")
	if code != http.StatusMethodNotAllowed || msg != "method not allowed" {
		t.Errorf("expected 405 JSON on the collection route, got %d %q", code, msg)
	}
}

// TestRouterMethodNotAllowedOnTodayShapeStillJSON checks the same contract on
// the current route table, where a 405 is written by the handler itself: the
// wrapper must leave it alone and the body must already be JSON.
func TestRouterMethodNotAllowedOnTodayShapeStillJSON(t *testing.T) {
	s := fullRouteServer(t)
	code, _, msg := doAPI(t, s, http.MethodDelete, "/api/v1/agents", nil)
	if code != http.StatusMethodNotAllowed || msg != "method not allowed" {
		t.Errorf("expected 405 JSON on the collection route, got %d %q", code, msg)
	}
}

// TestRouterNotFoundKeepsJSONError covers the paths no pattern matches, which is
// where the retired /api/v1/agents/{name}/tasks/… aliases now land (Decision 6:
// a stale agent image gets an honest 404 from the router, before any CRUD path).
func TestRouterNotFoundKeepsJSONError(t *testing.T) {
	s := fullRouteServer(t)

	for _, path := range []string{
		"/api/v1/nope",
		"/api/v1/agents/some-agent/next-task",
		"/api/v1/agents/some-agent/tasks/42",
		"/api/v1/agents/some-agent/tasks/42/ack",
	} {
		code, _, msg := doAPI(t, s, http.MethodGet, path, nil)
		if code != http.StatusNotFound {
			t.Errorf("%s: expected 404, got %d", path, code)
		}
		if msg != "not found" {
			t.Errorf("%s: expected error %q, got %q", path, "not found", msg)
		}
	}
}

// TestRouterErrorWrapperLeavesHandlerErrorsAlone pins the boundary of the
// rewrite: only the mux's own answers are replaced. A handler that writes its
// own 404 — plain text, JSON or empty — is passed through byte for byte.
func TestRouterErrorWrapperLeavesHandlerErrorsAlone(t *testing.T) {
	plain := http.NewServeMux()
	plain.HandleFunc("GET /text404", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("handler says no"))
	})
	empty := http.NewServeMux()
	empty.HandleFunc("GET /empty404", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	jsonErr := http.NewServeMux()
	jsonErr.HandleFunc("GET /json404", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "agent not found")
	})

	cases := []struct {
		name     string
		mux      *http.ServeMux
		path     string
		wantBody string
	}{
		{"handler plain text 404", plain, "/text404", "handler says no"},
		{"handler empty 404", empty, "/empty404", ""},
		{"handler json 404", jsonErr, "/json404", `{"error":"agent not found"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			jsonRouterErrors(tc.mux).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("expected 404, got %d", rec.Code)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != tc.wantBody {
				t.Errorf("expected body %q, got %q", tc.wantBody, got)
			}
		})
	}
}

// TestRouterErrorWrapperForwardsHijack is the /api/v1/ws guard: a wrapper that
// hides http.Hijacker from the handler breaks the WebSocket upgrade.
func TestRouterErrorWrapperForwardsHijack(t *testing.T) {
	m := http.NewServeMux()
	hijacked := false
	m.HandleFunc("GET /ws", func(w http.ResponseWriter, _ *http.Request) {
		conn, bw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		hijacked = true
		_, _ = bw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: test\r\n\r\nhi")
		_ = bw.Flush()
		_ = conn.Close()
	})

	srv := httptest.NewServer(jsonRouterErrors(m))
	defer srv.Close()
	// A raw connection, the way a WebSocket client connects: the upgrade only
	// works if the handler still reaches http.Hijacker through the wrapper.
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte("GET /ws HTTP/1.1\r\nHost: x\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if !strings.Contains(status, "101") {
		t.Errorf("expected the 101 upgrade on the hijacked conn, got %q", status)
	}
	if !hijacked {
		t.Error("handler did not reach Hijack through the wrapper")
	}
}

// TestRouterErrorWrapperForwardsFlush pins the streaming case over a real
// connection: the first chunk must reach the client before the handler ends.
func TestRouterErrorWrapperForwardsFlush(t *testing.T) {
	m := http.NewServeMux()
	m.HandleFunc("GET /stream", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("first\n"))
		w.(http.Flusher).Flush()
		// Only after the first chunk has been delivered may the handler finish.
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte("second\n"))
	})

	srv := httptest.NewServer(jsonRouterErrors(m))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/stream")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	br := bufio.NewReader(resp.Body)
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read first line: %v", err)
	}
	if line != "first\n" {
		t.Fatalf("expected the flushed first chunk, got %q", line)
	}
	rest, _ := io.ReadAll(br)
	if string(rest) != "second\n" {
		t.Errorf("expected the trailing chunk, got %q", string(rest))
	}
}

// TestRouterErrorWrapperPassesThroughRedirects covers the 301s the mux emits for
// path normalization — the wrapper must not touch them.
func TestRouterErrorWrapperPassesThroughRedirects(t *testing.T) {
	// A collection subtree pattern redirects the bare collection path to its
	// trailing-slash form. The redirect is an answer, not an error, so the
	// wrapper must let the mux's own status, Location and body through.
	m := http.NewServeMux()
	m.HandleFunc("GET /api/v1/plugins/", func(http.ResponseWriter, *http.Request) {})

	rec := httptest.NewRecorder()
	jsonRouterErrors(m).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/plugins", nil))
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected the mux's 307 for a collection redirect, got %d: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/api/v1/plugins/" {
		t.Errorf("expected Location /api/v1/plugins/, got %q", loc)
	}
	if !strings.Contains(rec.Body.String(), "/api/v1/plugins/") {
		t.Errorf("expected the mux's redirect body untouched, got %q", rec.Body.String())
	}
}

// --- guard helpers ---

// TestReadAndWriteGuards proves the registration-time shape of the guards: the
// handler only runs with the `{name}` path value and a passing check, and a
// denied caller never reaches it.
func TestReadAndWriteGuards(t *testing.T) {
	cases := []struct {
		name   string
		attach func(s *Server, h namedHandler) http.HandlerFunc
		method string
		// wantGranted is the status for a caller whose only claim on the
		// resource is that it is public: reads pass, writes do not — a public
		// resource is readable by everyone and writable by nobody.
		wantGranted int
	}{
		{name: "read", attach: func(s *Server, h namedHandler) http.HandlerFunc {
			return s.read("thing", func(_ context.Context, w http.ResponseWriter, name string) { h(w, nil, name) })
		}, method: http.MethodGet, wantGranted: http.StatusOK},
		{name: "write", attach: func(s *Server, h namedHandler) http.HandlerFunc {
			return s.write("thing", h)
		}, method: http.MethodPut, wantGranted: http.StatusForbidden},
		// The context-taking variant of the same guard, for the CRUD handlers
		// built for the pre-route-table shape.
		{name: "writeCtx", attach: func(s *Server, h namedHandler) http.HandlerFunc {
			return s.writeCtx("thing", func(_ context.Context, w http.ResponseWriter, name string) { h(w, nil, name) })
		}, method: http.MethodPut, wantGranted: http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := ""
			handler := func(w http.ResponseWriter, _ *http.Request, name string) {
				called = name
				writeJSON(w, http.StatusOK, map[string]string{"name": name})
			}

			// No authz configured (dev mode): the guard lets the request through.
			s := testServer(t)
			s.routes().HandleFunc(tc.method+" /api/v1/things/{name}", tc.attach(s, handler))
			code, _, _ := doAPI(t, s, tc.method, "/api/v1/things/thing-1", nil)
			if code != http.StatusOK || called != "thing-1" {
				t.Fatalf("expected pass-through with the path value, got %d (handler saw %q)", code, called)
			}

			// Authz configured, no caller in context: 401, handler untouched.
			called = ""
			s.authzChecker = authz.NewChecker(denyAuthzStore{}, authz.NewGroupCache(func(string) (map[string]authz.GroupRole, error) {
				return nil, nil
			}, time.Minute))
			code, _, msg := doAPI(t, s, tc.method, "/api/v1/things/thing-1", nil)
			if code != http.StatusUnauthorized || called != "" {
				t.Fatalf("expected 401 without a caller, got %d (handler saw %q, %q)", code, called, msg)
			}

			// Authz configured, caller without a grant: 403, handler untouched.
			called = ""
			s2 := fullRouteServer(t)
			s2.authzChecker = s.authzChecker
			s2.routes().HandleFunc(tc.method+" /api/v1/things/{name}", asCaller(tc.attach(s2, handler)))
			code, _, msg = doAPI(t, s2, tc.method, "/api/v1/things/thing-1", nil)
			if code != http.StatusForbidden || called != "" {
				t.Fatalf("expected 403 for a caller with no grant, got %d (handler saw %q, %q)", code, called, msg)
			}

			// A public resource: read passes, write does not.
			called = ""
			s2.authzChecker = authz.NewChecker(groupAuthzStore{public: true}, authz.NewGroupCache(func(string) (map[string]authz.GroupRole, error) {
				return nil, nil
			}, time.Minute))
			code, _, _ = doAPI(t, s2, tc.method, "/api/v1/things/thing-1", nil)
			if code != tc.wantGranted {
				t.Fatalf("expected %d for a public resource, got %d (handler saw %q)", tc.wantGranted, code, called)
			}

			// A writer in the resource's group: the guard passes and the name
			// arrives intact.
			called = ""
			s2.authzChecker = authz.NewChecker(groupAuthzStore{}, authz.NewGroupCache(func(uid string) (map[string]authz.GroupRole, error) {
				if uid != "u1" {
					return nil, nil
				}
				return map[string]authz.GroupRole{"g1": authz.RoleWriter}, nil
			}, time.Minute))
			code, _, _ = doAPI(t, s2, tc.method, "/api/v1/things/thing-1", nil)
			if code != http.StatusOK || called != "thing-1" {
				t.Fatalf("expected pass-through for a writer, got %d (handler saw %q)", code, called)
			}
		})
	}
}

// TestInternalTaskGuard pins the wrapper that replaces the single funnel of
// handleInternalAgent: no secret → 503, wrong token → 401, right token → the
// handler runs with the agent name from the path.
func TestInternalTaskGuard(t *testing.T) {
	var got string
	inner := func(w http.ResponseWriter, _ *http.Request, agentName string) {
		got = agentName
		writeJSON(w, http.StatusOK, map[string]string{"agent": agentName})
	}

	s := testServer(t)
	s.routes().HandleFunc("POST /api/internal/agents/{name}/tasks/{id}/ack", s.internalTask(inner))

	// Secret not configured: the endpoint is closed, not open.
	code, _, msg := doAPI(t, s, http.MethodPost, "/api/internal/agents/a-1/tasks/7/ack", nil)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 with no internal secret, got %d %q", code, msg)
	}

	s.internalValidateSecret = "s3cret"

	code, _, msg = doAPI(t, s, http.MethodPost, "/api/internal/agents/a-1/tasks/7/ack", nil)
	if code != http.StatusUnauthorized || got != "" {
		t.Fatalf("expected 401 without a token, got %d %q (handler saw %q)", code, msg, got)
	}
	code, _, msg = doAPI(t, s, http.MethodPost, "/api/internal/agents/a-1/tasks/7/ack",
		map[string]string{"X-Internal-Token": "wrong"})
	if code != http.StatusUnauthorized || got != "" {
		t.Fatalf("expected 401 with a wrong token, got %d %q (handler saw %q)", code, msg, got)
	}

	code, _, _ = doAPI(t, s, http.MethodPost, "/api/internal/agents/a-1/tasks/7/ack",
		map[string]string{"X-Internal-Token": "s3cret"})
	if code != http.StatusOK || got != "a-1" {
		t.Fatalf("expected the handler to run with the agent name, got %d (handler saw %q)", code, got)
	}
}

// --- the internal-token invariant ---

// internalTokenRoute is one /api/internal/* route and the status it must answer
// when the caller does not present X-Internal-Token.
type internalTokenRoute struct {
	method string
	path   string
	// wantNoSecret is the status with internalValidateSecret unset: 503 for a
	// token-gated route (closed rather than open), or the handler's own answer
	// for the routes that authenticate differently.
	wantNoSecret int
	// wantWrongToken is the status with the secret configured and a bad token.
	wantWrongToken int
}

// internalTokenRoutes enumerates every /api/internal/* route registered by
// New(...) plus SetSkillDiscovery, and how each must answer an unauthenticated
// caller. The token-gated ones are the invariant: they must never answer
// anything but 503/401, so a new internal route cannot ship unauthenticated.
//
// The last three rows are the deliberate exceptions, pinned so that turning one
// of them into a token-gated route (or the reverse) is a test change:
//
//   - /api/internal/plugins/{id}/credentials authenticates the plugin endpoint
//     itself (a verified caller identity), not the shared internal token.
//   - /api/internal/plugins/{id}/agent-mcp uses the internal secret as a bearer
//     token, not X-Internal-Token.
//   - /api/internal/skills/mcp uses the separate catalogue token on purpose.
var internalTokenRoutes = []internalTokenRoute{
	{method: http.MethodPost, path: "/api/internal/agents/a-1/tasks/7", wantNoSecret: http.StatusServiceUnavailable, wantWrongToken: http.StatusUnauthorized},
	{method: http.MethodPost, path: "/api/internal/agents/a-1/tasks/7/ack", wantNoSecret: http.StatusServiceUnavailable, wantWrongToken: http.StatusUnauthorized},
	{method: http.MethodPost, path: "/api/internal/agents/a-1/tasks/7/nack", wantNoSecret: http.StatusServiceUnavailable, wantWrongToken: http.StatusUnauthorized},
	{method: http.MethodPost, path: "/api/internal/events", wantNoSecret: http.StatusServiceUnavailable, wantWrongToken: http.StatusUnauthorized},
	{method: http.MethodPost, path: "/api/internal/task-logs", wantNoSecret: http.StatusServiceUnavailable, wantWrongToken: http.StatusUnauthorized},
	{method: http.MethodPost, path: "/api/internal/task-messages", wantNoSecret: http.StatusServiceUnavailable, wantWrongToken: http.StatusUnauthorized},
	{method: http.MethodGet, path: "/api/internal/plugin-tools", wantNoSecret: http.StatusServiceUnavailable, wantWrongToken: http.StatusUnauthorized},
	{method: http.MethodGet, path: "/api/internal/chat/sessions", wantNoSecret: http.StatusServiceUnavailable, wantWrongToken: http.StatusUnauthorized},
	{method: http.MethodGet, path: "/api/internal/chat/sessions/abc", wantNoSecret: http.StatusServiceUnavailable, wantWrongToken: http.StatusUnauthorized},
	{method: http.MethodPost, path: "/api/internal/chat/sessions/abc/messages", wantNoSecret: http.StatusServiceUnavailable, wantWrongToken: http.StatusUnauthorized},
	// The user-token validate route checks the token inline rather than through
	// requireInternalToken, and reports an unwired store first: same status for
	// both rows here, but a different code path, so it is listed explicitly.
	{method: http.MethodPost, path: "/api/internal/user-tokens/validate", wantNoSecret: http.StatusServiceUnavailable, wantWrongToken: http.StatusServiceUnavailable},
	// Exceptions — see the comment above.
	{method: http.MethodPost, path: "/api/internal/plugins/github/credentials", wantNoSecret: http.StatusNotFound, wantWrongToken: http.StatusNotFound},
	{method: http.MethodPost, path: "/api/internal/plugins/github/agent-mcp", wantNoSecret: http.StatusServiceUnavailable, wantWrongToken: http.StatusUnauthorized},
	{method: http.MethodPost, path: "/api/internal/skills/mcp", wantNoSecret: http.StatusServiceUnavailable, wantWrongToken: http.StatusServiceUnavailable},
}

// TestInternalRoutesRequireInternalToken drives the table above through the real
// stack. It is the mechanical half of Decision 7: while the internal agent
// routes are one subtree handler the funnel provides the guarantee, and once
// they are per-route patterns the s.internalTask wrapper does — this test is
// what proves either of those.
func TestInternalRoutesRequireInternalToken(t *testing.T) {
	for _, tc := range internalTokenRoutes {
		t.Run(fmt.Sprintf("%s %s", tc.method, tc.path), func(t *testing.T) {
			s := fullRouteServer(t)

			// No secret configured.
			code, _, _ := doAPI(t, s, tc.method, tc.path, nil)
			if code != tc.wantNoSecret {
				t.Errorf("without a configured secret: expected %d, got %d", tc.wantNoSecret, code)
			}

			// Secret configured, token missing or wrong.
			s = fullRouteServer(t)
			s.internalValidateSecret = "s3cret"
			s.skillsMCPToken = "catalogue-token"
			for _, hdr := range []map[string]string{nil, {"X-Internal-Token": "wrong"}} {
				code, _, _ := doAPI(t, s, tc.method, tc.path, hdr)
				if code != tc.wantWrongToken {
					t.Errorf("with a bad X-Internal-Token: expected %d, got %d", tc.wantWrongToken, code)
				}
			}
		})
	}
}

// internalTransitionalSubtrees are the /api/internal/* patterns that are still
// registered as one catch-all subtree, whose concrete routes the table above
// covers by path instead. Each is removed by its conversion PR in the order of
// issue #265 §9; the plugins one stays (Decision 8: plugin-authored endpoints
// are not knowable at construction).
var internalTransitionalSubtrees = map[string]string{
	"/api/internal/agents/":        "converted to per-route patterns with s.internalTask",
	"/api/internal/chat/sessions/": "converted to per-route patterns",
	"/api/internal/plugins/":       "stays dispatched: plugin-authored endpoints (Decision 8)",
}

// TestInternalRoutesCoverEveryRegisteredPattern fails when a /api/internal/*
// route is registered but neither covered by the table above nor recorded as a
// transitional subtree — the reason the table is worth keeping instead of
// trusting.
func TestInternalRoutesCoverEveryRegisteredPattern(t *testing.T) {
	s := fullRouteServer(t)
	covered := map[string]bool{}
	for _, tc := range internalTokenRoutes {
		covered[tc.path] = true
	}
	for _, p := range s.routePatterns() {
		_, path, found := strings.Cut(p, " ")
		if !found {
			path = p
		}
		if !strings.HasPrefix(path, "/api/internal/") {
			continue
		}
		if strings.HasSuffix(path, "/") {
			if _, ok := internalTransitionalSubtrees[path]; ok {
				continue
			}
			t.Errorf("internal subtree pattern %q is registered and not covered by the internal-token table", p)
			continue
		}
		if !covered[path] {
			t.Errorf("internal route %q is registered but not covered by the internal-token table", p)
		}
	}
}

// --- helpers ---

// asCaller injects an OIDC identity the way the auth middleware would, so a
// guard test can exercise the "caller exists but is not granted" branch.
func asCaller(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := oidc.ContextWithUser(r.Context(), &oidc.User{Sub: "u1", Username: "u1"})
		h(w, r.WithContext(ctx))
	}
}
