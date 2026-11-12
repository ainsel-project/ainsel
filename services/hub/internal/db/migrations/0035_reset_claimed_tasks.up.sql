-- The legacy claim path is gone (issue #47): next-task/ClaimTask was the only
-- writer of status 'claimed', and the stale-claim reaper that returned
-- abandoned claims to the queue went with it. A row still 'claimed' at upgrade
-- time would otherwise sit there forever, so give it the reaper's last word:
-- back to 'pending' (a per-task Job picks it up), or 'failed' once its claim
-- already spent the last attempt. claimed_at is cleared so the row reads as
-- queued, not started, until a worker writes its first conversation row.
--
-- 'claimed' stays in agent_tasks_status_check: historical reads still name it,
-- and narrowing the CHECK buys nothing once no code writes it.
UPDATE agent_tasks
   SET status      = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'pending' END,
       claimed_at  = NULL,
       retry_after = NULL,
       error       = CASE WHEN attempts >= max_attempts
                          THEN 'claim abandoned at upgrade: legacy claim path removed'
                          ELSE error END
 WHERE status = 'claimed';
