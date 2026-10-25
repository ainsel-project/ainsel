-- Rolling back forgets the per-invocation subject. Nothing else depends on it:
-- the origin of an invocation is still readable from its event payload, so no
-- data has to be reconstructed. The index goes with it — it exists to serve
-- the subject read path and nothing else queries (connector, start_time).
DROP INDEX IF EXISTS invocations_connector_start_idx;
ALTER TABLE invocations DROP COLUMN subject;
