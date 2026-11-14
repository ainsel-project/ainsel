# AInsel Conventions

Repo-wide conventions for commits, branches, entity naming, code style, and PRs. Tooling-specific config (CI workflows, lint configs, Dockerfiles) lives next to its tool — the one exception is the Go lint config: the root `.golangci.yml` is shared by all Go modules (issue #207).

---

## 1. Git Conventions

### Commit Messages

All commits follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/):

```
<type>: <description>

[optional body]
```

**Types:**

- `feat:` — new feature or functionality
- `fix:` — bug fix
- `chore:` — maintenance, dependency updates, CI changes
- `docs:` — documentation only
- `refactor:` — code restructuring without behavior change
- `test:` — adding or updating tests
- `perf:` — performance improvement

**Rules:**

- Lowercase type prefix, no capital first letter in description
- Imperative mood: "add login page", not "added login page"
- No period at the end
- Body is optional, separated by a blank line; use it to explain the *why*

Breaking changes need a marker: append `!` to the type
(`feat(api)!: drop enabledMCPs`) or add a `BREAKING CHANGE: <what>` footer.

**Why the format matters mechanically:** [release-please](https://github.com/googleapis/release-please)
parses these commits on `main` to compute the next version and to write
`CHANGELOG.md`. The subject becomes the changelog entry verbatim, so write it
for someone reading a changelog, not for someone reviewing a diff. A commit
whose subject does not parse is left out of the changelog and does not bump the
version.

### Sign-off

Every commit on a PR carries a `Signed-off-by:` trailer in its last paragraph:

```
fix(hub): stop dropping the correlation id

Explain the why here.

Signed-off-by: Ada Lovelace <ada@example.com>
```

`git commit -s` writes it; `--signoff` also works on `commit --amend` and on
`rebase` (rewrite a whole branch with `git rebase --signoff origin/main`).

The trailer is the Developer Certificate of Origin assertion: that the person
submitting the change wrote it, or otherwise has the right to submit it under
this project's licence. GitHub's terms grant a licence for public repositories;
they do not establish that the person pushing was entitled to, and nothing else
here records the claim. The trailer is the record.

`.github/workflows/pr-signed-off.yml` enforces it and fails the PR when a commit
lacks the trailer or gets its shape wrong; `scripts/check-signed-off.py` holds
the rules and its test pins them. Merge commits are exempt — their content is
other people's commits, each with its own assertion, and merging `main` into a
branch is not the merge author's work to certify. PRs authored by an app
(`dependabot[bot]` and friends) pass with a note: an app cannot certify origin,
and blocking it would also stop Dependabot's auto-merge path.

Squash-merge does not carry the trailers onto `main`; the record lives on the
PR's commit list, which is what the check reads. Add a `Signed-off-by:` line to
the PR body if you want it on the merge commit as well.

### Versioning

The repository uses [Semantic Versioning](https://semver.org/):

```
v<major>.<minor>.<patch>
```

Versions are computed by release-please from conventional commits, not chosen
by hand. `.release-please-manifest.json` records the last released version and
`release-please-config.json` sets the rules:

- **Major** — breaking changes (a `!` marker or a `BREAKING CHANGE:` footer),
  once the project reaches `1.0.0`
- **Minor** — `feat:` commits. While the version is below `1.0.0`,
  `bump-minor-pre-major` maps breaking changes here too, which is what semver
  prescribes before a stable API is promised
- **Patch** — `fix:` commits

Tags always include the `v` prefix and carry no component name (`v1.0.0`, not
`ainsel-v1.0.0`), because one version covers the whole product: the Helm chart
pins a single image tag per component.

### Branching

- `main` is the default branch: the integration line and the release line.
  Never commit directly to it — it is protected, and PR review is the only
  route in.
- Feature work happens on `<type>/<short-description>` branches
  (e.g., `feat/github-connector`, `fix/webhook-timeout`,
  `docs/architecture-update`), cut from `origin/main`.
- Open PRs against `main` and squash-merge them, so the PR title becomes
  the commit subject.
- **Squash-merging each PR is what keeps release-please correct.** It
  derives the version and `CHANGELOG.md` from the conventional commits
  reachable on `main`; one squashed PR commit carries exactly one
  conventional subject, which is what it needs. What stays forbidden is any
  merge onto `main` that collapses several PRs into a single
  non-conventional commit — release-please would then see no features and
  no fixes to release.
- Always `git pull --rebase` before pushing.

### Pull Requests

- One topic per PR. Don't bundle unrelated changes — open separate PRs.
- The PR description should explain *why*, not just *what*.
- Run lints and tests locally before opening (see [Code style](#4-code-style)).
- **Record AI provenance.** If a model generated any part of the diff, name its
  *family* and the licence it arrived under — not the product name. A reviewer cannot
  check an obligation against a brand, and the licence is what decides whether the code
  carries one. Write `none` when the diff is hand-written. What to check before
  adopting a model is in [Model Licensing](model-licensing.md).

### PR Labels

Labels are a human convention here - no workflow reads them, and nothing
creates them automatically. Only `dependencies` exists in the repository today
(Dependabot applies it to its own PRs); add the others with `gh label create`
if you want them.

| Label | Color | Purpose |
|-------|-------|---------|
| `dependencies` | `#0366d6` (blue) | Dependency bumps (applied by Dependabot) |

---

## 2. Naming Conventions

Every named entity — agents, connectors, channels, triggers, cron triggers,
personas, agent images — follows the platform naming scheme: kind prefix,
kebab-case, role word, fixed scope tokens. See
[Naming Convention](naming.md) for the rules, the scope vocabulary, and how
to rename things safely.

## 3. Documentation Conventions

- Every top-level package has a `README.md`. Follow the per-package template
  described in [`CONTRIBUTING.md`](../CONTRIBUTING.md).
- Cross-package links use relative folder paths (e.g., `../services/hub/`),
  not external URLs.
- Architecture lives in [`docs/architecture.md`](architecture.md), not in
  per-package READMEs. Per-package READMEs may include a *focused* internal
  diagram if it helps, but the system-level view stays in one place.
- API/CRD reference content lives in [`docs/crd-reference.md`](crd-reference.md)
  and [`docs/api-reference.md`](api-reference.md). Per-package docs link
  outward instead of duplicating.
- Markdown lines: aim for ~100 chars where reasonable. Use fenced code blocks
  with language tags.

---

## 4. Code Style

### Go

- `gofmt` is mandatory; CI fails on unformatted code.
- `golangci-lint` runs per module with the root `.golangci.yml` — the single
  source of truth (issue #207). CI uses
  `golangci-lint run -c ../../.golangci.yml ./...` from the module directory;
  use the same command for ad-hoc runs. Every package must carry a
  `// Package X ...` doc comment (staticcheck ST1000).
- Tests live next to source as `*_test.go`. Prefer table-driven tests.
- `go.work` joins every Go module; when you change a file in `shared/api/`,
  downstream modules pick it up automatically — no `replace` directives
  needed inside the workspace.

### Routing

Status: being adopted. The route table below is the target shape for issue
#265; the merged route table does not satisfy it yet — patterns still carry no
method, collection handlers still switch on `r.Method`, and the internal agent
task routes are behind one subtree funnel rather than `s.internalTask`. The
invariants are enforced by `router_test.go` as each group converts, so a rule
listed here is only binding for the routes that already follow it.

Hub HTTP routes are declared as a route table in
[`services/hub/internal/api/server.go`](../services/hub/internal/api/server.go),
one Go method + wildcard pattern per endpoint (issue #265):

- The pattern carries the method (`"GET /api/v1/agents/{name}"`), so
  `http.ServeMux` owns method matching. A handler must not switch on
  `r.Method` or re-parse sub-paths out of `r.URL.Path`; the mux's own `405`
  and `404` are the answer, and the router renders both as the documented JSON
  error body (see [API reference → Error Format](api-reference.md#error-format)).
- The single resource identifier of a path always uses the placeholder name
  `{name}`, including for resources addressed by database id. `ServeMux`
  panics at registration when the same path position carries two different
  wildcard names, so one convention removes that class of startup failure.
- Authz guards are attached at registration, not inside a handler branch:
  `s.read(type, h)` / `s.write(type, h)` gate on the `{name}` path value, and
  every `/api/internal/agents/…` task route goes through `s.internalTask`, the
  only guard for a surface the OIDC middleware does not cover.
- Converting a collection means deleting its old `collection` and
  `collection/` registrations in the same commit that adds the method
  patterns. A method-less pattern matches every method, so a leftover pair
  stops the mux from answering `405` at all.
- Register through `s.routes()`, never the raw mux: the route inventory in
  `router_test.go` reads back what was registered and fails on drift from its
  snapshot, from the plugin-credential route rules, and from these rules.

### TypeScript / Frontend

- Prettier + ESLint, configs at the repo root.
- Tests live next to source as `*.test.ts(x)` or under `__tests__/`.
- Frontend code lives under [`frontend/`](../frontend/); follow its package
  README for build/dev commands.
- All colours and themeable values must reference design tokens declared in
  `frontend/src/design/` — inline `style={{}}` objects included. A
  fallback-less `var()` to an undeclared token silently invalidates the
  declaration (issue #212 rendered white chat text on a white background);
  `frontend/src/design/tokens.guard.test.ts` enforces this.

#### Breakpoints and small screens

- The allowed `@media` widths are the list in the BREAKPOINTS header of
  `frontend/src/design/tokens.css` (480, 760, 899, 1000, 1100). Custom
  properties cannot be used inside a media query, so the scale is a documented
  constant rather than a token, and
  `frontend/src/design/responsive.guard.test.ts` fails the build on any other
  value. Do not add a tier for a preference; add one for a failure mode.
- Small-screen overrides live in `frontend/src/design/responsive.css`, imported
  after `primitives.css`. Keep `primitives.css` free of `@media`: three specs
  read it through `cssThemeKit.parseRules`, which refuses conditional at-rules on
  purpose, so a media query there breaks them.
- A page's outer container uses `className="page"`, never an inline padding.
  Inline styles cannot match a media query, so an inline `padding: '28px 32px'`
  keeps desktop gutters on a phone; the guard spec ratchets the residue.
- The same applies one level down: a collapsing grid takes its column count from
  a class (`.info-grid.cols-5`, `.cols-7`, `.form-grid.cols-3`), never from an
  inline `style={{ gridTemplateColumns: … }}`. An inline declaration beats every
  rule in `responsive.css` whatever the media query, so the grid keeps its
  desktop column count at 360px and overflows its panel.
- Tables are not made responsive. `.reg-wrap` scrolls horizontally and
  `table.reg { min-width: 960px }` stays — reading a register on a phone is a
  scroll, not a reflow. The phone tier only makes that scroll findable
  (`scrollbar-width`/`scrollbar-color`) and lets long mono strings break
  (`overflow-wrap: anywhere` on `table.reg .num`).
- The agent workspace's section nav is a 170px column down to the phone tier,
  where it becomes a full-width wrapping strip under the title: below 900px the
  workspace stacks, and a column of eight-plus links would be the whole first
  screen. The group captions (`Configure` / `Wiring` / `Plugins`) are hidden at
  that tier — the grouping stays in the DOM, only the caption goes. Those
  overrides live in `responsive.css` and are qualified `nav.section-nav`:
  component CSS is bundled after `responsive.css`, so an equal-specificity
  selector there would never win.
- `.canvas` clips horizontal overflow with `overflow-x: clip` (`hidden` as the
  fallback). A bare `hidden` computes `overflow-y` to `auto`, which makes the
  canvas the nearest scrolling ancestor of the sticky title block — a box that
  grows with its content and never scrolls, so the title block stops sticking.
- Viewport-sized chrome declares `vh` and `dvh` in that order (`min-height:
  100vh; min-height: 100dvh`). `vh` alone is the *largest* viewport on mobile
  browsers, so the URL bar covers the bottom of the page.

#### Console UI consistency

Five rules. None of them is a bug on its own; together they are what makes
the console read as one product rather than as one page.

- **One primary action per title block.** A page's main action is a
  `Button variant="primary"` passed to `Titleblock`'s `actions` — not a plain
  button somewhere in the page body, and not missing. Pages whose action
  opens a create form toggle it from there.
- **Page name = nav label = breadcrumb leaf.** The Spine entry, the
  `Titleblock` title, and the last breadcrumb crumb are the same string, and
  they agree with the route: `/settings/mcp-servers` is "MCP Servers" in all
  three places. A second title block under the page's own is deleted, not
  renamed.
- **Hints are sentence-case body text; labels are mono caps.** Explanatory
  prose uses `className="hint"` (`frontend/src/design/base.css`). `.label` is
  uppercase, letter-spaced mono — right for short field labels and table
  headers, unreadable for a paragraph.
- **No native form controls in pages.** Pages compose the primitives
  (`Select`, `Input`, `Autocomplete`, …); a raw `<select>` beside a custom
  control is exactly the inconsistency this rule exists for.
  `frontend/src/design/controls.guard.test.ts` ratchets the residue.
- **Empty panels are not rendered.** A panel with nothing to show — no
  identity in no-auth mode, no data, an action that cannot succeed in this
  mode — is not rendered at all. An empty frame is worse than no frame.

### Helm

- `helm lint chart/` must pass.
- `helm template chart/ -f your-values.yaml` should render without errors.
- Keep values keys lowercase-camelCase to match the rest of the chart.


