package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// hubInvocation mirrors the wire shape of the hub's invocation records. The
// JSON tags are copied from invocations.Invocation in
// services/hub/internal/invocations/types.go, which lives in the hub's own Go
// module and cannot be imported from here.
//
// Building fixtures through this struct is the point: an earlier version of
// this file fed the summariser hand-built maps keyed "agent", "tokensInput",
// "tokensOutput" and "costUSD" — keys the hub has never emitted — so the tool
// skipped every invocation and the test still passed. Decoding a real
// invocation through its JSON tags makes that class of mismatch fail here
// instead of in production. If the hub's struct changes, change this too.
type hubInvocation struct {
	ID          string     `json:"id"`
	AgentName   string     `json:"agentName"`
	TriggerName string     `json:"triggerName"`
	EventID     string     `json:"eventId,omitempty"`
	Connector   string     `json:"connector,omitempty"`
	StartTime   time.Time  `json:"startTime"`
	EndTime     *time.Time `json:"endTime,omitempty"`
	DurationMs  *int64     `json:"durationMs,omitempty"`
	Status      string     `json:"status"`
	Error       string     `json:"error,omitempty"`
}

// activityResponse is the subset of the tool's output worth asserting on.
type activityResponse struct {
	Agents []struct {
		Agent        string         `json:"agent"`
		Invocations  map[string]int `json:"invocations"`
		RecentErrors []struct {
			InvocationID string `json:"invocationId"`
			Summary      string `json:"summary"`
		} `json:"recentErrors"`
	} `json:"agents"`
	Note string `json:"_note"`
}

// newActivityHub serves the given invocations in the hub's list wrapper.
func newActivityHub(t *testing.T, invs []hubInvocation) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/invocations" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"invocations": invs,
			"total":       len(invs),
		}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
}

func callSummarize(t *testing.T, hubURL string) (string, activityResponse) {
	t.Helper()
	at := &ActivityTools{HubURL: hubURL, HTTPClient: http.DefaultClient}
	result, err := at.SummarizeAgentActivity(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	text := result.Content[0].(mcp.TextContent).Text
	var out activityResponse
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, text)
	}
	return text, out
}

func TestSummarizeAgentActivity(t *testing.T) {
	start := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	hub := newActivityHub(t, []hubInvocation{
		{ID: "inv-1", AgentName: "code-reviewer", TriggerName: "pr-opened", StartTime: start, Status: "success"},
		{ID: "inv-2", AgentName: "code-reviewer", TriggerName: "pr-opened", StartTime: start, Status: "success"},
		{ID: "inv-3", AgentName: "code-reviewer", TriggerName: "pr-opened", StartTime: start, Status: "failure", Error: "forgejo 403"},
		{ID: "inv-4", AgentName: "issue-triager", TriggerName: "issue-opened", StartTime: start, Status: "running"},
		{ID: "inv-5", AgentName: "issue-triager", TriggerName: "issue-opened", StartTime: start, Status: "queued"},
		{ID: "inv-6", AgentName: "issue-triager", TriggerName: "issue-opened", StartTime: start, Status: "timeout", Error: "deadline exceeded"},
	})
	defer hub.Close()

	text, out := callSummarize(t, hub.URL)

	if len(out.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d: %s", len(out.Agents), text)
	}
	// Agents come back sorted by name.
	if out.Agents[0].Agent != "code-reviewer" || out.Agents[1].Agent != "issue-triager" {
		t.Errorf("expected sorted agents, got %q, %q", out.Agents[0].Agent, out.Agents[1].Agent)
	}

	// Each breakdown value pins the status mapping, so a hub status name that
	// drifts from what this tool counts shows up as a failing assertion.
	want := map[string]map[string]int{
		"code-reviewer": {"total": 3, "success": 2, "failure": 1, "running": 0, "queued": 0, "timeout": 0},
		"issue-triager": {"total": 3, "success": 0, "failure": 0, "running": 1, "queued": 1, "timeout": 1},
	}
	for _, a := range out.Agents {
		for status, count := range want[a.Agent] {
			if got := a.Invocations[status]; got != count {
				t.Errorf("%s: invocations[%q] = %d, want %d (%s)", a.Agent, status, got, count, text)
			}
		}
	}

	// Recent errors are collected from the invocation's own error field.
	if n := len(out.Agents[0].RecentErrors); n != 1 || out.Agents[0].RecentErrors[0].InvocationID != "inv-3" {
		t.Errorf("code-reviewer recentErrors = %+v, want one entry for inv-3", out.Agents[0].RecentErrors)
	}

	// Latency aggregation is intentionally omitted (the hub does not expose
	// p50/p95 on invocation aggregates yet); the tool surfaces a _note about
	// that limitation in its response.
	if !strings.Contains(text, "_note") {
		t.Errorf("expected _note about latency aggregation; got: %s", text)
	}
	// Token usage and cost are not invocation fields, so the note has to say
	// where they live rather than the tool implying it reports them.
	if !strings.Contains(out.Note, "get_token_usage") {
		t.Errorf("expected _note to point at get_token_usage; got: %s", out.Note)
	}
}

// TestSummarizeAgentActivityUnknownStatus covers the two shapes that used to
// vanish silently: a status outside the known set, and an invocation whose
// status is missing entirely. Both must still be counted.
func TestSummarizeAgentActivityUnknownStatus(t *testing.T) {
	start := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	hub := newActivityHub(t, []hubInvocation{
		{ID: "inv-1", AgentName: "tester", TriggerName: "cron", StartTime: start, Status: "cancelled"},
		{ID: "inv-2", AgentName: "tester", TriggerName: "cron", StartTime: start},
	})
	defer hub.Close()

	text, out := callSummarize(t, hub.URL)

	if len(out.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d: %s", len(out.Agents), text)
	}
	a := out.Agents[0]
	if a.Invocations["total"] != 2 {
		t.Errorf("total = %d, want 2 (%s)", a.Invocations["total"], text)
	}
	if a.Invocations["cancelled"] != 1 {
		t.Errorf("cancelled = %d, want 1 (%s)", a.Invocations["cancelled"], text)
	}
	if a.Invocations["unknown"] != 1 {
		t.Errorf("unknown = %d, want 1 (%s)", a.Invocations["unknown"], text)
	}
}

// TestSummarizeAgentActivityEmptyPage pins the empty result: the tool must
// return an empty agent list, not an error, and must not invent rows.
func TestSummarizeAgentActivityEmptyPage(t *testing.T) {
	hub := newActivityHub(t, []hubInvocation{})
	defer hub.Close()

	_, out := callSummarize(t, hub.URL)
	if len(out.Agents) != 0 {
		t.Errorf("expected no agents, got %+v", out.Agents)
	}
}
