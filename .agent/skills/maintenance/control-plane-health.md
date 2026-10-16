# Skill: Control-Plane Health Check

**When to use:** when the platform itself looks wrong — components restarting,
nothing routing, the console erroring, "the agents are fine but the platform
isn't" — or as the first pass of any operational investigation.

[`check-agent-health.md`](check-agent-health.md) is for one agent; this is for
the machinery every agent depends on. Read-only.

---

## 0. What is supposed to be running

| Workload | Name (chart) | Role |
|---|---|---|
| Deployment | `hub-backend` | control plane: event queue, REST API, task store, metrics |
| Deployment | `hub-frontend` | the operations console (nginx) |
| Deployment | `k8s-ai-agent-operator` | renders per-task Jobs, template Deployments, persona ConfigMaps |
| Deployment | `k8s-event-source-gateway-operator` | reconciles `WebhookConnector` (receiver Deployment + Ingress) |
| Deployment | `ainsel-mcp` | MCP server registry |
| StatefulSet | `postgres` | the hub's database (when the chart manages it) |
| Deployment(s) | `connector-<connector-id>` | one webhook receiver per connector, created by the operator, **not** by the chart |

Everything is in one namespace (`ainsel` by default — `values.yaml#namespace`).
Older docs sometimes say `ainsel-hub` / `ainsel-agent-operator`; those names do
not exist in the chart. Use the names above.

Two facts that shape every command below:

- The Go component images are **distroless** — no shell, no `curl`. You cannot
  `kubectl exec deploy/hub-backend -- curl ...`. Port-forward, or run a throwaway
  `curlimages/curl` pod.
- All components log **structured JSON (slog) to stdout**. `kubectl logs` output
  pipes into `jq`, and that is usually the fastest way to see what is wrong.

---

## 1. Thirty-second pass

```bash
NS=ainsel

kubectl get pods -n $NS -o wide                      # anything not Running / high RESTARTS?
kubectl get deploy,sts -n $NS                        # ready columns all n/n?
kubectl get crd | grep ainsel.dev                    # agents, agentimages, webhookconnectors
kubectl get agents,webhookconnectors,mcpservers -n $NS
kubectl get events -n $NS --sort-by='.lastTimestamp' | tail -30
```

If a Deployment is not fully rolled out, `kubectl rollout status deploy/<x> -n $NS`
names the stuck ReplicaSet; then read that pod's events.

## 2. The one call that answers "is it up?"

```bash
# In-cluster (no token needed — /health is not exposed through the ingress)
kubectl run curl-test -n $NS --rm -it --restart=Never --image=curlimages/curl -- \
  curl -s http://hub-backend:8080/health

# Externally, with a token: subsystem-by-subsystem (database, operator connectivity)
kubectl port-forward -n $NS svc/hub-backend 8080:8080 &
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/platform/health | jq
```

`GET /api/v1/platform/health` returns each subsystem and its status; any
`"status": "unhealthy"` names where to dig next. The same data is one MCP call
for an agent: `get_platform_health`
([`docs/mcp.md`](../../../docs/mcp.md)).

## 3. Read the logs like they are structured (they are)

```bash
for c in hub-backend k8s-ai-agent-operator k8s-event-source-gateway-operator ainsel-mcp; do
  echo "== $c"; kubectl logs -n $NS deploy/$c --tail=200 --since=15m \
    | jq -r 'select(.level=="error" or .level=="warn") | "\(.time) \(.level) \(.msg)"' | tail -20
done
```

Startup lines worth reading in full — the hub announces how it wired itself:

```bash
kubectl logs -n $NS deploy/hub-backend | jq -r 'select(.msg | test("prometheus|metric source|auth|db|Listening";"i")) | .msg'
```

| Log line | Meaning |
|---|---|
| `HUB_PROMETHEUS_URL not set: ...` | Expected on installs without Prometheus; only the Prometheus-only panels go dark |
| `event metric source configured` `source: postgres` | Normal — the console reads the hub's own rows |
| `unknown HUB_METRICS_SOURCE, ignoring it` | A typo in `observability.metricsSource`; the hub stayed on `postgres` |
| `oidc: failed to fetch provider metadata` / `oidc discovery:` | Issuer unreachable from inside the cluster — the service refuses to start on purpose |
| `api auth middleware is not configured` | `auth.mode` unset and nothing opted into no-auth |
| hub exits early mentioning the DB secret | `ainsel-hub-db` (or your `existingSecret`) is missing/has the wrong keys |

## 4. Restart counts and resource pressure

