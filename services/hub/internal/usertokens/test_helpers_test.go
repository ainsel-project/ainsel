package usertokens_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ainsel-project/ainsel/services/hub/internal/dbtest"
	"github.com/ainsel-project/ainsel/services/hub/internal/usertokens"
)

func newTestStore(t *testing.T) (*usertokens.Store, func()) {
	t.Helper()
	store, _, _ := newTestStoreWithPool(t)
	return store, func() {}
}

// newTestStoreWithPool is newTestStore with direct pool access, for tests
// that must mutate rows outside the usertokens API (e.g. toggling the
// users.disabled flag).
func newTestStoreWithPool(t *testing.T) (*usertokens.Store, *pgxpool.Pool, func()) {
	t.Helper()
	pool := dbtest.Open(t)

	// Insert test users referenced by user_tokens.user_id FK.
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO users (id, email, username) VALUES ('user1', 'u1@test.com', 'user1'),
		                                               ('user2', 'u2@test.com', 'user2')
	`); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	return usertokens.NewStore(pool), pool, func() {}
}
