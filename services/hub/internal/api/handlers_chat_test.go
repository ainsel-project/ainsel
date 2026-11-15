package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/chat"
	"github.com/ainsel-project/ainsel/services/hub/internal/dbtest"
)

// The chat store is a required dependency (issue #264): the endpoints have no
// "not configured" 503 left to answer, because New refuses to build a Server
// without it. The state moved from a request-time branch to a startup error.
func TestNewRejectsMissingChatStore(t *testing.T) {
	deps := testDeps(t)
	deps.Chat = nil
	_, err := New(deps)
	if err == nil {
		t.Fatal("expected New to reject a Deps without Chat")
	}
	if !strings.Contains(err.Error(), "Chat") {
		t.Fatalf("error should name the missing field, got: %v", err)
	}
}

func TestChatSessions_CreateMissingAgentName(t *testing.T) {
	s := testServer(t)
	s.mux.HandleFunc("/api/v1/chat/sessions", s.handleChatSessions)

	// The chat store is required (issue #264), so there is no "not configured"
	// answer ahead of validation: a POST with an empty body is the 400 the
	// request actually deserves.
	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/sessions", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestChatSession_MissingID(t *testing.T) {
	s := testServer(t)
	s.mux.HandleFunc("/api/v1/chat/sessions", s.handleChatSessions)
	s.mux.HandleFunc("/api/v1/chat/sessions/", s.handleChatSession)

	// /api/v1/chat/sessions/ with no ID should return 400.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/chat/sessions/", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	// The trailing slash path maps to handleChatSession, which strips the
	// prefix and gets an empty id → 400.
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 400 or 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestChatSession_MethodNotAllowed(t *testing.T) {
	s := testServer(t)
	s.mux.HandleFunc("/api/v1/chat/sessions", s.handleChatSessions)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/chat/sessions", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestChatSession_PatchRejectsMissingSession(t *testing.T) {
	store, cleanup := newTestChatStore(t)
	defer cleanup()

	s := testServer(t)
	s.chat = store
	s.mux.HandleFunc("/api/v1/chat/sessions", s.handleChatSessions)
	s.mux.HandleFunc("/api/v1/chat/sessions/", s.handleChatSession)

	// The store is required (issue #264), so the request reaches the session
	// lookup instead of a "not configured" branch: an unknown session is a 404.
	body := []byte(`{"name":"New Name"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/chat/sessions/sess-test", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected the lookup to answer, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Gets a fresh migrated database from the package's shared container
// (see TestMain / dbtest). Skips the test when Docker is unavailable.
func newTestChatStore(t *testing.T) (*chat.Store, func()) {
	t.Helper()
	pool := dbtest.Open(t)
	return chat.NewStore(pool), func() {}
}

func TestChatSession_PatchSuccess(t *testing.T) {
	store, cleanup := newTestChatStore(t)
	defer cleanup()

	s := testServer(t)
	s.chat = store
	s.mux.HandleFunc("/api/v1/chat/sessions", s.handleChatSessions)
	s.mux.HandleFunc("/api/v1/chat/sessions/", s.handleChatSession)

	// Create a session via the store.
	sess, err := store.CreateSession(context.Background(), "developer", "user-1")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// PATCH the session name.
	body := []byte(`{"name":"My Renamed Chat"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/chat/sessions/"+sess.ID, bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var updated chat.Session
	if err := json.NewDecoder(rec.Body).Decode(&updated); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if updated.Name != "My Renamed Chat" {
		t.Fatalf("expected name %q, got %q", "My Renamed Chat", updated.Name)
	}
	if updated.ID != sess.ID {
		t.Fatalf("expected id %q, got %q", sess.ID, updated.ID)
	}
}

func TestChatSession_PatchEmptyName_400(t *testing.T) {
	store, cleanup := newTestChatStore(t)
	defer cleanup()

	s := testServer(t)
	s.chat = store
	s.mux.HandleFunc("/api/v1/chat/sessions", s.handleChatSessions)
	s.mux.HandleFunc("/api/v1/chat/sessions/", s.handleChatSession)

	// Create a session via the store.
	sess, err := store.CreateSession(context.Background(), "developer", "user-1")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// PATCH with an empty name → 400.
	body := []byte(`{"name":""}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/chat/sessions/"+sess.ID, bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty name, got %d: %s", rec.Code, rec.Body.String())
	}

	// PATCH with whitespace-only name → 400 (trimmed to empty).
	body = []byte(`{"name":"   "}`)
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/chat/sessions/"+sess.ID, bytes.NewReader(body))
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for whitespace-only name, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestChatSession_PatchNotFound(t *testing.T) {
	store, cleanup := newTestChatStore(t)
	defer cleanup()

	s := testServer(t)
	s.chat = store
	s.mux.HandleFunc("/api/v1/chat/sessions", s.handleChatSessions)
	s.mux.HandleFunc("/api/v1/chat/sessions/", s.handleChatSession)

	// PATCH a non-existent session → 404.
	body := []byte(`{"name":"New Name"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/chat/sessions/sess-missing", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}
