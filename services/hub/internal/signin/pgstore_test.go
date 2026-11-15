package signin

import (
	"context"
	"testing"
	"time"

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
		func(t *testing.T, _ Store, id string, d time.Duration) { ageRow(t, pool, id, d) })
}

func ageRow(t *testing.T, pool *pgxpool.Pool, id string, d time.Duration) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE agent_sign_in_sessions SET expires_at = expires_at - make_interval(secs => $2) WHERE id = $1`,
		id, d.Seconds()); err != nil {
		t.Fatalf("age row: %v", err)
	}
}
