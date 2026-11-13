package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// A window is a wall clock in its own zone, so a test that means "open now" has to read
// the clock there too: deriving HH:MM in the runner's zone puts a Europe/Berlin window
// an hour off on a UTC runner, which is a flake and not a finding. Minutes are
// truncated - the window is HH:MM, and a partial minute would make "open" depend on the
// second the request landed in. The boundary arithmetic itself is covered in
// shared/api, where the clock is fixed.
func zoneClock(tz string) time.Time {
	t := time.Now().UTC()
	if tz != "" && tz != "UTC" {
		if loc, err := time.LoadLocation(tz); err == nil {
			t = t.In(loc)
		}
	}
	return t.Truncate(time.Minute)
}

// openWindow is the hour either side of the current hour in tz, clamped inside one day:
// a window that wraps belongs to the day that started it, and "open now" should not
// depend on which day that was.
func openWindow(tz string) (string, string) {
	h := zoneClock(tz).Hour()
	switch h {
	case 0:
		return "00:00", "01:00"
	case 23:
		return "22:00", "23:59"
	}
	return fmt.Sprintf("%02d:00", h-1), fmt.Sprintf("%02d:00", h+1)
}

// closedWindow is the next full hour onward in tz. It can roll into tomorrow, which is
// still closed right now.
func closedWindow(tz string) (string, string) {
	h := zoneClock(tz).Hour()
	return fmt.Sprintf("%02d:00", (h+1)%24), fmt.Sprintf("%02d:00", (h+2)%24)
}

// everyDay keeps a window's openness off the calendar.
var everyDay = []int32{0, 1, 2, 3, 4, 5, 6}

// hoursTestServer is a hub with the agent routes mounted once - the mux panics on a
// duplicate pattern, so the helpers below must not register anything themselves.
func hoursTestServer(t *testing.T) *Server {
	t.Helper()
	s := testServer(t, testAgentImage("img-1", "git"))
	s.mux.HandleFunc("/api/v1/agents", s.handleAgents)
	s.mux.HandleFunc("/api/v1/agents/", s.handleAgent)
	return s
}

func workingHoursTestAgent(t *testing.T, s *Server, name string, wh *agentv1alpha1.AgentWorkingHours) *agentv1alpha1.Agent {
	t.Helper()
	agent := &agentv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: s.ns},
		Spec: agentv1alpha1.AgentSpec{
			DisplayName:  name,
			ImageRef:     agentv1alpha1.AgentImageRef{Name: "img-1"},
			WorkingHours: wh,
		},
	}
	if err := s.client.Create(context.Background(), agent); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return agent
}

