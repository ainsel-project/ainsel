package api

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/dbtest"
	"github.com/ainsel-project/ainsel/services/hub/internal/triggers"
)

func cronTriggerTestServer(t *testing.T) *Server {
	t.Helper()
	pool := dbtest.Open(t)
	s := testServer(t)
	s.triggerStore = triggers.NewStore(pool)
	s.mux.HandleFunc("/api/v1/cron-triggers", s.handleCronTriggers)
	s.mux.HandleFunc("/api/v1/cron-triggers/", s.handleCronTrigger)
	return s
}

func TestCronTriggers_CreateAndList(t *testing.T) {
	srv := cronTriggerTestServer(t)

	code, raw := do(t, srv, http.MethodPost, "/api/v1/cron-triggers", CronTriggerCreateRequest{
		Name:     "daily-standup",
		AgentRef: "standup-bot",
		Schedule: "0 9 * * 1-5",
		Prompt:   "Summarize open PRs and stale issues.",
	})
	if code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", code, raw)
	}
	created := decode[CronTriggerResponse](t, raw)
	if !regexp.MustCompile(`^c-[0-9a-f]{8}$`).MatchString(created.ID) {
		t.Errorf("expected id c-XXXXXXXX, got %s", created.ID)
	}
	if created.Name != "daily-standup" {
		t.Errorf("name = %q, want daily-standup", created.Name)
	}
	if created.AgentRef != "standup-bot" {
		t.Errorf("agentRef = %q, want standup-bot", created.AgentRef)
	}
	if created.Schedule != "0 9 * * 1-5" {
		t.Errorf("schedule = %q, want 0 9 * * 1-5", created.Schedule)
	}
	if created.Prompt != "Summarize open PRs and stale issues." {
		t.Errorf("prompt mismatch: %q", created.Prompt)
	}
	if !created.Enabled {
		t.Errorf("enabled should default to true")
	}

	// List and verify.
	code, raw = do(t, srv, http.MethodGet, "/api/v1/cron-triggers", nil)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", code, raw)
	}
	page := decode[struct {
		Items []CronTriggerResponse `json:"items"`
		Total int                   `json:"total"`
	}](t, raw)
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("expected 1 item, got total=%d items=%d", page.Total, len(page.Items))
	}
	if page.Items[0].AgentRef != "standup-bot" {
		t.Errorf("list item agentRef = %q", page.Items[0].AgentRef)
	}
}

func TestCronTriggers_CreateRejectsMissingFields(t *testing.T) {
	srv := cronTriggerTestServer(t)

	// Missing schedule and prompt.
	code, raw := do(t, srv, http.MethodPost, "/api/v1/cron-triggers",
		CronTriggerCreateRequest{Name: "x", AgentRef: "a"})
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing fields, got %d: %s", code, raw)
	}
}

func TestCronTriggers_GetUpdateDelete(t *testing.T) {
	srv := cronTriggerTestServer(t)

	// Create.
	_, raw := do(t, srv, http.MethodPost, "/api/v1/cron-triggers", CronTriggerCreateRequest{
		Name: "nightly", AgentRef: "bot", Schedule: "0 0 * * *", Prompt: "p",
	})
	created := decode[CronTriggerResponse](t, raw)

	// Get.
	code, raw := do(t, srv, http.MethodGet, "/api/v1/cron-triggers/"+created.ID, nil)
	if code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", code, raw)
	}

	// Update prompt + disable.
	code, raw = do(t, srv, http.MethodPut, "/api/v1/cron-triggers/"+created.ID,
		CronTriggerUpdateRequest{Prompt: "new prompt", Enabled: ptr(false)})
	if code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", code, raw)
	}
	updated := decode[CronTriggerResponse](t, raw)
	if updated.Prompt != "new prompt" {
		t.Errorf("updated prompt = %q, want new prompt", updated.Prompt)
	}
	if updated.Enabled {
		t.Errorf("updated enabled should be false")
	}

	// Delete.
	code, raw = do(t, srv, http.MethodDelete, "/api/v1/cron-triggers/"+created.ID, nil)
	if code != http.StatusNoContent {
		t.Fatalf("DELETE expected 204, got %d: %s", code, raw)
	}

	// Get again -> 404.
	code, _ = do(t, srv, http.MethodGet, "/api/v1/cron-triggers/"+created.ID, nil)
	if code != http.StatusNotFound {
		t.Errorf("GET after delete expected 404, got %d", code)
	}
}

func ptr[T any](v T) *T { return &v }
