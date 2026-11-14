package chat_test

import (
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/chat"
	"github.com/ainsel-project/ainsel/services/hub/internal/dbtest"
)

// Gets a fresh migrated database from the package's shared container
// (see TestMain / dbtest). Skips the test when Docker is unavailable.
func newTestStore(t *testing.T) (*chat.Store, func()) {
	t.Helper()
	pool := dbtest.Open(t)
	return chat.NewStore(pool), func() {}
}
