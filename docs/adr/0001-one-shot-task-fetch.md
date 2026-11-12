# ADR 0001: Per-task Jobs fetch their task by id; nothing claims

**Status:** Accepted (issue [#47](https://github.com/ainsel-project/ainsel/issues/47))

## Context

Agents used to run as resident pods that long-polled
`GET /api/internal/agents/{name}/next-task`. The hub held each request open on a
dedicated `LISTEN agent_tasks` connection, claimed the oldest due row with
`FOR UPDATE SKIP LOCKED` (`pending → claimed`, `attempts + 1`), and a reaper
returned claims older than `TASK_CLAIM_TIMEOUT_SECONDS` to the queue.

Since the job-per-task model (#359) the order is reversed: the hub publishes due
task ids onto `Agent status.pendingTaskIds`, the operator creates one Job per id
(`task-<agent>-<id>`, `AINSEL_TASK_ID` in its env), and the runner fetches exactly
that row. By the time a worker exists, its task has already been chosen, so the
long-poll, the NOTIFY wake-up and the claim lease served only pre-#359 images.

## Decision

- The worker's one round-trip is `GET /api/internal/agents/{name}/tasks/{id}`.
  It is read-only apart from re-pointing a retry at a fresh invocation row: it
  does not change `status`, and there is no `POST .../claim`.
- **Pending until ack** is the concurrency contract. A row stays `pending` while
  its Job runs; `ack` completes it, `nack` counts the attempt and schedules the
  retry. A pod that dies mid-task strands nothing — the Job restarts the same row.
- **The Job is the lease.** The deterministic Job name deduplicates workers per
  task and `backoffLimit` paces restarts, so there is no claim expiry to enforce.
- `next-task`, `ClaimTask`, `WaitForTask`, `NotifyAgent`, `ReapStaleClaims` and
  `TASK_CLAIM_TIMEOUT_SECONDS` are removed. Migration `0035` returns leftover
  `claimed` rows to the queue.

## Consequences

- `attempts` grows at nack and only there. A first retry carries `attempts == 1`.
- Pre-#359 runtime images get 404 from `next-task` and must move to a current image.
- The status `claimed` and column `claimed_at` survive for history only.
- Agent pause was enforced solely inside `next-task` and is not enforced on the
  Job path; its removal in favour of `replicas: 0` is tracked in
  [#249](https://github.com/ainsel-project/ainsel/issues/249).
- Nothing records when a worker actually starts. "Started" is inferred from the
  first conversation row, which lags pod boot by up to one LLM turn. A
  start marker can be added later as an idempotent write that leaves `status`
  `pending`, without reintroducing a lease.
