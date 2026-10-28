package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/invocations"
	"github.com/ainsel-project/ainsel/services/hub/internal/invocations/invocationstest"
)

func TestInvocations_ListReturnsAll(t *testing.T) {
	s := testServer(t)
	s.mux.HandleFunc("/api/v1/invocations", s.handleInvocations)
	s.mux.HandleFunc("/api/v1/invocations/", s.handleInvocation)

	now := time.Now().UTC()
	a := s.invocations.Record(invocations.Invocation{AgentName: "agent-a", TriggerName: "t1", StartTime: now.Add(-2 * time.Minute)})
	b := s.invocations.Record(invocations.Invocation{AgentName: "agent-b", TriggerName: "t2", StartTime: now.Add(-1 * time.Minute)})
	s.invocations.Complete(a.ID, invocations.StatusSuccess, "", now)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/invocations", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Invocations []invocations.Invocation `json:"invocations"`
		Total       int                      `json:"total"`
		Capacity    int                      `json:"capacity"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Total != 2 {
		t.Errorf("expected 2 invocations, got %d", resp.Total)
	}
	if resp.Capacity != 100 {
		t.Errorf("expected capacity 100, got %d", resp.Capacity)
	}
	// b is newer, should come first.
	if len(resp.Invocations) > 0 && resp.Invocations[0].ID != b.ID {
		t.Errorf("expected newest first (b=%s), got %s", b.ID, resp.Invocations[0].ID)
	}
}

func TestInvocations_ListFilters(t *testing.T) {
	s := testServer(t)
	s.mux.HandleFunc("/api/v1/invocations", s.handleInvocations)

	now := time.Now().UTC()
	a := s.invocations.Record(invocations.Invocation{AgentName: "agent-a", TriggerName: "t1", StartTime: now.Add(-2 * time.Minute)})
	s.invocations.Record(invocations.Invocation{AgentName: "agent-b", TriggerName: "t2", StartTime: now.Add(-1 * time.Minute)})
	s.invocations.Complete(a.ID, invocations.StatusFailure, "boom", now)

	cases := []struct {
		query    string
		wantPage int // length of the returned page
	}{
		{"agent=agent-a", 1},
		{"agent=agent-b", 1},
		{"agent=missing", 0},
		{"status=failure", 1},
		{"status=running", 1},
		// pageSize=1 returns one item on page 1; total still reflects all
		// matching records so the frontend can show "1 of 2".
		{"pageSize=1", 1},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/invocations?"+c.query, nil)
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("[%s] expected 200, got %d: %s", c.query, rec.Code, rec.Body.String())
		}
		var resp struct {
			Invocations []invocations.Invocation `json:"invocations"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if len(resp.Invocations) != c.wantPage {
			t.Errorf("[%s] expected %d items on page, got %d", c.query, c.wantPage, len(resp.Invocations))
		}
	}
}

