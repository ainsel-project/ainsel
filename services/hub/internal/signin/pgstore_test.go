package signin

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ainsel-project/ainsel/services/hub/internal/dbtest"
)

// TestPgSignInStoreRoundTrip runs the shared Store contract against Postgres.
// It needs Docker; without it the test skips (CI runs it).
func TestPgSignInStoreRoundTrip(t *testing.T) {
	pool := dbtest.Open(t)

	runContract(t,
		func(t *testing.T) Store {
			if _, err := pool.Exec(context.Background(), `TRUNCATE agent_sign_in_sessions`); err != nil {
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
