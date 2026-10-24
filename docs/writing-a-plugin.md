# Writing a Plugin for AInsel

A plugin is third-party code the platform runs for you: an HTTP workload in the
cluster that adds pages and panels to the console, tools to agents and
operators, and — if it wants them — permissions on the platform API. Unlike a
[connector](writing-a-connector.md), which is a declaration, a plugin is a
program: you build an image, deploy it, and register it.

Three facts shape everything else:

- **The manifest is the contract.** Your image serves
  `GET /ainsel-plugin.json`; that document says who you are, which console
  surfaces you add, and what you need. The operator reads it before your plugin
  is ever marked Ready.
- **Nothing is granted by default.** Permissions are declared in the manifest
  and approved by an admin; group access is a second, separate grant.
- **You never see the caller's token.** The hub hands your backend a short-lived
  *plugin credential* that stands for the caller, and you use it to call back.

The whole contract has one worked implementation: the `hello` fixture at
[`services/hub/internal/plugintest/hello/`](../services/hub/internal/plugintest/hello/)
— manifest, UI module, backend, MCP endpoint, skill, and a visibility test
written to be copied. Read it alongside this guide; every snippet below is
quoted from it.

> **Last verified against:** `main` (commit `cc0738c`), October 2026.
> File paths and field names should match the current codebase; if they
> drift, please open an issue or PR to update this doc.

---

## Table of Contents

