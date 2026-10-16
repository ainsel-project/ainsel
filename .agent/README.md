# `.agent` — Skills for Agents Working in This Repo

Step-by-step playbooks for agents (and humans) doing recurring work in this
repository. Each skill is a checklist of commands with expected results: read
the one that matches the task, follow it in order, verify each step before the
next, and report which steps you completed.

> **Not to be confused with platform Skills.** AInsel ships its own "skills"
> feature — instruction files delivered to *running agents* (`docs/skills.md`,
> `GET /api/v1/skills`). Nothing in this directory is that. These are the
> repo's own working procedures.

## Two sets

**`skills/development/`** — changing this repository.

| Skill | Use when |
|---|---|
| [`project-setup.md`](skills/development/project-setup.md) | Fresh clone/worktree; before editing anything. Toolchain pins, bootstrap, baseline checks |
| [`component-reference.md`](skills/development/component-reference.md) | Which verification command belongs to which component; CI path triggers |
| [`pr-checklist.md`](skills/development/pr-checklist.md) | Any PR: branch, commit, push, CI |
| [`bug-fix.md`](skills/development/bug-fix.md) | Fixing a reported bug, incl. this repo's recurring root causes |
| [`add-dependency.md`](skills/development/add-dependency.md) | Adding/bumping a Go module, pnpm or npm package — lockfiles, `go.sum`, the license manifest |
| [`change-crd.md`](skills/development/change-crd.md) | New/changed CRD field: types → operator base → chart copy → docs |

**`skills/maintenance/`** — operating a running install.

| Skill | Use when |
|---|---|
| [`check-agent-health.md`](skills/maintenance/check-agent-health.md) | "Agent X is erroring / never finishes" — hub records, Agent status, the task Job |
| [`control-plane-health.md`](skills/maintenance/control-plane-health.md) | The platform itself: hub, operators, Postgres, connectors, version skew |
| [`registry-retention.md`](skills/maintenance/registry-retention.md) | Docker Hub tag cleanup — report, tests, and what never gets deleted |

Start with `development/project-setup.md` on a checkout you have not verified
today; start with `maintenance/control-plane-health.md` when the question is
about a live install rather than the code.

## Conventions these skills share

- **Commands over prose.** Each step is runnable and states what success looks
  like. If you find yourself writing "verify it looks right", write the command
  that makes it checkable.
- **Point at the source of truth, don't restate it.** Versions come from pin
  files, policy from `docs/`, values from `chart/values.yaml`. A skill that
  copies a version number is a skill that goes stale.
- **Resource names must be real.** Deployment/CR/label names are quoted from
  the chart and the operators, because guessing them is the most common way an
  operations skill fails at 3am. If you correct one, grep the chart to confirm.
- **Read-only by default.** Anything destructive is marked as a deliberate act
  and says who is allowed to decide (tag deletion and Job deletion are the two
  that matter).
- **AGENTS.md wins** when a skill and `AGENTS.md` disagree — and fix the skill
  in the same PR.

## Adding or changing a skill

1. New file in `skills/development/` or `skills/maintenance/`, kebab-case name
   that reads as a task (`add-dependency.md`, not `deps.md`).
2. Front matter of a sort: `# Skill: <Task>` + `**When to use:**`, then the
   steps, then a **Common pitfalls** table.
3. Verify every command in it yourself, on a current checkout. A skill with a
   command that does not run is worse than no skill.
4. Relative links are `../../../<repo path>` from inside `skills/<set>/`.
5. Add it to the table above, and link it from `AGENTS.md` if agents should
   reach it without being told.
