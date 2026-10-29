-- Status/level/type columns on the observability tables were bare TEXT, so a
-- writer typo became an unreachable row instead of an error: dashboard filters
-- (idx_agent_tasks_pending, idx_invocations_status, the level counts) silently
-- under-count, and the bad data is only discovered when querying (issue #172).
--
-- Each CHECK below restates the closed set the Go write sites already emit —
-- it is a boundary assertion, not a new policy:
--   agent_tasks.status   — SQL literals in internal/eventqueue/store.go
--   invocations.status   — StatusRunning on record, terminal states via
--                          Complete ('queued'/'unknown' are display-only
--                          derived values and never reach the column)
--   task_logs.level      — constants in internal/tasklogs/types.go; the
--                          internal ingest handler now rejects other values
--                          with 400 before insert
--   chat_messages.role   — chat.RoleUser/RoleAssistant/RoleStatus, already
--                          validated by the chat handler
--   auth_events.event_type / auth_events.outcome — constants in
--                          internal/auditevents/store.go. event_type covers
--                          the six auth slog-correlated names *plus* the
--                          plugin management/API-write events, which share
--                          this table and its Insert path.
--
-- Plain validated ADD CONSTRAINT (no NOT VALID): existing rows are checked in
-- the same transaction, so a deployment with pre-existing garbage fails the
-- migration loudly at upgrade time instead of keeping silently unreachable
-- rows. The validation scan holds ACCESS EXCLUSIVE briefly; at self-hosted
-- scale with retention pruning on the two largest tables this is fine. If a
-- deployment ever has a huge task_logs, the escape hatch is to split the
-- statement into ADD CONSTRAINT ... NOT VALID plus a separate VALIDATE
-- CONSTRAINT.
--
-- Constraint names are left to Postgres (t_col_check) so pg_constraint output
-- stays predictable — no CHECK naming convention exists in this schema yet.
ALTER TABLE agent_tasks
    ADD CONSTRAINT agent_tasks_status_check
    CHECK (status IN ('pending', 'claimed', 'completed', 'failed'));

ALTER TABLE invocations
    ADD CONSTRAINT invocations_status_check
    CHECK (status IN ('running', 'success', 'failure', 'timeout'));

ALTER TABLE task_logs
    ADD CONSTRAINT task_logs_level_check
    CHECK (level IN ('debug', 'info', 'warn', 'error'));

ALTER TABLE chat_messages
    ADD CONSTRAINT chat_messages_role_check
    CHECK (role IN ('user', 'assistant', 'status'));

ALTER TABLE auth_events
    ADD CONSTRAINT auth_events_event_type_check
    CHECK (event_type IN (
        'auth_login',
        'auth_login_failed',
        'auth_password_changed',
        'user_created',
        'user_password_reset',
        'user_deleted',
        'plugin_installed',
        'plugin_uninstalled',
        'plugin_enabled',
        'plugin_disabled',
        'plugin_permissions_approved',
        'plugin_group_access_changed',
        'plugin_api_write'
    ));

ALTER TABLE auth_events
    ADD CONSTRAINT auth_events_outcome_check
    CHECK (outcome IN ('success', 'failure'));
