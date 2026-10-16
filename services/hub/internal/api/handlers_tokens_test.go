package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ainsel-project/ainsel/services/hub/internal/prometheus"
)

// newServerWithTokensRoute wires /api/v1/tokens (the endpoint the MCP
// get_token_usage tool calls) onto a server backed by the given fake
// Prometheus client.
func newServerWithTokensRoute(t *testing.T, prom *prometheus.Client) *Server {
	t.Helper()
	s := testServer(t)
	s.prom = prom
	s.mux.HandleFunc("/api/v1/tokens", s.handleTokens)
	return s
}

// tokensResponse mirrors the {"tokens": [...], "total": {...}, "source": ...} envelope.
type tokensResponse struct {
	Tokens []TokenEntry `json:"tokens"`
	Total  TokenTotals  `json:"total"`
	Source string       `json:"source"`
}

func TestListTokens_IncludesCacheComponentsInTotals(t *testing.T) {
	srv := fakePromServer(t, func(_ string, params url.Values) interface{} {
		return vectorResponse([]vectorSample{
			{Labels: map[string]string{"agent": "dev", "repo": "ainsel-project/ainsel", "issue_id": "42", "model": "glm", "token_type": "input"}, Value: "2400"},
			{Labels: map[string]string{"agent": "dev", "repo": "ainsel-project/ainsel", "issue_id": "42", "model": "glm", "token_type": "output"}, Value: "900"},
			{Labels: map[string]string{"agent": "dev", "repo": "ainsel-project/ainsel", "issue_id": "42", "model": "glm", "token_type": "cache_read"}, Value: "30000"},
			{Labels: map[string]string{"agent": "dev", "repo": "ainsel-project/ainsel", "issue_id": "42", "model": "glm", "token_type": "cache_write"}, Value: "1200"},
		})
	})
	defer srv.Close()

	s := newServerWithTokensRoute(t, prometheus.NewClient(srv.URL, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body tokensResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Tokens) != 1 {
		t.Fatalf("expected the four token_type series merged into 1 entry, got %d (%+v)", len(body.Tokens), body.Tokens)
	}

	entry := body.Tokens[0]
	if entry.Agent != "dev" || entry.Repository != "ainsel-project/ainsel" || entry.IssueNumber != "42" || entry.Model != "glm" {
		t.Errorf("entry mislabeled: %+v", entry)
	}
	if entry.InputTokens != 2400 || entry.OutputTokens != 900 {
		t.Errorf("entry input/output wrong: %+v", entry)
	}
	if entry.CacheReadTokens != 30000 || entry.CacheWriteTokens != 1200 {
		t.Errorf("entry cache components wrong: %+v", entry)
	}
	// pi's usage.input excludes cache traffic, so a total built from
	// input+output alone would report 3300 instead of the 34500 actually used.
	if entry.TotalTokens != 34500 {
		t.Errorf("entry total must include cache components, got %v", entry.TotalTokens)
	}

	if body.Total.InputTokens != 2400 || body.Total.OutputTokens != 900 {
		t.Errorf("totals input/output wrong: %+v", body.Total)
	}
	if body.Total.CacheReadTokens != 30000 || body.Total.CacheWriteTokens != 1200 {
		t.Errorf("totals cache components wrong: %+v", body.Total)
	}
	if body.Total.TotalTokens != 34500 {
		t.Errorf("grand total must include cache components, got %v", body.Total.TotalTokens)
	}
}

func TestListTokens_MergesMultipleEntriesAndSumsTotals(t *testing.T) {
	srv := fakePromServer(t, func(_ string, params url.Values) interface{} {
		return vectorResponse([]vectorSample{
			{Labels: map[string]string{"agent": "dev", "repo": "a", "issue_id": "1", "model": "m", "token_type": "input"}, Value: "100"},
			{Labels: map[string]string{"agent": "dev", "repo": "a", "issue_id": "1", "model": "m", "token_type": "cache_read"}, Value: "900"},
			{Labels: map[string]string{"agent": "rev", "repo": "b", "issue_id": "2", "model": "m", "token_type": "output"}, Value: "50"},
		})
	})
	defer srv.Close()

	s := newServerWithTokensRoute(t, prometheus.NewClient(srv.URL, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body tokensResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Tokens) != 2 {
		t.Fatalf("expected 2 entries, got %d (%+v)", len(body.Tokens), body.Tokens)
	}
	// Grand total spans both entries: 100 + 900 + 50.
	if body.Total.TotalTokens != 1050 {
		t.Errorf("grand total = %v, want 1050", body.Total.TotalTokens)
	}
	// An entry with no cache series must report zero rather than a stale value.
	for _, e := range body.Tokens {
		if e.Agent == "rev" && (e.CacheReadTokens != 0 || e.CacheWriteTokens != 0 || e.TotalTokens != 50) {
			t.Errorf("rev entry should have no cache traffic: %+v", e)
		}
		if e.Agent == "dev" && e.TotalTokens != 1000 {
			t.Errorf("dev total = %v, want 1000", e.TotalTokens)
		}
	}
}

func TestListTokens_PassesFiltersToPromQL(t *testing.T) {
	var gotQuery string
	srv := fakePromServer(t, func(_ string, params url.Values) interface{} {
		gotQuery = params.Get("query")
		return vectorResponse(nil)
	})
	defer srv.Close()

	s := newServerWithTokensRoute(t, prometheus.NewClient(srv.URL, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens?agent=dev&repository=ainsel-project/ainsel&issueNumber=42", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{`agent="dev"`, `repo="ainsel-project/ainsel"`, `issue_id="42"`} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query missing %s: %s", want, gotQuery)
		}
	}
}

func TestListTokens_Returns503WhenPromNotConfigured(t *testing.T) {
	s := newServerWithTokensRoute(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListTokens_PinnedPrometheusWithoutURLNamesPrometheus(t *testing.T) {
	// Pinned to Prometheus with no URL: the 503 must name Prometheus, not the
	// records — a pinned install is told the exact thing it has to fix, and
	// the endpoint never silently answers from the other backend.
	s := newServerWithTokensRoute(t, nil)
	s.SetMetricsSource(MetricsSourcePrometheus)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error != promRequiredMessage {
		t.Errorf("expected %q, got %q", promRequiredMessage, body.Error)
	}
}

func TestListTokens_BadIssueNumberIs400(t *testing.T) {
	srv := fakePromServer(t, func(_ string, _ url.Values) interface{} {
		t.Error("prometheus must not be queried for a rejected request")
		return vectorResponse(nil)
	})
	defer srv.Close()

	s := newServerWithTokensRoute(t, prometheus.NewClient(srv.URL, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens?issueNumber=not-a-number", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListTokens_PromPathReportsSource(t *testing.T) {
	srv := fakePromServer(t, func(_ string, _ url.Values) interface{} {
		return vectorResponse(nil)
	})
	defer srv.Close()

	s := newServerWithTokensRoute(t, prometheus.NewClient(srv.URL, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body tokensResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Source != metricsSourcePrometheus {
		t.Errorf("source = %q, want %q", body.Source, metricsSourcePrometheus)
	}
}

func TestHandleTokens_RejectsNonGET(t *testing.T) {
	srv := fakePromServer(t, func(_ string, _ url.Values) interface{} {
		return vectorResponse(nil)
	})
	defer srv.Close()

	s := newServerWithTokensRoute(t, prometheus.NewClient(srv.URL, nil))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tokens", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- records path: GET /api/v1/tokens from the hub's own conversation rows ---

// tokensSeedConversation inserts one conversation row with the full subject
// (repo, issue_number — migration 0024) and a cost (cost_usd — migration 0023),
// the shape TokenUsageByIssue groups. Pass repo="" / issue=0 for a row written
// before migration 0024.
func tokensSeedConversation(t *testing.T, pool *pgxpool.Pool, agent, repo string, issue int,
	model string, in, out, cacheRead, cacheWrite int, cost float64, at time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO task_conversations
		 (agent_name, role, content, model, input_tokens, output_tokens,
		  cache_read_tokens, cache_write_tokens, cost_usd, repo, issue_number, created_at)
		 VALUES ($1, 'assistant', 'done', $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		agent, model, in, out, cacheRead, cacheWrite, cost, repo, issue, at); err != nil {
		t.Fatalf("seed conversation for %s: %v", agent, err)
	}
}

// tokensRecordsServer boots the no-Prometheus install shape — the hub's own
// database and nothing else — with /api/v1/tokens wired.
func tokensRecordsServer(t *testing.T, paths ...string) (*Server, *pgxpool.Pool) {
	t.Helper()
	s, pool := obsDBServer(t, paths...)
	s.mux.HandleFunc("/api/v1/tokens", s.handleTokens)
	return s, pool
}

func getTokens(t *testing.T, s *Server, query string) tokensResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens"+query, nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/tokens%s: expected 200, got %d: %s", query, rec.Code, rec.Body.String())
	}
	var body tokensResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

func tokenRowByKey(rows []TokenEntry, agent, repo, issue, model string) *TokenEntry {
	for i := range rows {
		if rows[i].Agent == agent && rows[i].Repository == repo &&
			rows[i].IssueNumber == issue && rows[i].Model == model {
			return &rows[i]
		}
	}
	return nil
}

func TestListTokens_RecordsPathWithoutPrometheus(t *testing.T) {
	// The headline acceptance: a hub with no Prometheus answers the endpoint
	// from its own rows, with cost riding along on every row and the total.
	s, pool := tokensRecordsServer(t)
	now := time.Now().UTC()

	tokensSeedConversation(t, pool, "dev", "ainsel-project/ainsel", 42, "glm", 100, 10, 500, 50, 0.5, now.Add(-10*time.Minute))
	tokensSeedConversation(t, pool, "dev", "ainsel-project/ainsel", 42, "glm", 20, 5, 0, 0, 0.25, now.Add(-9*time.Minute))
	tokensSeedConversation(t, pool, "rev", "other/repo", 7, "m", 5, 1, 0, 0, 0, now.Add(-8*time.Minute))
	// Pre-0024 row: subject unknown. It is still consumption — it must appear
	// (rendered with an empty issueNumber), never be dropped.
	tokensSeedConversation(t, pool, "legacy", "", 0, "m", 1, 1, 0, 0, 0, now.Add(-7*time.Minute))

	body := getTokens(t, s, "")

	if body.Source != metricsSourcePostgres {
		t.Errorf("source = %q, want %q", body.Source, metricsSourcePostgres)
	}
	if len(body.Tokens) != 3 {
		t.Fatalf("expected 3 grouped entries, got %d (%+v)", len(body.Tokens), body.Tokens)
	}

	dev := tokenRowByKey(body.Tokens, "dev", "ainsel-project/ainsel", "42", "glm")
	if dev == nil {
		t.Fatalf("dev row missing: %+v", body.Tokens)
	}
	if dev.InputTokens != 120 || dev.OutputTokens != 15 || dev.CacheReadTokens != 500 || dev.CacheWriteTokens != 50 {
		t.Errorf("dev components wrong: %+v", dev)
	}
	if dev.TotalTokens != 685 {
		t.Errorf("dev total = %v, want 685", dev.TotalTokens)
	}
	if dev.CostUSD != 0.75 {
		t.Errorf("dev costUsd = %v, want 0.75", dev.CostUSD)
	}

	legacy := tokenRowByKey(body.Tokens, "legacy", "", "", "m")
	if legacy == nil {
		t.Fatalf("pre-0024 row with unknown subject must be kept: %+v", body.Tokens)
	}

	if body.Total.InputTokens != 126 || body.Total.OutputTokens != 17 ||
		body.Total.CacheReadTokens != 500 || body.Total.CacheWriteTokens != 50 {
		t.Errorf("totals wrong: %+v", body.Total)
	}
	if body.Total.TotalTokens != 693 {
		t.Errorf("grand total = %v, want 693", body.Total.TotalTokens)
	}
	if body.Total.CostUSD != 0.75 {
		t.Errorf("grand costUsd = %v, want 0.75", body.Total.CostUSD)
	}
}

func TestListTokens_RecordsTotalEqualsSummaryTile(t *testing.T) {
	// The asymmetry that motivated the issue: the console tile and the
	// agent-facing endpoint must not disagree. Same rows, same window — the
	// totals must be equal, cost included.
	s, pool := tokensRecordsServer(t, "/api/v1/observability/metrics/tokens/summary")
	now := time.Now().UTC()

	tokensSeedConversation(t, pool, "dev", "ainsel-project/ainsel", 42, "glm", 100, 10, 500, 50, 0.5, now.Add(-30*time.Minute))
	tokensSeedConversation(t, pool, "rev", "other/repo", 7, "m", 5, 1, 0, 0, 0.25, now.Add(-20*time.Minute))

	body := getTokens(t, s, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/observability/metrics/tokens/summary?range=24h", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("summary: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var summary TokensSummary
	if err := json.NewDecoder(rec.Body).Decode(&summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if summary.Source != metricsSourcePostgres {
		t.Fatalf("summary not served from records: %+v", summary)
	}

	if body.Total.TotalTokens != summary.TotalTokens {
		t.Errorf("endpoint total %v != summary tile total %v", body.Total.TotalTokens, summary.TotalTokens)
	}
	if body.Total.CostUSD != summary.CostUSD {
		t.Errorf("endpoint costUsd %v != summary tile costUsd %v", body.Total.CostUSD, summary.CostUSD)
	}
}

func TestListTokens_RecordsFiltersAreServerSide(t *testing.T) {
	s, pool := tokensRecordsServer(t)
	now := time.Now().UTC()

	tokensSeedConversation(t, pool, "dev", "ainsel-project/ainsel", 42, "glm", 100, 10, 0, 0, 0, now.Add(-10*time.Minute))
	tokensSeedConversation(t, pool, "rev", "other/repo", 7, "m", 5, 1, 0, 0, 0, now.Add(-9*time.Minute))
	// Unlabelled row: an issue filter must not sweep it in, and an
	// issueNumber=0 filter must find exactly it.
	tokensSeedConversation(t, pool, "legacy", "", 0, "m", 1, 1, 0, 0, 0, now.Add(-8*time.Minute))

	if body := getTokens(t, s, "?agent=dev"); len(body.Tokens) != 1 || body.Tokens[0].Agent != "dev" {
		t.Errorf("?agent=dev: %+v", body.Tokens)
	}
	if body := getTokens(t, s, "?repository=other/repo"); len(body.Tokens) != 1 || body.Tokens[0].Repository != "other/repo" {
		t.Errorf("?repository=other/repo: %+v", body.Tokens)
	}
	if body := getTokens(t, s, "?issueNumber=42"); len(body.Tokens) != 1 || body.Tokens[0].IssueNumber != "42" {
		t.Errorf("?issueNumber=42 must exclude the unlabelled row: %+v", body.Tokens)
	}
	if body := getTokens(t, s, "?issueNumber=0"); len(body.Tokens) != 1 || body.Tokens[0].Agent != "legacy" {
		t.Errorf("?issueNumber=0 must select only the unlabelled row: %+v", body.Tokens)
	}
	// No filter: all three rows, including the unlabelled one.
	if body := getTokens(t, s, ""); len(body.Tokens) != 3 {
		t.Errorf("no filter: %+v", body.Tokens)
	}
}

func TestListTokens_RecordsHonourSinceUntil(t *testing.T) {
	// The PromQL path reads cumulative counters and has no window; the records
	// path makes since/until real. Rows outside [since, until) must not count.
	s, pool := tokensRecordsServer(t)
	now := time.Now().UTC()

	tokensSeedConversation(t, pool, "dev", "r", 1, "m", 100, 0, 0, 0, 1, now.Add(-3*time.Hour))
	tokensSeedConversation(t, pool, "dev", "r", 1, "m", 7, 0, 0, 0, 0.5, now.Add(-10*time.Minute))

	body := getTokens(t, s, "?since="+url.QueryEscape(now.Add(-time.Hour).Format(time.RFC3339)))
	if len(body.Tokens) != 1 {
		t.Fatalf("expected the window to collapse both rows into one entry, got %+v", body.Tokens)
	}
	if body.Tokens[0].InputTokens != 7 {
		t.Errorf("since: input = %v, want 7 (the old row must be outside the window)", body.Tokens[0].InputTokens)
	}
	if body.Total.CostUSD != 0.5 {
		t.Errorf("since: costUsd = %v, want 0.5", body.Total.CostUSD)
	}

	body = getTokens(t, s, "?until="+url.QueryEscape(now.Add(-time.Hour).Format(time.RFC3339)))
	if body.Total.InputTokens != 100 {
		t.Errorf("until: input = %v, want 100 (only the old row is before the bound)", body.Total.InputTokens)
	}
}

func TestListTokens_RecordsRejectsBadSince(t *testing.T) {
	s, _ := tokensRecordsServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens?since=yesterday", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListTokens_PinnedPrometheusNeverAnswersFromRecords(t *testing.T) {
	// Pinned to Prometheus, database present, no Prometheus URL: still 503
	// naming Prometheus. A pinned install is never silently answered from the
	// records — that is the shared selector rule, not a third vocabulary.
	s, _ := tokensRecordsServer(t)
	s.SetMetricsSource(MetricsSourcePrometheus)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error != promRequiredMessage {
		t.Errorf("expected %q, got %q", promRequiredMessage, body.Error)
	}
}
