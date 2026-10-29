-- Rolling back only forgets the constraints; the rows themselves are
-- untouched (every row written under the constraints stays valid as free
-- text again). IF EXISTS keeps re-runs idempotent, matching the IF NOT
-- EXISTS style used elsewhere.
ALTER TABLE agent_tasks     DROP CONSTRAINT IF EXISTS agent_tasks_status_check;
ALTER TABLE invocations     DROP CONSTRAINT IF EXISTS invocations_status_check;
ALTER TABLE task_logs       DROP CONSTRAINT IF EXISTS task_logs_level_check;
ALTER TABLE chat_messages   DROP CONSTRAINT IF EXISTS chat_messages_role_check;
ALTER TABLE auth_events     DROP CONSTRAINT IF EXISTS auth_events_event_type_check;
ALTER TABLE auth_events     DROP CONSTRAINT IF EXISTS auth_events_outcome_check;
