package db_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/db"
)

func TestMigrateUpCreatesMcpServersTable(t *testing.T) {
	dsn, stop := startPostgres(t)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := db.Migrate(ctx, dsn); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	pool, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM mcp_servers`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("expected empty table, got %d rows", n)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	dsn, stop := startPostgres(t)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := db.Migrate(ctx, dsn); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if err := db.Migrate(ctx, dsn); err != nil {
		t.Fatalf("second Migrate (should be no-op): %v", err)
	}
}

func TestMigrateRejectsKeyValueDSN(t *testing.T) {
	err := db.Migrate(context.Background(), "host=localhost user=test")
	if err == nil || !strings.Contains(err.Error(), "URL form") {
		t.Errorf("expected URL-form rejection error, got: %v", err)
	}
}

// TestMigrateUpAddsSkillsManagedBy covers migration 0030: every skills row
// gains a managed_by that defaults to "user", so pre-existing console skills
// keep reading as user-owned without a backfill.
func TestMigrateUpAddsSkillsManagedBy(t *testing.T) {
	dsn, stop := startPostgres(t)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := db.Migrate(ctx, dsn); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, `INSERT INTO skills (id, name) VALUES ('plain', 'Plain')`); err != nil {
		t.Fatalf("insert without managed_by: %v", err)
	}
	var managedBy string
	if err := pool.QueryRow(ctx, `SELECT managed_by FROM skills WHERE id = 'plain'`).Scan(&managedBy); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if managedBy != "user" {
		t.Errorf("managed_by default: want %q, got %q", "user", managedBy)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO skills (id, name, managed_by) VALUES ('nul', 'Nul', NULL)`); err == nil {
		t.Error("expected NOT NULL on managed_by to reject an explicit NULL, insert succeeded")
	}
}

// TestMigrateUpAddsPersonasManagedBy covers migration 0033 (issue #146): every
// personas row gains a managed_by defaulting to "user", so pre-existing
// console personas keep reading as user-owned without a backfill.
func TestMigrateUpAddsPersonasManagedBy(t *testing.T) {
	dsn, stop := startPostgres(t)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := db.Migrate(ctx, dsn); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	// A minimal valid row: current_version_id points at a real version.
	if _, err := pool.Exec(ctx, `
		INSERT INTO persona_versions (persona_id, version_number, text) VALUES ('p1', 1, 'hello');
		INSERT INTO personas (id, name, current_version_id) VALUES ('p1', 'p1', 1);
	`); err != nil {
		t.Fatalf("insert without managed_by: %v", err)
	}
	var managedBy string
	if err := pool.QueryRow(ctx, `SELECT managed_by FROM personas WHERE id = 'p1'`).Scan(&managedBy); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if managedBy != "user" {
		t.Errorf("managed_by default: want %q, got %q", "user", managedBy)
	}
	if _, err := pool.Exec(ctx, `UPDATE personas SET managed_by = NULL WHERE id = 'p1'`); err == nil {
		t.Error("expected NOT NULL on managed_by to reject an explicit NULL, update succeeded")
	}
}
