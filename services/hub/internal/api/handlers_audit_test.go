package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/auditevents"
	"github.com/ainsel-project/ainsel/services/hub/internal/localauth"
)

// fakeAuditStore records inserted events and replays a canned List result.
type fakeAuditStore struct {
	inserted   []auditevents.Event
	insertErr  error
	listResult []auditevents.Event
	listTotal  int
	lastOpts   auditevents.ListOptions
}

func (f *fakeAuditStore) Insert(_ context.Context, e *auditevents.Event) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	e.ID = int64(len(f.inserted) + 1)
	e.OccurredAt = time.Now().UTC()
	f.inserted = append(f.inserted, *e)
	return nil
}

func (f *fakeAuditStore) List(_ context.Context, opts auditevents.ListOptions) ([]auditevents.Event, int, error) {
	f.lastOpts = opts
	return f.listResult, f.listTotal, nil
}

// auditTestServer wires local auth plus the audit store and route.
func auditTestServer(t *testing.T) (*Server, *fakeAuthzStore, *fakeAuditStore) {
	t.Helper()
	s, store := localAuthTestServer(t)
	audit := &fakeAuditStore{}
	s.SetAuditEventStore(audit)
	s.mux.HandleFunc("/api/v1/audit/auth-events", s.handleAuditAuthEvents)
	return s, store, audit
}

func seedLocalUser(t *testing.T, store *fakeAuthzStore, name string, admin bool) {
	t.Helper()
	hash, err := localauth.HashPassword("password-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateLocalUser(context.Background(), "local:"+name, "", name, hash, admin); err != nil {
		t.Fatal(err)
	}
}

// --- write sites ---

func TestAuditLoginSuccessWritesRow(t *testing.T) {
	s, store, audit := auditTestServer(t)
	seedAdmin(t, store)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": "admin", "password": "admin-password",
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(audit.inserted) != 1 {
		t.Fatalf("inserted = %d events, want 1", len(audit.inserted))
	}
	e := audit.inserted[0]
	if e.EventType != auditevents.TypeLogin || e.Outcome != auditevents.OutcomeSuccess {
		t.Errorf("event = %+v, want auth_login/success", e)
	}
	if e.ActorSub != "local:admin" || e.Target != "local:admin" {
		t.Errorf("actor/target = %q/%q, want local:admin/local:admin", e.ActorSub, e.Target)
	}
}

func TestAuditLoginFailureWritesRowForUnknownUser(t *testing.T) {
	s, store, audit := auditTestServer(t)
	seedAdmin(t, store)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": "ghost", "password": "wrong-password",
	}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(audit.inserted) != 1 {
		t.Fatalf("inserted = %d events, want 1", len(audit.inserted))
	}
	e := audit.inserted[0]
	// The attempted username is recorded even though the user does not exist:
	// attempted-username + source IP is the brute-force signal.
	if e.EventType != auditevents.TypeLoginFailed || e.Outcome != auditevents.OutcomeFailure {
		t.Errorf("event = %+v, want auth_login_failed/failure", e)
	}
	if e.ActorSub != "local:ghost" || e.Target != "local:ghost" {
		t.Errorf("actor/target = %q/%q, want local:ghost", e.ActorSub, e.Target)
	}
}

