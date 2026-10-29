-- Recent invocations for one or more connectors, newest first: the read behind
-- GET /api/v1/invocations?connector=…, which a plugin uses to show the work
-- that arrived through its own connectors. Without the index that filter is a
-- scan over the whole retained history; with it, an index range scan in the
-- order the query already sorts by.
--
-- Additive: no column changes, existing readers are unaffected.
CREATE INDEX IF NOT EXISTS invocations_connector_start_idx ON invocations (connector, start_time DESC);
