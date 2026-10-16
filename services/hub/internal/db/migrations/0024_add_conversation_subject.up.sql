-- Token tables that group by subject (repo / org / event type / issue) were
-- Prometheus-only because the hub kept no rows for those dimensions. The
-- runner already normalises the subject (normalizeEventContext) and reports
-- it on the agent_tokens_used_total counter; these columns store the same
-- values next to the token components they belong to, so the by-subject
-- views can be served from the hub's own records (issue #410, consumers
-- #411/#412). Rows written before this migration keep '' / 0: they are
-- still consumption, and readers must render their subject as "unknown"
-- rather than dropping them.
ALTER TABLE task_conversations
    ADD COLUMN IF NOT EXISTS repo         TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS org          TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS event_type   TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS issue_number INTEGER NOT NULL DEFAULT 0;

-- Windowed group-by for the by-subject token tables. Plain CREATE INDEX, not
-- CONCURRENTLY: golang-migrate submits each file as one implicit transaction
-- and PostgreSQL refuses a concurrent index build inside one (same reasoning
-- as migration 0021).
CREATE INDEX IF NOT EXISTS idx_task_conversations_subject
    ON task_conversations (agent_name, repo, event_type, created_at DESC);