func createAgentWithHours(t *testing.T, s *Server, name string, info *AgentWorkingHoursInfo) SimpleAgentResponse {
	t.Helper()
	body, _ := json.Marshal(SimpleAgentCreateRequest{
		Name:         name,
		ImageRef:     AgentImageRefInfo{Name: "img-1"},
		LLM:          AgentLLMInfo{Model: "glm-5.1:cloud", Provider: "ollama-cloud"},
		WorkingHours: info,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var out SimpleAgentResponse
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func updateAgentHours(t *testing.T, s *Server, id string, info *AgentWorkingHoursInfo) (int, SimpleAgentResponse, string) {
	t.Helper()
	body, _ := json.Marshal(SimpleAgentUpdateRequest{WorkingHours: info})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	// Read the body before decoding it: the decoder consumes it, and the raw text is
	// what a rejected request is judged on.
	raw := rec.Body.String()
	var out SimpleAgentResponse
	_ = json.Unmarshal([]byte(raw), &out)
	return rec.Code, out, raw
}

// An agent with no window may be handed work at any hour. This is every agent that
// existed before the field did, so it must not read as closed.
func TestWorkingHours_NoWindowMeansAlwaysOpen(t *testing.T) {
	s := hoursTestServer(t)
	created := createAgentWithHours(t, s, "always-on", nil)

	if !created.HoursOpen {
		t.Error("HoursOpen = false for an agent with no window, want true")
	}
	if created.WorkingHours != nil {
		t.Errorf("workingHours = %+v, want absent", created.WorkingHours)
	}
	if created.HoursClosesAt != "" || created.HoursNextOpenAt != "" {
		t.Errorf("closesAt=%q nextOpenAt=%q, want both empty with no window", created.HoursClosesAt, created.HoursNextOpenAt)
	}
}

// The window is echoed back and the open/closed answer is computed at read time, so
// the console can show "open until 18:00" or "opens Mon 09:00" without redoing the
// timezone arithmetic itself.
func TestWorkingHours_CreateEchoesWindowAndReportsOpenness(t *testing.T) {
	s := hoursTestServer(t)

	start, end := openWindow("Europe/Berlin")
	open := createAgentWithHours(t, s, "day-worker", &AgentWorkingHoursInfo{
		Timezone: "Europe/Berlin",
		Days:     everyDay,
		Start:    start,
		End:      end,
	})
	if open.WorkingHours == nil {
		t.Fatal("workingHours absent after create")
	}
	if open.WorkingHours.Timezone != "Europe/Berlin" || open.WorkingHours.Start != start || open.WorkingHours.End != end {
		t.Errorf("window = %+v, want %s-%s in Europe/Berlin", open.WorkingHours, start, end)
	}
	if len(open.WorkingHours.Days) != 7 {
		t.Errorf("days = %v, want all seven", open.WorkingHours.Days)
	}
	if !open.HoursOpen {
		t.Errorf("HoursOpen = false inside the window (closes %q), want true", open.HoursClosesAt)
	}
	if open.HoursClosesAt == "" {
		t.Error("HoursClosesAt empty while open; the console has nothing to show")
	}
	if open.HoursNextOpenAt != "" {
		t.Errorf("HoursNextOpenAt = %q while open, want empty", open.HoursNextOpenAt)
	}
	if closesAt, err := time.Parse(time.RFC3339, open.HoursClosesAt); err != nil {
		t.Errorf("HoursClosesAt %q is not RFC3339: %v", open.HoursClosesAt, err)
	} else if !closesAt.After(time.Now()) {
		t.Errorf("HoursClosesAt = %s, want a moment in the future", closesAt)
	}

	// A second hub: "now" may have crossed a minute boundary since the first window
	// was written, and a closed window wants a clean start.
	s2 := hoursTestServer(t)
	closedStart, closedEnd := closedWindow("UTC")
	closed := createAgentWithHours(t, s2, "nights-out", &AgentWorkingHoursInfo{
		Timezone: "UTC",
		Days:     everyDay,
		Start:    closedStart,
		End:      closedEnd,
	})
	if closed.HoursOpen {
		t.Error("HoursOpen = true outside the window, want false")
	}
	if closed.HoursNextOpenAt == "" {
		t.Error("HoursNextOpenAt empty while closed; that is when the backlog starts moving")
	}
	if opensAt, err := time.Parse(time.RFC3339, closed.HoursNextOpenAt); err != nil {
		t.Errorf("HoursNextOpenAt %q is not RFC3339: %v", closed.HoursNextOpenAt, err)
	} else if !opensAt.After(time.Now()) {
		t.Errorf("HoursNextOpenAt = %s, want a moment in the future", opensAt)
	}
	if closed.HoursClosesAt != "" {
		t.Errorf("HoursClosesAt = %q while closed, want empty", closed.HoursClosesAt)
	}
}

// A window the hub cannot evaluate is a 400 at the door. The alternative is a
// schedule that fails open at delivery time, where the only evidence is a log line.
func TestWorkingHours_UpdateRejectsUnusableWindow(t *testing.T) {
	s := hoursTestServer(t)
	created := createAgentWithHours(t, s, "typo-watch", nil)

	cases := []struct {
		name string
		info *AgentWorkingHoursInfo
		want string
	}{
		{"unknown zone", &AgentWorkingHoursInfo{Timezone: "Mars/Olympus", Start: "09:00", End: "17:00"}, "unknown timezone"},
		{"empty start", &AgentWorkingHoursInfo{Timezone: "UTC", Start: "", End: "17:00"}, "not HH:MM"},
		{"start equals end", &AgentWorkingHoursInfo{Timezone: "UTC", Start: "09:00", End: "09:00"}, "no window at all"},
		{"day out of range", &AgentWorkingHoursInfo{Timezone: "UTC", Days: []int32{9}, Start: "09:00", End: "17:00"}, "out of range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _, body := updateAgentHours(t, s, created.ID, tc.info)
			if code != http.StatusBadRequest {
				t.Fatalf("code = %d (%s), want 400", code, body)
			}
			if !strings.Contains(body, tc.want) {
				t.Errorf("error does not mention %q: %s", tc.want, body)
			}
		})
	}

	// The rejected writes must not have left a window behind: a failed update leaves
	// the agent as it was, running at any hour.
	_, after, _ := updateAgentHours(t, s, created.ID, nil)
	if after.WorkingHours != nil {
		t.Errorf("workingHours = %+v after rejected updates, want still absent", after.WorkingHours)
	}
	if !after.HoursOpen {
		t.Error("agent reads closed after a rejected window, want open")
	}
}

// Days come back as written - including a list the console would not offer, which is
// what a kubectl or GitOps write looks like. Openness is not asserted here: this is
// about the echo, and it must not depend on the day of the week.
func TestWorkingHours_DaysRoundTripAsWritten(t *testing.T) {
	s := hoursTestServer(t)

	created := createAgentWithHours(t, s, "weekdays-only", &AgentWorkingHoursInfo{
		Timezone: "Europe/Berlin",
		Days:     []int32{1, 2, 3, 4, 5},
		Start:    "09:00",
		End:      "17:00",
	})
	if created.WorkingHours == nil {
		t.Fatal("workingHours absent after create")
	}
	got := created.WorkingHours.Days
	if len(got) != 5 || got[0] != 1 || got[4] != 5 {
		t.Errorf("days = %v, want [1 2 3 4 5] back in order", got)
	}
	if created.WorkingHours.Start != "09:00" || created.WorkingHours.End != "17:00" {
		t.Errorf("clock = %q-%q, want 09:00-17:00", created.WorkingHours.Start, created.WorkingHours.End)
	}
}

// Absent means leave unchanged, present-but-empty means remove the window - the same
// patch convention the env overrides use, so the console has a way back to "any hour".
func TestWorkingHours_UpdateClearsWindow(t *testing.T) {
	s := hoursTestServer(t)
	openStart, openEnd := openWindow("Europe/Berlin")
	created := createAgentWithHours(t, s, "has-hours", &AgentWorkingHoursInfo{
		Timezone: "Europe/Berlin",
		Days:     everyDay,
		Start:    openStart,
		End:      openEnd,
	})
	if created.WorkingHours == nil {
		t.Fatal("window absent after create")
	}

	// Absent: unchanged.
	code, unchanged, _ := updateAgentHours(t, s, created.ID, nil)
	if code != http.StatusOK {
		t.Fatalf("code = %d, want 200", code)
	}
	if unchanged.WorkingHours == nil || unchanged.WorkingHours.Timezone != "Europe/Berlin" {
		t.Errorf("workingHours = %+v after an update that did not mention it, want it left alone", unchanged.WorkingHours)
	}

	// Present but blank: cleared.
	code, cleared, _ := updateAgentHours(t, s, created.ID, &AgentWorkingHoursInfo{})
	if code != http.StatusOK {
		t.Fatalf("code = %d, want 200", code)
	}
	if cleared.WorkingHours != nil {
		t.Errorf("workingHours = %+v after clearing, want absent", cleared.WorkingHours)
	}
	if !cleared.HoursOpen {
		t.Error("HoursOpen = false after clearing the window, want true")
	}
}

// setWorkingHours rewrites the window on the CR, which is how a schedule changes in
// practice - kubectl, GitOps, the console - and so, here, how a window that is shut
// right now is made to open.
func setWorkingHours(t *testing.T, s *Server, name string, wh *agentv1alpha1.AgentWorkingHours) {
	t.Helper()
	ctx := context.Background()
	var agent agentv1alpha1.Agent
	if err := s.client.Get(ctx, types.NamespacedName{Name: name, Namespace: s.ns}, &agent); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	agent.Spec.WorkingHours = wh
	if err := s.client.Update(ctx, &agent); err != nil {
		t.Fatalf("update agent: %v", err)
	}
}

func getAgentHours(t *testing.T, s *Server, id string) SimpleAgentResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+id, nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out SimpleAgentResponse
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// Nothing is told when a window opens: no controller, no timer, no restart. Both the
// report a console reads and the decision the queue signal makes are computed from the
// CR at the moment of the question, so an edit to the schedule is the whole event -
// this asserts the report half of that, across a real CR update.
func TestWorkingHours_OpeningTheWindowFlipsTheReportedState(t *testing.T) {
	s := hoursTestServer(t)
	name := "hours-flip"
	shutStart, shutEnd := closedWindow("UTC")
	workingHoursTestAgent(t, s, name, &agentv1alpha1.AgentWorkingHours{
		Timezone: "UTC",
		Days:     everyDay,
		Start:    shutStart,
		End:      shutEnd,
	})

	shut := getAgentHours(t, s, name)
	if shut.HoursOpen || shut.HoursNextOpenAt == "" || shut.HoursClosesAt != "" {
		t.Fatalf("closed window: open=%v closes=%q nextOpen=%q, want closed with a next-open time",
			shut.HoursOpen, shut.HoursClosesAt, shut.HoursNextOpenAt)
	}

	openStart, openEnd := openWindow("UTC")
	setWorkingHours(t, s, name, &agentv1alpha1.AgentWorkingHours{
		Timezone: "UTC",
		Days:     everyDay,
		Start:    openStart,
		End:      openEnd,
	})

	open := getAgentHours(t, s, name)
	if !open.HoursOpen || open.HoursClosesAt == "" || open.HoursNextOpenAt != "" {
		t.Fatalf("open window: open=%v closes=%q nextOpen=%q, want open with a closing time",
			open.HoursOpen, open.HoursClosesAt, open.HoursNextOpenAt)
	}
}
