-- The token views backed by task_conversations (by-event, per-invocation totals,
-- and the summary/stats figures once the hub serves them from its own records)
-- under-counted every agent loop with prompt caching, because pi reports
-- usage.input excluding cache traffic and the table only stored input/output.
-- Pi reports cacheRead/cacheWrite as their own components, so they get their
-- own columns rather than being folded into input — the same accounting rule
-- the Prometheus agent_tokens_used_total series applies with its token_type
-- label. Existing rows keep zero: their cache traffic was never reported and
-- cannot be reconstructed. See issue #281.
ALTER TABLE task_conversations
    ADD COLUMN IF NOT EXISTS cache_read_tokens  INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_write_tokens INTEGER NOT NULL DEFAULT 0;
