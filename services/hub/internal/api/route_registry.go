package api

import (
	"net/http"
	"sort"
	"sync"
)

// routeRegistrar is the registration surface of the route mux: everything the
// mux itself satisfies, and nothing more. It exists so the server can record
// every pattern as it is registered (see recordingMux) while callers keep
// writing `mux.HandleFunc("GET /api/v1/agents/{name}", ...)`.
type routeRegistrar interface {
	Handle(pattern string, handler http.Handler)
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}

var _ routeRegistrar = (*http.ServeMux)(nil)

// recordingMux wraps the ServeMux and remembers every pattern handed to it.
//
// The stdlib mux cannot report what is registered on it — there is no walk or
// list API on `http.ServeMux` — so the route inventory the tests check
// (router_test.go) is built from this record instead. That inventory is what
// keeps the two hand-maintained path tables (routeRules in plugin_routes.go,
// isAuthExempt in server.go) honest, and what proves every /api/internal/*
// route is covered by the internal-token table.
//
// It is the only way to register routes on a Server built by New: a pattern
// registered straight on the raw mux is invisible to the inventory, which is
// exactly the blind spot the inventory exists to close.
type recordingMux struct {
	mux *http.ServeMux

	mu       sync.Mutex
	patterns []string
}

func (m *recordingMux) Handle(pattern string, handler http.Handler) {
	m.record(pattern)
	m.mux.Handle(pattern, handler)
}

func (m *recordingMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	m.record(pattern)
	m.mux.HandleFunc(pattern, handler)
}

func (m *recordingMux) record(pattern string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.patterns = append(m.patterns, pattern)
}

// registeredPatterns returns every recorded pattern, sorted, deduplicated.
func (m *recordingMux) registeredPatterns() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := make(map[string]bool, len(m.patterns))
	for _, p := range m.patterns {
		set[p] = true
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// routes returns the recording registration surface. Every route of the server
// is mounted through it — New(...), the SetXxx wiring methods, and the
// self-registering persona and skill route groups.
func (s *Server) routes() routeRegistrar {
	if s.routeLog == nil {
		if s.mux == nil {
			s.mux = http.NewServeMux()
		}
		s.routeLog = &recordingMux{mux: s.mux}
	}
	return s.routeLog
}

// routePatterns reports every pattern registered on the server, sorted. Tests
// use it as the route inventory; production code does not read it.
func (s *Server) routePatterns() []string {
	if s.routeLog == nil {
		return nil
	}
	return s.routeLog.registeredPatterns()
}
