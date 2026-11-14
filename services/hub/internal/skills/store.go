package skills

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a skill doesn't exist.
var ErrNotFound = errors.New("skill not found")

// ErrIDTaken is returned when a skill ID collides.
var ErrIDTaken = errors.New("skill ID already in use")

// Store is the skill database layer.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wires a Store against the given pgxpool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// UpdateRequest is the partial-update input shape. Nil fields stay unchanged.
type UpdateRequest struct {
	Name        *string
	Description *string
	Body        *string
	Tags        *[]string
}

// ListFilter holds optional filters and pagination for List.
type ListFilter struct {
	Search string
	Tags   []string

	// AllowedIDs is the authorization allow-list resolved by the handler
	// (see accessibleSet in handlers_skills.go). It is applied in SQL as
	// `id = ANY($n)`, never as a post-filter. The nil-vs-empty distinction
	// is load-bearing and pinned by list_authz_parity_test.go:
	//   - nil   → unrestricted (access control off, or the caller is an admin)
	//   - empty → the caller may see nothing; List returns no rows and total 0
	//   - non-empty → restricts to exactly the listed ids
	AllowedIDs []string

	// OrderBy is a key of SortColumns, already validated by the handler.
	// Empty keeps the default order (created_at DESC, id). Desc reverses
	// the primary expression; the id tiebreaker stays ascending so pages
	// stay deterministic.
	OrderBy string
	Desc    bool

	// Limit/Offset paginate the query. Limit <= 0 means no LIMIT (fetch
	// the full match set); Offset rows are skipped.
	Limit  int
	Offset int
}

// SortColumns maps the (lowercase) orderBy values accepted by the skills
// list endpoint to their SQL ORDER BY expressions. The expressions mirror
// the case-insensitive comparisons the old in-memory sort applied to
// name/id, so pushing the sort into SQL keeps the same page contents.
// usedBy is deliberately absent: it is computed from Agent CRs, not from
// a column, so sorting by it stays in the handler.
var SortColumns = map[string]string{
	"name":      "lower(name)",
	"id":        "lower(id)",
	"createdat": "created_at",
	"updatedat": "updated_at",
}

// Create inserts a new skill. Populates s.CreatedAt, s.UpdatedAt.
func (s *Store) Create(ctx context.Context, sk *Skill) error {
	now := time.Now().UTC()
	if sk.Tags == nil {
		sk.Tags = []string{}
	}
	// managed_by is deliberately not an input here: Create is the user-facing
	// path, and only the plugin syncer's UpsertManaged may claim a row for a
	// plugin.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO skills (id, name, description, body, tags, managed_by, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, 'user', $6, $6)`,
		sk.ID, sk.Name, sk.Description, sk.Body, sk.Tags, now,
	); err != nil {
		if isUniqueViolation(err) {
			return ErrIDTaken
		}
		return fmt.Errorf("insert skill: %w", err)
	}
	sk.ManagedBy = ManagedByUser
	sk.CreatedAt = now
	sk.UpdatedAt = now
	return nil
}

// Get returns a skill by id.
func (s *Store) Get(ctx context.Context, id string) (*Skill, error) {
	var sk Skill
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, description, body, tags, managed_by, created_at, updated_at
		FROM skills WHERE id = $1
	`, id).Scan(&sk.ID, &sk.Name, &sk.Description, &sk.Body, &sk.Tags, &sk.ManagedBy, &sk.CreatedAt, &sk.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("skills.Get: %w", err)
	}
	if sk.Tags == nil {
		sk.Tags = []string{}
	}
	return &sk, nil
}

// List returns one page of skills as summaries (no body) plus the total
// number of rows matching the filter, before pagination. Search, tags, the
// authorization allow-list, the sort and the page bounds are all applied in
// SQL, so the rows read are proportional to the page, not to the table.
func (s *Store) List(ctx context.Context, filter ListFilter) ([]SkillSummary, int, error) {
	// Empty allow-list: the caller may see nothing. Short-circuit before
	// touching the table — and never treat empty as "no restriction".
	if filter.AllowedIDs != nil && len(filter.AllowedIDs) == 0 {
		return nil, 0, nil
	}

	var conditions []string
	var args []any
	argIdx := 1

	if filter.Search != "" {
		conditions = append(conditions,
			fmt.Sprintf("(id ILIKE '%%' || $%d || '%%' OR name ILIKE '%%' || $%d || '%%' OR description ILIKE '%%' || $%d || '%%')", argIdx, argIdx, argIdx))
		args = append(args, escapeILIKE(filter.Search))
		argIdx++
	}
	if len(filter.Tags) > 0 {
		conditions = append(conditions, fmt.Sprintf("tags && $%d::text[]", argIdx))
		args = append(args, filter.Tags)
		argIdx++
	}
	if filter.AllowedIDs != nil {
		conditions = append(conditions, fmt.Sprintf("id = ANY($%d)", argIdx))
		args = append(args, filter.AllowedIDs)
		argIdx++
	}
	whereSQL := ""
	if len(conditions) > 0 {
		whereSQL = " WHERE " + strings.Join(conditions, " AND ")
	}

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM skills`+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("skills.List count: %w", err)
	}

	orderBy := "created_at DESC, id"
	if filter.OrderBy != "" {
		col, ok := SortColumns[filter.OrderBy]
		if !ok {
			return nil, 0, fmt.Errorf("skills.List: unknown sort column %q", filter.OrderBy)
		}
		dir := "ASC"
		if filter.Desc {
			dir = "DESC"
		}
		// id tiebreaker keeps equal-key rows in a stable order across pages.
		orderBy = col + " " + dir + ", id"
	}

	query := `SELECT id, name, description, tags, managed_by, created_at, updated_at FROM skills` +
		whereSQL + " ORDER BY " + orderBy
	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, filter.Limit)
		argIdx++
	}
	if filter.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argIdx)
		args = append(args, filter.Offset)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("skills.List: %w", err)
	}
	defer rows.Close()
	var out []SkillSummary
	for rows.Next() {
		var ss SkillSummary
		if err := rows.Scan(&ss.ID, &ss.Name, &ss.Description, &ss.Tags, &ss.ManagedBy, &ss.CreatedAt, &ss.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("skills.List: %w", err)
		}
		if ss.Tags == nil {
			ss.Tags = []string{}
		}
		out = append(out, ss)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("skills.List: %w", err)
	}
	return out, total, nil
}

