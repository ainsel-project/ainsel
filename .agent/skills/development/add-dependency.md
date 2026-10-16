# Skill: Adding or Bumping a Dependency

**When to use:** when a change adds, removes, upgrades, or overrides anything
installed from a package registry — a Go module, a pnpm package, or an npm
dependency of a pi extension.

A dependency change is never just the import line or the `package.json` entry.
This repo ships three dependency surfaces, and each one has a committed
artifact that CI checks: `go.sum`, `pnpm-lock.yaml`, the pi extensions'
`package-lock.json`, and the generated third-party license manifest. A PR that
updates one and forgets the rest fails CI or ships an image whose contents
nobody reconciled.

---

## 0. Which surface are you touching?

| You are changing… | Tool | Committed artifacts that must change with it |
|---|---|---|
| a Go module (`services/*`, `operators/*`, `shared/*`) | `go` (8 modules, one `go.work`) | that module's `go.mod` + `go.sum` |
| a frontend / docs-site package | `pnpm` (workspace root) | root `pnpm-lock.yaml` |
| a pi extension package (`pi/pi-extensions/*`) | `npm`, per extension | that extension's `package.json` + `package-lock.json` (create the lockfile if the extension has none yet) |
| anything above | — | `THIRD_PARTY_LICENSES.md` + `LICENSES/` (regenerated, see §4) |

The pi extensions are **not** in the pnpm workspace: the pi image installs each
extension's dependencies with `npm install --omit=dev` at build time
([`pi/Dockerfile`](../../../pi/Dockerfile)), so their resolution is pinned by
a per-extension lockfile, not by `pnpm-lock.yaml`.

---

## 1. Go modules

```bash
cd services/hub                # the module you are changing, not the repo root
go get github.com/example/pkg@v1.2.3
go mod tidy                    # required: stale requirements break CI
go build ./... && go test ./...
```

- `go.mod`/`go.sum` are edited by the tool, never by hand.
- The repo root is **not** a module — `go get` there errors. Work inside a
  module (see
  [`project-setup.md`](project-setup.md) §3 for the per-module loop).
- Adding or removing a **module** (not a dependency) also means adding it to
  `go.work`'s `use (...)` and running `go work sync`, then checking every
  module that imports it still builds.
- `shared/api` and `shared/auth/oidc` are consumed by the services and
  operators: bump them where they are *used*, and expect every Go CI job to
  run ([`component-reference.md`](component-reference.md) — CI triggers).
- A dependency that needs a newer Go than `go.work` declares is a repo-wide
  decision, not a dependency bump — say so in the PR.

## 2. pnpm workspace (frontend, docs-site)

```bash
pnpm --filter frontend add <pkg>          # or remove / update
pnpm --filter frontend lint && pnpm --filter frontend test && pnpm --filter frontend build
```

- Always commit `pnpm-lock.yaml` with the `package.json` change. CI installs
  with `--frozen-lockfile`: an out-of-sync lockfile is a hard failure, not a
  warning.
- Install the exact `packageManager` version from the root `package.json`, or
  you will regenerate the lockfile with a different format.
- Root `package.json#pnpm.overrides` pins transitive packages (security
  fixes). If you add a dependency that drags in an overridden package, the
  override still wins — check `pnpm why <pkg>` before concluding a version
  landed.
- `pnpm.auditConfig.ignoreGhsas` lists accepted advisories. Adding an entry is
  a security decision for a human reviewer, never a way to make a check green.

## 3. pi extensions (npm)

```bash
cd pi/pi-extensions/<ext>
npm install <pkg> --save            # writes/updates package-lock.json
```

- Commit the extension's `package-lock.json`. Without it the image build
  resolves whatever is current at build time, so the image and the license
  manifest disagree about what shipped.
- **`.gitignore` ignores `package-lock.json`** (line 27, for the pnpm repo's
  sake). A new extension lockfile will silently not be staged — add it with
  `git add -f pi/pi-extensions/<ext>/package-lock.json` and confirm with
  `git ls-files`. (`ainsel-runner`'s is already tracked.)
- Respect the extension's `.npmrc` if it has one (`pi-auto-compact` uses
  `legacy-peer-deps`); generating a lockfile without it pins something the
  image will not install.