func TestAuditPasswordChangeWritesRow(t *testing.T) {
	s, store, audit := auditTestServer(t)
	seedLocalUser(t, store, "alice", false)

	rec := doJSON(t, s, http.MethodPut, "/api/v1/auth/password", map[string]string{
		"current": "password-123", "new": "new-password-456",
	}, asUser("local:alice", "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(audit.inserted) != 1 {
		t.Fatalf("inserted = %d events, want 1", len(audit.inserted))
	}
	e := audit.inserted[0]
	if e.EventType != auditevents.TypePasswordChanged || e.Outcome != auditevents.OutcomeSuccess {
		t.Errorf("event = %+v, want auth_password_changed/success", e)
	}
	if e.ActorSub != "local:alice" || e.Target != "local:alice" {
		t.Errorf("actor/target = %q/%q, want local:alice", e.ActorSub, e.Target)
	}
}

func TestAuditUserCreateResetDeleteWriteRows(t *testing.T) {
	s, store, audit := auditTestServer(t)
	seedAdmin(t, store)
	seedLocalUser(t, store, "bob", false)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/users", map[string]any{
		"username": "carol", "password": "carol-password",
	}, asUser("local:admin", "admin"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodPatch, "/api/v1/users/local:bob", map[string]any{
		"password": "reset-password-999",
	}, asUser("local:admin", "admin"))
	if rec.Code != http.StatusOK {
		t.Fatalf("reset status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodDelete, "/api/v1/users/local:bob", nil, asUser("local:admin", "admin"))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", rec.Code, rec.Body.String())
	}

	if len(audit.inserted) != 3 {
		t.Fatalf("inserted = %d events, want 3", len(audit.inserted))
	}
	want := []struct {
		typ, actor, target string
	}{
		{auditevents.TypeUserCreated, "local:admin", "local:carol"},
		{auditevents.TypeUserPasswordReset, "local:admin", "local:bob"},
		{auditevents.TypeUserDeleted, "local:admin", "local:bob"},
	}
	for i, w := range want {
		e := audit.inserted[i]
		if e.EventType != w.typ || e.ActorSub != w.actor || e.Target != w.target {
			t.Errorf("event %d = %+v, want type=%s actor=%s target=%s", i, e, w.typ, w.actor, w.target)
		}
		if e.Outcome != auditevents.OutcomeSuccess {
			t.Errorf("event %d outcome = %q, want success", i, e.Outcome)
		}
	}
}

func TestAuditInsertFailureDoesNotFailRequest(t *testing.T) {
	s, store, audit := auditTestServer(t)
	seedAdmin(t, store)
	audit.insertErr = errors.New("db down")

	rec := doJSON(t, s, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": "admin", "password": "admin-password",
	}, nil)
	// An audit write failure must never lock anybody out of login.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s; want 200 despite audit failure", rec.Code, rec.Body.String())
	}
}

// --- endpoint authz ---

