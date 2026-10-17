# Skill: Project Setup (Fresh Environment)

**Purpose:** bring a fresh clone of this repo to a *verified-working* state
before any edit. Every step is a command with an expected result — no
human-judgment steps.

**When to use:** first time in a fresh clone or worktree, when a baseline
check fails unexpectedly, or in a sandboxed/CI-like environment where the
toolchain is not preinstalled.

The human-facing setup prose lives in
[`CONTRIBUTING.md`](../../../CONTRIBUTING.md) — this file is the executable
version. It points at pin files instead of restating version numbers, so it
cannot go stale when a version bumps. Never copy a version number from here
into a script; read it from the pin file.

---

## 1. Toolchain

Install what is missing, then verify each tool before continuing.

| Tool | Pin source (source of truth) | Verify | Pass when |
|---|---|---|---|
| Node.js | [`.nvmrc`](../../../.nvmrc), root `package.json#engines` | `node -v` | major ≥ 24 |
| pnpm | root `package.json#packageManager` | `pnpm --version` | equals the pinned version |
| Go | `go` directive in [`go.work`](../../../go.work) / each `go.mod` | `go version` (run inside the repo) | ≥ the `go.work` version |
| Helm 3 | — | `helm version --short` | `v3.*` |
| kubectl | — | `kubectl version --client` | prints a client version |

Optional but expected:

| Tool | Needed for | Verify |
|---|---|---|
| `make` | operators' Makefile (envtest, lint, build) | `make --version` |
| `golangci-lint` | Go lint — must be built with Go ≥ the repo's target (see pitfalls) | `golangci-lint --version` |
| Docker + kind or k3d | local cluster for the section 4 smoke checks | `docker info`, `kind version` |
| `shellcheck` | linting `pi/entrypoint.sh` | `shellcheck --version` |

Go auto-toolchain: with `GOTOOLCHAIN=auto` (the default), a Go older than
`go.work` requests downloads the newer toolchain on first build — no manual
install needed, as long as the module cache is writable.

Sandbox note (read-only `$HOME`): `GOPATH`, `GOCACHE`, and the npm/pnpm
caches must point at a writable directory. Node ≥ 25 no longer bundles
corepack, so install pnpm with
`npm install -g pnpm@<packageManager pin> --prefix <writable-prefix>` and put
that prefix's `bin/` on `PATH`.

## 2. Bootstrap

```bash
git clone https://github.com/ainsel-project/ainsel.git
cd ainsel
git fetch origin
git checkout -b <type>/<short-name> origin/main   # never edit on main — see AGENTS.md

pnpm install --frozen-lockfile    # frontend workspace deps
```

Expected: `pnpm install` ends with `Done in ...` and no errors.

## 3. Baseline verification — every check must pass once before you edit

### Go

The workspace root is **not** a module, so `go build ./...` at the root
errors with *"directory prefix . does not contain modules listed in
go.work"*. Iterate over the modules instead:

```bash
for m in $(go list -m -f '{{.Dir}}'); do (cd "$m" && go build ./...); done
# expected: no output, exit 0
```

Services and shared modules test directly:

```bash
for m in services/hub services/mcp services/chat-mcp services/webhook-receiver \
         shared/api shared/auth/oidc; do (cd "$m" && go test ./...); done
# expected: every line starts with ok or ? (no test files)
```

Operators run envtest suites and need control-plane binaries first (CI does
the same via `make setup-envtest`). The versions are derived from each
operator's `go.mod`, never hardcoded:

```bash
for op in operators/agent operators/event-gateway; do
  ( cd "$op" \
    && CRT=$(grep 'sigs.k8s.io/controller-runtime v' go.mod | sed -E 's/.*v([0-9]+\.[0-9]+).*/release-\1/') \
    && K8S=$(grep 'k8s.io/api v' go.mod | sed -E 's/.*v0\.([0-9]+).*/1.\1/') \
    && go install sigs.k8s.io/controller-runtime/tools/setup-envtest@$CRT \
    && setup-envtest use $K8S --bin-dir "$PWD/bin" \
    && go test ./... )
done
# expected: ok ... (the suites auto-discover bin/k8s/*)
```

