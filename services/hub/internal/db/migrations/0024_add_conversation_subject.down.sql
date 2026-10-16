DROP INDEX IF EXISTS idx_task_conversations_subject;
ALTER TABLE task_conversations
    DROP COLUMN IF EXISTS repo,
    DROP COLUMN IF EXISTS org,
    DROP COLUMN IF EXISTS event_type,
    DROP COLUMN IF EXISTS issue_number;
