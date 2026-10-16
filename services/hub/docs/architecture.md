# Ainsel Hub Backend Architecture

> The system-wide data flow lives in
> [`docs/architecture.md`](../../../docs/architecture.md). This document
> covers the hub process internals only.

## Overview

The hub backend is the central control plane of Ainsel. It combines an event
router, a trigger index, a cron emitter, and a REST API into a single
process. Its durable store is PostgreSQL: the `events` and `agent_tasks`
tables form the event queue, and the same database holds triggers, cron
triggers, personas, skills, MCP servers, task logs and conversations.

## Component Structure

```mermaid
graph TD
    subgraph "ainsel-hub-backend process"
        MAIN[main.go]
        MAIN --> MGR[controller-runtime Manager<br/>cache + informers]
        MAIN --> RTR[Router<br/>internal/router/]
        MAIN --> API[API Server<br/>internal/api/]
        MAIN --> MET[Metrics<br/>internal/metrics/]
        MAIN --> SYNC[Sync loop<br/>cmd/hub/sync.go]
        SYNC --> IDX[Trigger Index<br/>internal/trigger/]
        SYNC --> CRON[Cron Emitter<br/>internal/cron/]
        RTR --> IDX
    end

    MGR -->|Agent CRUD, queue signals| K8s[Kubernetes API]
    RTR -->|poll unrouted, enqueue tasks| PG[(PostgreSQL<br/>events / agent_tasks)]
    API -->|ingest, claim, ack, nack| PG
    SYNC -->|list triggers| PG
    API -->|CRUD| K8s
```

## Event Routing Flow

```mermaid
sequenceDiagram
    participant C as Connector
    participant API as Hub ingest API
    participant E as events table
    participant R as Router
    participant TI as Trigger Index
    participant T as agent_tasks table

    C->>API: POST /api/internal/events
    API->>E: INSERT (routed_at = NULL)
    R->>E: poll unrouted (2s)
    R->>TI: Match(event)
    TI->>TI: Check event type (wildcard)
    TI->>TI: Check ignoreBotEvents
    TI->>TI: Evaluate data filters
    TI-->>R: Matched triggers → agent names
    loop For each matched agent
        R->>T: INSERT task (pending)
    end
    Note over T: Task pods claim via<br/>GET /api/internal/agents/{name}/next-task
```

## Trigger Index

The trigger index is an in-memory structure that supports fast event
matching. Triggers are stored in Postgres (see
[`internal/triggers/`](../internal/triggers/)); a sync loop
([`cmd/hub/sync.go`](../cmd/hub/sync.go)) re-lists them every 30 seconds and
applies additions and deletions to the index.

```mermaid
flowchart TD
    A[Event arrives] --> B{For each Trigger}
    B --> C{Event type matches?<br/>supports wildcards}
    C -->|No| B
    C -->|Yes| D{ignoreBotEvents?}
    D -->|Yes & actor.is_bot| B
    D -->|No or not bot| E{Filters match?}
    E -->|No| B
    E -->|Yes| F[Add to matched set]
    F --> B
    B -->|Done| G[Return matched agents]
```

## REST API

The API server serves two audiences:

- `/api/v1/*` — the operations console and external consumers: CRUD for
  agents (CRD-backed) and for triggers, personas, skills, MCP servers and
  connectors (Postgres-backed), plus observability endpoints.
- `/api/internal/*` — machine callers: connector event ingest, task
  pickup/ack/nack for per-task job pods, task-log ingest.

```mermaid
graph LR
    CLIENT[ainsel-hub-frontend / CLI] -->|/api/v1| API[API Server]
    CONNECTORS[Connectors] -->|/api/internal/events| API
    TASKPOD[Task job pods] -->|next-task / ack / nack| API
    API -->|Agent CRUD| K8s[Kubernetes API]
    API -->|everything else| PG[(PostgreSQL)]
```

### Handler Structure

| File | Endpoints |
|------|-----------|
| `handlers_agents.go` | `/api/v1/agents`, `/api/v1/agents/:name` |
| `handlers_connectors.go` | `/api/v1/connectors`, `/api/v1/connectors/:name` |
| `handlers_triggers.go` | `/api/v1/triggers`, `/api/v1/triggers/:name` |
| `handlers_eventqueue.go` | `/api/internal/agents/{name}/next-task`, `ack`, `nack` |
| `server.go` | Route registration, health check, JSON helpers |
