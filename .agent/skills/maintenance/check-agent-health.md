# Skill: Check a Running Agent for Errors

**When to use:** when someone says "agent X isn't working / is erroring / never
finishes", or when you are asked for a health check of a live install.

Read-only by design. Every step here is observation; the only mutating command
in this file is called out as a deliberate fix.

---

## 0. What "a running agent" means

An `Agent` is **not** a long-lived pod. The operator keeps a 0-replica template
Deployment per agent and renders **one Kubernetes Job per task**, from that
template:

| Object | Name / label | Lifetime |
|---|---|---|
| Template Deployment | `<agent>` (0 replicas) | permanent, scaled to 0 |
| Task Job | `task-<agent>-<taskID>` | deleted 1 h after it finishes (`TTLSecondsAfterFinished: 3600`) |
| Labels | `ainsel.dev/agent=<agent>`, `ainsel.dev/task-id=<taskID>` | — |
| Containers | `agent` (the runtime), `chat` (chat-mcp sidecar, optional) | per attempt |
| In-Job retries | `backoffLimit: 9` | — |

Two consequences: **an idle agent has no pods — that is not a fault**, and the
Job that just failed is gone an hour later, so capture the evidence before you
go away to think.

Error evidence exists in four places, cheapest first:

1. the hub's own records (API / console) — survives Job deletion and pod restarts
2. the `Agent` CR's `.status` — what the hub and the operator currently believe
3. the task Job and its pod — the ground truth for the last attempt
4. the platform components' logs — when 1–3 say "not my layer"

---

## 1. Cheap pass over the whole platform

```bash
# Everything, at once: agents healthy/total + errors in the last hour
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/stats

# What has been erroring (since defaults to 24 h ago; it takes an RFC3339 timestamp)
curl -s -H "Authorization: Bearer $TOKEN" 'http://localhost:8080/api/v1/errors?limit=50'

# Cluster view of every agent (READY column comes from status.conditions)
kubectl get agents -n <namespace>
```

Getting an API session:

```bash
kubectl port-forward -n <namespace> svc/hub-backend 8080:8080 &
# auth.mode: oidc|local → bearer token; the quickstart overlay (auth.mode: none) answers unauthenticated
```

The console's **Errors**, **Activity**, **Observability** and **Agents** pages
are the same endpoints. An agent in the platform can also answer this itself via
the hub MCP registry (`get_platform_health`, `get_recent_errors`,
`get_agent_logs`, `list_invocations` — see
[`docs/mcp.md`](../../../docs/mcp.md)).

## 2. Read the `Agent` status — it separates "asleep" from "broken"

```bash
kubectl get agent <agent> -n <namespace> -o json | jq '.metadata.generation, .status'
```

| Field | Healthy reading | If it says otherwise |
|---|---|---|
| `conditions[type=Ready]` | `True` | `kubectl describe agent <agent>` — which condition is False tells you the layer |
| `conditions[...Degraded]` | absent/False | its `reason`/`message` is the operator's own diagnosis |
| `observedGeneration` | == `metadata.generation` | the operator has not reconciled your change yet — check its logs |
| `pendingTasks` | 0 when idle | stuck > 0 with no Jobs → the operator is not rendering them (§4) |
| `activeTasks` | 0 when idle | > 0 for a long time → open a Job and read its pod |
| `lastInvocation` | recent if work is expected | old ⇒ nothing is reaching this agent; the fault is upstream (connector/trigger) |
| `scaling.reason` | `JobPerTask` | `Capped` = more work due than the concurrency cap allows — config, not a bug |

Conditions to look for: `Ready`, `DeploymentReady`, `ConsumerReady`,
`MCPDiscoveryComplete`, `PersonaConfigMapReady`, `ImageEnvSecretReady`,
`Degraded`.

## 3. The hub's records — the source that outlives the Job

```bash
# One agent's task logs (note: `app=` takes an *agent name*, it is a Loki holdover)
curl -s -H "Authorization: Bearer $TOKEN" \
  'http://localhost:8080/api/v1/observability/logs?app=<agent>&range=24h&limit=500'

# Runs, and how they ended: running | success | failure | timeout
curl -s -H "Authorization: Bearer $TOKEN" \
  'http://localhost:8080/api/v1/invocations?agent=<agent>&status=failure&limit=20'

# Did the events even arrive? status=unmatched means the trigger filter missed
curl -s -H "Authorization: Bearer $TOKEN" \
  'http://localhost:8080/api/v1/events?agent=<agent>&status=error&limit=50'
```

`task_logs` is retained for **7 days** — past that, the hub has no record and
you are reading Prometheus counters or nothing. Read the newest failure first:
an agent that errors 40 times usually errors for one reason.

## 4. The Job that actually ran

```bash
kubectl get job -n <namespace> -l ainsel.dev/agent=<agent> --sort-by=.metadata.creationTimestamp
JOB=task-<agent>-<taskID>

kubectl get job -n <namespace> $JOB -o json | jq '.status'      # succeeded/failed/active, conditions
kubectl logs   -n <namespace> job/$JOB -c agent --tail=200      # the runtime
kubectl logs   -n <namespace> job/$JOB -c chat  --tail=50       # the sidecar, if present
kubectl describe pod -n <namespace> -l ainsel.dev/task-id=<taskID>   # events, last state, exit code
```

What to extract, in order:

- `.status.failed` vs `backoffLimit` (9), and the `JobFailed` condition's
  `reason`. The Job sets no `activeDeadlineSeconds`, so a hung task stays
  `active` rather than failing — the turn limit is the runtime's
  (`TURN_TIMEOUT_MS`, default 600000 = 10 min, which aborts and nacks).
