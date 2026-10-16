# Skill: PR Checklist (All Components)

**When to use:** Any time you create a pull request in this repository — regardless of which component you're changing.

---

## Checklist

### 1. Before Writing Code

- [ ] **Read the issue** — Understand the bug/feature fully. Reproduce if possible.
- [ ] **Identify affected component(s)** — See `component-reference.md` for the map.
- [ ] **Sync with remote** — `git fetch origin` and branch from latest `origin/develop`.
- [ ] **Create a focused branch** — One topic per PR.
  - `fix/<short-description>` for bugs
  - `feat/<short-description>` for features
  - `chore/<short-description>` for tooling/docs
  - `refactor/<short-description>` for refactors
- [ ] **Check for existing work** — Search open PRs/branches to avoid duplicates.

### 2. While Writing Code

- [ ] **Minimal diff** — Only change what's necessary. No drive-by refactors.
- [ ] **Follow conventions** — Match existing code style, naming, and patterns.
- [ ] **Conventional Commit** — `fix:`, `feat:`, `chore:`, `docs:`, `refactor:`, `test:`, `perf:`. Imperative, lowercase, no trailing period.
- [ ] **Explain the WHY** — Commit body: root cause (fixes) or motivation (features).
- [ ] **Update tests** — If behavior changed, tests must reflect the new behavior. Add tests for new behavior.
- [ ] **Update docs** — If public API, CRD fields, config options, or UX changed.

### 3. Before Pushing — Verify Locally

Run the checks for **every component you touched** (see `component-reference.md`):

- [ ] **Lint** passes
- [ ] **Tests** pass
- [ ] **Build** succeeds
- [ ] **No unrelated changes** — `git diff` shows only this PR's topic

### 4. Pushing & Creating the PR

- [ ] **Push to your branch** — Never push directly to `develop` or `main` (both protected).
- [ ] **Create PR** targeting `develop` — GitHub pre-fills the base from the repo
      default, so check it reads `develop` before submitting. `main` is
      release-only and receives work only through a `develop` → `main`
      promotion PR (see [`AGENTS.md`](../../../AGENTS.md)).
- [ ] **PR title** — Conventional commit format.
- [ ] **PR body** — Include:
  - `## Problem` — What's broken or missing
  - `## Root Cause` — Why it happens (for bugs)
  - `## Fix` / `## Changes` — What you changed and why
  - `Closes #<issue>` — Link the issue
- [ ] **One topic per PR** — Multiple fixes → separate PRs.

### 5. After Creating the PR

- [ ] **Check CI** — Wait for status. If red:
  - Read failure logs (check the status target URL)
  - Fix the issue locally
  - Amend or add fixup commit → force-push to your branch
- [ ] **CI green** — All checks must pass before merge.
- [ ] **Respond to review** — Address comments, push updates.
- [ ] **Rebase if stale** — `git fetch origin && git rebase origin/develop` if `develop` moved.

### 6. Common Pitfalls

| Pitfall | Prevention |
|---------|-----------|
| Branch includes unrelated commits | Always branch from `origin/develop`, not a local branch |
| PR silently targets `main` | Work lands on the release line and needs a manual cherry-pick back | Confirm the base reads `develop` before submitting |
| CI fails on a test you changed | Run tests locally before pushing |
| Go workspace out of sync | Run `go work sync` if you added/removed modules |
| Go dependency drift | Run `go mod tidy` after adding/removing imports |
| Helm template error | `helm lint chart/` and `helm template chart/` before pushing |
| Missing `Closes #N` in PR body | Issue stays open after merge — always link it |
| Force-push to shared branch | Never. Only force-push your own feature branch |
| Touching CI workflows | Coordinate with CI workstream first (see AGENTS.md) |

---

## Quick Reference

```
1. git fetch origin → branch from origin/develop
2. Make minimal, focused changes
3. Run lint + test + build for affected components
4. Update tests if behavior changed
5. Push branch → create PR → link issue (Closes #N)
6. Wait for CI → fix if red → get review → merge
```
