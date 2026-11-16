-- TasksForInvocations enriches every invocations page with its tasks by
-- invocation_id, and agent_tasks had no index on that column, so each page read
-- the whole table. Partial, matching the query's own `invocation_id <> ''` and
-- idx_task_conversations_invocation (0013): rows without an invocation are never
-- looked up by it.
CREATE INDEX IF NOT EXISTS idx_agent_tasks_invocation ON agent_tasks (invocation_id)
    WHERE invocation_id <> '';
