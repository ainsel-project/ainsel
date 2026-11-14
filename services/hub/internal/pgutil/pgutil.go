// Package pgutil collects the small PostgreSQL error helpers that every hub
// store repeats: constraint-violation detection and the pgx.ErrNoRows to
// package-sentinel mapping.
//
// The package depends on pgx only — no Kubernetes or API imports — so it stays
// testable without a database.
package pgutil

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// SQLSTATE codes the hub's stores translate.
const (
	// CodeUniqueViolation is the SQLSTATE for a unique-constraint violation.
	CodeUniqueViolation = "23505"
	// CodeForeignKeyViolation is the SQLSTATE for a foreign-key violation.
	CodeForeignKeyViolation = "23503"
)

// IsUniqueViolation reports whether err is a PostgreSQL unique-constraint
// violation (SQLSTATE 23505), including when it is wrapped.
func IsUniqueViolation(err error) bool { return isSQLSTATE(err, CodeUniqueViolation) }

// IsForeignKeyViolation reports whether err is a PostgreSQL foreign-key
// violation (SQLSTATE 23503), including when it is wrapped.
func IsForeignKeyViolation(err error) bool {
	return isSQLSTATE(err, CodeForeignKeyViolation)
}

func isSQLSTATE(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

// MapNoRows maps a scan error onto the caller's own sentinel so stores stop
// repeating the same two-branch check:
//
//   - nil           → nil
//   - pgx.ErrNoRows → notFound, returned unwrapped so errors.Is keeps matching it
//   - any other err → fmt.Errorf("<op>: %w", err) when an op name is given,
//     otherwise err unchanged
//
// The sentinel is a parameter, not a package-level error, because every store
// package has its own (personas.ErrNotFound, skills.ErrNotFound, ...); callers
// and tests keep seeing the error they see today.
//
//	if err != nil {
//		return nil, pgutil.MapNoRows(err, ErrNotFound, "personas.Get")
//	}
func MapNoRows(err error, notFound error, op ...string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows):
		return notFound
	case len(op) > 0 && op[0] != "":
		return fmt.Errorf("%s: %w", op[0], err)
	default:
		return err
	}
}
