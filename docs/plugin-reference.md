# Plugin Reference

The wire contract between a plugin and AInsel: the manifest document, the
version rule that decides whether a plugin may run, the console extension
points, the permission vocabulary, the headers a plugin receives, and the
status reasons a `Plugin` reports. Written for plugin authors and for admins
reading a plugin's status.

The schema lives in one Go package,
[`shared/api/plugin/manifest.go`](../shared/api/plugin/manifest.go), and the
reconciler, the hub and the SDK all read it through that one definition — so
what is listed here is what the parser enforces. The
[author guide](writing-a-plugin.md) walks through a complete plugin; this page
is the lookup table.

> **Last verified against:** `main` (commit `cc0738c`), October 2026.
> File paths and field names should match the current codebase; if they
> drift, please open an issue or PR to update this doc.

---

## Table of Contents

1. [Manifest](#manifest)
2. [Manifest fields](#manifest-fields)
3. [Path rules](#path-rules)
4. [Versioning](#versioning)
5. [Slot catalogue](#slot-catalogue)
6. [Permission vocabulary](#permission-vocabulary)
7. [MCP tool audiences](#mcp-tool-audiences)
8. [Headers a plugin receives](#headers-a-plugin-receives)
9. [Plugin status reasons](#plugin-status-reasons)

---

## Manifest

A plugin serves its manifest at `GET /ainsel-plugin.json`, relative to the root
of its in-cluster Service. The event-gateway operator fetches it from a
verified endpoint every 60 s and stores the bytes verbatim on
`status.manifest`.

Parsing rules that bite:

| Rule | Value |
|---|---|
| Maximum body size | 256 KiB (`MaxManifestBytes`), checked before parsing |
| Unknown fields | **rejected** — a key the schema does not know is an error, not a silent drop |
| Required fields | `id`, `displayName`, `version`, `pluginApi` |
| Optional sections | `ui`, `api`, `mcp`, `skills`, `permissions` — absent means that surface does not exist |
| Validation | every problem is reported at once (joined error), not just the first |
| Identity | the manifest's `id` must equal the `Plugin` CR name |

## Manifest fields

Types are JSON types. "Required" means the parser rejects the manifest without
it. "Since" is the minimum `pluginApi` minor that introduced the field — the
host implements `1.1`.

### Top level

| Field | Type | Required | Rules | Since |
|---|---|---|---|---|
| `id` | string | yes | `^[a-z][a-z0-9-]{0,29}[a-z0-9]$` — 2–31 chars, lowercase, ends alphanumeric; equals the `Plugin` CR name | 1.0 |
| `displayName` | string | yes | non-empty after trimming whitespace | 1.0 |
| `version` | string | yes | free text; recorded verbatim on `status.version` | 1.0 |
| `description` | string | no | free text | 1.0 |
| `pluginApi` | string | yes | `"MAJOR.MINOR"` — two plain non-negative decimal parts, no patch, no prerelease, no sign | 1.0 |
| `ui` | object | no | see [UI](#ui-and-page) | 1.0 |
| `api` | object | no | see [API and MCP](#api-and-mcp) | 1.0 |
| `mcp` | object | no | see [API and MCP](#api-and-mcp) | 1.0 |
| `skills` | array | no | see [Skills](#skills); at most 20 entries | 1.1 |
| `permissions` | array of string | no | every entry must be in the [vocabulary](#permission-vocabulary); unknown names are rejected | 1.0 |

### UI and page

| Field | Type | Required | Rules | Since |
|---|---|---|---|---|
| `ui.entry` | string | yes if `ui` present | [path rules](#path-rules); the ES module the console imports | 1.0 |
| `ui.pages` | array | no | page `path`s must be unique within the plugin | 1.0 |
| `ui.pages[].path` | string | yes | [path rules](#path-rules); `"/"` is the plugin's index page | 1.0 |
| `ui.pages[].label` | string | no | not validated; it is the text the console shows in the sidebar and as the page's title, so leave it empty at your own cost | 1.0 |
| `ui.pages[].section` | string | no | the sidebar heading the page is listed under: a built-in one (`Operations`, `Fleet`, `Library`, `Docs`, `Admin`) or a heading of the plugin's own, such as its name, shown after Library; empty means "Plugins" | 1.0 |
| `ui.pages[].icon` | string | no | accepted and stored; the console does not render page icons yet | 1.0 |
| `ui.pages[].component` | string | no | exported component name, `^[A-Za-z_$][A-Za-z0-9_$]*$`; empty means the module's default export | 1.1 |
| `ui.pages[].visibility` | string | no | empty = public, `"admin"` = administrators only (the hub also strips admin pages from the manifest it sends to non-admins); any other value is rejected | 1.1 |
| `ui.slots` | object | no | slot id → exported component name; keys outside the [slot vocabulary](#slot-catalogue) are rejected | 1.0 |

### API and MCP

| Field | Type | Required | Rules | Since |
|---|---|---|---|---|
| `api.basePath` | string | yes if `api` present | [path rules](#path-rules); one trailing `/` allowed here | 1.0 |
| `mcp.path` | string | yes if `mcp` present | [path rules](#path-rules); the streamable-HTTP MCP endpoint | 1.0 |
| `mcp.audience` | object | no | tool name → `agent` \| `operator` \| `both`; a tool absent from the map is `both`; an empty tool name or any other value is rejected | 1.1 |

### Skills

| Field | Type | Required | Rules | Since |
|---|---|---|---|---|
| `skills[]` | array | no | at most 20 (`MaxSkills`) | 1.1 |
| `skills[].name` | string | yes | `^[a-z][a-z0-9-]{0,48}[a-z0-9]$`, unique within the manifest; the registry row is `<pluginId>-<name>` | 1.1 |
| `skills[].description` | string | no | at most 500 characters (`MaxSkillDescriptionLength`, counted in runes); an empty one parses, and shows as an empty summary in the skill list | 1.1 |
| `skills[].path` | string | yes | [path rules](#path-rules); where the plugin serves the body; stored body must be under 64 KiB | 1.1 |

## Path rules

Every path in a manifest — `ui.entry`, `ui.pages[].path`, `api.basePath`,
`mcp.path`, `skills[].path` — is plugin-relative and is later joined onto a
mount prefix, so:

- must start with `/`;
- allowed characters: `A-Z` `a-z` `0-9` `.` `_` `~` `-` `/`;
- rejected: `%` (any percent-encoding), `\`, `?`, `#`, `//` (empty segments),
  and `.` or `..` segments;
- `api.basePath` may end with a single `/`; every other field may not.

## Versioning

| Name | Value | Where |
|---|---|---|
| Host plugin API | `1.1` | `plugin.HostAPIVersion` in [`shared/api/plugin/manifest.go`](../shared/api/plugin/manifest.go) |
| SDK plugin API | `1.1` | `PLUGIN_API_VERSION` in [`packages/plugin-sdk/src/index.ts`](../packages/plugin-sdk/src/index.ts) |
| SDK package | `1.1.0` | [`@ainsel/plugin-sdk` on npm](https://www.npmjs.com/package/@ainsel/plugin-sdk), source in `packages/plugin-sdk/` |

**Which AInsel release supports which plugin API.** A row is added whenever
the host plugin API changes; AInsel releases in between keep the row above.

| AInsel release | Host plugin API | Accepts `pluginApi` | SDK |
|---|---|---|---|
| 0.3.0 and later | `1.1` | `1.0`, `1.1` | `@ainsel/plugin-sdk@^1.1` |

The SDK is versioned on its own, not with AInsel: its minor tracks the plugin
API (`1.1.x` implements `pluginApi "1.1"`), patch releases only fix the SDK, and
a major marks a breaking plugin API change. An AInsel release that changes
nothing for plugins publishes no new SDK.

**Compatibility rule.** `Compatible(pluginApi, host)` is *same major, manifest
minor not newer than the host*:

```
pluginApi "1.0" … "1.1"  → compatible with host 1.1
pluginApi "1.2"          → incompatible (newer than this host)
pluginApi "2.0"          → incompatible (different major)
```

A minor version only ever adds optional things — a field, a slot, an audience —
so a plugin targeting an older minor keeps working on a newer host, and a
manifest declaring a minor the host does not implement is refused with reason
`Incompatible` rather than partly understood. Majors are the breaking line.

A `pluginApi` that is not `"MAJOR.MINOR"` is a validation failure at parse time,
before the compatibility question is ever asked.

## Slot catalogue

`ui.slots` maps a slot to the exported component that fills it. Keys outside
this set are rejected (`Slots` in Go, `SLOT_IDS` in the SDK). Props come from
the host and are typed as `SlotProps` in the SDK.

| Slot | Props | Rendered |
|---|---|---|
| `observability.section` | `{ range: string }` | as a section of the observability page, for the range being viewed |
| `invocation.panel` | `{ invocationId: string; agentName: string }` | under one invocation |
| `agent.panel` | `{ agentName: string }` | under one agent |
| `persona.panel` | `{ personaId: string }` | under one persona |

In both agent slots, `agentName` is the agent's **id** (its resource
`metadata.name`, e.g. `a-5950e4c3`) — not the display name. Display names are
not guaranteed unique and no API resolves them; fetch the agent by id via the
`hub` client if you need the display name.

```js
// ui.slots: { "agent.panel": "AgentPanel" }
export function AgentPanel({ agentName }) {
  return _jsx('p', { children: 'Hello agent ' + agentName })
}
```

## Permission vocabulary

Closed set — a manifest naming anything else is rejected outright, so a plugin
cannot widen its own reach by inventing a name. Declaring a permission grants
nothing: an admin approves the exact declared set
(`POST /api/v1/plugins/{id}/approve-permissions`), and a declared permission
that was never approved simply is not effective.

| Resource | Read | Write |
|---|---|---|
| Agents | `agents:read` | `agents:write` |
| Agent images | `agent-images:read` | `agent-images:write` |
| Connectors | `connectors:read` | `connectors:write` |
| Invocations | `invocations:read` | — |
| MCP servers | `mcp-servers:read` | `mcp-servers:write` |
| Observability | `observability:read` | — |
| Personas | `personas:read` | `personas:write` |
| Skills | `skills:read` | `skills:write` |
| Triggers | `triggers:read` | `triggers:write` |

A permission is only useful where the [route table](#route-table) allows the
call. `invocations:read` and `observability:read` are read-only — their routes
answer `403` to any write method.

### Route table

Which routes a plugin credential may use at all
([`services/hub/internal/api/plugin_routes.go`](../services/hub/internal/api/plugin_routes.go)):

| Routes | Read (GET/HEAD) | Write |
|---|---|---|
| `/api/v1/plugin-context` | any credential | `POST /visible` only |
| `/api/v1/agents` | `agents:read` | `agents:write` |
| `/api/v1/agent-images` | `agent-images:read` | `agent-images:write` |
| `/api/v1/connectors` | `connectors:read` | `connectors:write` |
| `/api/v1/triggers` | `triggers:read` | `triggers:write` |
| `/api/v1/cron-triggers` | `triggers:read` | `triggers:write` |
| `/api/v1/mcp-servers` | `mcp-servers:read` | `mcp-servers:write` |
| `/api/v1/personas` | `personas:read` | `personas:write` |
| `/api/v1/skills` | `skills:read` | `skills:write` |
| `/api/v1/invocations`, `/api/v1/events`, `/api/v1/errors` | `invocations:read` | not available |
| `/api/v1/observability`, `/api/v1/stats`, `/api/v1/queue` | `observability:read` | not available |
| everything else (users, groups, auth, tokens, plugins, chat, channels, …) | not available | not available |

First matching prefix wins. A path that `path.Clean` would rewrite (`..`, `.`,
`//`) is not available to a plugin credential at all. Missing permission →
`403 {"error":"plugin lacks permission <perm>"}`; route outside the table →
`403 {"error":"route not available to plugin credentials"}`.

## MCP tool audiences

Declared per tool in `mcp.audience`; a tool the manifest does not list is
`both`. The split is enforced by the host, not by the plugin:

| Audience | Listed and callable through | Never |
|---|---|---|
| `agent` | the agent endpoint `/api/internal/plugins/<id>/agent-mcp` | AInsel's own MCP server, not even for an admin |
| `operator` | AInsel's MCP server (`GET /api/internal/plugin-tools`, `POST /api/v1/plugin-tools/call`) | the agent endpoint |
| `both` | both | — |

Operator tools are offered as `<pluginId>_<tool>`, with the id's dashes turned
into underscores. See [mcp.md](mcp.md#plugin-tools).

## Headers a plugin receives

The hub strips the caller's own credentials before forwarding:
`Authorization`, `Cookie`, `Proxy-Authorization`, `Forwarded`, every
`X-Forwarded-*`, `X-Real-Ip`, and every `X-Ainsel-*` the client sent. What
arrives instead:

| Header | On | Meaning |
|---|---|---|
| `X-Ainsel-Plugin-Id` | every proxied request | your own id, as the hub routed it |
| `X-Ainsel-Forwarded-Prefix` | every proxied request | `/api/v1/plugins/<id>/<surface>` — the prefix your paths are mounted under |
| `X-Ainsel-Credential` | `api` and `mcp` with a logged-in user; every MCP call the hub makes for an agent | an `ainselp_` token standing for the caller — use it as `Authorization: Bearer …` when calling back into the hub |
| `X-Ainsel-Context` | `api` and `mcp` with a logged-in user | base64url JSON: same shape as `GET /api/v1/plugin-context` (`pluginId`, `kind`, `sub`, `username`, `isAdmin`, `permissions`, `groups[]`, `invocationId`), with your approved permissions |
| `X-Ainsel-Invocation-Id` | MCP calls the hub makes for an agent | the invocation the call belongs to, `^[A-Za-z0-9._:-]{1,128}$`; absent when the runtime sent none |

Credential kinds, lifetimes and group semantics:
[access-control.md](access-control.md#plugin-credentials). A background
credential — the plugin acting as itself — is not pushed to the plugin; it is
fetched with `POST /api/internal/plugins/<id>/credentials`, which answers only
callers whose pod IP is a verified endpoint, and returns
`{"token":"ainselp_…","expiresAt":"<RFC3339>"}`.

Responses lose `Set-Cookie` and gain `X-Content-Type-Options: nosniff`.
The `ui` surface is proxied without a login and accepts only `GET`/`HEAD` —
treat everything under it as public.

## Plugin status reasons

One `Ready` condition carries everything. The checks run in order and the
**first failure wins**
([`operators/event-gateway/internal/controller/plugin_controller.go`](../operators/event-gateway/internal/controller/plugin_controller.go)):

`Disabled → Signature → VerifiedEndpoints → Unreachable → InvalidManifest (parse) → InvalidManifest (id mismatch) → Incompatible → PermissionsPending → Ready`

| Reason | Meaning | What to do |
|---|---|---|
| `Ready` | Manifest valid and compatible, every declared permission approved | Nothing — the plugin is live |
| `Disabled` | `spec.disabled` is set; the manifest is not even fetched | Enable it when you want it back |
| `SignatureRejected` | Signature verification failed, or could not run, while `plugins.requireSignature` is on | Check the image is signed and that the signing identity matches the trust material in `plugins.signing`. A signing policy that failed to load fails closed here too. When signatures are **not** required the plugin still goes Ready with `signature.verified: false` — recorded, not fatal |
| `NoVerifiedEndpoints` | No pod endpoint is running the verified image digest | Check the pods' image against `spec.image.digest`; the reconciler resolves endpoints from EndpointSlices and pods, and never falls back to DNS |
| `Unreachable` | The service did not answer `GET /ainsel-plugin.json` within 5 s | Check the Deployment and that `spec.service.name` / `port` point at a listening plugin |
| `InvalidManifest` | The served JSON does not parse, fails validation, or its `id` is not the plugin's name | Fix the manifest the plugin serves — the validation message names every field that is wrong; an `id` mismatch cannot be worked around from the AInsel side |
| `Incompatible` | `pluginApi` is a different major, or a newer minor than the host's `1.1` | Ship a plugin build targeting this platform's minor, or move the platform forward |
| `PermissionsPending` | Manifest accepted; declared permissions await approval | Review the manifest, then approve the full declared set — a subset, superset, or extra name is rejected with `409` and the current declared set |

The message of a non-Ready `Ready` condition is the operator's own sentence, and
for `PermissionsPending` it names exactly which permissions are outstanding:
`Declared permissions awaiting approval: agents:read, triggers:write`.

A manifest that parsed and declared the right identity is recorded even when a
later check fails: `Incompatible` and `PermissionsPending` leave
`status.manifest`, `status.declaredPermissions` and `status.version` populated,
so the console shows what the plugin asked for and waits for you. A parse failure
or an `id` mismatch stops before that point and leaves whatever was recorded by
an earlier reconcile.

Related: [administrator guide — Plugins](administrator-guide.md#plugins),
[CRD reference — Plugin](crd-reference.md#plugin),
[API reference — Plugins](api-reference.md#plugins).
