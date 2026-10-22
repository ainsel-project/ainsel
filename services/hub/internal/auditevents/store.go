// Package auditevents persists the hub's auth events (login success/failure,
// password change, user create/reset/delete) so admins can read the audit
// trail from the API instead of cluster logs (issue #116).
//
// The rows mirror the six existing log_type=auth_event slog sites 1:1: the
// event_type values are the slog names, so a row and its log line correlate.
// The schema is deliberately narrow — no free-form payload — which keeps
// "no secrets in the rows" auditable at a glance: Insert takes only the
// persisted fields and never a request body.
package auditevents

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Event types written by the api handlers. They match the slog event names at
// the six write sites exactly; changing one means changing the other.
const (
	TypeLogin             = "auth_login"
	TypeLoginFailed       = "auth_login_failed"
	TypePasswordChanged   = "auth_password_changed"
	TypeUserCreated       = "user_created"
	TypeUserPasswordReset = "user_password_reset"
	TypeUserDeleted       = "user_deleted"
)

// Plugin management events, written by the /api/v1/plugins admin API. Unlike
// the auth events above these have no slog counterpart: the row is the record.
// Every successful plugin write gets one, with target = the plugin id.
const (
	TypePluginInstalled           = "plugin_installed"
	TypePluginUninstalled         = "plugin_uninstalled"
	TypePluginEnabled             = "plugin_enabled"
	TypePluginDisabled            = "plugin_disabled"
	TypePluginPermissionsApproved = "plugin_permissions_approved"
	TypePluginGroupAccessChanged  = "plugin_group_access_changed"
)

// Outcomes. Every event is recorded as exactly one of these.
const (
	OutcomeSuccess = "success"
	OutcomeFailure = "failure"
)

// Event is one audit-trail row. ActorSub is the acting user's id; for a
// failed login against a non-existent user it is the derived local id
// ("local:<username>") of the attempted username — the brute-force signal is
// the attempt, not the account. Target is the affected user: equal to
// ActorSub for self-events, the affected id for admin actions.
type Event struct {
	ID         int64
	OccurredAt time.Time
	ActorSub   string
	EventType  string
	Target     string
	SourceIP   string
	Outcome    string
}

// ListOptions filters a page of events. User matches actor_sub OR target, so
// "everything involving user X" is one query. Types is OR-ed: an event is
// included if its event_type equals any entry. Since is inclusive, Until is
// exclusive; zero means unbounded.
type ListOptions struct {
	User  string
	Types []string
	Since time.Time
	Until time.Time
	Limit int
	// Offset is the number of rows to skip (page-1)*pageSize.
	Offset int
}

// Store is the auth_events database layer.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wires a Store against the given pgxpool. A nil pool is tolerated at
// construction; Ready reports the truth (same contract as tasklogs.Store).
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Ready reports whether the store can answer anything.
func (s *Store) Ready() bool {
	return s != nil && s.pool != nil
}

// Insert persists one event. The event's ID and OccurredAt are populated from
// the database on success.
func (s *Store) Insert(ctx context.Context, e *Event) error {
	return s.pool.QueryRow(ctx, `
		INSERT INTO auth_events (actor_sub, event_type, target, source_ip, outcome)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, occurred_at
	`, e.ActorSub, e.EventType, e.Target, e.SourceIP, e.Outcome).
		Scan(&e.ID, &e.OccurredAt)
}

// List returns one page of events newest-first plus the total count of rows
// matching the filters (before paging), so the API envelope can report
// totalPages. Ordering is (occurred_at DESC, id DESC): the id tiebreak keeps
// paging stable for rows sharing a timestamp.
func (s *Store) List(ctx context.Context, opts ListOptions) ([]Event, int, error) {
	where := []string{"1=1"}
	args := []any{}
	argN := 1

	if opts.User != "" {
		where = append(where, fmt.Sprintf("(actor_sub = $%d OR target = $%d)", argN, argN))
		args = append(args, opts.User)
		argN++
	}
	if len(opts.Types) > 0 {
		where = append(where, fmt.Sprintf("event_type = ANY($%d)", argN))
		args = append(args, opts.Types)
		argN++
	}
	if !opts.Since.IsZero() {
		where = append(where, fmt.Sprintf("occurred_at >= $%d", argN))
		args = append(args, opts.Since)
		argN++
	}
	if !opts.Until.IsZero() {
		where = append(where, fmt.Sprintf("occurred_at < $%d", argN))
		args = append(args, opts.Until)
		argN++
	}
	clause := ""
	for _, w := range where {
		clause += " AND " + w
	}

	var total int
	if err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM auth_events WHERE 1=1"+clause, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("auditevents.List count: %w", err)
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT id, occurred_at, actor_sub, event_type, target, source_ip, outcome
		FROM auth_events WHERE 1=1` + clause +
		fmt.Sprintf(" ORDER BY occurred_at DESC, id DESC LIMIT $%d OFFSET $%d", argN, argN+1)
	args = append(args, limit, opts.Offset)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("auditevents.List: %w", err)
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.OccurredAt, &e.ActorSub, &e.EventType,
			&e.Target, &e.SourceIP, &e.Outcome); err != nil {
			return nil, 0, fmt.Errorf("auditevents.List scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("auditevents.List rows: %w", err)
	}
	return out, total, nil
}

// Prune deletes events older than the given retention duration. Returns the
// number of deleted rows. A non-positive retention is a no-op: the documented
// "0 disables age-based deletion" value must never degenerate into a cutoff of
// now(), which would delete the entire audit trail.
func (s *Store) Prune(ctx context.Context, retention time.Duration) (int64, error) {
	if retention <= 0 {
		return 0, nil
	}
	cutoff := time.Now().Add(-retention)
	tag, err := s.pool.Exec(ctx, `DELETE FROM auth_events WHERE occurred_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("auditevents.Prune: %w", err)
	}
	return tag.RowsAffected(), nil
}

// TrimToMax enforces a row cap, keeping the newest maxRows rows (by id, which
// is identity-ordered). Returns the number of deleted rows. A non-positive
// max is a no-op: a cap of zero would mean "delete the audit trail".
func (s *Store) TrimToMax(ctx context.Context, maxRows int) (int64, error) {
	if maxRows <= 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM auth_events
		WHERE id NOT IN (
			SELECT id FROM auth_events
			ORDER BY occurred_at DESC, id DESC
			LIMIT $1
		)`, maxRows)
	if err != nil {
		return 0, fmt.Errorf("auditevents.TrimToMax: %w", err)
	}
	return tag.RowsAffected(), nil
}