func TestInvocations_GetByID(t *testing.T) {
	s := testServer(t)
	s.mux.HandleFunc("/api/v1/invocations", s.handleInvocations)
	s.mux.HandleFunc("/api/v1/invocations/", s.handleInvocation)

	rec0 := s.invocations.Record(invocations.Invocation{AgentName: "agent-a", TriggerName: "t1"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/invocations/"+rec0.ID, nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got invocations.Invocation
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != rec0.ID {
		t.Errorf("expected ID %s, got %s", rec0.ID, got.ID)
	}
	if got.AgentName != "agent-a" {
		t.Errorf("expected agent-a, got %s", got.AgentName)
	}
}

func TestInvocations_ListFilterByTrigger(t *testing.T) {
	s := testServer(t)
	s.mux.HandleFunc("/api/v1/invocations", s.handleInvocations)

	now := time.Now().UTC()
	s.invocations.Record(invocations.Invocation{AgentName: "agent-a", TriggerName: "trigger-1", StartTime: now.Add(-2 * time.Minute)})
	s.invocations.Record(invocations.Invocation{AgentName: "agent-b", TriggerName: "trigger-2", StartTime: now.Add(-1 * time.Minute)})
	s.invocations.Record(invocations.Invocation{AgentName: "agent-a", TriggerName: "trigger-1", StartTime: now})

	cases := []struct {
		query    string
		wantPage int
	}{
		{"trigger=trigger-1", 2},
		{"trigger=trigger-2", 1},
		{"trigger=missing", 0},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/invocations?"+c.query, nil)
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("[%s] expected 200, got %d: %s", c.query, rec.Code, rec.Body.String())
		}
		var resp struct {
			Invocations []invocations.Invocation `json:"invocations"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if len(resp.Invocations) != c.wantPage {
			t.Errorf("[%s] expected %d items, got %d", c.query, c.wantPage, len(resp.Invocations))
		}
	}
}

func TestInvocations_ListFilterByUntil(t *testing.T) {
	s := testServer(t)
	s.mux.HandleFunc("/api/v1/invocations", s.handleInvocations)

	now := time.Now().UTC()
	s.invocations.Record(invocations.Invocation{AgentName: "old", StartTime: now.Add(-30 * time.Minute)})
	s.invocations.Record(invocations.Invocation{AgentName: "mid", StartTime: now.Add(-10 * time.Minute)})
	s.invocations.Record(invocations.Invocation{AgentName: "recent", StartTime: now.Add(-1 * time.Minute)})

	// until 15 minutes ago should return only "old"
	until := now.Add(-15 * time.Minute).Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/invocations?until="+until, nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Invocations []invocations.Invocation `json:"invocations"`
		Total       int                      `json:"total"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Total != 1 {
		t.Errorf("expected 1 item for until filter, got %d", resp.Total)
	}
	if len(resp.Invocations) > 0 && resp.Invocations[0].AgentName != "old" {
		t.Errorf("expected agent 'old', got %q", resp.Invocations[0].AgentName)
	}
}

func TestInvocations_ListFilterBySinceAndUntil(t *testing.T) {
	s := testServer(t)
	s.mux.HandleFunc("/api/v1/invocations", s.handleInvocations)

	now := time.Now().UTC()
	s.invocations.Record(invocations.Invocation{AgentName: "too-early", StartTime: now.Add(-60 * time.Minute)})
	s.invocations.Record(invocations.Invocation{AgentName: "in-window", StartTime: now.Add(-20 * time.Minute)})
	s.invocations.Record(invocations.Invocation{AgentName: "too-late", StartTime: now.Add(-5 * time.Minute)})

	// between 30m and 10m ago should only match "in-window"
	since := now.Add(-30 * time.Minute).Format(time.RFC3339)
	until := now.Add(-10 * time.Minute).Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/invocations?since="+since+"&until="+until, nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Invocations []invocations.Invocation `json:"invocations"`
		Total       int                      `json:"total"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Total != 1 {
		t.Errorf("expected 1 item in time window, got %d", resp.Total)
	}
	if len(resp.Invocations) > 0 && resp.Invocations[0].AgentName != "in-window" {
		t.Errorf("expected agent 'in-window', got %q", resp.Invocations[0].AgentName)
	}
}

func TestInvocations_ListFilterByLimit(t *testing.T) {
	s := testServer(t)
	s.mux.HandleFunc("/api/v1/invocations", s.handleInvocations)

	now := time.Now().UTC()
	s.invocations.Record(invocations.Invocation{AgentName: "agent-a", StartTime: now.Add(-3 * time.Minute)})
	s.invocations.Record(invocations.Invocation{AgentName: "agent-b", StartTime: now.Add(-2 * time.Minute)})
	s.invocations.Record(invocations.Invocation{AgentName: "agent-c", StartTime: now.Add(-1 * time.Minute)})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/invocations?limit=2", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Invocations []invocations.Invocation `json:"invocations"`
		Total       int                      `json:"total"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Total != 3 {
		t.Errorf("expected total 3 (pre-limit match count), got %d", resp.Total)
	}
	if len(resp.Invocations) != 2 {
		t.Errorf("expected exactly 2 invocations with limit=2, got %d", len(resp.Invocations))
	}
}

func TestInvocations_ListCombinedFilters(t *testing.T) {
	s := testServer(t)
	s.mux.HandleFunc("/api/v1/invocations", s.handleInvocations)

	now := time.Now().UTC()
	a := s.invocations.Record(invocations.Invocation{AgentName: "agent-a", TriggerName: "t1", StartTime: now.Add(-60 * time.Minute)})
	s.invocations.Record(invocations.Invocation{AgentName: "agent-b", TriggerName: "t1", StartTime: now.Add(-30 * time.Minute)})
	s.invocations.Complete(a.ID, invocations.StatusSuccess, "", now)

	// Filter: trigger=t1 + status=success → only a
	req := httptest.NewRequest(http.MethodGet, "/api/v1/invocations?trigger=t1&status=success", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Invocations []invocations.Invocation `json:"invocations"`
		Total       int                      `json:"total"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Total != 1 {
		t.Errorf("expected 1 item for trigger=t1+status=success, got %d", resp.Total)
	}
}

func TestInvocations_GetUnknownIDReturns404(t *testing.T) {
	s := testServer(t)
	s.mux.HandleFunc("/api/v1/invocations/", s.handleInvocation)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/invocations/inv-does-not-exist", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestInvocations_NotConfiguredReturns503(t *testing.T) {
	s := testServer(t)
	s.invocations = nil
	s.mux.HandleFunc("/api/v1/invocations", s.handleInvocations)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/invocations", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

func TestInvocations_RejectsNonGet(t *testing.T) {
	s := testServer(t)
	s.mux.HandleFunc("/api/v1/invocations", s.handleInvocations)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/invocations", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestInvocations_ListFilterByEvent(t *testing.T) {
	s := testServer(t)
	s.mux.HandleFunc("/api/v1/invocations", s.handleInvocations)

	now := time.Now().UTC()
	s.invocations.Record(invocations.Invocation{AgentName: "agent-a", EventID: "evt-1", StartTime: now.Add(-3 * time.Minute)})
	s.invocations.Record(invocations.Invocation{AgentName: "agent-b", EventID: "evt-2", StartTime: now.Add(-2 * time.Minute)})
	s.invocations.Record(invocations.Invocation{AgentName: "agent-c", EventID: "evt-1", StartTime: now.Add(-1 * time.Minute)})

	cases := []struct {
		query    string
		wantPage int
	}{
		{"event=evt-1", 2},
		{"event=evt-2", 1},
		{"event=missing", 0},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/invocations?"+c.query, nil)
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("[%s] expected 200, got %d: %s", c.query, rec.Code, rec.Body.String())
		}
		var resp struct {
			Invocations []invocations.Invocation `json:"invocations"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if len(resp.Invocations) != c.wantPage {
			t.Errorf("[%s] expected %d items, got %d", c.query, c.wantPage, len(resp.Invocations))
		}
	}
}

// recordingInvocationStore captures the ListOptions the handler builds, so a
// test can assert on the filter the handler produced and not only on the rows
// that came back from it.
type recordingInvocationStore struct {
	invocations.Store
	opts []invocations.ListOptions
}

func (s *recordingInvocationStore) ListWithTotal(opts invocations.ListOptions) ([]invocations.Invocation, int) {
	s.opts = append(s.opts, opts)
	return s.Store.ListWithTotal(opts)
}

// TestInvocations_ListFilterByConnector covers the connector query parameter in
// both spellings clients use — repeated and comma-separated.
func TestInvocations_ListFilterByConnector(t *testing.T) {
	for _, query := range []string{"connector=c-1&connector=c-2", "connector=c-1,c-2"} {
		s := testServer(t)
		store := &recordingInvocationStore{Store: invocationstest.New(100)}
		s.invocations = store
		s.mux.HandleFunc("/api/v1/invocations", s.handleInvocations)

		now := time.Now().UTC()
		store.Record(invocations.Invocation{
			AgentName: "agent-a",
			Connector: "c-1",
			StartTime: now.Add(-3 * time.Minute),
		})
		store.Record(invocations.Invocation{
			AgentName: "agent-b",
			Connector: "c-2",
			StartTime: now.Add(-2 * time.Minute),
		})
		store.Record(invocations.Invocation{
			AgentName: "agent-c",
			Connector: "c-3",
			StartTime: now.Add(-1 * time.Minute),
		})

		req := httptest.NewRequest(http.MethodGet, "/api/v1/invocations?"+query, nil)
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("[%s] expected 200, got %d: %s", query, rec.Code, rec.Body.String())
		}
		if len(store.opts) != 1 {
			t.Fatalf("[%s] expected 1 ListWithTotal call, got %d", query, len(store.opts))
		}
		if got := store.opts[0].Connectors; !slices.Equal(got, []string{"c-1", "c-2"}) {
			t.Errorf("[%s] ListOptions.Connectors = %v, want [c-1 c-2]", query, got)
		}

		var resp struct {
			Invocations []struct {
				AgentName string `json:"agentName"`
			} `json:"invocations"`
			Total int `json:"total"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("[%s] decode: %v", query, err)
		}
		if resp.Total != 2 || len(resp.Invocations) != 2 {
			t.Fatalf("[%s] expected 2 filtered invocations, got %d (total %d)", query, len(resp.Invocations), resp.Total)
		}
		for _, inv := range resp.Invocations {
			if inv.AgentName == "agent-c" {
				t.Errorf("[%s] c-3 row survived the connector filter", query)
			}
		}
	}
}

// TestInvocations_ConnectorCap covers the per-request cap on connector filter
// values: exactly maxFilterValues unique connectors is accepted, one more is
// rejected with 400 naming the max, and the cap is applied after
// comma-splitting/trimming/dedup so mixed spellings cannot smuggle a request
// past it.
func TestInvocations_ConnectorCap(t *testing.T) {
	unique := func(n int) []string {
		out := make([]string, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, fmt.Sprintf("c-%d", i))
		}
		return out
	}
	// Mixed spellings: first half comma-separated in one parameter, second
	// half as repeated parameters — together one unique value over the cap.
	over := unique(maxFilterValues + 1)
	mixedOverCap := "connector=" + strings.Join(over[:maxFilterValues/2], ",") +
		"&connector=" + strings.Join(over[maxFilterValues/2:], "&connector=")

	cases := []struct {
		name     string
		query    string
		wantCode int
	}{
		{
			name:     "exactly at cap",
			query:    "connector=" + strings.Join(unique(maxFilterValues), ","),
			wantCode: http.StatusOK,
		},
		{
			name:     "one over cap",
			query:    "connector=" + strings.Join(unique(maxFilterValues+1), ","),
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "mixed spellings over cap",
			query:    mixedOverCap,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "duplicates above raw count dedup under cap",
			query:    "connector=" + strings.Join(append(unique(maxFilterValues), "c-0"), ","),
			wantCode: http.StatusOK,
		},
		{
			name:     "all blanks stay filter off",
			query:    "connector=,,&connector=+",
			wantCode: http.StatusOK,
		},
	}
	for _, c := range cases {
		s := testServer(t)
		store := &recordingInvocationStore{Store: invocationstest.New(100)}
		s.invocations = store
		s.mux.HandleFunc("/api/v1/invocations", s.handleInvocations)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/invocations?"+c.query, nil)
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, req)

		if rec.Code != c.wantCode {
			t.Fatalf("[%s] expected %d, got %d: %s", c.name, c.wantCode, rec.Code, rec.Body.String())
		}
		if c.wantCode != http.StatusOK {
			if !strings.Contains(rec.Body.String(), strconv.Itoa(maxFilterValues)) {
				t.Errorf("[%s] 400 body %q should mention the max (%d)", c.name, rec.Body.String(), maxFilterValues)
			}
			if len(store.opts) != 0 {
				t.Errorf("[%s] store must not be queried when the request is rejected", c.name)
			}
			continue
		}
		if len(store.opts) != 1 {
			t.Fatalf("[%s] expected 1 ListWithTotal call, got %d", c.name, len(store.opts))
		}
		if strings.HasPrefix(c.query, "connector=,,") && store.opts[0].Connectors != nil {
			t.Errorf("[%s] blank connectors should leave the filter off, got %v", c.name, store.opts[0].Connectors)
		}
	}
}

// TestInvocations_LimitClampedToMax asserts an oversized `limit` is clamped
// rather than rejected: a smaller limit still returns correct results, but an
// unbounded one makes the store materialize the whole table.
func TestInvocations_LimitClampedToMax(t *testing.T) {
	s := testServer(t)
	store := &recordingInvocationStore{Store: invocationstest.New(100)}
	s.invocations = store
	s.mux.HandleFunc("/api/v1/invocations", s.handleInvocations)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/invocations?limit=100000", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(store.opts) != 1 {
		t.Fatalf("expected 1 ListWithTotal call, got %d", len(store.opts))
	}
	if got := store.opts[0].Limit; got != maxInvocationLimit {
		t.Errorf("ListOptions.Limit = %d, want clamped to %d", got, maxInvocationLimit)
	}

	// A limit at the cap passes through untouched.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/invocations?limit=500", nil)
	rec = httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := store.opts[1].Limit; got != maxInvocationLimit {
		t.Errorf("ListOptions.Limit = %d, want %d", got, maxInvocationLimit)
	}
}
