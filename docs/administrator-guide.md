# Administrator Guide

This guide is for the people who configure AInsel for the rest of the
organization. If you're evaluating AInsel for adoption, the root
[`README.md`](../README.md) is the better starting point. If you're
extending the platform itself, see [`../CONTRIBUTING.md`](../CONTRIBUTING.md).

## 1. Why this role exists

AI is useful inside the tools your team already uses — but you can't
make every team member an AI expert, and you don't want different teams
running different models with different prompts at uncontrolled cost.
AInsel exists so one role — yours — owns the AI specifics. You pick
the model, write the persona, choose the tools, set the triggers. Your
team uses Forgejo (or whichever connector you've enabled) as they
always have; the AI shows up where it helps: a review comment, an
issue label, a draft reply.

This guide walks through the building blocks, the end-to-end journey
from a fresh deploy to a working agent, and four worked examples you
can adapt.

## 2. The building blocks

Six concepts; read them in order.

**Connector.** The bridge between AInsel and a source system. It
receives native webhooks (Forgejo, GitHub, or any HMAC-signed source)
and turns them into the canonical event format AInsel uses internally
(see [`event-schema.md`](event-schema.md)). A connector is a
declaration, not a program: you register it in the console (or via
`POST /api/v1/connectors`), the hub generates the webhook HMAC secret
and endpoint, and you paste both into the source's webhook settings.
CRD: `WebhookConnector`
(see [`crd-reference.md`](crd-reference.md#webhookconnector)). For
writing your own connector, see the
[connector developer tutorial](writing-a-connector.md).

**Agent.** One AI worker, configured with a model, persona, tool
set, and runtime settings (image, resources, scaling). You typically
run several agents — one per role (reviewer, triager, implementer)
— rather than one mega-agent. Smaller scopes are easier to reason
about and cheaper to operate. CRD: `Agent`
(see [`crd-reference.md`](crd-reference.md#agent)).

**Persona.** The prompt that defines the agent's behavior — the
locus of your AI expertise. Tone, scope, refusal patterns, output
structure, things to avoid. A good persona is specific and
constraining; a bad one is vague and lets the model improvise. Lives
either inline in `Agent.spec.persona.inline` or in a ConfigMap
referenced by `Agent.spec.persona.configMapRef`. ConfigMaps are
recommended once a persona grows beyond a paragraph — you can edit
them without touching the Agent.

**Tools and skills.** What the agent can act on — Forgejo API, git,
shell, the test runner, Kubernetes. `Agent.spec.enabledTools`
explicitly opts the agent into each tool; nothing is implicit. This
limits blast radius: a code reviewer doesn't need `shell` or
`docker-builder`, so don't grant them.

**Trigger.** Says "when an event from this connector matches these
filters, invoke this agent". Triggers are DB-backed (not CRDs) —
create them in the console or via `POST /api/v1/triggers`
(see [`api-reference.md`](api-reference.md#post-apiv1triggers)). You
configure `name` (a display label), `agentRef` (the agent's CR name —
its `a-…` id for console-created agents), `connectorRef` (the
connector's `c-…` id), and optional `filters` (operators: `eq`,
`neq`, `prefix`, `suffix`, `contains`, `not-contains`, `in`, `not-in`,
`regex`, ANDed) evaluated against the event payload — typically the
derived `type` and `action` fields. There is no `eventType` field and
no built-in bot filtering: match what you need with filters, and guard
agent loops by filtering out the agent's own forge user. Schema:
[`crd-reference.md`](crd-reference.md#trigger).

**CronTrigger.** A time-based trigger: "at this cron schedule, send
this prompt to this agent". Unlike a webhook `Trigger` it has no
connector — the hub runs an internal scheduler and enqueues a
task for the trigger's agent directly. Use it for anything an
agent should do on a clock rather than in reaction to a webhook
(daily digests, nightly sweeps). Cron triggers are DB-backed:
see [`crd-reference.md`](crd-reference.md#crontrigger) for the API schema.

**MCP servers.** Extend an agent's tool surface via the
[Model Context Protocol](https://modelcontextprotocol.io). AInsel
registers MCP servers centrally (DB-backed, managed by the hub and
proxied through [`services/mcp/`](../services/mcp/)); each agent opts in
through its own selection on the **Tools** tab, which the hub resolves
into `Agent.spec.mcp` — a snapshot of names, URLs and token references, so
editing the registry never rewrites running agents. The bundled MCP server
today is configured as a generic MCP server in the frontend; you can
register more.

![The MCP servers registry: a table of registered servers with name, URL,
token environment variable and last-updated age — a forge MCP server, the
GitHub MCP endpoint and a mem0 memory server](images/mcp-registry.webp)

*The registry behind the Tools tab — every MCP server an agent can opt
into. (In-cluster URLs redacted.)*

> **Removed:** `Agent.spec.enabledMCPs`, the older list of in-cluster
> `mcp-<name>` Service names. For one release beforehand the operator
> converted it into an equivalent `spec.mcp` snapshot on reconcile, folding in
> the servers the agent's runtime profile contributed so nothing changed at
> runtime. The field is now gone from the schema: any value still stored on a
> CR that was never reconciled in between is pruned by the API server, so
> re-select those servers on the **Tools** tab.

## 3. The end-to-end admin journey

Six steps from a fresh cluster to a working agent.

![The operations console after the journey below is complete: 8 active agents,
4 platform connectors, 28 routing rules and no recent errors, with the
connector table showing each webhook endpoint as Ready, a 24-hour throughput
chart, and a live activity feed](images/dashboard-overview.webp)

*Where the six steps land — the console reports agent, connector and trigger
state as the operators reconcile it.*

1. **Deploy AInsel itself.** Follow [`deployment.md`](deployment.md) to
   install the chart. By the end of this step you have the operators,
   hub, gateway, and frontend running, but no agents and no triggers
   yet.

2. **Add a connector.** Create a `WebhookConnector` CRD pointing at
   your Forgejo instance, with admin token and webhook secret in
   Secrets. The connector operator reconciles the webhook subscription
   and brings up a connector Deployment. Verify
   `kubectl get webhookconnectors -n ainsel` shows `Ready=True`.

3. **Define an agent.** Create an `Agent` CRD with a `displayName`,
   `llm.provider` (required — one of `ollama-cloud`, `opencode`,
   `alibaba-cloud`, `custom`), `llm.model`, `imageRef`, a `persona` (inline or
   ConfigMap), and an `enabledTools` list. The agent operator
   reconciles a Deployment (replica count from `spec.scaling`) and a
   per-agent metrics Service.
   Verify `kubectl get agents -n ainsel` shows `Ready=True`.

4. **Define a trigger.** Create a `Trigger` CRD that maps an event
   type and filters to your agent. Verify `kubectl get triggers
   -n ainsel` shows `AgentRefValid=True` and `ConnectorRefValid=True`.

5. **Observe what happens.** Take an action in Forgejo that matches
   your trigger (open a PR, file an issue, comment). The connector
   publishes the event, the hub matches it, the agent runs, and the
   result shows up as a Forgejo artifact (a comment, a label, a draft
   PR). The frontend console at `/ainsel/` lists agents, triggers,
   and recent invocations.

6. **Iterate.** Read what the agent did. Refine the persona based on
   what's wrong (too chatty, too quiet, misses things, hallucinates).
   Re-apply the ConfigMap or Agent CRD; the operator picks up changes
   on the next reconcile. Pod restart behavior on ConfigMap edits
   depends on volume mount semantics — see "Updating agents" below.

The next section walks through complete examples you can adapt.

## 4. Cookbook

Four self-contained examples, ordered simplest to most complex. Each
includes a real persona, working YAML, and iteration tips. The
personas are starting points — tune them to your team's voice and
standards before deploying.

### Example 1: AI code review on PRs

**What it does:** Posts first-pass feedback as a comment on newly
opened PRs targeting `main`. Doesn't replace human review — catches
the things humans skim past.

**Trigger:** filters `type eq pull_request`, `action eq opened`,
`pull_request.base.ref eq main`.
**Tools:** `forgejo`, `code-review`.

**Persona:**

```text
You are a code reviewer for this repository. You read the diff of a
pull request and post a single review comment summarizing what you
see.

Focus on: bugs and likely defects (off-by-one, nil derefs, races,
missing error handling); test coverage gaps relative to the changed
code; public-API changes not reflected in the docs.

Ignore: code style and formatting (the linter handles those);
subjective preferences (naming, ordering) unless they hurt
readability; anything outside the diff.

Output: one short top-level comment (2-4 sentences) summarizing the
PR, then file-level comments only on lines actually changed. Never
comment on unchanged code. Do not invent code. If the diff exceeds
500 lines, say so and stop.
```

**YAML:** (Store the persona above in a `ConfigMap` named
`code-reviewer-persona` with key `persona.md`, then apply:)

```yaml
apiVersion: ainsel.dev/v1alpha1
kind: Agent
metadata:
  name: code-reviewer
  namespace: ainsel
spec:
  displayName: "Code Reviewer"
  description: "Posts AI feedback on newly opened PRs."
  imageRef:
    name: ainsel-ai-agent-default
  runtime:
    provider: ollama-cloud
  llm:
    model: unsloth/gpt-oss-120b-GGUF:F16
    maxTurns: 15
    temperature: 0.2
  persona:
    configMapRef:
      name: code-reviewer-persona
      key: persona.md
  enabledTools:
    - forgejo
    - code-review
  scaling:
    replicas: 3
```

**Trigger:** created in the console or via `POST /api/v1/triggers` —
triggers are DB-backed, not CRDs. `connectorRef` is the connector's
`c-…` id (`GET /api/v1/connectors` lists them); `agentRef` is the
agent's CR name:

```json
{
  "name": "trigger-reviewer-pr-opened",
  "agentRef": "code-reviewer",
  "connectorRef": "c-1a2b3c",
  "filters": [
    {"field": "type", "op": "eq", "value": "pull_request"},
    {"field": "action", "op": "eq", "value": "opened"},
    {"field": "pull_request.base.ref", "op": "eq", "value": "main"}
  ]
}
```

**What end users see:** A comment on the PR within a minute or two,
signed with the agent's Forgejo identity, containing a brief summary
and inline comments on changed lines.

**Iteration tips:**

- Comments on trivial things → tighten "Focus on:" and add more
  explicit ignores.
- Hallucinates code → repeat "Only comment on lines visible in this
  diff" near the top of the persona.
- Reviews too slow → lower `llm.maxTurns` or pick a faster model.

### Example 2: Issue triage and labeling

**What it does:** Posts a triage comment and suggested labels when an
issue is opened. Prepares the issue for a human; doesn't make
decisions.

**Trigger:** filters `type eq issues`, `action eq opened`. **Tools:** `forgejo`.

**Persona:**

```text
You are an issue triager for this organization's repositories. When
an issue is opened you read it once and respond with one comment.

Your comment must include:

1. A one-line summary of what the user is asking for.
2. A category — exactly one of: `bug`, `feature`, `question`,
   `docs`, `unclear`.
3. A priority guess — one of: `low`, `medium`, `high`. Use `high`
   only for data loss, security, or production-broken issues.
4. Up to three suggested labels from the repo's existing label set.
   If you're not sure a label exists, don't propose it.
5. If the report is unclear or missing information, one specific
   follow-up question.

Never close, assign, or modify labels yourself — only suggest them
in the comment. Under 10 lines of prose total.
```

**YAML:** (Persona goes in a ConfigMap named `issue-triager-persona`,
key `persona.md`.)

```yaml
apiVersion: ainsel.dev/v1alpha1
kind: Agent
metadata:
  name: issue-triager
  namespace: ainsel
spec:
  displayName: "Issue Triager"
  description: "First-pass triage on newly opened issues."
  imageRef:
    name: ainsel-ai-agent-default
  runtime:
    provider: ollama-cloud
  llm:
    model: unsloth/gpt-oss-120b-GGUF:F16
    maxTurns: 10
    temperature: 0.3
  persona:
    configMapRef:
      name: issue-triager-persona
      key: persona.md
  enabledTools:
    - forgejo
  scaling:
    replicas: 2
```

**Trigger:** via `POST /api/v1/triggers`:

```json
{
  "name": "trigger-triager-issue-opened",
  "agentRef": "issue-triager",
  "connectorRef": "c-1a2b3c",
  "filters": [
    {"field": "type", "op": "eq", "value": "issues"},
    {"field": "action", "op": "eq", "value": "opened"}
  ]
}
```

**What end users see:** A triage comment on their issue within a
minute, with category, priority, and label suggestions, plus a
follow-up question if information is missing.

**Iteration tips:**

- Wrong labels → list your actual label vocabulary in the persona.
- Priority too high → give concrete `high` examples ("data
  corruption", "security disclosure").
- Agent labels the issue itself → strengthen "Never modify labels
  yourself".

### Example 3: Comment Q&A

**What it does:** Replies in-thread when an issue or PR comment
mentions the agent's handle (e.g., `@docs-helper`). Not a chatbot —
silent unless called.

**Triggers:** filters `type eq issue_comment`, `action eq created`
and `type eq pull_request_review_comment`, `action eq created`
(two triggers, same agent — on GitHub-style forges, plain conversation
comments on issues *and* PRs both arrive as `issue_comment`; review
comments are a separate type), each also filtered to comments
containing the handle. **Tools:** `forgejo`.

**Persona:**

```text
You are a helper agent that answers when explicitly mentioned in an
issue or pull request comment. You speak only when called.

When you reply:
- Quote the question being asked, then answer it directly.
- Stay scoped to the topic of the issue/PR you're commenting on.
- Link only to files under `docs/`, README files, or other documentation in
  this repository — never to external sources you cannot verify.
- If you don't know the answer, say "I don't know" and stop. Never
  guess.
- Keep replies under 8 lines unless detail was explicitly requested.

Do not reply to comments that don't mention you by handle. Do not
reply more than once per thread per question. Do not take any
action other than commenting (no labels, no closes, no pushes).
```

**YAML:** (Persona goes in a ConfigMap named `docs-helper-persona`,
key `persona.md`.)

```yaml
apiVersion: ainsel.dev/v1alpha1
kind: Agent
metadata:
  name: docs-helper
  namespace: ainsel
spec:
  displayName: "Docs Helper"
  description: "Answers questions when mentioned in comments."
  imageRef:
    name: ainsel-ai-agent-default
  runtime:
    provider: ollama-cloud
  llm:
    model: unsloth/gpt-oss-120b-GGUF:F16
    maxTurns: 10
    temperature: 0.2
  persona:
    configMapRef:
      name: docs-helper-persona
      key: persona.md
  enabledTools:
    - forgejo
  scaling:
    replicas: 2
```

**Triggers:** two `POST /api/v1/triggers` calls:

```json
[
  {
    "name": "trigger-docs-helper-issue-comment",
    "agentRef": "docs-helper",
    "connectorRef": "c-1a2b3c",
    "filters": [
      {"field": "type", "op": "eq", "value": "issue_comment"},
      {"field": "action", "op": "eq", "value": "created"},
      {"field": "comment.body", "op": "contains", "value": "@docs-helper"}
    ]
  },
  {
    "name": "trigger-docs-helper-review-comment",
    "agentRef": "docs-helper",
    "connectorRef": "c-1a2b3c",
    "filters": [
      {"field": "type", "op": "eq", "value": "pull_request_review_comment"},
      {"field": "action", "op": "eq", "value": "created"},
      {"field": "comment.body", "op": "contains", "value": "@docs-helper"}
    ]
  }
]
```

**What end users see:** When they write `@docs-helper how do I run
the integration tests?`, a focused reply appears under the comment
within a minute or two. Comments without the handle are ignored.

**Iteration tips:**

- Replies when not mentioned → double-check the `contains` filter
  (case sensitive) and the handle string.
- Answers off-topic → add a "stay on topic" line and list refusal
  topics.
- Hallucinates doc links → require verification via the forgejo tool
  before linking.

### Example 4: Issue → PR implementation

**What it does:** When an issue gets the `ai-please` label, the
agent clones the repo, makes the change, and opens a draft PR back
for human review. Humans still review and merge.

**Trigger:** filters `type eq issues`, `action eq labeled`,
`label.name eq ai-please`.
**Tools:** `forgejo`, `git`, `shell`, `test-runner`.

**Persona:**

```text
You are an implementation agent. You take a labeled issue and
produce a draft pull request that addresses it.

Workflow:
1. Read the issue title, body, and any clarifying comments.
2. Clone the repository and create a new branch named
   `ai/issue-<NUMBER>`.
3. Make the smallest possible change that addresses the issue. Stay
   strictly inside the scope. Do not refactor, do not "improve"
   unrelated code, do not change formatting outside touched lines.
4. Run the test suite if one exists (`make test`, `go test ./...`,
   `pnpm test`). If tests fail because of your change, stop and
   explain in the PR body.
5. Open a draft pull request with a descriptive title referencing
   the issue and a body listing what you changed and why.

Hard rules:
- Never merge a PR. Always open as draft.
- Never force-push. Always create a fresh branch per issue.
- If scope is unclear or the change would touch more than ~50 lines
  across more than ~3 files, stop and post a comment on the issue
  saying the scope is too large for autonomous implementation.
- Never touch CI configuration, secrets, or
  release infrastructure.
```

**YAML:** (Persona goes in a ConfigMap named `implementer-persona`,
key `persona.md`.)

```yaml
apiVersion: ainsel.dev/v1alpha1
kind: Agent
metadata:
  name: implementer
  namespace: ainsel
spec:
  displayName: "Issue Implementer"
  description: "Opens draft PRs for issues labeled ai-please."
  imageRef:
    name: ainsel-ai-agent-default
  runtime:
    provider: ollama-cloud
    resources:
      requests:
        cpu: 500m
        memory: 1Gi
      limits:
        cpu: 2000m
        memory: 4Gi
  llm:
    model: unsloth/gpt-oss-120b-GGUF:F16
    maxTurns: 50
    temperature: 0.2
  persona:
    configMapRef:
      name: implementer-persona
      key: persona.md
  enabledTools:
    - forgejo
    - git
    - shell
    - test-runner
  scaling:
    replicas: 2
```

**Trigger:** via `POST /api/v1/triggers`:

```json
{
  "name": "trigger-implementer-label-ai-please",
  "agentRef": "implementer",
  "connectorRef": "c-1a2b3c",
  "filters": [
    {"field": "type", "op": "eq", "value": "issues"},
    {"field": "action", "op": "eq", "value": "labeled"},
    {"field": "label.name", "op": "eq", "value": "ai-please"}
  ]
}
```

**What end users see:** Within minutes of applying the `ai-please`
label, a draft PR appears linked to the issue. Reviewers review and
merge as they would any human-authored draft.

**Iteration tips:**

- Scope creep → add concrete out-of-scope examples to the persona.
- Touches files it shouldn't → list paths explicitly under
  "Never touch:".
- Broken tests in PRs → raise `llm.maxTurns` or require test output
  in the PR body.

### Example 5: Scheduled prompts (cron triggers)

**What it does:** Runs an agent on a fixed schedule rather than in
reaction to a webhook. The agent receives a prompt you define and
acts on it — e.g. a daily standup digest, a nightly stale-issue
reminder, or a periodic dependency-check sweep.

A cron trigger is a self-contained schedule: it references an agent,
a 5-field cron expression, and a prompt. The hub inserts a synthetic
event (connector `cron`) into the `events` table and enqueues the task
for the trigger's agent directly, so the existing pull-based delivery
model (and invocation tracking) applies unchanged. There is no connector.

**CronTrigger:** DB-backed like webhook triggers — create it in the
console or via `POST /api/v1/cron-triggers`:

```json
{
  "name": "cron-standup-digest",
  "agentRef": "standup-bot",
  "schedule": "0 9 * * 1-5",
  "prompt": "Summarize all pull requests opened in the last 24h and any issues labelled \"stale\". Post a short digest as a comment on the team's standup issue (#42).",
  "enabled": true
}
```

The agent (`standup-bot`) must already exist and reference a persona
capable of the task. The `prompt` is delivered verbatim as the user
message — it is the full instruction the model sees, with no forgejo
event wrapper.

**What end users see:** Every weekday at 09:00 the bot posts a digest
comment, unprompted.

**Iteration tips:**

- Schedule fires in the **hub's local time**. Confirm the hub pod's
  timezone (default UTC) when picking times.
- Pausing without deletion → update it with `enabled: false`
  (console, MCP, or `PUT /api/v1/cron-triggers/{id}`).
- Backfill missed runs is intentionally not supported — each fire is
  one invocation; if the hub is down during a slot, that slot is
  skipped.
- One `CronTrigger` per prompt/agent pair. To run the same prompt on
  multiple agents, create one `CronTrigger` per agent.

## 5. Operating the platform

### Token management

Agents emit per-invocation token metrics
(`agent_tokens_used_total{agent,repo,org,event_type,token_type,model}`)
to Prometheus, and the runner reports the same four components with each
conversation message it posts to the hub. The hub's observability endpoints
expose a 24h summary, a sparkline, and a per-agent / per-repo / per-model
breakdown that the frontend renders as the "Tokens last 24h" tile. So you can
*see* what's being consumed today — and because the summary, the sparkline and the
by-subject breakdown all read the hub's conversation rows by default, a
scale-to-zero agent that finishes before Prometheus ever scrapes it is still
counted.

`token_type` splits consumption into `input`, `output`, `cache_read` and
`cache_write`. The split matters: prompt-cache reads are billed at a small
fraction of the input rate, and in an agent loop they are usually the large
majority of the prompt. Reported totals include all four components, so a
total that only added `input` and `output` would under-report real
consumption by roughly an order of magnitude. Use
`cache_read / (input + cache_read + cache_write)` as the cache hit rate — a
sudden drop means something is invalidating the prompt prefix and the same
work is being re-billed at the full input rate.

#### Cost tracking

Token counts only become a bill once models are priced. Set `spec.llm.cost`
(USD per million tokens) on the Agent and the operator writes it into the
agent's generated `models.json`; pi then prices every message (`usage.cost`).
The runner reports that per-message cost with its conversation rows and
raises `agent_cost_usd_total{agent,repo,org,event_type,model}` for
Prometheus. The hub sums both into the token summary tile, the by-subject
and by-event tables, the conversation ledger and the `get_token_usage` MCP
tool. Without `spec.llm.cost` there is no cost figure to report: the runner
omits the field (the hub stores NULL and raises no counter series), and the
console renders cost as "not reported" — a different fact from a priced
model whose runs happen to cost `$0.00` (issue #75).

```yaml
spec:
  llm:
    model: claude-sonnet-4
    cost:
      input: 3        # $/Mtok uncached input
      output: 15      # $/Mtok output
      cacheRead: 0.3  # $/Mtok prompt-cache reads
      cacheWrite: 3.75 # $/Mtok prompt-cache writes
```

The cache rates are priced separately from `input` because they are billed
very differently (on Anthropic, roughly $0.30 / $3.75 / $3.00 per Mtok for
cache-read / cache-write / input): a cost derived from an input+output total
alone would be wrong in both directions at once. Changing `spec.llm.cost`
regenerates `models.json`, which restarts the agent's pods so the new rates
apply to the next turn.

The "not reported" semantics above are a runner–hub contract, so upgrade the
two together: migration 0025 NULLs the ambiguous stored zeros, but a runner
older than this change still sends an explicit `0` for every figure it did not
measure, so its rows land as measured zeros again the moment they arrive.
Redeploy the agents (new runner image) alongside the upgraded hub. Note also
that the 0025 *down* migration restores the `NOT NULL DEFAULT 0` constraint by
collapsing every NULL back to `0` — rolling the migration back is lossy for
presence by design.

![The Token Detail page: all-time total and 24-hour input/output tiles above
a by-agent table breaking usage down per repo/event, agent and model, with
input, output, I/O ratio and total columns and a per-row link to the
underlying events](images/token-detail.webp)

*The full token view behind the dashboard tile — per-agent, per-model,
per-event-type spend, with the I/O ratio that spots context-heavy agents
at a glance. (Private repo names redacted. This screenshot predates the
cache and cost columns; the live page also shows Cache R / Cache W and
Cost per row.)*

The console renders the cache columns alongside input and output, so the
visible columns sum to the displayed total, and the dashboard carries a
cache hit-rate tile next to the token total.

What is **not yet** wired up:

- **Per-agent / per-repo budget enforcement** — cost is now measured, but no
  policy pauses an agent on budget overrun. *Planned.*
- **Pre-built Grafana dashboards** — metrics are scrapeable but no
  shipped dashboards. *Planned.*
- **PrometheusRules for cost alerts** — not bundled. *Planned.*

For today: watch the observability tile and, if an agent
misbehaves, scale it to zero (below) while you fix the persona.

### Quality guardrails

Mostly persona-driven today: refusal patterns ("never close
issues"), output-format constraints, blast-radius limits.

![The agent detail view for agent-reviewer-gh, showing its model and image, a
runtime status of Ready with configured replicas and containers, and the
persona-reviewer shared template rendered alongside its Scope Constraint
section](images/agent-persona.webp)

*A reviewer agent's guardrails, in the console. The persona's scope
constraints — review the diff only, never propose rewrites of unchanged code,
never open issues or apply labels — are the guardrail, and they are versioned
with the persona rather than living in someone's head.*

The
platform supplements this with:

- **Per-agent tool restrictions** via `Agent.spec.enabledTools` —
  a reviewer without `git` can't push. Use this aggressively.
- **Bot-loop guards via filters** — triggers fire on every matching
  event, including agent-authored ones. Filter out the agent's own
  forge user (e.g. `sender.login neq <bot-user>`) to prevent loops.
- **Filter operators** on triggers scope by repo, branch, label,
  comment body, etc.

Not yet platform-enforced:
**rate limiting per agent/repo** (*Planned*);
**approval workflows for destructive actions** (*Planned*).

### Updating agents

To change a persona, edit the ConfigMap and re-apply. Pods mount it
as a volume; new contents propagate within ~60 seconds (kubelet
sync period). Pods do **not** auto-restart on ConfigMap changes —
they pick up the new persona on their next invocation. For
immediate cutover, delete the agent pods and let the Deployment
recreate them.

To change the model or runtime image, edit the `Agent` CRD. The
operator updates the Deployment, which rolls. In-flight invocations
on old pods finish on old config; new invocations land on new
config. There is no zero-downtime guarantee for invocations that
span a rollout.

### Disabling an agent quickly

Three escalating options, fastest to most thorough:

1. **Disable the trigger.** Delete the trigger (console, MCP, or
   `DELETE /api/v1/triggers/{id}`), or update its filters to something
   that never matches, and/or disable any cron trigger referencing the
   agent (`enabled: false`). Reversible; in-flight invocations finish.
2. **Scale to zero.** Set `Agent.spec.scaling.replicas: 0`. The operator
   pins the Deployment at zero and queued work waits in the event queue
   until you raise it again — an explicit zero is treated as the user
   saying "none", so a growing queue does not overrule it. Events already
   claimed by a pod finish; that pod's termination honours
   `--agent-grace-period`.
3. **Delete the agent.** `kubectl delete agent <name> -n ainsel`.
   Garbage-collects the Deployment, Service, persona ConfigMap and the
   MCP secret the owner references point at.

For incidents, prefer (1) — reversible, leaves config in place for
analysis.

### Plugins

Plugins are third-party extensions: HTTP workloads that add tools and UI
slots to the platform, reached over a Kubernetes Service in the same
namespace. Because they are third-party code, everything about them leans on
two anchors — the image is **pinned to a digest** (a mutable tag would let a
compromised registry push new code into your cluster silently), and nothing
they ask for is granted until **you** approve it.

**Registering a plugin.** A plugin's Deployment and Service are yours to
deploy (helm, plain manifests — anything). Once the Service answers, register
it via the console or `POST /api/v1/plugins` with its `id` (lowercase,
`^[a-z][a-z0-9-]{0,29}[a-z0-9]$` — 2–31 characters, not ending in `-`), service
name (a Kubernetes service name: DNS-1123 label, max 63 characters) and port,
image repository and `sha256:` digest. Registration grants nothing: the CR is
created with an empty approved-permission set.

The event-gateway operator then fetches the manifest the plugin serves at
`/ainsel-plugin.json`, checks it (well-formed, its declared `id` matches the
CR name, its `pluginApi` version is compatible with the platform), and
records what the plugin declares — permissions, version, the manifest itself
— on the Plugin's status.

**Signing.** The digest in `spec.image` says *which* bytes run; a signature says
*who published them*. Without the second check, a compromised registry hands
new code to your cluster and every digest still matches itself. The
event-gateway operator verifies each plugin image's [cosign](https://github.com/sigstore/cosign)
signature against one cluster-wide policy before it accepts the image, and a
plugin whose image does not verify is not let through.

The policy is a Helm value, not a per-plugin setting — trust in a publisher is a
platform decision, and one place to audit beats one place per plugin. It renders
into a ConfigMap the operator mounts read-only at
`/etc/ainsel/plugin-signing/policy.yaml` (keys in
[chart/docs/configuration.md](../chart/docs/configuration.md#plugin-signing)):

```yaml
plugins:
  requireSignature: true        # default: an unsigned image is rejected
  signing:
    keys:                       # PEM public keys — cosign's --key form
      - |
        -----BEGIN PUBLIC KEY-----
        ...base64 DER, one line per 64 chars...
        -----END PUBLIC KEY-----
    keyless:                    # issuer + exactly one of subject / subjectRegExp
      - issuer: https://token.actions.githubusercontent.com
        subjectRegExp: ^https://github\.com/acme/.+/\.github/workflows/release\.yml@refs/tags/v.+$
    trustedRootFile: ""         # empty = the public-good sigstore root
```

`requireSignature: false` accepts unsigned images; it does not make a
signature that *is* present any less required to verify. Turning it off is a
per-install choice about how much you trust your registry, not a way to
install a plugin whose signature is broken.

*Keyed vs keyless.* Pick by who holds the secret:

- **Keyed** — the publisher gives you a public key and signs with the private
  half. Put the PEM in `keys`. No network, no identity provider, and
  verification is exactly as strong as that key. The cost is yours: rotation,
  revocation, and "which build produced this" all live outside the policy.
- **Keyless** — the publisher's CI signs with its OIDC job identity, so there is
  no key to exchange. You name the issuer and what was running. For a plugin
  released from GitHub Actions by a `release.yml` workflow:

  ```yaml
  keyless:
    # Every v* release out of any acme repo, as long as release.yml tagged it:
    - issuer: https://token.actions.githubusercontent.com
      subjectRegExp: ^https://github\.com/acme/.+/\.github/workflows/release\.yml@refs/tags/v.+$
    # ...or one exact release, no regexp:
    - issuer: https://token.actions.githubusercontent.com
      subject: https://github.com/acme/plugin-mem0/.github/workflows/release.yml@refs/tags/v1.4.2
  ```

  Anchor the regexp and keep the `@refs/tags/v` suffix: a subject that matches a
  branch push lets any commit that workflow built pass as a release. An
  identity with neither `subject` nor `subjectRegExp`, or with both, is rejected
  when the policy loads — so is a regexp that doesn't compile.

*Air-gapped.* Verification normally fetches sigstore's certificate authority
and revocation data from the public TUF root. Where there is no path to it,
stage sigstore's trusted root as a file in the cluster and point at it:

```yaml
plugins:
  signing:
    trustedRootFile: /etc/sigstore/trusted_root.json
connectorOperator:
  extraVolumes:
    - name: sigstore-root
      secret:
        secretName: sigstore-trusted-root
  extraVolumeMounts:
    - name: sigstore-root
      mountPath: /etc/sigstore
      readOnly: true
```

The operator reads the policy once, at startup, and the pod template carries a
checksum of it — so `helm upgrade` with a changed policy rolls the operator.
A policy file it cannot parse or validate is a startup error, and the operator
then runs requiring a signature it can never verify rather than treating
signing as off: a typo in trust configuration breaks plugins instead of
opening a door.

**Approving permissions.** If the manifest declares any permissions, the plugin
stays not-Ready with reason `PermissionsPending` until you approve them.
Approval is all-or-nothing against the declared set: `POST
/api/v1/plugins/{id}/approve-permissions` with `"permissions"` naming exactly
what the plugin declared — a subset, a superset, or anything the manifest never
asked for is rejected with `409` and the current declared set in the error.
That is deliberate: you approve what you have just seen, not what a stale
console tab remembers. A declared permission you never approve simply is not
effective.

A plugin can only ever ask for names from this closed vocabulary — anything
else in a manifest is rejected outright, so a plugin cannot invent a
permission to widen its own reach:

| Resource | Permissions |
|---|---|
| Agents | `agents:read`, `agents:write` |
| Agent images | `agent-images:read`, `agent-images:write` |
| Connectors | `connectors:read`, `connectors:write` |
| Invocations | `invocations:read` |
| MCP servers | `mcp-servers:read`, `mcp-servers:write` |
| Observability | `observability:read` |
| Personas | `personas:read`, `personas:write` |
| Skills | `skills:read`, `skills:write` |
| Triggers | `triggers:read`, `triggers:write` |

Read verbs let a plugin look at that resource through the platform API; write
verbs let it change it. Judge a plugin by the write verbs it asks for.

**Group access.** `PUT /api/v1/plugins/{id}/group-access` sets the groups the
plugin's background identity (work done without a user) may act in, with the
`writer` role, bounded by the approved permissions. Unknown group ids are
rejected; an empty list means the background identity can read only public
resources. See [Plugin credentials](access-control.md#plugin-credentials).

**Traffic and network.** The browser and agents never talk to a plugin
directly: the hub proxies its UI assets, API and MCP endpoint under
`/api/v1/plugins/{id}/ui|api|mcp`, and only to pods running the verified image
digest. API and MCP calls need a login; the hub swaps the caller's token for a
short-lived plugin credential, so the plugin never sees a session or `ainsel_`
token. UI assets (the plugin's JavaScript and styles) are served without a
login, because browsers cannot attach a token when they load a module; treat
them as public. Label your plugin pods `ainsel.io/plugin: "true"`: with
`networkPolicy.enabled` (the default) the chart then lets plugin pods reach the
hub and accept traffic only from the hub and the event-gateway operator.
Outbound rules for the external services a plugin uses belong in the plugin's
own chart.

**Disabling.** `POST /api/v1/plugins/{id}/disable` (or the console switch)
stops routing to the plugin, makes it vanish from non-admin listings, and
pauses manifest fetching — the registration and your approvals stay put, so
re-enabling is instant. Uninstalling (`DELETE /api/v1/plugins/{id}`) removes
the CR; the plugin's own Deployment/Service are yours to delete separately.

Every plugin change you make — install, uninstall, enable, disable,
approval, group-access — lands a row in the audit trail (auth events) with
your identity and the plugin id.

**What each status means.** The console and the API surface one `Ready`
condition with a reason; the first failing check wins:

| Reason | Meaning | What you do |
|---|---|---|
| `Ready` | Manifest valid and compatible, every declared permission approved. | Nothing — the plugin is live. |
| `Disabled` | `spec.disabled` is set; the manifest is not even fetched. | Enable it when you want it back. |
| `SignatureRejected` | The plugin image signature verification failed; required by policy. | Verify the image is signed correctly and matches the policy's trust material. |
| `NoVerifiedEndpoints` | No pod endpoints are running the verified image digest. | Check that pod(s) running the plugin have the correct image digest. |
| `Unreachable` | The service at `serviceName:servicePort` didn't answer `/ainsel-plugin.json` in 5 s. | Check the plugin's Deployment/Service actually run and the name/port are right. |
| `InvalidManifest` | The served JSON doesn't parse, or its declared `id` isn't the plugin's name. | Report it to the plugin's author; a plugin cannot claim to be someone else. |
| `Incompatible` | The manifest's `pluginApi` major version doesn't match the platform's. | Get a plugin version built for this platform, or hold the platform back. |
| `PermissionsPending` | Manifest is fine; declared permissions await you. | Review the manifest in the plugin detail view, then approve the full declared set. |

## 6. What you do NOT manage

The whole point of AInsel is to hide AI specifics from end users.
The following are administrator-only concerns:

- **End users do NOT pick which model runs.** That's
  `Agent.spec.llm.model`, configured by you.
- **End users do NOT write or see prompts.** Personas live in
  ConfigMaps you control.
- **End users do NOT choose which tools an agent has.**
  `enabledTools` is yours.
- **End users do NOT see token usage or cost.** Metrics surface on
  the admin frontend's observability page only.
- **End users do NOT interact with Kubernetes, the hub API,
  or any internal mechanism.** They use Forgejo (or whichever
  source system the connector is reading from).
- **End users may not even know they're talking to AI.** Agents
  act through normal forge artifacts — a comment, a label, a draft
  PR — under a normal-looking user identity.

If something on this list leaks to end users — a model error
message surfaces in a forge comment, a stack trace ends up in a PR
body, an internal field name appears in an issue label — that's a
bug. File it.

## Talking to AInsel via MCP

The platform's MCP server (`services/mcp/`) exposes the AInsel control
surface to any MCP-capable AI client. The intended use is to connect it
to a **local agent** — Claude Code, Claude Desktop, an IDE plugin — and
use that agent to control AInsel and ask it questions: inspect triggers
and connectors, list and update agents, read and edit personas and
skills, watch invocations, costs, logs, and platform health.

Read tools answer questions like "what workflows do I have?", "show me
the code-reviewer's persona", "how much did agents cost today?",
"what's been failing today?". Mutation tools let the agent change
platform state directly: update an agent's model, create or delete
triggers and cron triggers, create and edit personas and skills,
register agent images. Mutations act immediately against the hub (no
pending-change staging yet), so review what the agent proposes before
it calls a write tool.

See [`mcp.md`](mcp.md) for the full guide — connecting a local agent,
authentication, the complete tool reference, and example conversations.

## 7. Where to go next

- [`crd-reference.md`](crd-reference.md) — full CRD specs (`Agent`,
  `Trigger`, `WebhookConnector`, `AgentImage`). (Known drift between
  the reference and the current schema.)
- [`event-schema.md`](event-schema.md) — canonical event format and
  the full event-type list.
- [`api-reference.md`](api-reference.md) — hub REST API (useful if
  you build admin tooling on top of AInsel).
- [`deployment.md`](deployment.md) — install / upgrade the platform.
- [`adding-memory.md`](adding-memory.md) — give agents persistent memory
  with the self-hosted mem0 REST API.
- [`architecture.md`](architecture.md) — technical deep-dive.
