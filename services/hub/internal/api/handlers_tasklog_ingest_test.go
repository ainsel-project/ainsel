package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/ainsel-project/ainsel/services/hub/internal/db"
	"github.com/ainsel-project/ainsel/services/hub/internal/tasklogs"
)

// Migration 0032 adds CHECK constraints on the enum-like observability columns.
// The task-log ingest endpoint decodes the request body straight into the row,
// so it is the one write path where the constrained value is caller-supplied:
// these tests pin that a bad level is a 400 at the handler (not a 500 from the
// constraint) and that the constraint itself is actually present in the schema.
// Skips when Docker is unavailable, like the other integration suites here.

func ingestDBServer(t *testing.T) (*Server, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	c, err := pgcontainer.Run(ctx, "postgres:17-alpine",
		pgcontainer.WithDatabase("ainsel_test"),
		pgcontainer.WithUsername("test"),
		pgcontainer.WithPassword("test"),
		pgcontainer.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = c.Terminate(ctx)
		t.Fatalf("connection string: %v", err)
	}
	if err := db.Migrate(ctx, dsn); err != nil {
		_ = c.Terminate(ctx)
		t.Fatalf("migrate: %v", err)
	}
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		_ = c.Terminate(ctx)
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		_ = c.Terminate(context.Background())
	})

	s := testServer(t)
	s.SetTaskLogStore(tasklogs.NewStore(pool))
	s.internalValidateSecret = "sekret"
	s.mux.HandleFunc("/api/internal/task-logs", s.handleIngestTaskLog)
	return s, pool
}

func ingestPost(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/internal/task-logs", strings.NewReader(body))
	req.Header.Set("X-Internal-Token", "sekret")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec
}

func TestIngestTaskLogAcceptsValidLevels(t *testing.T) {
	s, pool := ingestDBServer(t)

	for _, level := range []string{"debug", "info", "warn", "error"} {
		body, _ := json.Marshal(tasklogs.Entry{AgentName: "a", Level: level, Message: "m"})
		if rec := ingestPost(t, s, string(body)); rec.Code != http.StatusAccepted {
			t.Fatalf("level %q: got %d, want 202 (body %s)", level, rec.Code, rec.Body.String())
		}
	}
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM task_logs WHERE agent_name = 'a'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 4 {
		t.Fatalf("stored %d rows, want 4", n)
	}
}

func TestIngestTaskLogRejectsInvalidLevel(t *testing.T) {
	s, pool := ingestDBServer(t)

	// The CHECK is case-sensitive and the empty level is likewise rejected —
	// both used to be silently storable free text.
	for _, level := range []string{"PENDING", "", "warning"} {
		body, _ := json.Marshal(tasklogs.Entry{AgentName: "a", Level: level, Message: "m"})
		if rec := ingestPost(t, s, string(body)); rec.Code != http.StatusBadRequest {
			t.Fatalf("level %q: got %d, want 400", level, rec.Code)
		}
	}
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM task_logs`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("stored %d rows, want 0", n)
	}
}

// TestMigration0032ConstraintsRejectBadValues writes through the pool directly
// to prove the constraints exist in the schema, not just in handler code.
func TestMigration0032ConstraintsRejectBadValues(t *testing.T) {
	_, pool := ingestDBServer(t)
	ctx := context.Background()

	// agent_tasks.event_id has a FK and chat_messages.session_id has a FK; seed
	// both so a rejection can only come from the 0032 CHECKs, not from a
	// constraint unrelated to this migration.
	if _, err := pool.Exec(ctx,
		`INSERT INTO events (id, connector, headers, data, raw, received_at)
		 VALUES ('e1', 'test', '{}', '{}', '', now())`); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	if _, err := pool.Exec(ctx,
		// name is NOT NULL since migration 0010; the seed backfill used name = id.
		`INSERT INTO chat_sessions (id, name, agent_name, user_id) VALUES ('s1', 's1', 'a', 'u1')`); err != nil {
		t.Fatalf("seed chat session: %v", err)
	}

	cases := []struct {
		name, query string
		args        []any
	}{
		{"agent_tasks.status",
			`INSERT INTO agent_tasks (event_id, agent_name, trigger_name, headers, payload, status)
			 VALUES ('e1', 'a', 't', '{}', '{}', 'PENDING')`, nil},
		{"invocations.status",
			`INSERT INTO invocations (id, agent_name, trigger_name, event_id, connector, status, start_time)
			 VALUES ('i1', 'a', 't', 'e1', 'c', 'queued', now())`, nil},
		{"chat_messages.role",
			`INSERT INTO chat_messages (session_id, role, content, tokens, created_at)
			 VALUES ('s1', 'system', 'hi', 0, now())`, nil}, // session seeded below
		{"auth_events.event_type",
			`INSERT INTO auth_events (actor_sub, event_type, outcome)
			 VALUES ('u1', 'auth_login_disabled_user', 'success')`, nil},
		{"auth_events.outcome",
			`INSERT INTO auth_events (actor_sub, event_type, outcome)
			 VALUES ('u1', 'auth_login', 'ok')`, nil},
	}
	for _, tc := range cases {
		if _, err := pool.Exec(ctx, tc.query, tc.args...); err == nil {
			t.Errorf("%s: bad value inserted without a constraint violation", tc.name)
		}
	}

	// The legal ends of the same columns must insert cleanly — including the
	// plugin event types, which share auth_events with the auth events.
	if _, err := pool.Exec(ctx,
		`INSERT INTO auth_events (actor_sub, event_type, outcome)
		 VALUES ('u1', 'plugin_api_write', 'failure')`); err != nil {
		t.Errorf("plugin event type rejected: %v", err)
	}
}