- The pod's container state: `WAITING/ContainerCreating` (pulling, scheduling),
  `WAITING/CrashLoopBackOff` (read `--previous`), `TERMINATED` with exit code
  `1` (the runtime's own error, in the log) or `137` (OOM — raise the limit).
- `kubectl get events -n <namespace> --sort-by='.lastTimestamp' | tail -30` —
  often the whole answer (`Insufficient cpu`, `ImagePullBackOff`).

If the task is `claimed` with no live Job, the hub's stale-claim reaper returns
it to the queue after `TASK_CLAIM_TIMEOUT_SECONDS` (default 1800 s, swept every
5 min) and notifies the agent — so a dead pod self-heals within ~30 min. Check
`kubectl logs deploy/hub-backend --since=1h | jq -r 'select(.msg=="reaped stale claim")'`
to see whether that has already been happening in a loop: a task that is reaped
repeatedly is a task that kills its pod, not a queue bug.

**Save the evidence**: `kubectl logs -n <namespace> job/$JOB -c agent > /tmp/<agent>-<taskID>.log`
before the TTL deletes it. Task logs and transcripts contain the payload that
triggered the run — treat them as sensitive, redact before pasting into issues.

## 5. Symptom → layer → next command

| Symptom | Likely layer | Go look with |
|---|---|---|
| `lastInvocation` old, no tasks, events `unmatched` | not the agent — trigger filters / connector | `GET /api/v1/events?status=unmatched`; `kubectl describe webhookconnector -n <namespace>` |
| Events arrive, no task for this agent | trigger/bridge/permission scope | hub log (`deploy/hub-backend`) lines for that event id |
| `pendingTasks > 0`, no Jobs | operator | `kubectl logs deploy/k8s-ai-agent-operator --tail=200`, `describe agent` |
| Task `claimed` with no live Job | dead pod; the hub reaps it after `TASK_CLAIM_TIMEOUT_SECONDS` | hub logs: `reaped stale claim` (repeated = the task kills its pod) |
| Job exists, pod `Pending` | scheduling / capacity | `kubectl describe pod` events (`Insufficient cpu`) |
| `ImagePullBackOff` | registry / pull secret | pod events; the `AgentImage`'s image reference |
| `CrashLoopBackOff` in `agent`, message like `api-key secret not found` | credentials | secret `<agent>-<provider>-key`, key `api-key` |
| Fails at boot with `oidc discovery:` | provider unreachable | [`docs/troubleshooting.md`](../../../docs/troubleshooting.md) → OIDC / authentication errors |
| Runs, then `status=failure` with a model/API error (429, 401, overloaded) | the LLM provider, not AInsel | the task log itself; provider quota |
| `agent` container `Completed` but Job still `active` | chat sidecar holds the pod open | [`docs/troubleshooting.md`](../../../docs/troubleshooting.md) → "A task never finishes"; version skew between operator and runtime image |
| Tool calls fail, everything else fine | MCP server unreachable (NetworkPolicy) | [`docs/network-policies.md`](../../../docs/network-policies.md); `kubectl describe mcpserver` |
| Invocation `timeout` | the turn exceeded `TURN_TIMEOUT_MS` (default 10 min) | the Job's log tail; the agent's model/tool latency |

## 6. Only when you decide to act

```bash
# Re-dispatch a task whose Job is wedged: the operator sees the task still
# claimed and creates a fresh Job. Safe, but it re-runs the work.
kubectl delete job -n <namespace> task-<agent>-<taskID>

# Restart a control-plane component after fixing its config
kubectl rollout restart -n <namespace> deploy/hub-backend
kubectl rollout status  -n <namespace> deploy/hub-backend
```

Never delete a Job as "cleanup" while investigating: it is often the only
remaining witness, and the task it belongs to may be re-run by design.

## 7. What to report back

Not "the logs had errors" — a verdict with its evidence:

```
agent:      <name> (ns <namespace>), generation <n>, observedGeneration <n>
verdict:    <what is wrong, in one line> — <which layer: trigger | task | runtime | provider | platform>
evidence:   <the 2-3 lines that prove it, from the commands above>
window:     first seen <ts>, last seen <ts>, <n> occurrences
artifacts:  job/<name> (expires <ts>), log saved to <path>
next:       <the one change or the next command to run>
```

If you could not localize it, say which layers you *ruled out* and why —
that is what keeps the next reader from re-running the whole skill.

---

## Common pitfalls

| Pitfall | Why it misleads | Fix |
|---|---|---|
| "The agent has no pods" treated as an outage | Idle agents are scaled to 0 by design | Check `lastInvocation` / `pendingTasks` first |
| Reading `kubectl get pods` only | Task pods live and die per task | Read Jobs (`-l ainsel.dev/agent=`) and hub records |
| Arriving a day late and finding nothing | Job TTL is 1 h, `task_logs` retains 7 days | Capture logs into a file as soon as you find the failing Job |
| `kubectl exec deploy/hub-backend -- curl ...` | The Go images are distroless — no shell, no curl | `kubectl port-forward svc/hub-backend`, or `kubectl run curl-test --image=curlimages/curl` |
| Reading stale docs' resource names | Older text uses `deploy/ainsel-hub`, `deploy/ainsel-agent-operator` | The chart ships `hub-backend`, `hub-frontend`, `k8s-ai-agent-operator`, `k8s-event-source-gateway-operator`, `ainsel-mcp` |
| `app=<agent>` in the logs API treated as a Kubernetes label | It filters `task_logs.agent_name` | Pass the agent name |
| Debugging the agent because it is the thing that errored | `unmatched` events and connector failures surface as "agent did nothing" | Step 1's `events` check before anything else |
| Pasting raw task logs into an issue | They quote the triggering payload | Redact first |
