package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The task-log store is a required dependency (issue #264): the endpoint has no
// "not configured" 503 to answer, because New refuses a Deps without it.
func TestNewRejectsMissingTaskLogStore(t *testing.T) {
	deps := testDeps(t)
	deps.TaskLogs = nil
	_, err := New(deps)
	if err == nil {
		t.Fatal("expected New to reject a Deps without TaskLogs")
	}
	if !strings.Contains(err.Error(), "TaskLogs") {
		t.Fatalf("error should name the missing field, got: %v", err)
	}
}

func TestErrors_RejectsNonGet(t *testing.T) {
	s := testServer(t)
	s.mux.HandleFunc("/api/v1/errors", s.handleErrors)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/errors", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}
