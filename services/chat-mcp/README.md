# Chat MCP Sidecar

MCP server that exposes **chat tools** to the agent runtime. Runs as a
sidecar container in the agent pod, reachable via `localhost`. The agent's
`ainsel-mcp` pi extension connects to this server and registers its tools
as `mcp__chat__<tool>`, making them available to the LLM during
conversation turns.

## Role in the platform

See [`../../docs/architecture.md`](../../docs/architecture.md).

The chat sidecar is the **outbound** half of the chat feature: it gives
the agent tools to interact with chat sessions (send replies, read
history, list sessions). The **inbound** half — delivering a human's chat
message to the agent — goes through the hub's task queue, claimed by the
agent's task pod, same as any other event.

The sidecar is **stateless**: every tool call proxies to the hub backend's
internal chat REST API (`/api/internal/chat/*`, authenticated with the
shared `X-Internal-Token`). The hub owns session storage, message history,
and WebSocket routing to the frontend.

```
┌──────────────────────────────────────────────────────────┐
│  Agent Pod                                                │
│                                                           │
│  ┌──────────────────┐     ┌─────────────────────┐        │
│  │  Pi runtime       │     │  Chat sidecar       │        │
│  │                   │     │  (this service)      │        │
│  │  ainsel-mcp ──────┼────►│  /mcp  (MCP server) │        │
│  │  (connects to     │     │                     │        │
│  │   localhost:8081) │     │  Tools:             │        │
│  │                   │     │   chat.list_sessions │        │
│  │  LLM calls tools  │     │   chat.get_history   │        │
│  │  during turn      │     │   chat.send_reply    │        │
│  │                   │     │   chat.send_status   │        │
│  └──────────────────┘     └────────┬────────────┘        │
│                                      │                     │
└──────────────────────────────────────┼─────────────────────┘
                                       │ HTTP proxy
                                       ▼
                              ┌──────────────────┐
                              │  Hub backend     │
                              │  /api/internal/* │
                              │  (sessions,      │
                              │   messages,      │
                              │   WebSocket)     │
                              └──────────────────┘
```

## Tools

| Tool | Args | Description |
|------|------|-------------|
| `chat.list_sessions` | — | List active chat sessions for this agent |
| `chat.get_history` | `session_id`, `limit?` | Fetch message history for a session |
| `chat.send_reply` | `session_id`, `content` | Send the agent's response to a session |
| `chat.send_status` | `session_id`, `content` | Send an intermediate status message |

## Lifecycle

The sidecar exists to serve one thing: the agent runtime in the same pod. On a
**per-task pod** — the operator dispatches one Kubernetes Job per queued task — it
must not outlive that runtime, because a Job completes only when every container
in its pod has exited. A sidecar still listening on `:8081` leaves the pod
`Running`, the Job never gains a completion condition, and the slot it holds in
the agent's concurrency ceiling then blocks both new tasks and that task's own
retry.

The contract, all of it set by the operator on task Jobs:

1. The pod shares its PID namespace (`shareProcessNamespace`) and mounts a shared
   directory at `/ainsel-task` in every container.
2. The runtime writes its PID to `/ainsel-task/agent.pid` as its first act (it
   learns the path from `AINSEL_TASK_DIR`).
3. This server polls that PID and exits when it disappears, so the pod ends as a
   unit regardless of the order containers happen to exit in.

If the pid file never appears, the server exits non-zero rather than serving an
empty pod forever. If `AINSEL_TASK_DIR` is unset — a long-lived pod (scale-to-zero
or paused), or a plain local run — none of this applies and the server serves
until it is signalled down.

## Local development

```bash
make build
# Or directly:
go build -o bin/ainsel-chat-mcp ./cmd/server

# Run (HUB_URL defaults to in-cluster service DNS)
PORT=8081 \
AGENT_NAME=developer \
HUB_URL=http://localhost:8080 \
./bin/ainsel-chat-mcp
```

Ports:

- `8081` — HTTP server hosting `/mcp` and `/health` (`PORT`)

Env vars:

| Var | Required | Default | Purpose |
|-----|----------|---------|---------|
| `PORT` | no | `8081` | HTTP listen port |
| `HUB_URL` | yes | `http://hub-backend.ainsel.svc.cluster.local:8080` | Hub backend REST API URL |
| `AGENT_NAME` | yes | — | Logical agent name (set by operator on the pod) |
| `AINSEL_TASK_DIR` | no | — | Set by the operator on per-task Jobs: the directory holding the runtime's `agent.pid`. When set, this sidecar ends with the runtime — see [Lifecycle](#lifecycle) |

## Testing

```bash
make test
# Or:
go test ./...
```

## Reference

- [Hub REST API](../../docs/api-reference.md) — chat endpoints (planned)
- [Event schema](../../docs/event-schema.md) — `chat.message` event type (planned)
- [Pi runtime](../../pi/) — `ainsel-mcp` extension that connects to this server
- [Platform MCP server](../mcp/) — the platform-wide MCP server (different scope)