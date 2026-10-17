# Ainsel Hub Backend REST API Reference

Base URL: `/api/v1`

## Pagination

Collection endpoints (`/agents`, `/connectors`, `/triggers`, `/github-apps`,
`/invocations`) return paginated responses so the API never streams every
record back in a single payload.

**Query parameters:**

| Param | Description |
|-------|-------------|
| `page` | 1-based page number. Defaults to `1`. Values `< 1` are coerced to `1`. |
| `pageSize` | Items per page. Defaults to `50`. Values `< 1` are coerced to `1`, values above `200` are clamped to `200`. |

Non-numeric `page` or `pageSize` values return `400`.

**Response envelope:**

```json
{
  "items": [...],
  "total": 123,
  "page": 1,
  "pageSize": 50,
  "totalPages": 3
}
```

- `total` is the total number of matching records *after* any filters are
  applied, so the frontend can render "Page 1 of N" without a second call.
- `totalPages` is `ceil(total / pageSize)`, or `0` when `total == 0`.
- `items` is always present, even when empty.
- The `/invocations` endpoint preserves its legacy `invocations` key (rather
  than `items`) for backward compatibility but otherwise carries the same
  pagination fields.

Pages are ordered deterministically by resource name (Kubernetes-backed
endpoints) or by start time (invocations) so a given `page` returns the same
slice across calls.

---

## Health Check

### GET /health

Returns the health status of the hub backend.

**Response:** `200 OK`
```json
{"status": "ok"}
```

---

## Agents

### GET /api/v1/agents

List all Agent resources in the configured namespace.

