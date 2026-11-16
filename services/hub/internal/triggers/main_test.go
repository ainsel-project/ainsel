package triggers_test

import (
	"os"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/dbtest"
)

// TestMain starts the package's shared Postgres testcontainer (dbtest), which
// migrates once into a template database. Tests get a fresh cloned database
// from dbtest.Open and skip when Docker is unavailable.
func TestMain(m *testing.M) {
	os.Exit(dbtest.Main(m))
}
