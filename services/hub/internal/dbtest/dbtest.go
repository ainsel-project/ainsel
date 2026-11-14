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
// green on machines without Docker.
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
// preserving the per-test "docker unavailable" skip convention.
var Unavailable error

var (
	containerDSN string        // DSN of the running container, base database
	nextDB       atomic.Uint64 // clone-name counter
)

// Main starts the package's Postgres container, migrates the template
// database, runs m.Run(), and tears everything down. It returns the exit code
// for os.Exit. A container start-up failure is not fatal: it is recorded in
// Unavailable, logged once to stderr, and the tests run (and skip) normally.
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
		Unavailable = fmt.Errorf("connection string: %w", err)
		fmt.Fprintf(os.Stderr, "dbtest: %v, DB-backed tests will skip\n", Unavailable)
		return m.Run()
	}
	containerDSN = dsn

	// Migrate once into the template database. Migrate wants a URL-form DSN
	// pointing at the target database, so retarget the container DSN.
	tplDSN, err := dsnWithDatabase(dsn, templateName)
	if err != nil {
		Unavailable = fmt.Errorf("template dsn: %w", err)
		fmt.Fprintf(os.Stderr, "dbtest: %v, DB-backed tests will skip\n", Unavailable)
		return m.Run()
	}
	if err := createDatabase(ctx, dsn, templateName); err != nil {
		Unavailable = fmt.Errorf("create template db: %w", err)
		fmt.Fprintf(os.Stderr, "dbtest: %v, DB-backed tests will skip\n", Unavailable)
		return m.Run()
	}
	if err := db.Migrate(ctx, tplDSN); err != nil {
		Unavailable = fmt.Errorf("migrate template: %w", err)
		fmt.Fprintf(os.Stderr, "dbtest: %v, DB-backed tests will skip\n", Unavailable)
		return m.Run()
	}

	return m.Run()
}

// Open clones the migrated template database and returns a pool connected to
// the clone. The pool is closed and the database dropped when the test ends.
// The test is skipped when the package's container could not start.
func Open(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return openClone(t, templateName)
}

// OpenEmpty clones the unmigrated base database and returns a pool connected
// to the clone, for tests that run db.Migrate themselves. The test is skipped
// when the package's container could not start.
func OpenEmpty(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return openClone(t, baseDatabase)
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
	if err := createDatabase(context.Background(), containerDSN, name); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	t.Cleanup(func() { dropDatabase(context.Background(), containerDSN, name) })
	return dsn
}

func openClone(t *testing.T, template string) *pgxpool.Pool {
	t.Helper()
	if Unavailable != nil {
		t.Skipf("docker unavailable: %v", Unavailable)
	}
	prefix := ""
	if template == baseDatabase {
		prefix = "e"
	}
	name := cloneName(prefix)
	ctx := context.Background()
	if err := createDatabase(ctx, containerDSN, name); err != nil {
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

func createDatabase(ctx context.Context, adminDSN, name string) error {
	pool, err := db.Open(ctx, adminDSN)
	if err != nil {
		return err
	}
	defer pool.Close()
	_, err = pool.Exec(ctx, "CREATE DATABASE "+quoteIdent(name))
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
