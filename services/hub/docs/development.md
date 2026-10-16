# Development Guide

## Prerequisites

- Go 1.22+
- PostgreSQL (the hub refuses to start without `HUB_DB_URL`)
- Access to a Kubernetes cluster with CRDs installed

## Project Structure

```
cmd/
  hub/
    main.go                     # Entry point, wiring
internal/
  api/
    server.go                   # HTTP server, routing
    handlers_agents.go          # Agent CRUD handlers
    handlers_connectors.go      # Connector CRUD handlers
    handlers_triggers.go        # Trigger CRUD handlers
  router/
    router.go                   # Polls unrouted events, matches triggers, enqueues tasks
  trigger/
    index.go                    # In-memory trigger index
    index_test.go               # Index tests
  metrics/
    metrics.go                  # Prometheus metrics
```

## Building

```bash
make build
```

## Testing

```bash
make test

# Verbose
go test -v ./...
```

## Running Locally

1. Ensure Postgres is running (migrations apply on startup):
   ```bash
   docker run -d -e POSTGRES_USER=ainselhub -e POSTGRES_PASSWORD=pw \
     -e POSTGRES_DB=ainselhub -p 5432:5432 postgres:17
   ```

2. Ensure CRDs are installed in your cluster:
   ```bash
   kubectl apply -f ../../chart/templates/crds/
   ```

3. Run the hub backend:
   ```bash
   export HUB_DB_URL="postgres://ainselhub:pw@localhost:5432/ainselhub?sslmode=disable"
   export HUB_NAMESPACE="ainsel"
   go run ./cmd/hub/
   ```

4. Test the API:
   ```bash
   curl http://localhost:8080/health
   curl http://localhost:8080/api/v1/agents
   ```

## Key Design Decisions

- Uses `controller-runtime` Manager for Kubernetes informers (not a full operator, just cache)
- Triggers live in Postgres; the in-memory index is re-synced from the DB every 30s for fast matching
- REST API covers agent CRD CRUD plus the Postgres-backed resources
- Event routing is queue-based: insert into `events`, poll unrouted, match, enqueue `agent_tasks`