// Update applies a partial update.
func (s *Store) Update(ctx context.Context, id string, req UpdateRequest) (*Skill, error) {
	var (
		name, description, body, managedBy string
		tags                               []string
		createdAt                          time.Time
	)
	err := s.pool.QueryRow(ctx, `
		SELECT name, description, body, tags, managed_by, created_at
		FROM skills WHERE id = $1
		FOR UPDATE
	`, id).Scan(&name, &description, &body, &tags, &managedBy, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("skills.Update: %w", err)
	}

	if req.Name != nil {
		name = *req.Name
	}
	if req.Description != nil {
		description = *req.Description
	}
	if req.Body != nil {
		body = *req.Body
	}
	if req.Tags != nil {
		tags = *req.Tags
	}
	if tags == nil {
		tags = []string{}
	}

	now := time.Now().UTC()
	if _, err := s.pool.Exec(ctx,
		`UPDATE skills SET name=$1, description=$2, body=$3, tags=$4, updated_at=$5 WHERE id=$6`,
		name, description, body, tags, now, id,
	); err != nil {
		return nil, fmt.Errorf("skills.Update: %w", err)
	}

	return &Skill{
		ID:          id,
		Name:        name,
		Description: description,
		Body:        body,
		Tags:        tags,
		ManagedBy:   managedBy,
		CreatedAt:   createdAt,
		UpdatedAt:   now,
	}, nil
}

// Delete removes a skill. Returns ErrNotFound if no row was removed.
func (s *Store) Delete(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM skills WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("skills.Delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpsertManaged inserts or updates a plugin-owned skill under the caller's
// id. The id is not validated here: plugin skills carry the fixed
// "<pluginId>-<skillName>" shape the syncer derives from the manifest, and
// that shape is the syncer's contract, not user input. The row is always left
// owned by the plugin, even if the id collided with an existing row.
// ErrIDTakenByUser is returned by UpsertManaged when a skill with that id
// exists and is not plugin-managed.
var ErrIDTakenByUser = errors.New("skill id is taken by a user-managed skill")

func (s *Store) UpsertManaged(ctx context.Context, sk *Skill) error {
	now := time.Now().UTC()
	if sk.Tags == nil {
		sk.Tags = []string{}
	}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO skills (id, name, description, body, tags, managed_by, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, 'plugin', $6, $6)
		 ON CONFLICT (id) DO UPDATE SET
		   name = EXCLUDED.name,
		   description = EXCLUDED.description,
		   body = EXCLUDED.body,
		   tags = EXCLUDED.tags,
		   managed_by = 'plugin',
		   updated_at = EXCLUDED.updated_at
		 WHERE skills.managed_by = 'plugin'
		 RETURNING created_at, updated_at`,
		sk.ID, sk.Name, sk.Description, sk.Body, sk.Tags, now,
	).Scan(&sk.CreatedAt, &sk.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// The conflict target exists but a user owns it: never take it over.
		return ErrIDTakenByUser
	}
	if err != nil {
		return fmt.Errorf("skills.UpsertManaged: %w", err)
	}
	sk.ManagedBy = ManagedByPlugin
	return nil
}

// ListManaged returns the full rows (body included) of every plugin-owned
// skill, ordered by id so a reconcile pass visits them in a stable order.
// The syncer reconciles against this set alone, which is what makes the pass
// safe to run against a registry full of user skills.
func (s *Store) ListManaged(ctx context.Context) ([]Skill, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, description, body, tags, managed_by, created_at, updated_at
		FROM skills WHERE managed_by = 'plugin' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("skills.ListManaged: %w", err)
	}
	defer rows.Close()
	var out []Skill
	for rows.Next() {
		var sk Skill
		if err := rows.Scan(&sk.ID, &sk.Name, &sk.Description, &sk.Body, &sk.Tags, &sk.ManagedBy, &sk.CreatedAt, &sk.UpdatedAt); err != nil {
			return nil, fmt.Errorf("skills.ListManaged: %w", err)
		}
		if sk.Tags == nil {
			sk.Tags = []string{}
		}
		out = append(out, sk)
	}
	return out, rows.Err()
}

// DeleteManaged removes a row by id with no ownership check. It exists for
// the plugin skill syncer, which reconciles the rows ListManaged returned and
// is the only code allowed to drop one; every other caller goes through the
// service's ownership-guarded Delete.
func (s *Store) DeleteManaged(ctx context.Context, id string) error {
	return s.Delete(ctx, id)
}

// escapeILIKE escapes LIKE/ILIKE metacharacters (\, %, _) so user-supplied
// search terms match literally instead of acting as wildcards. The value is
// still bound as a query parameter, so this is about wildcard semantics, not
// injection safety. PostgreSQL's default LIKE escape character is backslash.
func escapeILIKE(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