1. [What a Plugin Is](#what-a-plugin-is)
2. [The Development Loop](#the-development-loop)
3. [The Manifest](#the-manifest)
4. [Console UI with the SDK](#console-ui-with-the-sdk)
5. [Backend API and the Scoped Credential](#backend-api-and-the-scoped-credential)
6. [MCP Tools, Naming and Audience](#mcp-tools-naming-and-audience)
7. [Group Awareness](#group-awareness)
8. [Background Work](#background-work)
9. [Skills](#skills)
10. [Packaging: Helm Chart + Plugin CR](#packaging-helm-chart--plugin-cr)
11. [Signing](#signing)
12. [Uninstalling Leaves What You Created](#uninstalling-leaves-what-you-created)
13. [Checklist](#checklist)

---

## What a Plugin Is

A plugin is one container image serving three possible surfaces on one port:

| Surface | Declared by | Reached at | Auth |
|---|---|---|---|
| UI | `ui.entry` | `/api/v1/plugins/<id>/ui/<file>` | none (public assets) |
| API | `api.basePath` | `/api/v1/plugins/<id>/api/<path>` | logged-in user |
| MCP | `mcp.path` | proxied by the hub only | user, agent, or plugin credential |

Nobody talks to your pod directly. The hub proxies every request to one of your
`status.verifiedEndpoints` — pod IPs the operator confirmed are running the
image digest you registered with. A pod running other bytes is not routed to.

You own the Deployment and the Service. AInsel owns the `Plugin` resource that
describes them, and it never creates or deletes your workload.

## The Development Loop

### Pick the versions

Your plugin targets a **plugin API** version, declared as `pluginApi` in the
manifest, and builds against the matching **SDK** from npm. Pick them from
the oldest AInsel release you want to run on:

| AInsel release | Highest `pluginApi` | SDK to install |
|---|---|---|
| 0.3.0 and later | `1.1` | `@ainsel/plugin-sdk@^1.1` |

```bash
npm install --save-dev @ainsel/plugin-sdk@^1.1 react
```

The SDK minor always matches the plugin API (`1.1.x` ↔ `pluginApi: "1.1"`);
patch releases only fix the SDK itself. A dev dependency is enough: at runtime
the console hands your module its own React and SDK, so neither ends up in
your bundle (see [Console UI with the SDK](#console-ui-with-the-sdk)). An
AInsel install accepts any `pluginApi` with the same major and a minor up to
its own — a `1.1` plugin keeps working on a later `1.2` host; a `1.2` plugin is
refused by a `1.1` host with reason `Incompatible`. The full table lives in the
[reference](plugin-reference.md#versioning).

### Iterate against a dev install

Dev against a throwaway install, where signature verification is off:

```yaml
# values-dev.yaml
plugins:
  requireSignature: false   # an unsigned image is accepted; a present
                            # signature must still verify
```

Then, per iteration:

1. **Build and push** the image; note the digest, not the tag — registration
   requires `sha256:` + 64 hex characters.
2. **Deploy** the Deployment and Service into the AInsel namespace. Label the
   pods `ainsel.io/plugin: "true"` so the platform's default NetworkPolicy lets
   them reach the hub (see
   [Packaging](#packaging-helm-chart--plugin-cr)).
3. **Register** — the console lists and governs plugins but does not create
   them; registration is `POST /api/v1/plugins`:

   ```bash
   curl -X POST $HUB/api/v1/plugins \
     -H "Authorization: Bearer <admin-token>" -H "Content-Type: application/json" \
     -d '{"id":"hello","displayName":"Hello","serviceName":"hello",
          "servicePort":8080,"imageRepository":"ghcr.io/acme/hello-plugin",
          "imageDigest":"sha256:…"}'
   ```

   or, from an MCP client, `install_plugin` with the same fields.
4. **Watch it go Ready.** The event-gateway operator fetches your manifest every
   60 s; `kubectl get plugin hello` prints the Ready reason in its own column. If
   your manifest declares permissions, the last stop is `PermissionsPending` — an
   admin approves the exact declared set:

   ```bash
   curl -X POST $HUB/api/v1/plugins/hello/approve-permissions \
     -H "Authorization: Bearer <admin-token>" -H "Content-Type: application/json" \
     -d '{"permissions":["agents:read"]}'
   ```

Changing the image means a new digest, and there is no update endpoint on the
API — you change the `Plugin` resource itself (`kubectl edit plugin hello`, or
apply your chart again) and the operator picks the new pods up within a resync.
Rebuild loops are faster when the chart owns the CR, so a redeploy and a
registration are the same step.

Status reasons and what to do about each: see the
[reference page](plugin-reference.md#plugin-status-reasons).

## The Manifest

`static/ainsel-plugin.json` from the fixture, in full:

```json
{
  "id": "hello",
  "displayName": "Hello",
  "version": "1.0.0",
  "description": "Fixture plugin exercising the AInsel plugin contract",
  "pluginApi": "1.1",
  "ui": {
    "entry": "/static/ui/index.js",
    "pages": [
      { "path": "/", "label": "Hello", "section": "Library" },
      { "path": "/admin", "label": "Hello admin", "component": "AdminPage", "visibility": "admin" }
    ],
    "slots": { "agent.panel": "AgentPanel" }
  },
  "api": { "basePath": "/api" },
  "mcp": { "path": "/mcp", "audience": { "remember": "agent", "reset": "operator" } },
  "permissions": ["agents:read"],
  "skills": [
    {
      "name": "hello-usage",
      "description": "How agents use the hello plugin",
      "path": "/static/skills/hello-usage.md"
    }
  ]
}
```

Serving it is the whole loading story: the operator fetches
`GET /ainsel-plugin.json` off a verified endpoint, parses it
([`shared/api/plugin/manifest.go`](../shared/api/plugin/manifest.go)) and
stores the bytes verbatim on `status.manifest`. Unknown fields are
**rejected**, so a misspelled key fails loudly instead of being ignored. Cap:
256 KiB.

Reading the fixture line by line:

- `id` — `^[a-z][a-z0-9-]{0,29}[a-z0-9]$`: 2–31 chars, lowercase, ends
  alphanumeric. It must equal the `Plugin` CR name; a plugin cannot claim to be
  someone else. It becomes a URL segment, a CSS/tool prefix and the prefix of
  your operator tool names.
- `pluginApi: "1.1"` — the version of the plugin API you target. The host is
  `1.1`; the rule is *same major, minor not newer* (see
  [Versioning](plugin-reference.md#versioning)).
- `ui.pages[]` — two pages: the default at `/p/hello`, and an admin-only one at
  `/p/hello/admin` rendering the named export `AdminPage`. `section` groups the
  nav entry; a section outside the console's built-in names puts it under
  "Plugins". The console frames each page like its own: a title block with
  the section and the `label` above a padded body, so a page renders only its
  content, with no title or outer margin of its own.
- `ui.slots` — one extension point filled: `agent.panel` renders `AgentPanel`
  under each agent.
- `api.basePath` — marks that you serve a backend under `/api`, reachable only
  through the hub proxy.
- `mcp.audience` — `remember` is for agents, `reset` for operators, and
  `greet`, absent from the map, is `both`.
- `permissions` — `agents:read`, which an admin must approve before it does
  anything.
- `skills` — one skill document served by the plugin itself.

Path rules apply to every path in the manifest (`ui.entry`, page paths,
`api.basePath`, `mcp.path`, `skills[].path`): absolute within the plugin,
characters `A-Z a-z 0-9 . _ ~ - /`, no `%`, `\`, `?`, `#`, `//`, `.` or `..`
segments. Full field table:
[plugin-reference.md](plugin-reference.md#manifest-fields).

## Console UI with the SDK

Your UI is one ES module — the file `ui.entry` names. The console imports it in
the browser, so **it is your build output, not your source**: no JSX syntax, no
bundler-only features, and these three imports are resolved by the console's
import map and must not be bundled (install `@ainsel/plugin-sdk` from npm for
the types — see [Pick the versions](#pick-the-versions)):

- `react`
- `react/jsx-runtime`
- `@ainsel/plugin-sdk`

`react-dom` and `react-dom/client` are exposed too, but a plugin rarely needs
them — the console mounts you.

The console fetches the entry by file name from the directory `ui.entry` names:
`/static/ui/index.js` is requested as
`/api/v1/plugins/<id>/ui/index.js`, and anything it imports relatively resolves
in that same directory — so bundle into one folder and let relative imports do
the work. That surface is proxied without a login: anything under it is public.

The fixture's module (`static/ui/index.js`) is the minimum honest version:

```js
import { jsx as _jsx } from 'react/jsx-runtime'
import { usePluginContext } from '@ainsel/plugin-sdk'

// Default export: the page the console renders at /p/hello.
export default function HelloPage() {
  const { pluginId } = usePluginContext()
  return _jsx('p', { children: 'Hello from ' + pluginId })
}

// Named export, referenced by the manifest's pages[].component.
export function AdminPage() {
  return _jsx('p', { children: 'Hello admin' })
}

// Slot fill: the host passes the agent the panel renders under.
export function AgentPanel({ agentName }) {
  return _jsx('p', { children: 'Hello agent ' + agentName })
}
```

`usePluginContext()` returns `{ pluginId, user, hub, api, navigate, theme }`:

| Member | Meaning |
|---|---|
| `pluginId` | your own id |
| `user` | `{ sub, username, isAdmin, groups[] }` — from `GET /api/v1/plugin-context` |
| `hub` | HTTP client bound to the console's API base, `/api/v1` (the platform API, as the caller) |
| `api` | HTTP client bound to `/api/v1/plugins/<id>/api` (your own backend) |
| `navigate(to)` | in-app navigation, scoped to `/p/<id>` |
| `theme` | `'light'` or `'dark'` |

Pages are the components your manifest names: the default export, or
`pages[].component`. Slots pass props — `agent.panel` gets `{ agentName }`,
`invocation.panel` gets `{ invocationId, agentName }`, and so on; the full
catalogue is in the [reference](plugin-reference.md#slot-catalogue).

**Primitives.** Render with the console's own components so your UI matches it
and follows the theme: `Button`, `Panel`, `Tabs`, `Tag`, `Notice`, `Field`,
`Input`, `Select`, `Textarea`, `Dot`, `RegisterTable`, `Markdown`,
`TimeSeriesChart`, `Sparkbar`, all exported by `@ainsel/plugin-sdk`.

**CSS.** There is no stylesheet hook in the manifest. Colour, spacing and
radius come from the console's CSS custom properties — `--paper`, `--ink`,
`--signal`, `--u`, `--radius`, … — which the console defines per theme, so use
them instead of literals and your page works in dark mode for free. Prefix every
class you emit with your plugin id (`hello-…`): your markup is mounted inside
the console's DOM, and an unprefixed `.panel` belongs to somebody else.

## Backend API and the Scoped Credential

`api.basePath` says you serve an API. Callers reach it at
`/api/v1/plugins/<id>/api/<path>`; the hub forwards to your `basePath` and adds
two headers worth caring about (full header list in the
[reference](plugin-reference.md#headers-a-plugin-receives)):

- `X-Ainsel-Credential` — an `ainselp_` token standing for **the caller**
- `X-Ainsel-Context` — base64url JSON describing that caller (same shape as
  `GET /api/v1/plugin-context`)

Your API is not authenticated by your own secret: the caller's identity arrives
in `X-Ainsel-Credential`, and the way to act on it is to call back into the hub
with it. The fixture's handler
([`hello.go`](../services/hub/internal/plugintest/hello/hello.go)) is the
pattern — `GET /api/visible-agents?names=a,b` asks the hub what the caller may
see:

```go
credential := r.Header.Get(credentialHeader)   // "X-Ainsel-Credential"
if credential == "" {
    writeJSONError(w, http.StatusUnauthorized, "missing credential")
    return
}

req, err := http.NewRequestWithContext(ctx, http.MethodPost,
    hubURL+"/api/v1/plugin-context/visible", bytes.NewReader(body))
req.Header.Set("Authorization", "Bearer "+credential)
```

With a credential that is absent — a dev install without auth, or a caller who
slipped past the proxy — answer `401`. Never fall back to your own identity for
a question about somebody else's.

Which routes a plugin credential may use is a closed table: agents, agent
images, connectors, triggers, MCP servers, personas and skills — each behind its
own permission — plus `POST /api/v1/plugin-context/visible`. Anything else
answers `403 route not available to plugin credentials`, and a route in the table
answers `403 plugin lacks permission <perm>` until an admin approves that name.
The full table, with the read-only routes, is in the
[reference](plugin-reference.md#route-table).

Ask for the read permission of what you list and the write permission of what
you change. Every non-GET call you make is audited as `plugin_api_write` with
the caller's subject and `<pluginId> <METHOD> <path>` — write verbs are a
public statement, so declare the ones you use and nothing more.

## MCP Tools, Naming and Audience

`mcp.path` says you serve an MCP endpoint over streamable HTTP. The fixture is
stateless (`server.WithStateLess(true)`), which is the right default: your pods
are replicas and nothing has to share session state.

Audience is declared per tool in `mcp.audience`, and the split is enforced by
the platform, not by you:

| Audience | Listed + callable through | Never through |
|---|---|---|
| `agent` | the agent runtime's endpoint | AInsel's MCP server (not even for an admin) |
| `operator` | AInsel's MCP server | the agent endpoint |
| `both` (unlisted default) | both | — |

Two consequences worth internalising:

- **Operators see your tools prefixed.** `services/mcp` polls the hub and
  registers each operator-facing tool as `<pluginId>_<tool>` with dashes turned
  into underscores: plugin `mem0-cache`, tool `get_entry` →
  `mem0_cache_get_entry`. Choose tool names that read well with that prefix,
  and don't pick a name that equals a built-in tool — it is skipped, not
  shadowing.
- **Agents see them unprefixed**, through a registry row the hub maintains for
  you: name `plugin-<id>`, URL `<HUB_SELF_URL>/api/internal/plugins/<id>/agent-mcp`.
  You never publish a plugin URL to agent pods.

The agent runtime names itself to the hub with `X-Ainsel-Agent`; what reaches
your plugin is the credential the hub mints for that call — kind `agent`, subject
`agent:<name>` — plus `X-Ainsel-Invocation-Id` when the runtime knows one. That
invocation id is how you bind what you stored to the invocation that stored it.
Reading it needs the header→context hop the fixture does:

```go
server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
    id := r.Header.Get("X-Ainsel-Invocation-Id")
    if id == "" {
        return ctx
    }
    return context.WithValue(ctx, invocationKey{}, id)
})
```

## Group Awareness

AInsel has a group model: users and agents belong to groups, and resources are
granted per group. Your plugin gets **one** credential per request — minted for
the plugin, carrying the caller's subject — and it must not answer "what may
this caller see" from its own identity. Ask the hub, as the caller:

```
POST <hubURL>/api/v1/plugin-context/visible
Authorization: Bearer <X-Ainsel-Credential>

{"resourceType":"agent","names":["a1","a2","a3"]}
→ {"visible":["a1"]}
```

`resourceType` is one of `agent`, `agent-image`, `connector`, `trigger`,
`mcp-server`, `persona`, `skill` (1–500 names, and the credential needs the
matching read permission). Return exactly what the hub listed.

`visibility_test.go` in the fixture package is written to be copied:

```go
// TestVisibility_OnlyAgentsTheCallerCanRead is written to be copied into your
// own plugin's test suite: it needs nothing from this package except New, so
// paste it next to your handler and change the names.
```

It runs a fake hub with a small group model — `ainselp_reader` may read `a1`,
`ainselp_admin` may read `a1` and `a2`, nobody may read `a3` — and asserts both
halves: the answer contains nothing the hub did not list, **and** the hub was
asked with the caller's bearer, exactly once. Paste it next to your handler and
change the names. If your plugin ever answers from its own identity, the second
assertion fails — which is precisely the bug it exists to catch.

## Background Work

Work with no caller — a reconcile loop, a scheduled sync — uses the plugin's own
identity. Fetch a background credential:

```
POST <hubURL>/api/internal/plugins/<id>/credentials
→ {"token":"ainselp_…","expiresAt":"2026-10-23T14:00:00Z"}
```

Only a pod whose IP is one of your `status.verifiedEndpoints` gets an answer —
so this call works from inside your own pods and nowhere else. Tokens last five
minutes; fetch one when you need it rather than caching it aggressively.

That identity holds the `writer` role on each group an admin granted with
`PUT /api/v1/plugins/<id>/group-access`, and nothing else. With no groups
granted it reads only public resources — which is the correct default, not a
broken install. So design background work to say plainly what it needs: "this
plugin reconciles agents, grant it these groups" is a question an admin can
answer, and a loop that silently sees nothing is not.

## Skills

A skill is a markdown document an agent loads when it needs to know how to use
something. `skills[]` in the manifest contributes yours; the plugin serves the
body and the hub stores it:

- id and name become `<pluginId>-<skill name>` — a slug
  (`^[a-z][a-z0-9-]{0,48}[a-z0-9]$`), unique within your manifest
- the row carries the tag `plugin:<pluginId>` and `managedBy: plugin`: nobody
  can edit it through the API, and you cannot claim a row a user owns
- at most 20 skills, each description at most 500 characters
- the body is fetched over a verified endpoint and must be **under 64 KiB** —
  over that it is rejected, not truncated

Write the document for the agent reading it: when to use each tool, what the
answer looks like, what a failure means. The fixture's
[hello-usage.md](../services/hub/internal/plugintest/hello/static/skills/hello-usage.md)
does exactly that, including "if it answers with an error instead, your runtime
is not sending `X-Ainsel-Invocation-Id`".

## Packaging: Helm Chart + Plugin CR

Ship one chart that renders the workload; the `Plugin` resource is what the
operator reconciles. Two pieces:

```yaml
# templates/deployment.yaml — the important labels
metadata:
  labels:
    ainsel.io/plugin: "true"      # the platform NetworkPolicy keys off this
spec:
  template:
    metadata:
      labels:
        ainsel.io/plugin: "true"
```

With `networkPolicy.enabled` (the default) the chart lets pods labelled
`ainsel.io/plugin: "true"` reach the hub, and accepts traffic only from the hub
and the event-gateway operator. Outbound rules for whatever else your plugin
calls are yours to add in your own chart.

```yaml
# templates/plugin-cr.yaml
apiVersion: ainsel.dev/v1alpha1
kind: Plugin
metadata:
  name: hello
  namespace: ainsel
spec:
  displayName: "Hello"
  service:
    name: hello
    port: 8080
  image:
    repository: ghcr.io/acme/hello-plugin
    digest: sha256:0000000000000000000000000000000000000000000000000000000000000000
```

`image.digest` is required, not recommended: plugins are third-party code, and
a mutable tag lets a compromised registry push new code in silently. Publish by
digest, and keep the tag in your chart only as a human-readable annotation.

## Signing

A digest says *which* bytes run; a signature says *who published them*. Admins
configure trust once, cluster-wide, in Helm values
([administrator guide](administrator-guide.md#plugins)):

```yaml
plugins:
  requireSignature: true
  signing:
    keyless:
      - issuer: https://token.actions.githubusercontent.com
        subjectRegExp: ^https://github\.com/acme/.+/\.github/workflows/release\.yml@refs/tags/v.+$
```

As a publisher, **keyless from GitHub Actions** is the shape that matches that
policy — no key to hand out, and the identity is your workflow file:

```yaml
# .github/workflows/release.yml
jobs:
  release:
    permissions:
      contents: read
      packages: write
      id-token: write          # keyless signing needs the workflow's OIDC token
    steps:
      - uses: docker/build-push-action@v6
        id: build
        with:
          tags: ghcr.io/acme/hello-plugin:v1.4.2
          push: true
      - uses: sigstore/cosign-installer@v3
      - name: Sign the digest that was pushed
        env:
          DIGEST: ${{ steps.build.outputs.digest }}
        run: cosign sign --yes "ghcr.io/acme/hello-plugin@${DIGEST}"
```

Sign the digest you ship, tagless or alongside the tag. The identity an admin
must allow is the one cosign reports —
`https://github.com/<org>/<repo>/.github/workflows/release.yml@refs/tags/<tag>`.
Pin it exactly for one release, or use an anchored `subjectRegExp` for a release
series; a regexp that also matches a branch push lets any commit that workflow
built pass as a release, so keep the `@refs/tags/v` suffix. If you would rather
hand over a key than an identity, that works too — admins put the PEM in
`plugins.signing.keys`.

## Uninstalling Leaves What You Created

Uninstall — `DELETE /api/v1/plugins/<id>`, or `uninstall_plugin` — deletes the
`Plugin` CR only. What goes with it: the `plugin-<id>` MCP registry row and your
skill rows, both because the plugin was their author. What stays:

- **your Deployment and Service** — you deploy them, you delete them;
- **every resource your plugin created through the platform API** — agents,
  triggers, connectors, personas, skills, MCP servers your credential made stay
  in place and keep working.

That is deliberate: a plugin that made an agent should not take the agent down
when the plugin is removed, and an admin who uninstalls should not be surprised
by a cascade. But it means your plugin is not the lifecycle owner of what it
made. Prefer a tag, label or naming convention that identifies what you created,
and say in your own docs how to clean it up.

## Checklist

- [ ] `@ainsel/plugin-sdk` minor equals the manifest's `pluginApi`, and that
      `pluginApi` is supported by the oldest AInsel release you target
- [ ] Manifest served at `GET /ainsel-plugin.json`, parses with unknown fields
      absent, `id` matches the `Plugin` CR name, `pluginApi` ≤ the host's
- [ ] UI module imports only `react`, `react/jsx-runtime`, `@ainsel/plugin-sdk`;
      classes prefixed with the plugin id; colours from console tokens
- [ ] Backend answers `401` when `X-Ainsel-Credential` is missing, and calls
      back into the hub with the caller's credential
- [ ] Visibility: any list the caller gets is filtered by
      `POST /api/v1/plugin-context/visible`, with `visibility_test.go` copied in
      and passing
- [ ] MCP tools named with the `<pluginId>_` prefix in mind; each tool's
      audience declared; invocation id read from `X-Ainsel-Invocation-Id`
- [ ] Background work uses `POST /api/internal/plugins/<id>/credentials` and
      documents the groups it needs
- [ ] Skills ≤ 20, descriptions ≤ 500 chars, bodies under 64 KiB
- [ ] Chart renders Deployment + Service labelled `ainsel.io/plugin: "true"` and
      a `Plugin` CR with `image.digest`
- [ ] Image signed with cosign; release identity published to admins so they can
      put it in `plugins.signing`
- [ ] Docs state what the plugin creates and that uninstalling leaves it behind
