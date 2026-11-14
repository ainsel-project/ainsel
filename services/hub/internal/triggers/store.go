package triggers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/pgutil"
	ainselapishared "github.com/ainsel-project/ainsel/shared/api"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a trigger or cron trigger doesn't exist.
var ErrNotFound = errors.New("not found")

// ErrIDTaken is returned when a trigger ID collides.
var ErrIDTaken = errors.New("ID already in use")

// Store is the database layer for triggers and cron triggers.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wires a Store against the given pgxpool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// ---------------------------------------------------------------------------
// Trigger CRUD
// ---------------------------------------------------------------------------

// Column lists for the two tables, shared by the single-row lookups and the
// paginated lists so the two can never drift apart.
const (
	triggerColumns     = `SELECT id, display_name, agent_ref, connector_ref, filters, agent_valid, connector_valid, created_at, updated_at`
	fromTriggers       = ` FROM triggers`
	cronTriggerColumns = `SELECT id, display_name, agent_ref, schedule, prompt, enabled, agent_valid, schedule_valid, last_run, next_run, created_at, updated_at`
	fromCronTriggers   = ` FROM cron_triggers`
)

// CreateTrigger inserts a new trigger.
func (s *Store) CreateTrigger(ctx context.Context, t *Trigger) error {
	now := time.Now().UTC()
	filtersJSON, err := json.Marshal(t.Filters)
	if err != nil {
		return fmt.Errorf("triggers.CreateTrigger: marshal filters: %w", err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO triggers (id, display_name, agent_ref, connector_ref, filters, agent_valid, connector_valid, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)`,
		t.ID, t.DisplayName, t.AgentRef, t.ConnectorRef, filtersJSON, t.AgentValid, t.ConnectorValid, now,
	); err != nil {
		if pgutil.IsUniqueViolation(err) {
			return ErrIDTaken
		}
		return fmt.Errorf("insert trigger: %w", err)
	}
	t.CreatedAt = now
	t.UpdatedAt = now
	return nil
}

// GetTrigger returns a trigger by id.
func (s *Store) GetTrigger(ctx context.Context, id string) (*Trigger, error) {
	var t Trigger
	var filtersJSON []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, display_name, agent_ref, connector_ref, filters, agent_valid, connector_valid, created_at, updated_at
		FROM triggers WHERE id = $1
	`, id).Scan(&t.ID, &t.DisplayName, &t.AgentRef, &t.ConnectorRef, &filtersJSON, &t.AgentValid, &t.ConnectorValid, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, pgutil.MapNoRows(err, ErrNotFound, "triggers.GetTrigger")
	}
	if len(filtersJSON) > 0 {
		if err := json.Unmarshal(filtersJSON, &t.Filters); err != nil {
			return nil, fmt.Errorf("triggers.GetTrigger: unmarshal filters: %w", err)
		}
	}
	return &t, nil
}

// TriggerListOptions holds the filters and page bounds for ListTriggers.
type TriggerListOptions struct {
	// AgentRef and ConnectorRef narrow the set as before; empty means "any".
	AgentRef     string
	ConnectorRef string

	// AllowedIDs is the authorization allow-list resolved by the handler
	// (see accessibleIDs in server.go). It is applied in SQL as
	// `id = ANY($n)`, never as a post-filter. The nil-vs-empty distinction
	// is load-bearing and pinned by list_authz_parity_test.go:
	//   - nil   → unrestricted (access control off, or the caller is an admin)
	//   - empty → the caller may see nothing; the call returns no rows and total 0
	//   - non-empty → restricts to exactly the listed ids
	AllowedIDs []string

	// Limit/Offset paginate the query. Limit <= 0 means no LIMIT (fetch
	// the full match set); Offset rows are skipped.
	Limit  int
	Offset int
}

// CronTriggerListOptions holds the filters and page bounds for
// ListCronTriggers. AllowedIDs carries the same contract as in
// TriggerListOptions.
type CronTriggerListOptions struct {
	AgentRef   string
	AllowedIDs []string
	Limit      int
	Offset     int
}

// ListTriggers returns one page of triggers plus the total number of rows
// matching the options, before pagination. The agent/connector filters, the
// authorization allow-list and the page bounds are all applied in SQL, so the
// rows read are proportional to the page, not to the table. Order is by id,
// which is unique, so pages are stable.
func (s *Store) ListTriggers(ctx context.Context, opts TriggerListOptions) ([]Trigger, int, error) {
	// Empty allow-list: the caller may see nothing. Short-circuit before
	// touching the table — and never treat empty as "no restriction".
	if opts.AllowedIDs != nil && len(opts.AllowedIDs) == 0 {
		return nil, 0, nil
	}

	var conditions []string
	var args []any
	argIdx := 1

	if opts.AgentRef != "" {
		conditions = append(conditions, fmt.Sprintf("agent_ref = $%d", argIdx))
		args = append(args, opts.AgentRef)
		argIdx++
	}
	if opts.ConnectorRef != "" {
		conditions = append(conditions, fmt.Sprintf("connector_ref = $%d", argIdx))
		args = append(args, opts.ConnectorRef)
		argIdx++
	}
	if opts.AllowedIDs != nil {
		conditions = append(conditions, fmt.Sprintf("id = ANY($%d)", argIdx))
		args = append(args, opts.AllowedIDs)
		argIdx++
	}
	whereSQL := ""
	if len(conditions) > 0 {
		whereSQL = " WHERE " + strings.Join(conditions, " AND ")
	}

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM triggers`+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("triggers.ListTriggers count: %w", err)
	}

	query := triggerColumns + fromTriggers + whereSQL + " ORDER BY id"
	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, opts.Limit)
		argIdx++
	}
	if opts.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argIdx)
		args = append(args, opts.Offset)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("triggers.ListTriggers: %w", err)
	}
	defer rows.Close()
	var out []Trigger
	for rows.Next() {
		var t Trigger
		var filtersJSON []byte
		if err := rows.Scan(&t.ID, &t.DisplayName, &t.AgentRef, &t.ConnectorRef, &filtersJSON, &t.AgentValid, &t.ConnectorValid, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("triggers.ListTriggers: %w", err)
		}
		if len(filtersJSON) > 0 {
			if err := json.Unmarshal(filtersJSON, &t.Filters); err != nil {
				return nil, 0, fmt.Errorf("triggers.ListTriggers: unmarshal filters: %w", err)
			}
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("triggers.ListTriggers: %w", err)
	}
	return out, total, nil
}

// UpdateTrigger applies a partial update to a trigger.
func (s *Store) UpdateTrigger(ctx context.Context, id string, displayName, agentRef, connectorRef *string, filters *[]ainselapishared.Filter) (*Trigger, error) {
	var (
		curName, curAgent, curConnector string
		curFilters                      []byte
		agentValid, connectorValid      bool
		createdAt                       time.Time
	)
	err := s.pool.QueryRow(ctx, `
		SELECT display_name, agent_ref, connector_ref, filters, agent_valid, connector_valid, created_at
		FROM triggers WHERE id = $1 FOR UPDATE
	`, id).Scan(&curName, &curAgent, &curConnector, &curFilters, &agentValid, &connectorValid, &createdAt)
	if err != nil {
		return nil, pgutil.MapNoRows(err, ErrNotFound, "triggers.UpdateTrigger")
	}

	name := curName
	agent := curAgent
	connector := curConnector
	var newFilters []byte

	if displayName != nil {
		name = *displayName
	}
	if agentRef != nil {
		agent = *agentRef
		// Changing the agent ref invalidates prior agent validation.
		agentValid = false
	}
	if connectorRef != nil {
		connector = *connectorRef
		// Changing the connector ref invalidates prior connector validation.
		connectorValid = false
	}
	if filters != nil {
		newFilters, err = json.Marshal(*filters)
		if err != nil {
			return nil, fmt.Errorf("triggers.UpdateTrigger: marshal filters: %w", err)
		}
	} else {
		newFilters = curFilters
	}

	now := time.Now().UTC()
	if _, err := s.pool.Exec(ctx,
		`UPDATE triggers SET display_name=$1, agent_ref=$2, connector_ref=$3, filters=$4, agent_valid=$5, connector_valid=$6, updated_at=$7 WHERE id=$8`,
		name, agent, connector, newFilters, agentValid, connectorValid, now, id,
	); err != nil {
		return nil, fmt.Errorf("triggers.UpdateTrigger: %w", err)
	}

	var fl []ainselapishared.Filter
	if len(newFilters) > 0 {
		if err := json.Unmarshal(newFilters, &fl); err != nil {
			return nil, fmt.Errorf("triggers.UpdateTrigger: unmarshal filters: %w", err)
		}
	}
	return &Trigger{
		ID:             id,
		DisplayName:    name,
		AgentRef:       agent,
		ConnectorRef:   connector,
		Filters:        fl,
		AgentValid:     agentValid,
		ConnectorValid: connectorValid,
		CreatedAt:      createdAt,
		UpdatedAt:      now,
	}, nil
}

// DeleteTrigger removes a trigger by id.
func (s *Store) DeleteTrigger(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM triggers WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("triggers.DeleteTrigger: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetTriggerValidity updates the agent_valid and connector_valid flags.
func (s *Store) SetTriggerValidity(ctx context.Context, id string, agentValid, connectorValid bool) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE triggers SET agent_valid=$1, connector_valid=$2, updated_at=now() WHERE id=$3`,
		agentValid, connectorValid, id)
	if err != nil {
		return fmt.Errorf("triggers.SetTriggerValidity: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// CronTrigger CRUD
// ---------------------------------------------------------------------------

// CreateCronTrigger inserts a new cron trigger.
func (s *Store) CreateCronTrigger(ctx context.Context, ct *CronTrigger) error {
	now := time.Now().UTC()
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO cron_triggers (id, display_name, agent_ref, schedule, prompt, enabled, agent_valid, schedule_valid, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`,
		ct.ID, ct.DisplayName, ct.AgentRef, ct.Schedule, ct.Prompt, ct.Enabled, ct.AgentValid, ct.ScheduleValid, now,
	); err != nil {
		if pgutil.IsUniqueViolation(err) {
			return ErrIDTaken
		}
		return fmt.Errorf("insert cron trigger: %w", err)
	}
	ct.CreatedAt = now
	ct.UpdatedAt = now
	return nil
}

// GetCronTrigger returns a cron trigger by id.
func (s *Store) GetCronTrigger(ctx context.Context, id string) (*CronTrigger, error) {
	var ct CronTrigger
	var lastRun, nextRun *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT id, display_name, agent_ref, schedule, prompt, enabled, agent_valid, schedule_valid, last_run, next_run, created_at, updated_at
		FROM cron_triggers WHERE id = $1
	`, id).Scan(&ct.ID, &ct.DisplayName, &ct.AgentRef, &ct.Schedule, &ct.Prompt, &ct.Enabled, &ct.AgentValid, &ct.ScheduleValid, &lastRun, &nextRun, &ct.CreatedAt, &ct.UpdatedAt)
	if err != nil {
		return nil, pgutil.MapNoRows(err, ErrNotFound, "triggers.GetCronTrigger")
	}
	ct.LastRun = lastRun
	ct.NextRun = nextRun
	return &ct, nil
}

// ListCronTriggers returns one page of cron triggers plus the total number of
// rows matching the options, before pagination — same contract as
// ListTriggers: the agent filter, the authorization allow-list and the page
// bounds are applied in SQL, and the order is by the unique id.
func (s *Store) ListCronTriggers(ctx context.Context, opts CronTriggerListOptions) ([]CronTrigger, int, error) {
	// Empty allow-list: the caller may see nothing. Never treat empty as
	// "no restriction".
	if opts.AllowedIDs != nil && len(opts.AllowedIDs) == 0 {
		return nil, 0, nil
	}

	var conditions []string
	var args []any
	argIdx := 1

	if opts.AgentRef != "" {
		conditions = append(conditions, fmt.Sprintf("agent_ref = $%d", argIdx))
		args = append(args, opts.AgentRef)
		argIdx++
	}
	if opts.AllowedIDs != nil {
		conditions = append(conditions, fmt.Sprintf("id = ANY($%d)", argIdx))
		args = append(args, opts.AllowedIDs)
		argIdx++
	}
	whereSQL := ""
	if len(conditions) > 0 {
		whereSQL = " WHERE " + strings.Join(conditions, " AND ")
	}

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM cron_triggers`+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("triggers.ListCronTriggers count: %w", err)
	}

	query := cronTriggerColumns + fromCronTriggers + whereSQL + " ORDER BY id"
	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, opts.Limit)
		argIdx++
	}
	if opts.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argIdx)
		args = append(args, opts.Offset)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("triggers.ListCronTriggers: %w", err)
	}
	defer rows.Close()
	var out []CronTrigger
	for rows.Next() {
		var ct CronTrigger
		var lastRun, nextRun *time.Time
		if err := rows.Scan(&ct.ID, &ct.DisplayName, &ct.AgentRef, &ct.Schedule, &ct.Prompt, &ct.Enabled, &ct.AgentValid, &ct.ScheduleValid, &lastRun, &nextRun, &ct.CreatedAt, &ct.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("triggers.ListCronTriggers: %w", err)
		}
		ct.LastRun = lastRun
		ct.NextRun = nextRun
		out = append(out, ct)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("triggers.ListCronTriggers: %w", err)
	}
	return out, total, nil
}

// UpdateCronTrigger applies a partial update to a cron trigger.
func (s *Store) UpdateCronTrigger(ctx context.Context, id string, displayName, agentRef, schedule, prompt *string, enabled *bool) (*CronTrigger, error) {
	var (
		curName, curAgent, curSchedule, curPrompt string
		curEnabled                                bool
		agentValid, scheduleValid                 bool
		createdAt                                 time.Time
	)
	err := s.pool.QueryRow(ctx, `
		SELECT display_name, agent_ref, schedule, prompt, enabled, agent_valid, schedule_valid, created_at
		FROM cron_triggers WHERE id = $1 FOR UPDATE
	`, id).Scan(&curName, &curAgent, &curSchedule, &curPrompt, &curEnabled, &agentValid, &scheduleValid, &createdAt)
	if err != nil {
		return nil, pgutil.MapNoRows(err, ErrNotFound, "triggers.UpdateCronTrigger")
	}

	name := curName
	agent := curAgent
	sched := curSchedule
	promp := curPrompt
	enab := curEnabled

	if displayName != nil {
		name = *displayName
	}
	if agentRef != nil {
		agent = *agentRef
		// Changing the agent ref invalidates prior agent validation.
		agentValid = false
	}
	if schedule != nil {
		sched = *schedule
		// Changing the schedule invalidates prior schedule validation.
		scheduleValid = false
	}
	if prompt != nil {
		promp = *prompt
	}
	if enabled != nil {
		enab = *enabled
	}

	now := time.Now().UTC()
	if _, err := s.pool.Exec(ctx,
		`UPDATE cron_triggers SET display_name=$1, agent_ref=$2, schedule=$3, prompt=$4, enabled=$5, agent_valid=$6, schedule_valid=$7, updated_at=$8 WHERE id=$9`,
		name, agent, sched, promp, enab, agentValid, scheduleValid, now, id,
	); err != nil {
		return nil, fmt.Errorf("triggers.UpdateCronTrigger: %w", err)
	}

	return &CronTrigger{
		ID:            id,
		DisplayName:   name,
		AgentRef:      agent,
		Schedule:      sched,
		Prompt:        promp,
		Enabled:       enab,
		AgentValid:    agentValid,
		ScheduleValid: scheduleValid,
		CreatedAt:     createdAt,
		UpdatedAt:     now,
	}, nil
}

// DeleteCronTrigger removes a cron trigger by id.
func (s *Store) DeleteCronTrigger(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM cron_triggers WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("triggers.DeleteCronTrigger: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetCronTriggerValidity updates the agent_valid and schedule_valid flags.
func (s *Store) SetCronTriggerValidity(ctx context.Context, id string, agentValid, scheduleValid bool) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE cron_triggers SET agent_valid=$1, schedule_valid=$2, updated_at=now() WHERE id=$3`,
		agentValid, scheduleValid, id)
	if err != nil {
		return fmt.Errorf("triggers.SetCronTriggerValidity: %w", err)
	}
	return nil
}

// UpdateCronTriggerRunTimes updates last_run and next_run for a cron trigger.
func (s *Store) UpdateCronTriggerRunTimes(ctx context.Context, id string, lastRun, nextRun *time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE cron_triggers SET last_run=$1, next_run=$2, updated_at=now() WHERE id=$3`,
		lastRun, nextRun, id)
	if err != nil {
		return fmt.Errorf("triggers.UpdateCronTriggerRunTimes: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------
