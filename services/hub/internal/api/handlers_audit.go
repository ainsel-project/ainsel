package api

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/auditevents"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"
)

// recordAuthEvent writes one audit-trail row best-effort. It is called from
// the auth/user handlers next to the existing slog line. An audit row must
// never become an availability dependency for the request it records: a nil
// store (no DB wired) is a no-op, and a write failure is logged, never
// returned. The row carries only the fields below — never a request body —
// so persisting a credential is structurally impossible.
func (s *Server) recordAuthEvent(r *http.Request, eventType, actorSub, target, outcome string) {
	if s.authEvents == nil {
		return
	}
	e := &auditevents.Event{
		ActorSub:  actorSub,
		EventType: eventType,
		Target:    target,
		SourceIP:  sourceIP(r),
		Outcome:   outcome,
	}
	// Detach from the request context so a client disconnect cannot drop the
	// row, but keep the values for tracing.
	ctx := context.WithoutCancel(r.Context())
	if err := s.authEvents.Insert(ctx, e); err != nil {
		slog.Error("auth event insert failed", "event_type", eventType, "actor", actorSub, "error", err)
	}
}

// sourceIP extracts the caller's IP. The hub sits behind an ingress, so the
// first X-Forwarded-For entry is the pragmatic source; otherwise the peer
// address. Caveat: XFF is spoofable if the hub is ever exposed directly
// without a trusted proxy — acceptable for this audit surface, documented in
// services/hub/docs/api.md.
func sourceIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first, _, _ := strings.Cut(xff, ","); strings.TrimSpace(first) != "" {
			return strings.TrimSpace(first)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// callerSub returns the authenticated caller's sub for audit rows. Every call
// site is behind requireAdmin, so a sub is present; the empty string is the
// honest fallback rather than a guess.
func callerSub(r *http.Request) string {
	if u, ok := oidc.FromContext(r.Context()); ok {
		return u.Sub
	}
	return ""
}

// authEventItem is one row of the audit endpoint's JSON response.
type authEventItem struct {
	ID         int64  `json:"id"`
	OccurredAt string `json:"occurredAt"`
	ActorSub   string `json:"actorSub"`
	EventType  string `json:"eventType"`
	Target     string `json:"target"`
	SourceIP   string `json:"sourceIp"`
	Outcome    string `json:"outcome"`
}

// handleAuditAuthEvents serves GET /api/v1/audit/auth-events (issue #116).
//
// Admin-only list of persisted auth events, newest first, in the standard
// pagination envelope (see services/hub/docs/api.md).
//
// Query parameters:
//
//	?user=<id>     Match actor_sub OR target (everything involving one user).
//	?type=<name>   Event type; repeatable, OR-ed.
//	?from=<ts>     RFC3339, inclusive lower bound on occurred_at.
//	?to=<ts>       RFC3339, exclusive upper bound on occurred_at.
//	?page / ?pageSize  Standard pagination (default 50, max 200).
//
// Non-admins get 401 (anonymous) or 403 (authenticated, not admin — including
// the no-auth mode where no admin concept exists). Unparseable page/from/to
// return 400. Missing store (no DB) returns 503.
func (s *Server) handleAuditAuthEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.requireAdmin(w, r) {
		return
	}
	if s.authEvents == nil {
		writeError(w, http.StatusServiceUnavailable, "audit event store not configured")
		return
	}

	q := r.URL.Query()
	page, err := ParsePageParams(q)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	opts := auditevents.ListOptions{
		User:  q.Get("user"),
		Types: q["type"],
		Limit: page.PageSize,
	}
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid from: must be RFC3339")
			return
		}
		opts.Since = t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid to: must be RFC3339")
			return
		}
		opts.Until = t
	}
	opts.Offset = (page.Page - 1) * page.PageSize

	events, total, err := s.authEvents.List(r.Context(), opts)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to query audit events: "+err.Error())
		return
	}

	items := make([]authEventItem, 0, len(events))
	for _, e := range events {
		items = append(items, authEventItem{
			ID:         e.ID,
			OccurredAt: e.OccurredAt.UTC().Format(time.RFC3339Nano),
			ActorSub:   e.ActorSub,
			EventType:  e.EventType,
			Target:     e.Target,
			SourceIP:   e.SourceIP,
			Outcome:    e.Outcome,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":      items,
		"total":      total,
		"page":       page.Page,
		"pageSize":   page.PageSize,
		"totalPages": page.TotalPages(total),
	})
}
