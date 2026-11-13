package signin

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/ainsel-project/ainsel/services/hub/internal/db"
)

// TestPgSignInStoreRoundTrip runs the shared Store contract against Postgres.
// It needs Docker; without it the test skips (CI runs it).
func TestPgSignInStoreRoundTrip(t *testing.T) {
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
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	if err := db.Migrate(ctx, dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	runContract(t,
		func(t *testing.T) Store {
			if _, err := pool.Exec(ctx, `TRUNCATE agent_sign_in_sessions`); err != nil {
				t.Fatalf("truncate: %v", err)
			}
			return NewPgStore(pool)
		},
		func(t *testing.T, _ Store, id string) { expireRow(t, pool, id) })
}

func expireRow(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE agent_sign_in_sessions SET expires_at = now() - interval '1 second' WHERE id = $1`, id); err != nil {
		t.Fatalf("expire row: %v", err)
	}
}
