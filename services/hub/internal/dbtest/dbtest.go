// Package dbtest provides the shared Postgres fixture for hub tests.
//
// Each test package that needs a database calls Main from a TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }
//
// Main starts exactly one postgres:17-alpine testcontainer for the whole
// package, applies the hub migrations once into a template database, and runs
// the tests. Individual tests then call Open, which clones the migrated
// template into a private database (CREATE DATABASE ... TEMPLATE) and hands
// back a pool — the same "fresh world per test" semantics the old per-file
// fixtures had, at a fraction of the cost. OpenEmpty hands out an unmigrated
// clone for tests that exercise db.Migrate itself.
//
// TestMain cannot skip, so when Docker is unavailable Main records the error
// in Unavailable and still runs the suite; Open/OpenEmpty then skip every test
// with the familiar "docker unavailable" message, keeping `go test ./...`
// green on machines without Docker. That skip path is only for "no Docker":
// anything that fails after the container is up (template creation, migration)
// is a real failure and makes Main return a non-zero exit code, so a broken
// migration can never turn the whole DB-backed suite into silent skips.
//
// This is the only place in the hub that may call pgcontainer.Run directly.
package dbtest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/ainsel-project/ainsel/services/hub/internal/db"
)

const (
	containerImage = "postgres:17-alpine"
	baseDatabase   = "ainsel_test" // container's default database, kept unmigrated
	templateName   = "ainsel_tpl"  // migrated once by Main, cloned by Open
	user           = "test"
	password       = "test"
)

// Unavailable is non-nil when Main could not start the container (typically
// no Docker daemon). Open and OpenEmpty skip the calling test in that case,
// preserving the per-test "docker unavailable" skip convention. It is never
// set for a failure after the container started — those fail the package.
var Unavailable error

var (
	containerDSN string        // DSN of the running container, base database
	nextDB       atomic.Uint64 // clone-name counter
)

// Main starts the package's Postgres container, migrates the template
// database, runs m.Run(), and tears everything down. It returns the exit code
// for os.Exit. A container start-up failure is not fatal: it is recorded in
// Unavailable, logged once to stderr, and the tests run (and skip) normally.
// Every later failure is fatal — Main returns 1 instead of setting
// Unavailable, so a migration that breaks while Docker is available fails the
// package rather than skipping the whole suite green.
func Main(m *testing.M) int {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	c, err := pgcontainer.Run(ctx, containerImage,
		pgcontainer.WithDatabase(baseDatabase),
		pgcontainer.WithUsername(user),
		pgcontainer.WithPassword(password),
		pgcontainer.BasicWaitStrategies(),
	)
	if err != nil {
		Unavailable = err
		fmt.Fprintf(os.Stderr, "dbtest: postgres container unavailable, DB-backed tests will skip: %v\n", err)
		return m.Run()
	}
	defer func() { _ = c.Terminate(context.Background()) }()

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return fatal("connection string", err)
	}
	containerDSN = dsn

	// Migrate once into the template database. Migrate wants a URL-form DSN
	// pointing at the target database, so retarget the container DSN.
	tplDSN, err := dsnWithDatabase(dsn, templateName)
	if err != nil {
		return fatal("template dsn", err)
	}
	// The template is cloned from template1 (empty template argument) and must
	// be free of sessions by the time Open clones it: Postgres refuses to use a
	// TEMPLATE that still has other connections attached. The pool Migrate
	// opens on tplDSN is closed before m.Run() — do not introduce a long-lived
	// connection to ainsel_tpl, or every Open will fail.
	if err := createDatabase(ctx, dsn, templateName, ""); err != nil {
		return fatal("create template db", err)
	}
	if err := db.Migrate(ctx, tplDSN); err != nil {
		return fatal("migrate template", err)
	}

	return m.Run()
}

