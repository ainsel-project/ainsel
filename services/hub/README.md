# Hub

Control plane for the AInsel agent platform. Ingests canonical events,
matches them against the trigger index, records one task per matched
agent in the Postgres-backed event queue, and exposes a REST API for
managing the platform.

## Role in the platform

See [`docs/architecture.md`](../../docs/architecture.md) for the full data
flow.

The hub backend:

- Accepts events at `POST /api/internal/events` from
  [`services/webhook-receiver/`](../webhook-receiver/) and other
  connectors, storing them in the `events` table.
- Holds an in-memory trigger index, synced from the Postgres trigger
  store every 30s.
- Routes unrouted events through the router: trigger match → one row
  per matched agent in `agent_tasks`; the queue-signal publisher mirrors
  per-agent queue depth onto `Agent` status so
  [`operators/agent/`](../../operators/agent/) can render one Job per
  task.
- Serves task fetch (`/api/internal/agents/{name}/tasks/{id}`, `ack`,
  `nack`) for task pods and `/api/v1/*` for the [`frontend/`](../../frontend/)
  console.

## Local development

```bash
# Build
make build
# Or directly:
go build -o bin/hub ./cmd/hub

# Run (needs a kubeconfig and a Postgres DSN)
HUB_DB_URL=postgres://ainselhub:pw@localhost:5432/ainselhub?sslmode=disable \
KUBECONFIG=~/.kube/config \
./bin/hub
```

Ports:

- `8080` — REST API (`HUB_PORT`)
- `9090` — Prometheus metrics (`HUB_METRICS_PORT`)

Required env vars: `HUB_DB_URL` (Postgres DSN — event queue, task logs, MCP
server registry, and the source the event metrics and charts are served from).
Optional: `HUB_PROMETHEUS_URL` for the token metric endpoints and raw PromQL,
and `HUB_METRICS_SOURCE` (`postgres` by default, `prometheus` to pin those event
panels onto the scraped counters). `HUB_NAMESPACE` defaults to `ainsel`.

`HUB_SELF_URL` — the hub's own in-cluster address — turns on the plugin registry
sync loop, which registers a plugin's agent MCP endpoint as a registry row agents
can attach. Unset, the loop logs that it is disabled and stays off, because
there is no address to point those rows at.

## Testing

```bash
make test
# Or:
go test ./...
```

Postgres-backed tests use `internal/dbtest`: each package's `TestMain` starts
one `postgres:17-alpine` testcontainer and migrates it once, and tests get a
fresh cloned database from `dbtest.Open(t)` (or `dbtest.OpenEmpty(t)` /
`dbtest.EmptyDSN(t)` for the migration tests in `internal/db`). Do not call
`pgcontainer.Run` directly in hub tests — `dbtest` is the only place that
does. Without Docker the tests skip (`docker unavailable`) rather than fail;
a failure after the container started (a broken migration, for example) fails
the package instead of skipping.

## Reference

- [REST API endpoints](../../docs/api-reference.md)
- [Event schema](../../docs/event-schema.md)
- [CRD specs (Agent, AgentImage, WebhookConnector)](../../docs/crd-reference.md)
- [Migrating triggers from CRD storage](../../docs/upgrade-guide.md#migrating-triggers-from-crd-storage)
  — one-time `migrate-triggers` tool for upgrading from CRD-based trigger
  storage to the database-backed model
- [`hub migrate-skills-shape`](../../docs/upgrade-guide.md#migrating-the-skills-wrapper-to-a-list)
  — one-time rewrite of `Agent.spec.skills` from the removed `{items: […]}` wrapper
  to the plain list. A subcommand of the hub binary (`/hub migrate-skills-shape`), so it
  runs from the hub image in-cluster; it reads `spec.skills` unstructured, because the
  typed decode is what fails on the old shape. Idempotent, and it exits non-zero when it
  left an unrecognized shape alone
