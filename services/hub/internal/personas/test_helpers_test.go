package personas_test

import (
	"testing"

	pgxpool "github.com/jackc/pgx/v5/pgxpool"

	"github.com/ainsel-project/ainsel/services/hub/internal/dbtest"
	"github.com/ainsel-project/ainsel/services/hub/internal/personas"
)

// Gets a fresh migrated database from the package's shared container
// (see TestMain / dbtest). Skips the test when Docker is unavailable.
func newTestStore(t *testing.T) (*personas.Store, func()) {
	t.Helper()
	pool, _ := newTestPool(t)
	return personas.NewStore(pool), func() {}
}

// Gets a fresh migrated database from the package's shared container
// (see TestMain / dbtest). Skips the test when Docker is unavailable.
func newTestPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	pool := dbtest.Open(t)
	return pool, func() {}
}

func ptr[T any](v T) *T { return &v }
