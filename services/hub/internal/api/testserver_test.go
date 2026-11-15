package api

import (
	"context"
	"github.com/ainsel-project/ainsel/shared/auth/oidc"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ainsel-project/ainsel/services/hub/internal/auditevents"
	"github.com/ainsel-project/ainsel/services/hub/internal/authz"
	"github.com/ainsel-project/ainsel/services/hub/internal/channels"
	"github.com/ainsel-project/ainsel/services/hub/internal/chat"
	"github.com/ainsel-project/ainsel/services/hub/internal/eventqueue"
	"github.com/ainsel-project/ainsel/services/hub/internal/invocations/invocationstest"
	"github.com/ainsel-project/ainsel/services/hub/internal/mcpservers"
	"github.com/ainsel-project/ainsel/services/hub/internal/personas"
	"github.com/ainsel-project/ainsel/services/hub/internal/signin"
	"github.com/ainsel-project/ainsel/services/hub/internal/skills"
	"github.com/ainsel-project/ainsel/services/hub/internal/tasklogs"
	"github.com/ainsel-project/ainsel/services/hub/internal/telemetry"
	"github.com/ainsel-project/ainsel/services/hub/internal/triggers"
	"github.com/ainsel-project/ainsel/services/hub/internal/usertokens"
)

// testDeps returns a Deps with every required field satisfied, so a test states
// only the dependency it is actually exercising:
//
//	d := testDeps()
//	d.Chat = chatStoreThatFails
//	s := newTestServer(t, d)
//
// The stores that are not interesting to the test are real types over a dead
// pool: present rather than absent — a Server has no "not wired" state left to
// branch on (issue #264) — and answering every query with a connection error
// rather than panicking. Tests that exercise a store replace the field: with a
// fake where the package has one, with dbtest.Open where it does not.
//
// tasklogs and telemetry are the exception. Those two carry the observability
// backend selection: a store over a nil pool reports Ready() == false, which is
// how the hub says "no records backend" and falls back to Prometheus. A test that
// wants the records backend sets them explicitly.
func testDeps(t *testing.T) Deps {
	t.Helper()
	pool := deadPool(t)

	channelStore := channels.NewStore(pool)
	channelSvc := channels.NewService(channelStore, nil, nil)
	inv := invocationstest.New(100)

	return Deps{
		Client:       newFakeClient(),
		Namespace:    "test-ns",
		ConnectorCfg: ConnectorConfig{},

		AuthzStore:     authz.NewPermissiveStore(),
		AuthzChecker:   authz.NewChecker(authz.NewPermissiveStore(), nil),
		AuthMiddleware: func(next http.Handler) http.Handler { return next },

		Invocations: inv,
		MCP:         mcpservers.NewService(mcpservers.NewStore(pool)),
		Personas:    personas.NewService(personas.NewStore(pool), nil, nil),
		Skills:      skills.NewService(skills.NewStore(pool), nil, nil),

		EventQueue:      eventqueue.NewStore(pool),
		Chat:            chat.NewStore(pool),
		TriggerStore:    triggers.NewStore(pool),
		ChannelSvc:      channelSvc,
		ChannelTransfer: channels.NewTransfer(channelSvc, eventqueue.NewStore(pool), inv),
		UserTokens:      usertokens.NewStore(pool),
		SignIn:          signin.NewPgStore(pool),
		TaskLogs:        tasklogs.NewStore(nil),
		AuditEvents:     &recordingAuditStore{},
		Telemetry:       telemetry.NewStore(nil),
	}
}

// ServeWithTestCaller wraps a mux so every request carries the same synthetic
// admin caller the auth middleware injects. Tests that exercise a route
// registrar reach the mux directly, with no middleware in front of it.
func ServeWithTestCaller(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(oidc.ContextWithUser(r.Context(), &oidc.User{
			Sub: "test-admin", Username: "test-admin", Roles: []string{"admin"},
		})))
	})
}

// newTestServer builds a Server through the same one-step constructor
// production uses. A Deps that is missing a required dependency fails the test
// here rather than panicking inside a handler three lines later.
func newTestServer(t *testing.T, deps Deps) *Server {
	t.Helper()
	s, err := New(deps)
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	return s
}

// recordingAuditStore is the inert audit sink testDeps installs. Tests that
// assert on what was recorded replace the field with their own.
type recordingAuditStore struct {
	events []auditevents.Event
}

func (s *recordingAuditStore) Insert(_ context.Context, e *auditevents.Event) error {
	s.events = append(s.events, *e)
	return nil
}

func (s *recordingAuditStore) List(context.Context, auditevents.ListOptions) ([]auditevents.Event, int, error) {
	return s.events, len(s.events), nil
}

// deadPool is a pgx pool pointed at a port nobody listens on. Stores built on it
// are structurally present but answer every query with a connection error, so a
// test that never meant to touch them gets a failure the handler can handle
// instead of a nil dereference. connect_timeout keeps the failure immediate.
func deadPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	pool, err := pgxpool.New(context.Background(),
		"postgres://hub:hub@"+addr+"/hub?connect_timeout=1")
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = pool.Ping(ctx)
		pool.Close()
	})
	return pool
}
