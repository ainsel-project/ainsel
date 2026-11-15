-- A queued task whose agent cannot run it (paused, scaled to zero, waiting for
-- a person) waited forever: the sender was never told and a task picked up days
-- later might no longer be relevant (issue #236). EnqueueTask now stamps each
-- task with a pickup deadline (TASK_EXPIRY_SECONDS, default 1 day; 0 disables,
-- which leaves expires_at NULL). A reaper flips pending rows past the deadline
-- to the terminal status 'expired' — distinct from 'failed', because nothing
-- went wrong with the task itself, it was simply never picked up.
--
-- expires_at is fixed at enqueue time rather than derived from created_at at
-- reap time, so changing TASK_EXPIRY_SECONDS never retroactively expires (or
-- rescues) rows an already-running deployment enqueued under the old setting.
ALTER TABLE agent_tasks
    ADD COLUMN expires_at TIMESTAMPTZ;

-- The reaper scans only live rows with a deadline; NULL expires_at (expiry
-- disabled) and terminal rows never match, so the partial index stays small.
CREATE INDEX idx_agent_tasks_expires_at ON agent_tasks (expires_at)
    WHERE status = 'pending' AND expires_at IS NOT NULL;

-- 'expired' is a terminal status alongside 'completed'/'failed' (see the
-- restatement convention in 0032: this CHECK mirrors the SQL literals in
-- internal/eventqueue/store.go, it is a boundary assertion, not new policy).
ALTER TABLE agent_tasks
    DROP CONSTRAINT agent_tasks_status_check;
ALTER TABLE agent_tasks
    ADD CONSTRAINT agent_tasks_status_check
    CHECK (status IN ('pending', 'claimed', 'completed', 'failed', 'expired'));
