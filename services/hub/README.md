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
- Serves task pickup (`/api/internal/agents/{name}/next-task`, `ack`,
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

## Testing

```bash
make test
# Or:
go test ./...
```

## Reference

- [REST API endpoints](../../docs/api-reference.md)
- [Event schema](../../docs/event-schema.md)
- [CRD specs (Agent, AgentImage, WebhookConnector)](../../docs/crd-reference.md)
- [Migrating triggers from CRD storage](../../docs/upgrade-guide.md#migrating-triggers-from-crd-storage)
  — one-time `migrate-triggers` tool for upgrading from CRD-based trigger
  storage to the database-backed model
