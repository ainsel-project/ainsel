package signin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/pgutil"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"
)

// PgStore is the Postgres-backed Store (table agent_sign_in_sessions). It is
// what lets more than one hub replica serve the same sign-in: the console may
// reach one replica and the sign-in pod another.
type PgStore struct {
	pool *pgxpool.Pool
}

// NewPgStore returns a PgStore over pool.
func NewPgStore(pool *pgxpool.Pool) *PgStore {
	return &PgStore{pool: pool}
}

const liveStates = `('waiting_for_pod', 'awaiting_redirect', 'exchanging')`

// sessionColumns reads a live session past its expiry as expired, the same
// rule MemStore applies, so no sweeper is needed for correctness.
const sessionColumns = `id, agent,
	CASE WHEN state IN ` + liveStates + ` AND expires_at <= now() THEN 'expired' ELSE state END,
	auth_url, error, created_at, expires_at, pod_token_hash`

func scanSession(row pgx.Row) (Session, error) {
	var s Session
	var state string
	if err := row.Scan(&s.ID, &s.Agent, &state, &s.AuthURL, &s.Error, &s.CreatedAt, &s.ExpiresAt, &s.PodTokenHash); err != nil {
		return Session{}, pgutil.MapNoRows(err, ErrNotFound)
	}
	s.State = State(state)
	return s, nil
}

func (p *PgStore) Start(ctx context.Context, agent, podTokenHash string) (Session, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return Session{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// An expired session still holds the one-live-session index until it is
	// marked; do that first so it cannot block a new sign-in.
	if _, err := tx.Exec(ctx, `
		UPDATE agent_sign_in_sessions SET state = 'expired', redirect_url = NULL
		 WHERE agent = $1 AND state IN `+liveStates+` AND expires_at <= now()`, agent); err != nil {
		return Session{}, fmt.Errorf("expire stale sign-in sessions: %w", err)
	}
	s, err := scanSession(tx.QueryRow(ctx, `
		INSERT INTO agent_sign_in_sessions (id, agent, state, expires_at, pod_token_hash)
		VALUES ($1, $2, 'waiting_for_pod', now() + $3::interval, $4)
		RETURNING `+sessionColumns, ulid.Make().String(), agent, TTL.String(), podTokenHash))
	if err != nil {
		if pgutil.IsUniqueViolation(err) {
			return Session{}, ErrLive
		}
		return Session{}, fmt.Errorf("insert sign-in session: %w", err)
	}
	return s, tx.Commit(ctx)
}

func (p *PgStore) Latest(ctx context.Context, agent string) (Session, error) {
	return scanSession(p.pool.QueryRow(ctx, `
		SELECT `+sessionColumns+` FROM agent_sign_in_sessions
		 WHERE agent = $1 ORDER BY created_at DESC, id DESC LIMIT 1`, agent))
}

func (p *PgStore) Get(ctx context.Context, agent, id string) (Session, error) {
	return scanSession(p.pool.QueryRow(ctx, `
		SELECT `+sessionColumns+` FROM agent_sign_in_sessions
		 WHERE agent = $1 AND id = $2`, agent, id))
}

// whyNot explains a guarded UPDATE that matched no row.
func (p *PgStore) whyNot(ctx context.Context, agent, id string) error {
	s, err := p.Get(ctx, agent, id)
	if err != nil {
		return err
	}
	if !s.State.Live() {
		return ErrClosed
	}
	return ErrWrongState
}

func (p *PgStore) SetAuthURL(ctx context.Context, agent, id, url string) error {
	tag, err := p.pool.Exec(ctx, `
		UPDATE agent_sign_in_sessions SET auth_url = $3, state = 'awaiting_redirect'
		 WHERE agent = $1 AND id = $2 AND state = 'waiting_for_pod' AND expires_at > now()`, agent, id, url)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return p.whyNot(ctx, agent, id)
	}
	return nil
}

func (p *PgStore) SubmitRedirect(ctx context.Context, agent, url string) error {
	tag, err := p.pool.Exec(ctx, `
		UPDATE agent_sign_in_sessions SET redirect_url = $2
		 WHERE agent = $1 AND state = 'awaiting_redirect' AND expires_at > now()`, agent, url)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	s, err := p.Latest(ctx, agent)
	if err != nil {
		return err
	}
	if !s.State.Live() {
		return ErrNotFound
	}
	return ErrWrongState
}

func (p *PgStore) TakeRedirect(ctx context.Context, agent, id string) (string, error) {
	// Read and clear in one statement, so the pasted URL is handed out once
	// even if two requests race.
	var url string
	err := p.pool.QueryRow(ctx, `
		UPDATE agent_sign_in_sessions s SET redirect_url = NULL, state = 'exchanging'
		  FROM (SELECT id, redirect_url FROM agent_sign_in_sessions
		         WHERE agent = $1 AND id = $2 AND redirect_url IS NOT NULL
		           AND state IN `+liveStates+` AND expires_at > now()
		         FOR UPDATE) old
		 WHERE s.id = old.id
		RETURNING old.redirect_url`, agent, id).Scan(&url)
	if err == nil {
		return url, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if err := p.whyNot(ctx, agent, id); !errors.Is(err, ErrWrongState) {
		return "", err
	}
	return "", nil // live, nothing pasted yet
}

func (p *PgStore) Finish(ctx context.Context, agent, id string, ok bool, errMsg string) error {
	state := StateSucceeded
	if !ok {
		state = StateFailed
	} else {
		errMsg = ""
	}
	tag, err := p.pool.Exec(ctx, `
		UPDATE agent_sign_in_sessions SET state = $3, error = $4, redirect_url = NULL
		 WHERE agent = $1 AND id = $2
		   AND (state = 'exchanging' OR (state IN `+liveStates+` AND expires_at > now()))`,
		agent, id, string(state), errMsg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return p.whyNot(ctx, agent, id)
	}
	return nil
}

func (p *PgStore) Cancel(ctx context.Context, agent string) error {
	_, err := p.pool.Exec(ctx, `
		UPDATE agent_sign_in_sessions SET state = 'cancelled', redirect_url = NULL
		 WHERE agent = $1 AND state IN ('waiting_for_pod', 'awaiting_redirect')`, agent)
	return err
}

func (p *PgStore) Prune(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := p.pool.Exec(ctx, `DELETE FROM agent_sign_in_sessions WHERE created_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
