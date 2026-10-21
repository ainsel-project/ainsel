-- Auth events were only structured slog lines (log_type=auth_event), invisible
-- without cluster log access (issue #116). This table persists the same six
-- event types so admins can read the audit trail from the hub API/UI.
--
-- Deliberately narrow: exactly the columns the issue fixes
-- (timestamp, actor sub, event type, target, source IP, outcome). No free-form
-- payload, so "no secrets in the rows" is auditable at a glance — none of the
-- six write sites has a password or token in scope, and the insert helper
-- takes only these fields.
--
-- event_type keeps the existing slog names (auth_login, auth_login_failed,
-- auth_password_changed, user_created, user_password_reset, user_deleted) so a
-- row correlates 1:1 with its log line.
CREATE TABLE IF NOT EXISTS auth_events (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_sub   TEXT NOT NULL,
    event_type  TEXT NOT NULL,
    target      TEXT NOT NULL DEFAULT '',
    source_ip   TEXT NOT NULL DEFAULT '',
    outcome     TEXT NOT NULL
);

-- Newest-first scans back the list endpoint; (event_type, occurred_at) serves
-- the type filter with the same ordering; (actor_sub) serves the user filter.
-- Plain CREATE INDEX, not CONCURRENTLY: golang-migrate submits each file as
-- one implicit transaction (same reasoning as migration 0024).
CREATE INDEX IF NOT EXISTS idx_auth_events_occurred
    ON auth_events (occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_auth_events_actor
    ON auth_events (actor_sub);
CREATE INDEX IF NOT EXISTS idx_auth_events_type_occurred
    ON auth_events (event_type, occurred_at DESC);
