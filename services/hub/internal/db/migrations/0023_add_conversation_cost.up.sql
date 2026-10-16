-- Cost tracking (issue #281). The runtime reports pi's usage.cost.total per
-- message — computed by pi from the model's cost block in the generated
-- models.json (spec.llm.cost) — so the hub can price an agent's consumption
-- from its own records instead of a Prometheus-only counter. Stored per row
-- rather than derived at read time because cache reads/writes are priced
-- very differently from plain input; a cost derived from an input+output
-- total would be wrong in both directions at once. Pre-migration rows keep
-- zero: their usage.cost was never reported and cannot be reconstructed.
ALTER TABLE task_conversations
    ADD COLUMN IF NOT EXISTS cost_usd DOUBLE PRECISION NOT NULL DEFAULT 0;
