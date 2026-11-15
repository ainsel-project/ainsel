package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newAuditStub(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	lastQuery := new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/audit/auth-events" {
			writeErrorJSON(w, http.StatusNotFound, "unexpected path "+r.URL.Path)
			return
		}
		*lastQuery = r.URL.RawQuery
		if r.URL.Query().Get("page") == "9" {
			writeErrorJSON(w, http.StatusForbidden, "admin role required")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{"id": "ae-1", "occurredAt": "2026-11-15T10:00:00Z", "actorSub": "u-1", "eventType": "login", "outcome": "success"},
			},
			"total": 120, "page": 1, "pageSize": 50, "totalPages": 3,
		})
	}))
	t.Cleanup(srv.Close)
	return srv, lastQuery
}

func TestListAuthEventsForwardsFilters(t *testing.T) {
	srv, lastQuery := newAuditStub(t)
	at := &AuditTools{HubURL: srv.URL, HTTPClient: srv.Client()}
	text := userToolText(t, at.ListAuthEvents, map[string]any{
		"user": "u-1",
		"type": []any{"login", "password_reset"},
		"from": "2026-11-01T00:00:00Z",
		"to":   "2026-11-15T00:00:00Z",
	})
	q := *lastQuery
	for _, want := range []string{"user=u-1", "type=login", "type=password_reset", "from=2026-11-01", "to=2026-11-15", "page=1", "pageSize=50"} {
		if !strings.Contains(q, want) {
			t.Errorf("expected %q in the forwarded query; got %q", want, q)
		}
	}
	if !strings.Contains(text, "ae-1") {
		t.Errorf("expected the event rows; got: %s", text)
	}
	if !strings.Contains(text, `"truncated":true`) {
		t.Errorf("expected truncated=true (120 total, page 1 of 50); got: %s", text)
	}
}

func TestListAuthEventsPassesThrough403(t *testing.T) {
	srv, _ := newAuditStub(t)
	at := &AuditTools{HubURL: srv.URL, HTTPClient: srv.Client()}
	msg := userToolError(t, at.ListAuthEvents, map[string]any{"page": float64(9)})
	if !strings.Contains(msg, "403") || !strings.Contains(msg, "admin role required") {
		t.Errorf("expected the hub's 403 to pass through; got: %s", msg)
	}
}
