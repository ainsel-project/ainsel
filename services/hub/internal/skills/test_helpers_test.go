package skills_test

import (
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/dbtest"
	"github.com/ainsel-project/ainsel/services/hub/internal/skills"
)

// Gets a fresh migrated database from the package's shared container
// (see TestMain / dbtest). Skips the test when Docker is unavailable.
func newTestStore(t *testing.T) (*skills.Store, func()) {
	t.Helper()
	pool := dbtest.Open(t)
	return skills.NewStore(pool), func() {}
}

func ptr[T any](v T) *T { return &v }
