package api

import (
	"context"
	"net/http"
)

// Guard helpers: authz attached to a route at registration instead of inside a
// branch of a method switch. A route that cannot reach its handler without
// passing the guard cannot forget it — which is the failure mode the hand-rolled
// dispatch this replaces had (a task sub-route dispatched ahead of the
// requireRead/requireWrite checks below the switch).
//
// They read the resource identifier from the `{name}` path value, the one
// placeholder name the route table uses for a resource id, and keep the
// `(w, r, name)` handler signature the item handlers already have, so
// converting a route group is a deletion in the handler and one line here.
//
// Guards that need the loaded object (a managedBy conflict check, a persona's
// id-scoped read) stay in the handler: these helpers cover the resource-name
// scoped checks the route already knows how to answer.

// namedHandler is an item handler: it takes the resource identifier the route
// already carries as a `{name}` wildcard, so the guard can check authz before
// the handler runs and the handler keeps its own signature.
type namedHandler func(w http.ResponseWriter, r *http.Request, name string)

// namedContextHandler is the same for the handlers that take the request
// context explicitly (the CRUD handlers built for the pre-route-table shape).
type namedContextHandler func(ctx context.Context, w http.ResponseWriter, name string)

// read guards a route with requireRead on the `{name}` path value.
//
//	mux.HandleFunc("GET /api/v1/agents/{name}", s.read("agent", s.getAgent))
func (s *Server) read(resourceType string, h namedContextHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !s.requireRead(w, r, resourceType, name) {
			return
		}
		h(r.Context(), w, name)
	}
}

// write guards a route with requireWrite on the `{name}` path value.
//
//	mux.HandleFunc("PUT /api/v1/agents/{name}", s.write("agent", s.pauseAgent))
func (s *Server) write(resourceType string, h namedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !s.requireWrite(w, r, resourceType, name) {
			return
		}
		h(w, r, name)
	}
}

// writeCtx is write for the handlers that take the request context explicitly.
//
//	mux.HandleFunc("PUT /api/v1/agents/{name}", s.writeCtx("agent", s.updateAgent))
func (s *Server) writeCtx(resourceType string, h namedContextHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !s.requireWrite(w, r, resourceType, name) {
			return
		}
		h(r.Context(), w, name)
	}
}

// internalTask guards an agent task route on /api/internal/agents/{name}/… with
// the shared internal token, then hands the agent name to the task handler.
//
// The /api/internal/* surface is outside the OIDC middleware (serveHTTPInner
// only guards /api/v1/*), so the X-Internal-Token check is the only thing
// standing in front of these routes. While they were one subtree handler, that
// check ran once, before any sub-route dispatch. Per-route patterns remove that
// funnel, so every internal task route must be registered through this helper —
// and the table in router_test.go asserts each one of them answers 401/503
// without the token.
func (s *Server) internalTask(h namedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.requireInternalToken(w, r) {
			return
		}
		h(w, r, r.PathValue("name"))
	}
}
