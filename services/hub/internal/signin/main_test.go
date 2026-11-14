package signin

import (
	"os"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/dbtest"
)

// Gets a fresh migrated database from the package's shared container
// (see TestMain / dbtest). Skips the test when Docker is unavailable.
func TestMain(m *testing.M) {
	os.Exit(dbtest.Main(m))
}
