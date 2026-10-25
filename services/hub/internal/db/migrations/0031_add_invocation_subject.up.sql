-- What an invocation's event was about — one issue or pull/merge request —
-- written by the router at dispatch time. The hub already holds the webhook
-- payload when it records the invocation, so a small subject is stored on the
-- row: a reader asking "what did the agents work on" gets repo, number and
-- title from an indexed query instead of paging through every invocation and
-- fetching each ~20 KB event payload back out.
--
-- Nullable, and deliberately so: rows written before this migration have no
-- subject, and neither has an event that is not about an issue or a pull (a
-- push, a build, a chat message). Adding a column is additive, so existing
-- readers are unaffected.
--
-- The connector index serves the read path this column enables — recent work
-- for one connector, newest first — so the filter is an index scan rather than
-- a scan over the whole retained history.
ALTER TABLE invocations ADD COLUMN subject JSONB;
CREATE INDEX IF NOT EXISTS invocations_connector_start_idx ON invocations (connector, start_time DESC);
