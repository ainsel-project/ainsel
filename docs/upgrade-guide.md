# Upgrade Guide

This guide covers how to upgrade an existing Ainsel installation to a newer chart version.

## General upgrade procedure

Before upgrading, complete these pre-upgrade steps:

1. **Back up the Postgres database.** All persistent state (agents, personas, triggers, connectors, invocation history) lives in Postgres. Create a backup before proceeding.
2. **Read the changelog.** Review [`CHANGELOG.md`](https://github.com/ainsel-project/ainsel/blob/main/CHANGELOG.md) or the notes for the target version on the [releases page](https://github.com/ainsel-project/ainsel/releases). Breaking changes are listed first.
3. **Check for breaking changes.** See [Checking for breaking changes](#checking-for-breaking-changes) below.
4. **Fetch the released chart.** Charts are published to GHCR as OCI artifacts,
   one per release, tagged with the chart version:

   ```bash
   helm pull oci://ghcr.io/ainsel-project/charts/ainsel --version <X.Y.Z>
   ```

5. **Upgrade:**

   ```bash
   helm upgrade ainsel oci://ghcr.io/ainsel-project/charts/ainsel \
     --version <X.Y.Z> -n <namespace> -f values.yaml
   ```

   If you are deploying directly from the repository chart directory:

   ```bash
   helm upgrade ainsel ./chart -n <namespace> -f values.yaml
   ```

## Checking for breaking changes

`CHANGELOG.md` at the root of this repository is generated and maintained by [release-please](https://github.com/googleapis/release-please) from conventional commits, and the same content is published on each GitHub Release. Entries are grouped by type (*BREAKING*, *Features*, *Bug Fixes*, *Miscellaneous Chores*) and linked to their commits and PRs.

To check whether the new chart version introduces new or changed values, diff the default values between versions:

```bash
# Export the new chart's default values
helm show values ainsel/ainsel > new-values.yaml

# Compare against your current values.yaml
diff values.yaml new-values.yaml
```

This reveals keys that are new (you may need to add them) or keys that have been renamed or removed (you must update your `values.yaml` accordingly).

## CRD upgrades

Helm does **not** upgrade CRDs automatically on `helm upgrade`. This is intentional: Helm skips CRD updates during upgrades to prevent accidental data loss caused by destructive schema changes.

If a release introduces new or changed CRDs, apply them manually before or after the Helm upgrade:

```bash
kubectl apply -f chart/templates/crds/
```

Check the changelog for the release to find out whether CRDs have changed and what the recommended apply order is.

## Database migrations

The hub backend runs database migrations automatically at startup. When you upgrade the hub image, any pending migrations are applied when the new pod starts.

If a migration fails, the hub pod will crash-loop. Diagnose with:

```bash
kubectl logs -n <namespace> deployment/ainsel-hub
```

Look for migration error messages near the top of the log output.

**Always back up Postgres before upgrading.** If a migration produces unexpected results, the only safe recovery path is to restore from the backup taken before the upgrade.

### Migration history integrity

Besides `schema_migrations`, the hub keeps a `schema_history` table with one row per applied migration version and a checksum of the migration file as it was when it was applied. On every startup it compares those rows against the migrations shipped in the image.

This exists because `schema_migrations` stores only the *highest* applied version. A deployment that has been taken to version 27 cannot tell that the file behind version 26 was replaced — `Up()` sees nothing to do and reports success, while the schema keeps a hole. Queries against the missing column then fail on every request, with a hub pod that started clean.

If the recorded checksum for an applied version differs from the file shipping now, the hub refuses to start:

```
migration history drift: applied version 26 does not match migration file 26 (recorded 3f2a1c9d…, file 8b41e0aa…). Migrations must never be renumbered or edited once deployed…
```

Recover by making the database and the history agree on what is actually applied:

1. Check what the schema really has, against the named migration file:
   ```bash
   kubectl -n <namespace> exec statefulset/postgres -- \
     psql -U <user> -d <db> -c '\d <table>'
   ```
2. Bring the schema in line with the code — apply the DDL the skipped migration would have run (an `ADD COLUMN` that is missing), or undo an extra change.
3. Tell the history to re-record that version, then restart the hub. The row is re-derived from the shipping file on the next startup:
   ```sql
   DELETE FROM schema_history WHERE version = 26;
   ```

A database migrated before `schema_history` existed is not drift: on its first startup with this check, every version already recorded in `schema_migrations` is trusted and written as the baseline. Rolling back to an older image is likewise allowed — recorded versions with no file are the shape of a rollback, and these migrations are additive.

## Migrating triggers from CRD storage

If your installation still stores triggers as `Trigger` / `CronTrigger`
custom resources, run the one-time `migrate-triggers` tool after upgrading so
those triggers land in the hub's Postgres database:

```bash
HUB_DB_URL=postgres://user:pass@host:5432/dbname?sslmode=disable \
KUBECONFIG=/path/to/kubeconfig \
go run ./services/hub/cmd/migrate-triggers
```

- `HUB_DB_URL` is required; `HUB_NAMESPACE` (default `ainsel`) selects the
  namespace the CRs are read from.
- The cluster is reached via `KUBECONFIG` or, when run in-cluster, via the
  ServiceAccount's credentials — which then need read access to `Trigger` and
  `CronTrigger` CRDs in that namespace.
- The tool is **idempotent**: triggers whose IDs already exist in the database
  are skipped, so a partial run can simply be re-executed.

The package doc comment in
[`services/hub/cmd/migrate-triggers/main.go`](https://github.com/ainsel-project/ainsel/blob/main/services/hub/cmd/migrate-triggers/main.go)
is the source of truth for usage.

## Migrating the skills wrapper to a list

`Agent.spec.skills` changed from the wrapper object `skills: {items: […]}` to a plain
list. Objects still in the old shape cannot be read by the hub or the agent operator at
all — the field is on the `Agent` CR, so one un-migrated agent fails the whole list. Run
the one-time rewrite after applying the new CRDs:

```bash
kubectl apply -f chart/templates/crds/
kubectl exec deploy/hub-backend -n <namespace> -- /hub migrate-skills-shape -n <namespace>
```

- The chart runs this for you: the `migrations.skillsShape` hook Job is a
  `post-install,post-upgrade` hook, so it runs after the chart applies the CRDs — the
  ordering the rewrite needs. Set `migrations.skillsShape.enabled: false` to opt out.
- `-n` selects the namespace (default `$HUB_NAMESPACE`, then `ainsel`);
  `--all-namespaces` covers a multi-namespace install; `--dry-run` reports without writing.
- It needs no database and starts nothing, so it runs from the hub image in any cluster
  the hub's own ServiceAccount can reach — the ServiceAccount already has
  `get`/`list`/`patch` on `ainsel.dev/agents`.
- It prints one line per agent — what shape it found and what it did — and exits non-zero
  when it left something alone, so a hook Job failure means "read this output".

Per object:

| stored `spec.skills` | what happens |
|---|---|
| absent | skipped |
| a list, including `[]` | skipped, never rewritten — this is what makes a second run a no-op and keeps an explicit `[]` as `[]` |
| `{items: [ids…]}` | rewritten to the same list |
| `{}`, `{items: []}`, `{items: null}` | rewritten to an explicit `[]` — never `null`, never removed |
| anything else (string, number, a wrapper holding a non-string) | reported and skipped, counted as an error |

Assert the empty-selection case against the Kubernetes object, not the hub's response:

```bash
kubectl get agent <name> -n <namespace> -o jsonpath='{.spec.skills}'   # -> []
```

The hub's REST response omits an empty list because `AgentSpec.Skills` is `omitempty`, so
`[]` and absent are indistinguishable there — and both mean "no skills".

The implementation is
[`services/hub/cmd/hub/migrate_skills.go`](https://github.com/ainsel-project/ainsel/blob/main/services/hub/cmd/hub/migrate_skills.go);
it reads `spec.skills` as unstructured JSON on purpose, since the typed decode is what fails
on the old shape.

## Rolling back

List available revisions:

```bash
helm history ainsel -n <namespace>
```

Roll back to a previous revision:

```bash
helm rollback ainsel <revision> -n <namespace>
```

> **Important:** Rolling back the Helm release restores the previous Kubernetes resources (Deployments, ConfigMaps, etc.) and the previous image versions. It does **not** reverse any database migrations that have already run. If the migration must be reversed, restore the Postgres database from the backup taken before the upgrade.

## Version-specific notes

### Unreleased — the agent's LLM provider is required and validated

- **Changed: creating an agent without `llm.provider` returns `400`.** The hub
  now rejects a create request whose `llm.provider` is missing or not one of
  `ollama-cloud`, `opencode`, `alibaba-cloud`, `openai`, `anthropic`, `google`,
  `custom`, and names the valid ids. Before, such an agent was stored and the
  operator then marked it `Ready=False` / `InvalidLLMProvider`, so it never
  ran. On update, an empty or absent provider still leaves the stored one
  unchanged; an unknown one is now a `400`. **Action:** set `llm.provider` in
  any script, IaC or REST client that creates agents.
- **New: `TemperatureApplied` condition.** `llm.temperature` only reaches the
  OpenAI-compatible providers. On `openai`, `anthropic` and `google` the
  condition reads `False` (reason `TemperatureNotSupported`). A temperature
  stored while the agent used an OpenAI-compatible provider stays on the spec
  after a switch to one of these three, and the condition stays `False`; clear
  it with `kubectl` if the warning should go away. The console hides the
  field for these providers.

### Unreleased — tools and skills belong to the agent, not the image

An `AgentImage` is now a **runtime profile**: the container, its environment, and the
capability catalog it ships (`spec.tools`). What an agent may use is selected on the
**agent**. Three consequences:

- **Removed: `AgentImage.spec.enabledSkills`.** Images carry no skill selection. The
  field is pruned on apply; skills that were only enabled at image level stop being
  delivered until an agent selects them.
- **Changed: `Agent.spec.skills` is a plain list.** It used to be a wrapper object
  (`skills: {items: […]}`), where an absent value meant "inherit the image". Inheriting
  has nothing to inherit from now: **absent or empty = no skills**.
- **New: `AGENT_TOOLS` is enforced.** The operator has always passed the agent's
  `spec.enabledTools` to the pod, and nothing read it — every agent effectively had
  every tool its image provided. The pi runtime's `ainsel-tools` extension now narrows
  the declared tool set to that selection, blocks any other tool call, and withholds
  catalog binaries the agent was not given from the shell.

**Upgrade order.** The CRD change makes existing `Agent` objects with the wrapper
shape unreadable, and the fix has to run *after* the new CRDs are applied — see
[Migrating the skills wrapper to a list](#migrating-the-skills-wrapper-to-a-list).
The chart does it for you as a `post-install,post-upgrade` hook Job
(`migrations.skillsShape`, enabled by default).

Until patched, the hub and operator cannot read those agents (they surface as errors rather
than silently running with the wrong skills), so do it in the same maintenance window. Expect
a brief error loop from the hub and the operator between their new pods starting and the hook
Job finishing — that is the state above, and it clears itself when the migration runs.

**The API rejects the old shape with a pointer.** A create or update whose `skills` is an
object gets `400 skills must be a list of skill ids; the {items: [...]} wrapper was removed -
see docs/upgrade-guide.md` instead of the decoder's Go type name, so a client that still sends
the wrapper is told what to send.

**Review each agent's tool selection before upgrading.** A non-empty `enabledTools` list
used to be cosmetic; from now on it is a hard limit. An agent that lists tools but omits
one it relies on at runtime (often `bash`, which is how catalog binaries get called) will
lose that capability. Leave `enabledTools` empty for an agent that should keep seeing
everything its image provides.

**API and console.** `enabledSkills` is gone from every agent-image request and response
(an old client that still sends it is accepted and the field ignored). Skill assignment
endpoints address agents: `PUT`/`DELETE /api/v1/skills/{id}/assignments/{agentName}`, and
the delete-conflict referrers are `{"agentName", "agentId"}` instead of
`{"agentImageName", "agentImageId"}`. In the console the image's Tools tab is now clearly
the catalog, and each agent's Tools and Skills sections edit that agent's own selection.

### Unreleased — `next-task` long-poll removed

- **Removed: `GET /api/internal/agents/{name}/next-task`.** Per-task Job
  runtimes fetch their task by id (`.../tasks/{id}`) and never called it; it
  only served runtime images predating #359, which now get 404. Point such an
  agent's `spec.imageRef` at a current `AgentImage`.
- **Removed: `TASK_CLAIM_TIMEOUT_SECONDS` and the stale-claim reaper.** Nothing
  claims any more, so there is nothing to reap. Drop the variable from
  `hub.extraEnv`; the hub ignores it. Migration `0035` returns any row still
  `claimed` at upgrade to `pending` (or `failed` if its last attempt is spent).
- **Agent pause is not enforced.** It was withheld only at `next-task`; the
  per-task Job path never read `spec.pause`, so this changes no runtime
  behaviour. Use `replicas: 0` to stop an agent's dispatch. See
  [#249](https://github.com/ainsel-project/ainsel/issues/249) for the
  feature's removal.

### Unreleased — legacy metric aliases removed

`GET /api/v1/metrics/{summary,timeseries,agents}` are gone and answer `404`.
They existed only for the deployed frontend bundle predating the dashboard
rewrite; every shipped frontend since uses the canonical
`/api/v1/observability/metrics/...` paths. Custom dashboards calling the old
paths must switch to the canonical ones.
### Upgrading to v0.2

- **Job-per-task execution model.** Task execution moved to one-shot
  Kubernetes Jobs (one Job per task, single execution model). Agent runtimes
  must talk to the internal task routes
  (`/api/internal/agents/{name}/tasks/{id}`,
  `.../tasks/{id}/ack`, `.../tasks/{id}/nack`) — true for every runtime image
  built after that change.
- **Removed: legacy agent task route aliases.** The
  `/api/v1/agents/{name}/next-task`, `.../tasks/{id}/ack`, and
  `.../tasks/{id}/nack` aliases (leftovers of the resident-agent era) are
  removed and now return 404. If a per-task Job pod reports 404 on task
  traffic, it is running a stale runtime image — point the agent's
  `spec.imageRef` at a current `AgentImage` (a runtime image built after #359).

### Upgrading to v0.1

No breaking changes.

### Upgrading to v0.2

**The chart no longer bundles Qdrant.** Agent memory is now a bring-your-own
external service: you run your own [mem0](https://github.com/mem0ai/mem0)
instance (mem0 Cloud or self-hosted, with its own vector store) and point
agents at it via `MEM0_API_URL` — the chart does not deploy it (see
[`docs/adding-memory.md`](adding-memory.md)). On `helm upgrade`:

1. The bundled `qdrant` StatefulSet, its Service, and the `qdrant`
   NetworkPolicy are removed. Any `qdrant:` keys left in your values file
   become inert no-ops and can be deleted.
2. The StatefulSet's PVC is **not** release-owned and is left behind. If you
   believe you need the data in it, export it *before* upgrading; otherwise
   delete the orphaned PVC afterwards:

   ```bash
   kubectl delete pvc data-qdrant-0 -n <namespace>
   ```

3. Agent egress to `qdrant:6333` is no longer granted by the `agent-egress`
   policy. If you run a private Qdrant in-cluster, add your own additive
   egress and ingress policies (see
   [docs/network-policies.md](network-policies.md) for the out-of-band
   pattern).