- The pi image is built from the extension directories as-is, so a dependency
  you add here runs inside every agent container. Justify it in the PR body.

## 4. Licenses — the part that is easy to forget

The repo is Apache-2.0 (`LICENSE`) and redistributes its dependencies' license
texts. `THIRD_PARTY_LICENSES.md` and `LICENSES/` are **generated artifacts**,
committed so the shipped images can carry them; the license CI regenerates them
and fails on any diff. Treat them as output, never as a file to hand-edit.

After any change to a lockfile or `go.sum`:

```bash
python3 scripts/generate-third-party-licenses.py   # from the repo root
git status --porcelain                             # the diff IS the deliverable
```

Commit the regenerated `THIRD_PARTY_LICENSES.md` and `LICENSES/` in the **same
PR** as the dependency change — a drift check on the lockfile's PR is a green
CI that fails later on someone else's PR.

- `NOTICE` records license *elections* (e.g. a dual-licensed package) and
  project copyright. It is hand-written and deliberately free of version
  numbers — do not add versions to it, and do not add an election without a
  human deciding it.
- A new dependency whose license is not Apache-2.0-compatible (GPL, AGPL, SSPL,
  "non-commercial", no license at all) is a **stop**: report it, do not add it.
  The generator surfaces packages whose text it cannot find.
- The manifest exists to be *shipped*: the images carry the license texts, so
  changing what must ship (a new surface, a new artifact) is Dockerfile
  territory — coordinate with the CI/Dockerfile workstream
  ([`AGENTS.md`](../../../AGENTS.md)) instead of editing a `Dockerfile` here.
- If `scripts/generate-third-party-licenses.py` is not in your checkout, the
  manifest machinery has not landed on this branch yet — do not hand-write
  `THIRD_PARTY_LICENSES.md`; check the dependency's license by hand (its
  repository or the registry metadata), and say in the PR body that the
  manifest still has to be regenerated.

## 5. Verify before pushing

```bash
# Go: the module you touched, plus anything that consumes it
(cd <module> && go build ./... && go test ./...)
# pnpm
pnpm install --frozen-lockfile && pnpm lint && pnpm test && pnpm build
# licenses: regenerate after committing — a second pass must produce an empty diff
python3 scripts/generate-third-party-licenses.py && git diff --exit-code -- THIRD_PARTY_LICENSES.md LICENSES/
```

- [ ] Lockfile / `go.sum` change is committed **with** the manifest regeneration
- [ ] No vendored copies, no `replace`/`overrides` added to dodge a problem
- [ ] License of the new package is compatible, and its text is in `LICENSES/`
- [ ] Commit message: `chore(deps): <what>` (or `feat:` when the dependency is
      the feature). Dependabot PRs use the same shape — match it.

---

## Common pitfalls

| Pitfall | Symptom | Fix |
|---|---|---|
| `go get` at the repo root | `go: go.work ... not a module` / nothing happens | Run it inside the module directory |
| Lockfile not committed | CI: `ERR_PNPM_OUTDATED_LOCKFILE` | `pnpm install` locally with the pinned pnpm, commit the lockfile |
| License manifest left stale | license CI job fails on a dependency-only PR | Regenerate (§4) in the same PR |
| pi extension dependency added, no lockfile | image builds, manifest cannot account for it | `npm install` in the extension dir, commit `package-lock.json` |
| Lockfile staged but absent from the commit | `.gitignore` ignores `package-lock.json` | `git add -f`, then check `git ls-files` |
| Manual `go.mod` cleanup | `go.sum` mismatches at build time | `go mod tidy` in every affected module |
| Adding an override to "fix" a conflict | resolves locally, breaks a consumer module | Find the root cause; overrides are for published advisories |
| Incompatible license slipped in | legal cleanup later, or an image that must be pulled | Check the license before `go get` / `npm install`, not after |