func TestAuditEndpointRejectsNonAdmins(t *testing.T) {
	s, store, _ := auditTestServer(t)
	seedAdmin(t, store)
	seedLocalUser(t, store, "bob", false)

	// Anonymous → 401.
	rec := doJSON(t, s, http.MethodGet, "/api/v1/audit/auth-events", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous status = %d, want 401", rec.Code)
	}
	// Authenticated non-admin → 403.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/audit/auth-events", nil, asUser("local:bob", "bob"))
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin status = %d, want 403", rec.Code)
	}
	// Admin → 200.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/audit/auth-events", nil, asUser("local:admin", "admin"))
	if rec.Code != http.StatusOK {
		t.Errorf("admin status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestAuditEndpointForbiddenWhenAuthzUnwired(t *testing.T) {
	// none mode: authzChecker is nil → requireAdmin answers 403, matching the
	// AC "non-admins get 403".
	s, _, _ := auditTestServer(t)
	s.authzChecker = nil
	rec := doJSON(t, s, http.MethodGet, "/api/v1/audit/auth-events", nil, asUser("local:admin", "admin"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestAuditEndpointMethodGuard(t *testing.T) {
	s, store, _ := auditTestServer(t)
	seedAdmin(t, store)
	rec := doJSON(t, s, http.MethodPost, "/api/v1/audit/auth-events", nil, asUser("local:admin", "admin"))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestAuditEndpointStoreUnwiredIs503(t *testing.T) {
	s, store, _ := auditTestServer(t)
	seedAdmin(t, store)
	s.SetAuditEventStore(nil)
	rec := doJSON(t, s, http.MethodGet, "/api/v1/audit/auth-events", nil, asUser("local:admin", "admin"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// --- endpoint filters / paging ---

type auditListResp struct {
	Items      []json.RawMessage `json:"items"`
	Total      int               `json:"total"`
	Page       int               `json:"page"`
	PageSize   int               `json:"pageSize"`
	TotalPages int               `json:"totalPages"`
}

func getAuditList(t *testing.T, s *Server, query string) (*auditListResp, auditevents.ListOptions) {
	t.Helper()
	rec := doJSON(t, s, http.MethodGet, "/api/v1/audit/auth-events?"+query, nil, asUser("local:admin", "admin"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d for %q, body = %s", rec.Code, query, rec.Body.String())
	}
	var resp auditListResp
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	return &resp, s.authEvents.(*fakeAuditStore).lastOpts
}

func TestAuditEndpointFilterParsing(t *testing.T) {
	s, store, audit := auditTestServer(t)
	seedAdmin(t, store)
	audit.listResult = []auditevents.Event{{ID: 7, OccurredAt: time.Now(), ActorSub: "local:alice",
		EventType: auditevents.TypeLogin, Target: "local:alice", Outcome: "success"}}
	audit.listTotal = 1

	from := "2026-10-01T00:00:00Z"
	to := "2026-10-02T00:00:00Z"
	resp, opts := getAuditList(t, s, "user=local:alice&type=auth_login&type=user_created&from="+from+"&to="+to+"&page=2&pageSize=10")

	if opts.User != "local:alice" {
		t.Errorf("user filter = %q", opts.User)
	}
	if len(opts.Types) != 2 || opts.Types[0] != "auth_login" || opts.Types[1] != "user_created" {
		t.Errorf("type filter = %v, want [auth_login user_created]", opts.Types)
	}
	if got := opts.Since.UTC().Format(time.RFC3339); got != from {
		t.Errorf("since = %q, want %q", got, from)
	}
	if got := opts.Until.UTC().Format(time.RFC3339); got != to {
		t.Errorf("until = %q, want %q", got, to)
	}
	if opts.Limit != 10 || opts.Offset != 10 {
		t.Errorf("limit/offset = %d/%d, want 10/10", opts.Limit, opts.Offset)
	}
	if resp.Total != 1 || resp.Page != 2 || resp.PageSize != 10 || resp.TotalPages != 1 {
		t.Errorf("envelope = %+v", resp)
	}
	if len(resp.Items) != 1 {
		t.Errorf("items = %d, want 1", len(resp.Items))
	}
}

func TestAuditEndpointBadParamsAre400(t *testing.T) {
	s, store, _ := auditTestServer(t)
	seedAdmin(t, store)

	for _, q := range []string{"page=abc", "pageSize=xyz", "from=not-a-time", "to=2026-13-45"} {
		rec := doJSON(t, s, http.MethodGet, "/api/v1/audit/auth-events?"+q, nil, asUser("local:admin", "admin"))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("query %q status = %d, want 400", q, rec.Code)
		}
	}
}

func TestAuditEndpointPageSizeClamped(t *testing.T) {
	s, store, _ := auditTestServer(t)
	seedAdmin(t, store)
	_, opts := getAuditList(t, s, "pageSize=5000")
	if opts.Limit != maxPageSize {
		t.Errorf("limit = %d, want clamped %d", opts.Limit, maxPageSize)
	}
}

// --- sourceIP ---

func TestSourceIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.7:5555"
	if got := sourceIP(req); got != "203.0.113.7" {
		t.Errorf("RemoteAddr only: got %q", got)
	}
	req.Header.Set("X-Forwarded-For", "198.51.100.2, 10.0.0.1")
	if got := sourceIP(req); got != "198.51.100.2" {
		t.Errorf("XFF: got %q, want first entry", got)
	}
	req.Header.Set("X-Forwarded-For", " ")
	if got := sourceIP(req); got != "203.0.113.7" {
		t.Errorf("blank XFF: got %q, want RemoteAddr host", got)
	}
}
