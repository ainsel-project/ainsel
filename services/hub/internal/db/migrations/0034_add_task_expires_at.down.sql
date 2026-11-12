DROP INDEX IF EXISTS idx_agent_tasks_expires_at;
ALTER TABLE agent_tasks DROP COLUMN IF EXISTS expires_at;
-- Restore the pre-0034 vocabulary. Rows already expired cannot be represented
-- in it: adding the narrower CHECK fails loudly rather than silently deleting
-- history or stranding rows under a dropped CHECK. Clean them up by hand first
-- if a rollback with expired rows present is really wanted.
ALTER TABLE agent_tasks
    DROP CONSTRAINT agent_tasks_status_check;
ALTER TABLE agent_tasks
    ADD CONSTRAINT agent_tasks_status_check
    CHECK (status IN ('pending', 'claimed', 'completed', 'failed'));