// fatal reports a fixture failure that happened with Docker available and
// returns the exit code Main should hand to os.Exit. Such a failure is never
// turned into Unavailable: skipping would hide it as a green run.
func fatal(what string, err error) int {
	fmt.Fprintf(os.Stderr, "dbtest: %s: %v\n", what, err)
	return 1
}

// Open clones the migrated template database and returns a pool connected to
// the clone. The pool is closed and the database dropped when the test ends.
// The test is skipped when the package's container could not start.
func Open(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return openClone(t, templateName, "")
}

// OpenEmpty clones the unmigrated base database and returns a pool connected
// to the clone, for tests that run db.Migrate themselves. The test is skipped
// when the package's container could not start.
func OpenEmpty(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return openClone(t, "", "e")
}

// EmptyDSN returns a URL-form DSN for a fresh unmigrated database that the
// caller migrates itself, for tests that call db.Migrate before opening a
// pool. The database is dropped when the test ends (the caller's pools are
// closed first). The test is skipped when the container could not start.
func EmptyDSN(t *testing.T) string {
	t.Helper()
	if Unavailable != nil {
		t.Skipf("docker unavailable: %v", Unavailable)
	}
	name := cloneName("e")
	dsn, err := dsnWithDatabase(containerDSN, name)
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}
	if err := createDatabase(context.Background(), containerDSN, name, ""); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	t.Cleanup(func() { dropDatabase(context.Background(), containerDSN, name) })
	return dsn
}

// openClone creates a fresh clone of template and returns a pool connected to
// it. An empty template clones Postgres' own template1, i.e. an unmigrated
// database. prefix only namespaces the clone name (hubtest_NNNN vs
// hubtest_eNNNN) and has nothing to do with which template is cloned.
func openClone(t *testing.T, template, prefix string) *pgxpool.Pool {
	t.Helper()
	if Unavailable != nil {
		t.Skipf("docker unavailable: %v", Unavailable)
	}
	name := cloneName(prefix)
	ctx := context.Background()
	if err := createDatabase(ctx, containerDSN, name, template); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	dsn, err := dsnWithDatabase(containerDSN, name)
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		dropDatabase(ctx, containerDSN, name)
		t.Fatalf("open pool: %v", err)
	}
	// LIFO: the pool closes before the drop, so no connection holds the
	// database open. The drop uses WITH (FORCE) as a safety net.
	t.Cleanup(func() { dropDatabase(context.Background(), containerDSN, name) })
	t.Cleanup(pool.Close)
	return pool
}

// cloneName builds short, lowercase names (hubtest_NNNN / hubtest_eNNNN) that
// stay far below Postgres' 63-byte identifier limit.
func cloneName(prefix string) string {
	return fmt.Sprintf("hubtest_%s%04d", prefix, nextDB.Add(1))
}

// createDatabase creates name as a clone of template. An empty template leaves
// the clause off, so Postgres clones its own template1 (an unmigrated
// database); a non-empty one must have no sessions attached, or Postgres
// rejects the statement.
func createDatabase(ctx context.Context, adminDSN, name, template string) error {
	pool, err := db.Open(ctx, adminDSN)
	if err != nil {
		return err
	}
	defer pool.Close()
	stmt := "CREATE DATABASE " + quoteIdent(name)
	if template != "" {
		stmt += " TEMPLATE " + quoteIdent(template)
	}
	_, err = pool.Exec(ctx, stmt)
	return err
}

func dropDatabase(ctx context.Context, adminDSN, name string) {
	pool, err := db.Open(ctx, adminDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dbtest: drop %s: %v\n", name, err)
		return
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, "DROP DATABASE IF EXISTS "+quoteIdent(name)+" WITH (FORCE)"); err != nil {
		fmt.Fprintf(os.Stderr, "dbtest: drop %s: %v\n", name, err)
	}
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// dsnWithDatabase retargets a postgres:// DSN at another database.
func dsnWithDatabase(dsn, database string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	u.Path = "/" + database
	return u.String(), nil
}