```bash
kubectl get pods -n $NS -o custom-columns='NAME:.metadata.name,RESTARTS:.status.containerStatuses[*].restartCount,NODE:.spec.nodeName'
kubectl top pods -n $NS                 # if metrics-server is installed
kubectl describe pod -n $NS <pod> | sed -n '/Last State/,/Events/p'   # why did it die: OOMKilled / Error / back-off
```

A pod that restarts on a fixed interval is usually OOM or a failing readiness
probe, not a crash — `Last State` distinguishes them. A CrashLoop that clears
after a minute is usually a dependency (Postgres, OIDC) that was not up yet.

## 5. Data path — is anything actually flowing?

```bash
# Events reaching the hub at all?
curl -s -H "Authorization: Bearer $TOKEN" 'http://localhost:8080/api/v1/events?limit=10' | jq '.total, [.events[].status]'

# Tasks being claimed and completed?
curl -s -H "Authorization: Bearer $TOKEN" 'http://localhost:8080/api/v1/stats' | jq
```

- Zero events while the forge shows deliveries → the connector path: `kubectl
  describe webhookconnector -n $NS`, its receiver Deployment
  (`connector-<id>`), and the Ingress.
- Events present, none matched → filters, not health: see
  [`check-agent-health.md`](check-agent-health.md) §5.
- Tasks claimed but never completed → per-task Jobs, not the control plane.

## 6. Database

```bash
kubectl get sts postgres -n $NS                                   # chart-managed?
kubectl logs -n $NS sts/postgres --tail=50 | tail -20             # restarts, fatals, WAL/pvc errors
kubectl get pvc -n $NS                                            # data volume bound and not full

# The chart-managed image is a real postgres, so psql is in the pod (unlike the
# distroless app images). Defaults: user/db from values.yaml#postgres.
kubectl exec -n $NS sts/postgres -- psql -U ainselhub -d ainselhub -c \
  "select status, count(*) from agent_tasks group by status;"
kubectl exec -n $NS sts/postgres -- psql -U ainselhub -d ainselhub -c \
  "select count(*) from task_logs where created_at > now() - interval '1 day';"
```

If the install brings its own database, use its DSN (the hub reads it from
`HUB_DB_URL`) instead.

The hub will not start without a working `HUB_DB_URL`, so "hub is down" plus
"postgres is fine" usually means the *secret* or the *URL*, not the server.
`task_logs` older than 7 days is pruned — an empty recent window means agents are
not reporting, not that retention is broken.

## 7. Version skew (the quiet one)

The control plane and the agent runtime images must be from the same release:
an old operator with a new runtime image (or the reverse) breaks the
task-completion handoff, so tasks hang instead of erroring.

```bash
kubectl get deploy -n $NS -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.template.spec.containers[0].image}{"\n"}{end}'
kubectl get agentimages -n $NS -o custom-columns=NAME:.metadata.name,IMAGE:.spec.image
```

Mixed `:dev` and `:vX.Y.Z` tags across those two lists is the answer more often
than a code bug is.

## 8. Report

Same discipline as the agent check: state the pass/fail per subsystem with the
line that proves it, and name the layer you are *not* suspecting.

```
namespace:  <ns>   workloads: 5/5 ready, postgres 1/1   restarts(24h): hub-backend 0, operator 3
health:     /api/v1/platform/health → db ok, operator ok
anomaly:    k8s-ai-agent-operator CrashLoopBackOff since <ts>; last log: "<line>"
ruled out:  database (healthy, pvc 40%), OIDC (discovery ok)
next:       <the one thing to change or the next command to run>
```

---

## Common pitfalls

| Pitfall | Why it misleads | Fix |
|---|---|---|
| Looking for `deploy/ainsel-hub` | That name is not what the chart creates | `hub-backend` (and the other names in §0) |
| `kubectl exec` into a component | Distroless images have no shell | port-forward or a `curlimages/curl` pod |
| `kubectl get pods` says "no agents" | Idle agents are 0-replica by design | Agent questions belong to the other skill |
| Missing `connector-<id>` deployments | Operator-created, not chart-created | `kubectl get deploy -l ainsel.dev/connector=<id>`; check the operator |
| Treating a restart count as the bug | Restarts are the symptom of a dependency or an OOM | `Last State` + events first |
| Concluding "the DB is broken" from a hub crash loop | The hub exits early on a missing/incorrect secret long before querying | Check `ainsel-hub-db` keys (`host`, `port`, `user`, `password`, `dbname` or one `dsn`) |
| Reading plain-text expectations into JSON logs | `grep ERROR` finds nothing in lowercase `"level":"error"` | Pipe through `jq` (see §3) |
| Debugging code while images are mismatched | Skew produces hangs, not errors | §7 before §3 |