Supports [pagination](#pagination) via `page` / `pageSize`.

**Response:** `200 OK`
```json
{
  "items": [
    {
      "id": "a-1a2b3c4d",
      "name": "Code Reviewer",
      "forgejo": {"username": "code-reviewer", "email": "..."},
      "runtime": {"image": "registry/agent:v1", "provider": "claude"},
      "llm": {"model": "claude-sonnet-4-20250514"},
      "status": {"ready": true, "replicas": 1}
    }
  ],
  "total": 1,
  "page": 1,
  "pageSize": 50,
  "totalPages": 1
}
```

### POST /api/v1/agents

Create a new Agent resource.

**Request Body:** Agent spec (JSON)
**Response:** `201 Created`

### GET /api/v1/agents/:name

Get a specific Agent by name.

**Response:** `200 OK` or `404 Not Found`

### PUT /api/v1/agents/:name

Update an existing Agent.

**Request Body:** Agent spec (JSON)
**Response:** `200 OK` or `404 Not Found`

### DELETE /api/v1/agents/:name

Delete an Agent.

**Response:** `200 OK` or `404 Not Found`

### POST /api/v1/agents/:name/pause

Pause the agent: the hub stops handing it tasks (`next-task` returns no content
while the pause is active). Routed tasks stay queued and drain in id order on
resume — nothing is lost while paused. In-flight tasks finish normally and
interactive chat keeps working. State lives on the Agent CRD (`spec.pause`);
expiry is evaluated at read time. Caveat: while a pause is active nothing
prunes the backlog — an **indefinite** pause lets `agent_tasks` and invocation
rows grow without bound until it is lifted.

**Request Body:** optional. `{"duration": "8h"}` (Go duration from now) or
`{"until": "<RFC3339>"}` (mutually exclusive, must be in the future). An
absent/empty body pauses indefinitely until an explicit resume.

**Response:** `200 OK` with the updated agent (`paused: true`), `400` on an
invalid body, `404 Not Found`. Requires write access on the agent.

### DELETE /api/v1/agents/:name/pause

Resume the agent and wake the queue so the backlog is picked up promptly.

**Response:** `200 OK` with the updated agent (`paused: false`), `404 Not Found`.
Requires write access on the agent.

---

## Agent Images

An AgentImage describes a container image that can back one or more Agents. It
also carries the list of tools the image exposes (populated by the tool-sync
job — see [POST `.../sync`](#post-apiv1agent-imagesnamesync) below). The hub
validates `agents.imageRef.name` and `agents.enabledTools` against this list so
agents cannot reference a missing image or an unknown tool.

### GET /api/v1/agent-images

List all AgentImage resources in the configured namespace. Supports
[pagination](#pagination) via `page` / `pageSize`.

**Response:** `200 OK`
```json
{
  "items": [
    {
      "id": "img-1a2b3c4d",
      "displayName": "Claude Dev Image",
      "description": "Standard Claude development image",
      "imageURL": "registry.example.com/claude-dev:v1",
      "tools": [
        {
          "name": "git",
          "kind": "shell",
          "description": "git CLI",
          "examples": [
            {"title": "status", "snippet": "git status"}
          ]
        }
      ],
      "status": {
        "phase": "Ready",
        "lastSync": "2026-05-10T15:30:00Z",
        "syncError": "",
        "orphanTools": []
      }
    }
  ],
  "total": 1,
  "page": 1,
  "pageSize": 50,
  "totalPages": 1
}
```

`status.phase` is one of `Pending`, `Syncing`, `Ready`, `SyncFailed`.
`tools[].kind` is one of `container`, `shell`, `claude-cli`.

### POST /api/v1/agent-images

Create a new AgentImage. The image starts in `Pending` and has no tools until
a sync job runs successfully (see [POST `.../sync`](#post-apiv1agent-imagesnamesync)).

**Request Body:**
```json
{
  "displayName": "Claude Dev Image",
  "description": "Standard Claude development image",
  "imageURL": "registry.example.com/claude-dev:v1"
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `displayName` | yes | Human-readable name shown in the dashboard. |
| `description` | no | Free-form description. |
| `imageURL` | yes | Fully-qualified container image reference (registry/name:tag). |

**Response:** `201 Created` with the created AgentImage. `400 Bad Request`
when `displayName` or `imageURL` is missing.

### GET /api/v1/agent-images/:name

Get a specific AgentImage by name.

**Response:** `200 OK` or `404 Not Found`.

### PUT /api/v1/agent-images/:name

Update an existing AgentImage. All fields are optional — only fields present
in the request body are mutated. The `tools` field is a full replacement: a
non-nil empty array clears the tool list, while omitting `tools` leaves the
current list untouched.

**Request Body:**
```json
{
  "displayName": "Claude Dev Image (v2)",
  "description": "Updated description",
  "imageURL": "registry.example.com/claude-dev:v2",
  "tools": [
    {
      "name": "git",
      "kind": "shell",
      "description": "git CLI",
      "examples": [{"title": "status", "snippet": "git status"}]
    }
  ]
}
```

**Response:** `200 OK` with the updated AgentImage.

**Tool-removal protection:** If the request would remove a tool that is still
listed in some Agent's `enabledTools` (and that Agent's `imageRef` points at
this image), the request is rejected with `409 Conflict`:

```json
{
  "error": "tool referenced by agent(s)",
  "affectedAgents": ["a-1a2b3c4d"],
  "removedTools": ["git"]
}
```

Resolve by updating the affected agents to remove the tool from their
`enabledTools` first, then retry.

Returns `404 Not Found` when the AgentImage does not exist, `400 Bad Request`
on invalid JSON.

### DELETE /api/v1/agent-images/:name

Delete an AgentImage.

**Response:** `204 No Content` on success, with no response body.

**Reference protection:** If any Agent in the namespace has `imageRef.name`
pointing at this image, the delete is rejected with `409 Conflict`:

```json
{
  "error": "agent image referenced by agent(s)",
  "affectedAgents": ["a-1a2b3c4d", "a-5e6f7g8h"]
}
```

Resolve by deleting or repointing the listed agents first, then retry.

Returns `404 Not Found` when the AgentImage does not exist, and
`500 Internal Server Error` for any other backend failure (network, RBAC,
missing CRD).

### POST /api/v1/agent-images/:name/sync

Trigger an asynchronous tool-sync job for an AgentImage. The hub creates a
short-lived Kubernetes Job that runs `agent --list-tools` against the
image; when the Job completes, the controller writes the discovered tools
back into `.spec.tools` and transitions `.status.phase` from `Syncing` to
`Ready` (or `SyncFailed` if the Job errored or timed out).

**Request Body:** none.

**Response:**
- `202 Accepted` — Job created, phase advanced to `Syncing`. No body.
- `404 Not Found` — AgentImage does not exist.
- `409 Conflict` — a sync Job for this image is already running. Wait for it
  to finish before retrying.
- `500 Internal Server Error` — Job creation or status update failed.

Sync Jobs have an `activeDeadlineSeconds` of 60 and a `ttlSecondsAfterFinished`
of 300, so they self-clean up shortly after completion.

---

## Connectors

### GET /api/v1/connectors

List all connector resources. Supports
[pagination](#pagination) via `page` / `pageSize`.

**Response:** `200 OK`

### POST /api/v1/connectors

Create a new WebhookConnector resource.

**Request Body:** WebhookConnector spec (JSON)
**Response:** `201 Created`

### GET /api/v1/connectors/:name

Get a specific WebhookConnector by name.

### PUT /api/v1/connectors/:name

Update a WebhookConnector.

### DELETE /api/v1/connectors/:name

Delete a WebhookConnector.

---

## Triggers

### GET /api/v1/triggers

List Trigger resources. Supports [pagination](#pagination) via `page` /
`pageSize`. Pagination is applied after filtering, so `total` reflects the
filtered count.

**Query parameters:**

| Param | Description |
|-------|-------------|
| `agent` | Filter by `spec.agentRef` (exact, case-sensitive match) |
| `connector` | Filter by `spec.connectorRef` (exact, case-sensitive match) |
| `eventType` | Filter by `spec.eventType` (exact, case-sensitive match, e.g. `issue.opened`) |
| `page` | See [pagination](#pagination) |
| `pageSize` | See [pagination](#pagination) |

Filters compose with AND semantics; an unset filter matches everything.
An empty query string returns every trigger in the namespace (paginated).

**Response:** `200 OK`
```json
{
  "items": [
    {
      "id": "t-1a2b3c4d",
      "name": "develop-on-assign",
      "agentRef": "dev-agent",
      "connectorRef": "forgejo-dev",
      "eventType": "issue.assigned",
      "ignoreBotEvents": true,
      "status": {"agentValid": true, "connectorValid": true}
    }
  ],
  "total": 1,
  "page": 1,
  "pageSize": 50,
  "totalPages": 1
}
```

**Examples:**

```
GET /api/v1/triggers?agent=dev-agent
GET /api/v1/triggers?connector=forgejo-dev&eventType=issue.opened&page=2&pageSize=20
GET /api/v1/triggers?agent=reviewer&connector=forgejo-dev&eventType=issue.opened
```

### POST /api/v1/triggers

Create a new Trigger resource.

**Request Body:** Trigger spec (JSON)
**Response:** `201 Created`

### GET /api/v1/triggers/:name

Get a specific Trigger by name.

### PUT /api/v1/triggers/:name

Update a Trigger.

### DELETE /api/v1/triggers/:name

Delete a Trigger.

---

## Channels

A channel is a named stream events live in. The hub owns them in Postgres —
they are not CRDs — and provisions them from the registries that already
exist: one **connector** channel per `WebhookConnector` (where its events
are born), one **agent** channel per `Agent` (its inbox), plus **custom**
grouping channels users create. Identity is the channel id, never the name:
a connector and an agent may both be called `forgejo`.

Subscriptions come from two registries and are read together:

| Source | Owned by | Meaning |
|--------|----------|---------|
| `trigger` | the trigger registry | a connector's events are routed to an agent inbox — still the single source of truth for that routing, never copied into a bridge |
| `bridge` | the channel graph | events arriving in one channel are transferred into another; at least one end must be custom, and the graph must stay acyclic |

### GET /api/v1/channels

List channels with their traffic counts. Supports
[pagination](#pagination). Sorted by event count in the window, then id.

Query parameters: `kind` (`connector` | `agent` | `custom`), `since`
(RFC 3339 lower bound of the count window; defaults to 24 hours ago).

**Response:** `{ items, total, page, pageSize, totalPages, window }` where
each item is:

```json
{
  "id": "ch-9f3c1a02",
  "kind": "connector",
  "name": "forgejo",
  "description": "Events published by connector \"forgejo\"",
  "entityRef": "forgejo",
  "orphaned": false,
  "createdAt": "2026-07-08T09:00:00Z",
  "updatedAt": "2026-07-08T09:00:00Z",
  "counts": { "events": 12, "unmatched": 3, "failed": 1 },
  "bridges": 1,
  "subscriptions": 2
}
```

`counts` are over the window: events **born** in the channel, of those the
ones no subscription transferred onward, and the ones whose run failed.
An agent inbox counts what **reached** it, born there or transferred in.

### POST /api/v1/channels

Create a custom grouping channel. Provisioned kinds are not creatable here —
they exist because their connector or agent does.

**Body:** `{ "name": "team-inbox", "description": "optional", "groupId": "optional" }`.
`groupId` is required when access control is enabled.

**Response:** `201 Created` with the channel, or `400` (blank name, missing
group) / `403`.

### GET /api/v1/channels/:id

One channel plus its edges: the list item's fields with
`incoming` and `outgoing` arrays of subscriptions.

```json
{
  "id": "ch-9f3c1a02",
  "kind": "custom",
  "name": "team-inbox",
  "incoming": [{ "source": "bridge", "refId": "br-4d2e8b11", "name": "all-issues",
                 "fromChannel": "ch-9f3c1a02", "toChannel": "ch-9f3c1a02",
                 "fromName": "forgejo", "fromKind": "connector" }],
  "outgoing": []
}
```

### PUT /api/v1/channels/:id

Rename a custom channel or change its description. Provisioned channels carry
their entity's name and are not renamed here.

### DELETE /api/v1/channels/:id

Delete a custom channel. `409 Conflict` while any subscription is attached to
it — a stream cannot disappear underneath the subscriptions that use it.

### GET /api/v1/channels/:id/events

The channel's timeline: events born in it, plus events transferred into it.
Query parameters: `limit` (default 50, max 200), `offset`, `since`, `agent`,
`subject` (`<connector>.<eventType>`), `status`. Same envelope as
`GET /api/v1/events`.

### POST /api/v1/channels/:id/bridges

Transfer the channel's events into another. **Body:**
`{ "to": "ch-9f3c1a02", "name": "optional" }`. Rejected with `400` on a
self-edge, when neither end is a custom channel, when the edge would create a
cycle, or when it joins two provisioned channels directly (that pairing is a
trigger); `409` if the same edge already exists.

### DELETE /api/v1/channels/:id/bridges/:bridgeId

Remove one bridge. The `:id` must be the bridge's **source** channel; the
callers' write permission on both endpoints is required.

### GET /api/v1/channel-subscriptions

Every edge in the graph in one call, both registries merged, so a client can
draw the subscriptions without walking each channel. An edge is reported only
when the caller can see both of its endpoints.

**Response:** `{ items, total }`.

### Authorization

A provisioned channel is authorized as its entity: reading a connector's
channel needs read access to that **connector**, an agent's inbox needs the
**agent**. Custom channels are authorized as `channel` resources. Grants on
existing connectors and agents therefore cover their channels without a new
permission step.

---

## Invocations

The hub records every event dispatch to an agent in the `invocations`
Postgres table (48h retention, aligned with the conversation transcript
retention, pruned hourly). Each record tracks who was invoked, with which
trigger, when it started/completed, and what the outcome was. Agents report
completion when its worker acks the task (`POST
/api/internal/agents/{name}/ack`); failures and timeouts are recorded on the
nack and reaper paths. The `X-Invocation-ID` header from the dispatched
message ties the transcript to the record.

### GET /api/v1/invocations

List recent invocations newest-first. Supports [pagination](#pagination) via
`page` / `pageSize`. The `limit` parameter from earlier revisions has been
superseded by `pageSize` — older clients that send `limit` will fall through
to the default pageSize.

**Query parameters:**

| Param | Description |
|-------|-------------|
| `agent` | Filter by agent name |
| `status` | Filter by status: `running`, `success`, `failure`, `timeout` |
| `since` | RFC3339 timestamp; only invocations started at or after this time |
| `page` | See [pagination](#pagination) |
| `pageSize` | See [pagination](#pagination) |

**Response:** `200 OK`
```json
{
  "invocations": [
    {
      "id": "inv-1a2b3c4d",
      "agentName": "dev-agent",
      "triggerName": "issue-assigned",
      "eventId": "evt-abc",
      "eventType": "issue.assigned",
      "eventSource": "forgejo",
      "connector": "my-forgejo",
      "startTime": "2026-05-10T15:30:00Z",
      "endTime": "2026-05-10T15:30:42Z",
      "durationMs": 42000,
      "status": "success"
    }
  ],
  "total": 1,
  "capacity": 1000,
  "page": 1,
  "pageSize": 50,
  "totalPages": 1
}
```

The `invocations` key (instead of `items`) is kept for backward compatibility
with frontend code that pre-dates the unified pagination envelope.

### GET /api/v1/invocations/:id

Get a single invocation by ID.

**Response:** `200 OK` or `404 Not Found`

When the invocation store is not configured, both endpoints return `503`.

---

## Observability Metrics

The hub exposes a small read-only API over its event metrics so the frontend
dashboard can render hub-internal counters, time series, and per-agent token
usage without talking to a metrics backend directly.

Two backends can serve these. The endpoints that can be answered from either one
— `metrics/summary`, `metrics/timeseries`, `metrics/agents`, `tokens/summary`,
`tokens/timeseries` and `tokens/by-subject` — name the backend that answered in a
`source` field (`"postgres"` / `"prometheus"`); the single-backend endpoints carry
no such field. For the panel-by-panel view of which backend serves which dashboard
panel, see [docs/observability.md](../../../docs/observability.md) — this page
stays about the endpoints.

| `source` | Reads | Serves | Point value |
|----------|-------|--------|-------------|
| `postgres` | The rows the hub wrote while routing: `events`, `agent_tasks`, `task_logs`, plus the conversation rows agents report (`task_conversations`) and the invocation records (`invocations`) | `metrics/summary`, `metrics/timeseries` and `metrics/agents` by default, and the `tokens/*` endpoints as their own rules below allow | count per bucket |
| `prometheus` | The hub's own scraped counters (`hub_events_consumed_total`, …) and the agent runtime's token counters (`agent_tokens_used_total`, `agent_invocations_total`) | the dual-backend endpoints above when pinned or when there is no database, plus raw PromQL (`metrics/query`) | per-second rate for a metric that defines a rate query, otherwise the counter total |

The hub's own records answer these endpoints by default. Those rows are
written by the same routing decision that increments the counters, so asking
Prometheus about them is asking a copy of the hub's own ledger — and the copy is
worse in two ways: a counter restarts at zero with the pod, so `sum(hub_events_consumed_total)`
after a roll reports one pod's uptime rather than the platform's, and `events` and
`agent_tasks` are never pruned, so they hold at least as much history as a
retention-bounded TSDB.

Set `observability.metricsSource` (`HUB_METRICS_SOURCE`) to `prometheus` to pin the
counters instead. That is the honest choice for one case: `task_logs` is pruned
after 7 days, so a `7d` error figure read from the records covers only the errors
still retained, where a counter that predates the pruning remembers all of them.
A pin whose dependency is missing is reported as `503`, not silently answered from
the other backend.

Per endpoint, the routing rule is:

- `tokens/summary` and `tokens/timeseries` — the hub's own `task_conversations`
  rows by default: since migration 0022 they carry every token component the
  runtime reports, cache traffic included. Prometheus answers them when pinned,
  or as a fallback when the hub has no database. Records are the better source
  here for a sharper reason than the event rows: agent pods often finish before
  the first scrape, so a scale-to-zero agent's consumption can be invisible to
  Prometheus forever, while its conversation rows are written when the turn
  ends. Responses report `source`.
- `tokens/by-event` — records only. It always reads `task_conversations`, even
  when the source is pinned to Prometheus; it is the only token endpoint that
  never answers from Prometheus. With no database it answers `503` with
  `{"error": "hub database not configured: these panels read the hub's own task log table"}`.
- `tokens/by-subject` — the hub's own `task_conversations` rows by default: the
  rows are grouped by the subject columns migration 0024 added (repo, event
  type), and the same four token components and the cost are summed per group,
  so the table's total equals `tokens/summary`'s for the same window. Prometheus
  answers when pinned, or as a fallback when the hub has no database. Rows
  written before migration 0024 carry no subject; they group under the empty
  string rather than being dropped — they are still consumption. Responses
  report `source`.
- `metrics/agents` — the hub's own `task_conversations` and `invocations` rows
  by default: the agent-side counters it used to read (`agent_tokens_used_total`,
  `agent_invocations_total`) live in agent pods that routinely finish before the
  first scrape, while the hub writes an invocation record on every dispatch.
  Prometheus answers it when pinned, or as a fallback when the hub has no
  database. Responses report `source`.
- `metrics/query` (raw PromQL) — Prometheus only, and admin-only.

Responses are cached server-side for ~30 seconds keyed by backend, so a busy
dashboard does not generate one-query-per-poll and a hub that switches source
cannot serve stale figures from the other one.

### Canonical paths

| Path | Returns |
|------|---------|
| `GET /api/v1/observability/metrics/summary` | Count of each hub metric, from the hub's records (default) or from counters when pinned |
| `GET /api/v1/observability/metrics/timeseries?metric=<name>&range=<1h\|6h\|24h\|7d>` | One metric across the window in dense, evenly-spaced points |
| `GET /api/v1/observability/metrics/agents` | Per-agent token usage and invocations, from the hub's records (default) or from the agent counters when pinned; response reports `source` |
| `GET /api/v1/observability/metrics/tokens/summary?range=<1h\|6h\|24h\|7d>` | Token totals over the window, from the hub's conversation rows (default) or from counters when pinned; response reports `source` |
| `GET /api/v1/observability/metrics/tokens/timeseries?range=24h` | Token usage across the window in dense, evenly-spaced points; records by default, Prometheus when pinned or when there is no database; response reports `source` |
| `GET /api/v1/observability/metrics/tokens/by-event?range=<1h\|6h\|24h\|7d>` | Token totals per event, from the hub's conversation rows only (ignores the pin) |
| `GET /api/v1/observability/metrics/tokens/by-subject?range=<1h\|6h\|24h\|7d>` | Token and cost totals per agent/repo/event-type/model, from the hub's conversation rows (default) or from the agent counters when pinned; response reports `source` |

### GET /api/v1/observability/metrics/summary

**Query parameters:**

| Param | Description |
|-------|-------------|
| `range` | Optional. One of `1h`, `6h`, `24h`, `7d`. When supplied, counters are windowed using `increase()` over the given range. When omitted, returns all-time cumulative counter values (legacy behavior). |

**Response:** `200 OK`
```json
{
  "eventsConsumed": 42,
  "triggersMatched": 30,
  "eventsRouted": 29,
  "routingErrors": 1,
  "updatedAt": "2026-05-10T15:30:00Z",
  "source": "postgres"
}
```

`source` is `"postgres"` when the hub answered from its own records — the default
— and `"prometheus"` when it read counters. Under Postgres the counts cover only
rows the hub still retains, and `range` omitted means "everything retained" rather
than "lifetime".

Metrics that have never been observed (e.g. fresh hub) return `0` rather
than an error so the dashboard renders a clean zero state. Returns `400`
when `range` is outside the supported set, `503` when the hub has neither
Prometheus nor a database to read its own records from, and `502` when the
backend it did pick fails to answer.

### GET /api/v1/observability/metrics/tokens/summary

**Query parameters:**

| Param | Description |
|-------|-------------|
| `range` | Optional. One of `1h`, `6h`, `24h`, `7d`. Defaults to `24h` when omitted. |

**Response:** `200 OK`
```json
{
  "source": "postgres",
  "inputTokens": 800000,
  "outputTokens": 400000,
  "cacheReadTokens": 1200000,
  "cacheWriteTokens": 30000,
  "totalTokens": 2430000,
  "previousTotalTokens": 950000,
  "costUsd": 1.23,
  "cacheReported": true,
  "costReported": true,
  "updatedAt": "2026-05-10T15:30:00Z"
}
```

`source` is `"postgres"` when the hub answered from its own conversation rows —
the default — and `"prometheus"` when it read the token counters. `totalTokens`
sums all four components. `costUsd` is the priced consumption over the window;
it is only meaningful once the agent's model is priced (`spec.llm.cost`).
`cacheReported`/`costReported` are presence flags: `false` means nothing in the
window reported that figure — an un-caching provider, an unpriced model, or a
window of rows that predate the nullable columns — so the corresponding numeric
is a placeholder readers must render as "not reported", never as a measured
`0`/`0%`/`$0.00`. A reported zero (a real 0% hit rate, a free run) keeps its
flag `true`. See [docs/observability.md](../../../docs/observability.md).
`previousTotalTokens` contains the token total for the equivalent prior
window (used by the frontend for trend indicators). Returns `400` when
`range` is outside the supported set; `503` when the hub has neither a database
nor Prometheus to read tokens from —
`{"error": "no metrics backend: give the hub a database to read its own records from, or set observability.prometheus.url"}` —
or when Prometheus is pinned but not configured —
`{"error": "prometheus not configured: set observability.prometheus.url"}`; and
`502` when the backend it did pick fails to answer.

### GET /api/v1/observability/metrics/tokens/timeseries

The sparkline behind the token tile: token usage across the window in dense,
evenly-spaced points. Answered from the hub's conversation rows by default,
from the token counters when `observability.metricsSource` pins Prometheus or
the hub has no database.

**Query parameters:**

| Param | Description |
|-------|-------------|
| `range` | Optional. Only `24h` is supported today (the tile is hardcoded to 24h); any other value returns `400`. |

**Response:** `200 OK`
```json
{
  "source": "postgres",
  "range": "24h",
  "step": "30m",
  "points": [
    {"timestamp": "2026-05-10T14:00:00Z", "value": 12000},
    {"timestamp": "2026-05-10T14:30:00Z", "value": 9500}
  ]
}
```

The series is **dense**: every bucket across the window is present, gaps
zero-filled, so the sparkline can place points by index. Presence is reported
on `tokens/summary` and the by-event/by-subject rows only: a point here is a
single summed total with no cache or cost component to qualify, so the series
carries no `cacheReported`/`costReported` flag. Returns `400` when
`range` is not `24h`, `503` when neither backend can answer (the `503` reason
names the missing dependency), and `502` when the chosen backend fails.

### GET /api/v1/observability/metrics/tokens/by-event

Token totals grouped by event ID, for attributing usage to the issue or PR that
caused it. Reads the hub's own `task_conversations` rows grouped by invocation
and maps each invocation to its event. This endpoint is records-only: it
ignores `observability.metricsSource` and never answers from Prometheus. Its
response carries no `source` field because there is only one backend.

**Query parameters:**

| Param | Description |
|-------|-------------|
| `range` | Optional. One of `1h`, `6h`, `24h`, `7d`. Defaults to `24h` when omitted. |

**Response:** `200 OK`
```json
{
  "range": "24h",
  "rows": [
    {
      "event": "issue-42",
      "inputTokens": 15000,
      "outputTokens": 4000,
      "cacheReadTokens": 60000,
      "cacheWriteTokens": 2000,
      "totalTokens": 81000,
      "costUsd": 0.42,
      "cacheReported": true,
      "costReported": true
    }
  ],
  "updatedAt": "2026-05-10T15:30:00Z"
}
```

Each row carries the same `cacheReported`/`costReported` presence flags as
`tokens/summary`: true when any invocation of that event reported the figure,
false when the summed cache/cost are placeholders for "not reported".

Returns `400` when `range` is outside the supported set, `503` with
`{"error": "hub database not configured: these panels read the hub's own task log table"}`
when the hub has no database, and `502` when the query fails.

### GET /api/v1/observability/metrics/tokens/by-subject

Token and cost totals per `(agent, repo, event type, model)` tuple — the Agent
activity table on the dashboard. The hub's own `task_conversations` rows answer
by default, grouped by the subject columns migration 0024 added; the Prometheus
counters answer when the source is pinned, or when the hub has no database. The
two differ in coverage, not units: a scale-to-zero agent has conversation rows
even when it was never scraped, and rows written before migration 0024 carry no
subject — they group under the empty string rather than being dropped, so the
table's total equals `tokens/summary`'s for the same window on either backend.
The one exception, on both backends: a turn with no agent name is left out of
the table (it would render as a blank row) while the summary tile still counts
it, so the totals can differ by exactly those rows. See
[docs/observability.md](../../../docs/observability.md) for the panel view.

**Query parameters:**

| Param | Description |
|-------|-------------|
| `range` | Optional. One of `1h`, `6h`, `24h`, `7d`. Defaults to `24h` when omitted. |

**Response:** `200 OK`
```json
{
  "range": "24h",
  "rows": [
    {
      "agent": "dev",
      "agentName": "Developer",
      "repo": "ainsel-project/ainsel",
      "eventType": "issue",
      "model": "claude-sonnet",
      "inputTokens": 1500,
      "outputTokens": 300,
      "cacheReadTokens": 9000,
      "cacheWriteTokens": 500,
      "totalTokens": 11300,
      "costUsd": 0.05,
      "cacheReported": true,
      "costReported": true
    }
  ],
  "updatedAt": "2026-05-10T15:30:00Z",
  "source": "postgres"
}
```

Each row carries the same `cacheReported`/`costReported` presence flags as
`tokens/summary` — per group, not per window.

Returns `400` when `range` is outside the supported set, `503` with
`{"error": "no metrics backend: give the hub a database to read its own records from, or set observability.prometheus.url"}`
when neither backend can answer — or with
`{"error": "prometheus not configured: set observability.prometheus.url"}` when
Prometheus is pinned but not configured — and `502` when the query fails.

### GET /api/v1/observability/metrics/timeseries

**Query parameters:**

| Param | Description |
|-------|-------------|
| `metric` | One of `events_consumed`, `triggers_matched`, `events_routed`, `routing_errors`. Defaults to `events_consumed` so the dashboard's "no metric picked yet" view renders. |
| `range` | One of `1h`, `6h`, `24h`, `7d`. Defaults to `1h`. |

**Response:** `200 OK`
```json
{
  "metric": "events_consumed",
  "range": "1h",
  "step": "30s",
  "points": [
    {"timestamp": "2026-05-10T14:30:00Z", "value": 0.5},
    {"timestamp": "2026-05-10T14:30:30Z", "value": 0.7}
  ],
  "source": "prometheus"
}
```

Points are spaced so each chart has roughly 60-180 samples regardless of
range, and the series is **dense**: every bucket across the window is present,
gaps zero-filled, so a chart can place points by index. For counter metrics
under Prometheus the `points[].value` is the per-second rate over the range's
natural rate window (`1m` for `1h`, `5m` for `6h`, etc.), not the raw counter;
under Postgres it is the number of matching rows inside that bucket. The
response's `source` says which of the two you are looking at. Returns `400`
when `metric` is unknown, when the active backend cannot answer it, or when
`range` is outside the supported set. A failure *during* the read is not a
`400`: `503` when the hub has neither Prometheus nor a database, and `502`
when the query fails or the records store returns buckets the hub cannot place
on this window's grid — refused rather than drawn as a mis-dated chart.

### GET /api/v1/observability/metrics/agents

**Query parameters:**

| Param | Description |
|-------|-------------|
| `range` | Optional. One of `1h`, `6h`, `24h`, `7d`. Applied on the records path, which windows both store queries. When omitted, returns everything still retained (see the retention note below). The Prometheus path reads all-time counters and ignores `range`. |

**Response:** `200 OK`
```json
{
  "agents": [
    {
      "agent": "dev",
      "agentName": "Dev Bot",
      "inputTokens": 1500,
      "outputTokens": 300,
      "cacheReadTokens": 8000,
      "cacheWriteTokens": 200,
      "totalTokens": 10000,
      "invocations": 5,
      "failures": 1,
      "avgDurationMs": 3200,
      "cacheReported": true
    }
  ],
  "updatedAt": "2026-05-10T15:30:00Z",
  "source": "postgres"
}
```

`totalTokens` sums all four components — pi's reported `input` excludes the
cache components, so a total built from input + output alone under-counts by
the whole cached volume. `cacheReported` is the row's presence flag for the
cache components ("not reported" vs. a measured zero — see `tokens/summary`).
`failures` counts invocations in a `failure` or
`timeout` status and `avgDurationMs` averages the completed invocations'
duration; both are populated on the records path only (zero under
`source: "prometheus"`, where they have no counter to come from).
`invocations` counts every status, including `running`, over
`start_time >= since AND < until` — the same bounds as
`GET /api/v1/invocations?agent=<name>`, so the two views agree for a given
window.

Agents are sorted alphabetically for stable diffing across polls. An agent
with no activity in the window has no row; an agent appears if it has either
conversation rows or invocation rows, so a dispatched-but-failed agent (no
conversations) still shows up.

Retention applies: `task_conversations` and `invocations` are pruned after
48 hours, so on the records path "all-time" (range omitted) means
everything still retained, not lifetime — the same caveat the token summary
documents. A Prometheus pin remains the way to read wider windows.

---

## Observability Logs

The hub serves the console's log panels from its own `task_logs` table, which
agents fill while a task runs. No external log backend is involved.

### GET /api/observability/logs

Also available at `GET /api/v1/observability/logs`.

**Query parameters:**

| Param | Description |
|-------|-------------|
| `app` | Agent **name** to filter by (the label name is a holdover from when this endpoint proxied Loki). Requires read access to that agent, else `403`. |
| `range` | Lookback window: `1h`, `6h`, or `24h`. Defaults to `1h`. |
| `since` | Go duration string (e.g. `90m`); overrides `range`. |
| `limit` | Max log lines to return. Defaults to 500, capped at 1000. |

When `app` is omitted the result is scoped to the agents the caller may read,
so a non-admin never receives another tenant's task logs. Error and log content
is conversation data: whatever the agent and the user said is in it.

The hub's own process log (startup, routing decisions, `activity_event` lines)
is stdout JSON, not this table — read it with `kubectl logs`.

**Response:** `200 OK`
```json
{
  "logs": [
    {
      "timestamp": "2026-05-10T15:30:00Z",
      "message": "starting hub",
      "labels": {"app": "hub-backend", "namespace": "ainsel"}
    }
  ],
  "total": 1,
  "query": "{namespace=\"ainsel\",app=\"hub-backend\"}"
}
```

Lines are returned newest-first. Returns `503` when the hub has no database
configured and `502` when the query fails.

---

## Error Responses

All error responses follow the format:

```json
{"error": "error message"}
```

| Status | Meaning |
|--------|---------|
| `400` | Bad request (invalid JSON, missing fields) |
| `404` | Resource not found |
| `409` | Conflict (resource already exists, or referenced by another resource — e.g. deleting an AgentImage that an Agent still uses) |
| `500` | Internal server error |

Successful mutations return `200 OK` (update) or `201 Created` (create) with a
JSON body. Successful `DELETE` returns `204 No Content` with an empty body.
Asynchronous trigger endpoints (e.g. `POST /agent-images/:name/sync`) return
`202 Accepted` with no body.

## Ingress

When deployed via the ainsel-chart, the API is exposed at:
```
https://ainsel.example.com/ainsel/api/v1/...
```

The nginx ingress rewrites `/ainsel/api` to `/api`.