With `make` available, the canonical form in each operator directory is
`make setup-envtest && go test ./...` (or `make test`, which also runs
`manifests generate fmt vet`).

### Frontend

```bash
pnpm lint     # expected: exit 0 (eslint --max-warnings=0)
pnpm test     # expected: all Vitest suites pass
pnpm build    # expected: "built in ..." and exit 0
```

### Helm chart

```bash
helm lint chart/                                        # expected: 0 chart(s) failed
helm template chart/ > /dev/null                        # expected: exit 0
helm template chart/ -f chart/values-example.yaml > /dev/null   # expected: exit 0
```

### Pi runtime

```bash
node --check pi/agent-shim.js    # expected: exit 0
shellcheck pi/entrypoint.sh      # expected: exit 0 (if shellcheck is installed)
```

If a baseline check fails and fixing it is not your task, stop — AGENTS.md:
*"A failing baseline is not your bug to fix unless that's the task."*

## 4. Local-cluster smoke checks (optional)

Only when your change touches the chart or deploy paths. Full install
walkthroughs are [`GETTING_STARTED.md`](../../../GETTING_STARTED.md) and
[`docs/deployment.md`](../../../docs/deployment.md) — do not duplicate them
here; this section only proves the environment can render the chart and
reach a cluster.

```bash
kind create cluster --name ainsel-dev      # or: k3d cluster create ainsel-dev
kubectl config current-context             # expected: context for ainsel-dev
helm lint chart/
helm template chart/ -f chart/values-local.yaml > /dev/null        # hub-managed local accounts
helm template chart/ -f chart/values-quickstart.yaml > /dev/null   # zero-auth / port-forward
```

`chart/values-local.yaml` and `chart/values-quickstart.yaml` are the local
overlays; their header comments document the matching `helm install`
command. Tear down with `kind delete cluster --name ainsel-dev`.

## 5. Done — handoff

Setup is complete when every check above has passed once on this checkout.
From here, use the sibling skills in `development/`:

- [`component-reference.md`](component-reference.md) — verification commands
  per component
- [`pr-checklist.md`](pr-checklist.md) — branch/commit/PR lifecycle
- [`bug-fix.md`](bug-fix.md) — approaching a reported bug
- [`add-dependency.md`](add-dependency.md) — adding or bumping a dependency
  (lockfiles, `go.sum`, the third-party license manifest)
- [`change-crd.md`](change-crd.md) — changing a CRD schema and keeping the
  chart copy in sync

If the work is operating a running install rather than changing code, the
[`../maintenance/`](../maintenance/) set is the one to use.

---

## Common pitfalls

| Pitfall | Symptom | Fix |
|---|---|---|
| `go build ./...` / `go test ./...` at repo root | `pattern ./...: directory prefix . does not contain modules listed in go.work` | The root is not a module — iterate modules (section 3) |
| Operator tests fail on a fresh clone | `fork/exec .../etcd: no such file or directory` | envtest binaries missing — run `make setup-envtest` (section 3) |
| Relative `KUBEBUILDER_ASSETS` | same etcd error, path resolved from the wrong cwd | Pass an absolute path, or leave it unset — the suites auto-discover `bin/k8s/*` |
| golangci-lint refuses to run | `the Go language version ... used to build golangci-lint is lower than the targeted Go version` | Install a golangci-lint built with Go ≥ the repo's target (CI uses `go install ...@latest`) |
| Operator lint misses plugin checks | plain `golangci-lint run` passes but CI flags logcheck | Operators use `.custom-gcl.yml` — `make lint` builds the custom binary |
| pnpm version mismatch | `ERR_PNPM_OUTDATED_LOCKFILE` or engine warnings | Install the exact version from `package.json#packageManager` |
| Read-only `$HOME` | `mkdir /home/...: read-only file system` from go / golangci-lint / npm | Point `GOPATH`, `GOCACHE`, `XDG_CACHE_HOME`, and the npm cache at a writable dir |
| Restating versions in docs or scripts | doc drifts the next time a pin bumps | Read versions from the pin files — this file's own rule |
