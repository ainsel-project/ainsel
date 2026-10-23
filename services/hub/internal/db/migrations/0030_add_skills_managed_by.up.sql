-- Skills shipped by plugins (plugin-system phase 6). A plugin manifest that
-- lists skills registers each one as a platform skill owned by that plugin;
-- the column records who owns a row, mirroring mcp_servers.managed_by.
--
-- Additive with a DEFAULT: every pre-existing skill row is console-owned and
-- reads as "user" without a backfill, which is exactly the truth for them.
-- The only writer of 'plugin' is the plugin registry syncer; user-facing
-- update/delete of a plugin-owned row is refused so the mapping from a plugin
-- to its skills has exactly one author.
ALTER TABLE skills ADD COLUMN managed_by TEXT NOT NULL DEFAULT 'user';
