# AGENTS.md

Short rule sheet for AI agents working in this repo. If you're a human, read
[`CONTRIBUTING.md`](CONTRIBUTING.md) instead.

## What this repo is

AInsel: a Kubernetes-native platform for AI agents that react to events from
code forges. Monorepo — every component lives here, deploys as a single Helm
chart into a single namespace.

Folder map (role -> path):

| Path | Role |
|---|---|
| `frontend/` | React operations console |
| `services/hub/` | Control plane (event routing, REST API) |
| `services/webhook-receiver/` | Forgejo webhook receiver / event normalizer |
| `services/mcp/` | MCP server registry |
| `services/chat-mcp/` | Chat MCP sidecar (chat tools for the agent runtime) |
| `operators/agent/` | K8s operator for the `Agent` CRD (task Jobs, 0-replica template Deployments, Services, persona ConfigMaps) |
| `operators/event-gateway/` | K8s operator for `WebhookConnector` CRD |
| `shared/api/` | Shared Go module (event schema, filter engine, naming helpers) |
| `chart/` | Helm chart |
| `pi/` | Pi-native agent runtime |
| `docs/` | Architecture, CRDs, deployment, conventions |

## First time in this repo?

Run [`.agent/skills/development/project-setup.md`](.agent/skills/development/project-setup.md)
top-to-bottom before editing anything: toolchain pins + verify commands,
workspace bootstrap, baseline verification, and optional local-cluster
smoke checks. Skip it only if this checkout passed those checks today.

## Skills

[`.agent/`](.agent/) holds the playbooks for recurring work, in two sets —
`skills/development/` for changing this repo (setup, verification per
component, PRs, bug fixes, dependencies and licenses, CRD changes) and
`skills/maintenance/` for operating a running install (checking an agent for
errors, control-plane health, Docker Hub tag retention). The index with
"use when" triggers is [`.agent/README.md`](.agent/README.md).

## Non-negotiable rules

- **Open every PR against `develop`.** `develop` is the default branch and the
  only PR target. `main` is release-only: it receives work exclusively through
  a `develop` → `main` promotion PR, never through a PR of its own.
  Branch from `origin/develop`, open the PR against `develop`. Never commit
  directly to either branch.

  This is stricter than "don't commit to `main`": a PR *targeting* `main` from
  an otherwise clean feature branch still lands work on the release line. That
  is how #336, #337 and #339 reached `main`, and once there they needed a
  manual cherry-pick to return to `develop` — a `main` → `develop` merge was
  not clean (#340, #341).
- **Conventional Commits** for every commit: `feat:`, `fix:`, `chore:`,
  `docs:`, `refactor:`, `test:`, `perf:`. Imperative, lowercase, no trailing period.
- **One topic per PR.** Don't bundle unrelated changes — open separate PRs.
- **Sync before editing:** `git fetch origin` and check for divergence on
  your branch. Multiple agents work in this repo; local state goes stale.
- **`git pull --rebase`** before push. Never force-push to shared branches.
- **Lint and test before claiming done:** per-module `go build ./...` and
  `go test ./...` (see
  [`.agent/skills/development/project-setup.md`](.agent/skills/development/project-setup.md) — the
  go.work root is not a module), `golangci-lint run`, `pnpm lint`,
  `pnpm test`, `helm lint chart/` where applicable.
- **When a dependency changes, so does the license manifest.** Lockfiles,
  `go.sum` and `THIRD_PARTY_LICENSES.md` / `LICENSES/` move together — see
  [`.agent/skills/development/add-dependency.md`](.agent/skills/development/add-dependency.md).
- **Do not touch CI workflows or any `Dockerfile` without
  coordinating with the CI/Dockerfile workstream.**
- **Never read `.env` files.** Use `.env.example` for reference.

## Before you edit

1. `git fetch origin && git status` — confirm your branch is in sync with
   the remote, you're not on `main`, and your branch was cut from
   `origin/develop`.
2. When you open the PR, confirm the base reads `develop` before submitting.
   GitHub pre-fills the base from the repository default, so a branch that
   drifted off `main` will silently offer `main` as the base.
3. If you're picking up an open PR, verify it's still open and unmerged
   before adding commits.
4. Run `pnpm install` and the per-module Go build (see
   [`.agent/skills/development/project-setup.md`](.agent/skills/development/project-setup.md) —
   `go build ./...` at the repo root errors because the workspace root is
   not a module) once to confirm a clean baseline before changing anything.
   A failing baseline is not your bug to fix unless that's the task.

## Where to look for what

| If you need… | Look in… |
|---|---|
| Platform architecture, data flow, Postgres event queue | [`docs/architecture.md`](docs/architecture.md) |
| CRD specs (`Agent`, `AgentImage`, `WebhookConnector`; `Trigger`/`CronTrigger` documented there as DB-backed) | [`docs/crd-reference.md`](docs/crd-reference.md) |
| Admin-facing concepts (personas, models, tools, triggers) | [`docs/administrator-guide.md`](docs/administrator-guide.md) |
| Canonical event schema | [`docs/event-schema.md`](docs/event-schema.md) |
| Hub REST API endpoints | [`docs/api-reference.md`](docs/api-reference.md) |
| Deploying the platform | [`docs/deployment.md`](docs/deployment.md) |
| Docker Hub tag retention, registry cleanup | [`docs/maintenance.md`](docs/maintenance.md) |
| Current and planned work | [open issues](https://github.com/ainsel-project/ainsel/issues) — there is no roadmap doc; planned work is tracked as issues |
| Repo conventions (commits, branching, style) | [`docs/conventions.md`](docs/conventions.md), [`CONTRIBUTING.md`](CONTRIBUTING.md) |
| Step-by-step playbooks (deps, CRDs, PRs; agent / control-plane health) | [`.agent/README.md`](.agent/README.md) |
| Operating a live install: symptoms → commands | [`docs/troubleshooting.md`](docs/troubleshooting.md), [`.agent/skills/maintenance/`](.agent/skills/maintenance/) |
| Package-specific details (how to run, env vars) | that package's `README.md` |

## Done-criteria checklist

Before reporting work as complete:

- [ ] Tests pass: `go test ./...` and `pnpm test` in any package you touched.
- [ ] Lint passes: `golangci-lint run`, `pnpm lint`.
- [ ] Build passes: per-module `go build ./...`, `pnpm build` (frontend),
      `helm lint chart/` (if chart touched).
- [ ] Commit messages use Conventional Commits style; PR title does too.
- [ ] PR body explains *why*, not just *what*.
- [ ] Diff contains only changes relevant to the PR's one topic.
- [ ] No commented-out code, no debug prints, no `TODO` left for the
      reviewer to chase.
- [ ] Cross-links in any docs you touched still resolve to real files.
