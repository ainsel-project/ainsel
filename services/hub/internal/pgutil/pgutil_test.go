package pgutil

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var errSentinel = errors.New("store sentinel")

func TestIsUniqueViolation(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"other error", errors.New("boom"), false},
		{"unique violation", &pgconn.PgError{Code: CodeUniqueViolation}, true},
		{"wrapped unique violation",
			fmt.Errorf("personas.Create: %w", &pgconn.PgError{Code: CodeUniqueViolation}), true},
		{"foreign key violation", &pgconn.PgError{Code: CodeForeignKeyViolation}, false},
		{"other sqlstate", &pgconn.PgError{Code: "22P02"}, false},
		{"wrapped pgx.ErrNoRows", fmt.Errorf("ctx: %w", pgx.ErrNoRows), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsUniqueViolation(tc.err); got != tc.want {
				t.Errorf("IsUniqueViolation(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsForeignKeyViolation(t *testing.T) {
	if !IsForeignKeyViolation(&pgconn.PgError{Code: CodeForeignKeyViolation}) {
		t.Error("23503 must report a foreign-key violation")
	}
	if !IsForeignKeyViolation(fmt.Errorf("ctx: %w", &pgconn.PgError{Code: CodeForeignKeyViolation})) {
		t.Error("wrapped 23503 must report a foreign-key violation")
	}
	if IsForeignKeyViolation(&pgconn.PgError{Code: CodeUniqueViolation}) {
		t.Error("23505 must not report a foreign-key violation")
	}
	if IsForeignKeyViolation(nil) {
		t.Error("nil must not report a foreign-key violation")
	}
}

func TestMapNoRows(t *testing.T) {
	if got := MapNoRows(nil, errSentinel, "pkg.Get"); got != nil {
		t.Errorf("MapNoRows(nil) = %v, want nil", got)
	}

	// pgx.ErrNoRows maps to the caller's sentinel, unwrapped.
	got := MapNoRows(pgx.ErrNoRows, errSentinel, "pkg.Get")
	if got != errSentinel {
		t.Errorf("MapNoRows(ErrNoRows) = %v, want the sentinel itself", got)
	}
	if !errors.Is(got, errSentinel) {
		t.Errorf("mapped error must satisfy errors.Is(sentinel), got %v", got)
	}

	// A wrapped pgx.ErrNoRows maps the same way.
	if got := MapNoRows(fmt.Errorf("scan: %w", pgx.ErrNoRows), errSentinel); !errors.Is(got, errSentinel) {
		t.Errorf("wrapped ErrNoRows must map to the sentinel, got %v", got)
	}

	// Other errors keep their identity when no op name is given...
	other := errors.New("connection reset")
	if got := MapNoRows(other, errSentinel); got != other {
		t.Errorf("MapNoRows(other) = %v, want the error unchanged", got)
	}

	// ...and are wrapped with the op name otherwise, exactly as the stores did.
	want := "authz.GetUser: connection reset"
	if got := MapNoRows(other, errSentinel, "authz.GetUser"); got.Error() != want {
		t.Errorf("MapNoRows(other, op) = %q, want %q", got.Error(), want)
	}
	if !errors.Is(MapNoRows(other, errSentinel, "authz.GetUser"), other) {
		t.Error("wrapped error must still match the original with errors.Is")
	}
}
