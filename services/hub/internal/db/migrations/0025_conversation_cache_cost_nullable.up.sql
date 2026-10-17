-- Unreported cache usage and cost were indistinguishable from measured zeros
-- (issue #75): a provider that never reports prompt-cache figures produced the
-- same rows as one that reports cached_tokens: 0, so every surface asserted a
-- measured 0 where nothing was measured. NULL is the only encoding that
-- survives SUM: the read paths keep their COALESCE(SUM(...), 0) numerics and
-- gain honest presence flags from COUNT(col) > 0.
--
-- The ingest path (tasklogs.ConversationMessage) now writes NULL for absent
-- figures and a real number — including a reported 0 — otherwise. Pre-
-- migration rows keep their ambiguous 0 and therefore read as "not reported"
-- on the presence flags, which is the correct reading: their cache/cost usage
-- was never confirmed. No backfill: a timestamp predicate could not tell a
-- legacy row from a reported zero written after the upgrade either; only the
-- ingest path knows the difference, and from here on it encodes it.
ALTER TABLE task_conversations
    ALTER COLUMN cache_read_tokens  DROP DEFAULT,
    ALTER COLUMN cache_read_tokens  DROP NOT NULL,
    ALTER COLUMN cache_write_tokens DROP DEFAULT,
    ALTER COLUMN cache_write_tokens DROP NOT NULL,
    ALTER COLUMN cost_usd           DROP DEFAULT,
    ALTER COLUMN cost_usd           DROP NOT NULL;
