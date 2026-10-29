# Access Control

AInsel uses **group-based access control** to isolate resources between
teams. Every resource belongs to a group, and a user's permissions are
determined by their role in that group.

## Authentication

Interactive users authenticate through an external OIDC provider, with
hub-managed local accounts (`auth.mode: local`), or both at once
(`auth.mode: oidc+local`), depending on the deployment. In addition, every
user can create personal access tokens for programmatic use. All methods
resolve to the same user identity and grant the same permissions.

### OIDC (interactive)

Browser-based authentication via an external provider (e.g. Zitadel). The
frontend handles the OAuth flow automatically. API requests carry a standard
`Authorization: Bearer <jwt>` header.

### Local users (interactive)

When the platform runs with `auth.mode: local` (or `oidc+local`, where the
login page offers both paths), users sign in with
username/password against the hub itself. An `admin` account is bootstrapped
on first start; further users are managed by admins via the UI (**Admin →
Users**) or the `/api/v1/users` API. Successful logins receive a session
JWT, used exactly like the OIDC one below.

#### Forced password change

A password that someone else set is not a password the user chose, so such an
account is flagged `must_change_password` and must replace it before it can use
the console. The flag is armed when:

- the bootstrap `admin` account is created with the chart-generated password,
- an admin creates a user with a password,
- an admin resets a user's password.

It is cleared when the user changes their own password through **Profile →
Change password** (`PUT /api/v1/auth/password`). Changing it does not log the
session out.

While the flag is set, the hub answers `403` with a stable code for every
`/api/v1` endpoint except the few the change itself needs:

```json
{"error": "forbidden", "code": "password_change_required", "message": "password change required"}
```

`POST /api/v1/auth/login`, `GET /api/v1/auth/me`, `PUT /api/v1/auth/password`
and `POST /api/v1/auth/logout` stay available — the user has to be able to log
in to change the password, and to log out if they would rather not. The console
uses the code to show a forced change screen instead of the dashboard.

The flag is local accounts only: an OIDC subject authenticates at the identity
provider, which owns that password, and the hub never sets it for them.

### User tokens (programmatic)

Personal access tokens (PATs) for scripts, CI pipelines, and MCP clients.
Tokens are prefixed with `ainsel_` and are created via the API or the
frontend profile page.

```bash
curl -H "Authorization: Bearer ainsel_abc123..." \
  https://ainsel.example.com/api/v1/agents
```

A user token has the **same permissions as the user who created it**. It
cannot be scoped further.

Create a token:

```bash
curl -X POST https://ainsel.example.com/api/v1/user-tokens \
  -H "Authorization: Bearer <jwt>" \
  -H "Content-Type: application/json" \
  -d '{"name": "ci-token", "expiresInDays": 90}'
```

The plaintext token is returned **once** in the response. Store it securely.

## Groups

Groups are the tenancy boundary. They are flat — no nesting or hierarchy.

Any authenticated user can create a group and becomes its **owner**. All
authenticated users can browse the group directory (list groups, view
memberships).

### Roles

Each group membership carries one of three roles:

| Role   | Permissions |
|--------|-------------|
| reader | View all resources in the group and their observability data |
| writer | Create, update, and delete resources in the group |
| owner  | Writer permissions, plus manage group membership and toggle resource visibility |

Roles apply to **all resources in the group** — there are no per-resource
permission overrides.

### Managing groups

```bash
# Create a group (you become the owner)
curl -X POST https://ainsel.example.com/api/v1/groups \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"name": "platform-team", "description": "Platform engineering"}'

# Add a member as writer
curl -X POST https://ainsel.example.com/api/v1/groups/<group-id>/members \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"userId": "<user-id>", "role": "writer"}'

# List members
curl https://ainsel.example.com/api/v1/groups/<group-id> \
  -H "Authorization: Bearer <token>"
```

## Resources and Groups

Every resource (agent, agent-image, connector, trigger, cron-trigger,
MCP server, persona, skill) belongs to exactly one group. You choose the
group when creating the resource, from the groups you are a member of.

One exception: a persona an **agent owns** is not a group resource of its own.
The hub creates it on the agent's behalf when that agent's persona is edited
inline, and it is read and written through `/api/v1/agents/{name}/persona` — so
the *agent's* group decides access, there is nothing extra to grant, and the
persona does not appear in the persona library. Shared persona templates remain
ordinary group resources.

```bash
curl -X POST https://ainsel.example.com/api/v1/agents \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"name": "code-reviewer", "groupId": "<group-id>", ...}'
```

### Visibility rules

| Action | Who can do it |
|--------|---------------|
| Read a resource | Members of the resource's group (reader or above), any user if the resource is public, admins |
| Modify a resource | Members with writer or owner role in the resource's group, admins |
| Delete a resource | Members with writer or owner role in the resource's group, admins |
| Toggle public flag | Members with owner role in the resource's group, admins |

### Public resources

Any resource can be marked as **public**, granting read access to every
authenticated user regardless of group membership. Write access still
requires group membership.

```bash
# Mark a resource as public
curl -X PUT https://ainsel.example.com/api/v1/agents/<id> \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"public": true}'
```

### Listing resources

List endpoints return only resources in **your groups** by default. Add
`?includePublic=true` to also include public resources from other groups.

```bash
# Only my groups' agents
curl https://ainsel.example.com/api/v1/agents \
  -H "Authorization: Bearer <token>"

# My agents + public agents
curl "https://ainsel.example.com/api/v1/agents?includePublic=true" \
  -H "Authorization: Bearer <token>"
```

Admins see all resources regardless of group membership.

A single group's resources are browsable at `GET /api/v1/groups/{id}/resources`
— the same visibility rules apply, so a non-member sees only what the list
endpoints would show them.

## Chat Sessions

Chat sessions are **personal** — they belong to the user who created them.
They are not associated with a group and cannot be made public. Only the
session owner and platform admins can view or manage a session.

## Observability

Observability data (events, invocations, logs, metrics, conversations) is
scoped to the resources you can read. For example, you only see invocation
logs for agents in your groups. Aggregate dashboards only include data from
accessible resources.

## Platform Admins

Users with the admin flag bypass all authorization checks. Admins can:

- Access any resource in any group
- Manage any group's membership
- Promote/demote other admins
- Disable/enable user accounts
- See all observability data

Admin status is managed via the users API:

```bash
curl -X PATCH https://ainsel.example.com/api/v1/users/<user-id> \
  -H "Authorization: Bearer <admin-token>" \
  -H "Content-Type: application/json" \
  -d '{"isAdmin": true}'
```

Admins can also **disable** an account without deleting it — group
memberships and user tokens are kept, but the user fails all authentication:
login is rejected and existing session JWTs and user tokens return 401 while
disabled. Re-enabling restores access immediately.

One accepted boundary: an already-established WebSocket connection
(`/api/v1/ws`) is not torn out when the user is disabled — it stays open
until the client reconnects, at which point the upgrade itself fails
authentication. Until then the connection only keeps receiving the read-only
stats/event stream; the hub does not interpret client messages on it, so no
new authorization decisions are granted to a disabled user.

```bash
curl -X PATCH https://ainsel.example.com/api/v1/users/<user-id> \
  -H "Authorization: Bearer <admin-token>" \
  -H "Content-Type: application/json" \
  -d '{"disabled": true}'
```

## Plugin Credentials

Third-party plugins running in the cluster can authenticate as themselves or
on behalf of users and agents to call the hub API. Plugin credentials are
short-lived (5 minutes) and scoped to the permissions and group accesses the
admin has granted the plugin.

### Token Format and Lifetime

Plugin credential tokens are prefixed `ainselp_` and use HS256 JWT signing.
Each token carries claims for `kind` (user, agent, or plugin), `sub` (the
caller's subject), `permissions`, and `groups`. Tokens expire in 5 minutes and
the `exp` claim is always present.

### Caller Kinds

- **user**: Minted on behalf of an authenticated end-user. Inherits the user's
  admin status (if any) and effective permissions = user's permissions ∩
  plugin's approved permissions.
- **agent**: Issued during an agent run. Carries the agent's owning group
  (if mapped) with reader role, and the plugin's approved permissions.
- **plugin**: Issued for the plugin's own background operations. It holds the
  `writer` role on each group an admin granted on the Plugins page
  (`spec.groupAccess`) and nothing else, so with no granted groups it can read
  only public resources. A plugin fetches it with
  `POST /api/internal/plugins/<id>/credentials`, which only answers callers
  whose pod IP is one of the plugin's verified endpoints (pods running the
  signed image digest).
  Pod IPs are recycled, so the match is as fresh as the operator's last
  reconcile of the plugin: endpoint and pod changes trigger one immediately,
  and the 60-second resync bounds the window otherwise.

Agent and plugin kinds never carry the admin bypass — they are always
constrained by the permissions table.

### Effective Permissions and Groups

Every request is gated by the intersection of:
- The credential's token permissions
- The plugin's current `spec.approvedPermissions`

Similarly, for plugin kind:
- The token's group grants
- The plugin's current `spec.groupAccess`

This means admins can immediately revoke access by narrowing approvals or
removing groups, even if credentials are already issued.

### Route Access

Plugin credentials access the hub API via specific `/api/v1/*` routes only.
Each route requires a permission (e.g. `agents:read`). A plugin without the
required permission receives `403` with `{"error":"plugin lacks permission
<perm>"}`. Routes not in the allowlist return `403` with `{"error":"route not
available to plugin credentials"}`.

| Routes | Read (GET/HEAD) | Write |
|---|---|---|
| `/api/v1/plugin-context` | any credential | `POST /visible` only |
| `/api/v1/agents` | `agents:read` | `agents:write` |
| `/api/v1/agent-images` | `agent-images:read` | `agent-images:write` |
| `/api/v1/connectors` | `connectors:read` | `connectors:write` |
| `/api/v1/triggers`, `/api/v1/cron-triggers` | `triggers:read` | `triggers:write` |
| `/api/v1/mcp-servers` | `mcp-servers:read` | `mcp-servers:write` |
| `/api/v1/personas` | `personas:read` | `personas:write` |
| `/api/v1/skills` | `skills:read` | `skills:write` |
| `/api/v1/invocations`, `/events`, `/errors` | `invocations:read` | not available |
| `/api/v1/observability`, `/stats`, `/queue` | `observability:read` | not available |
| everything else (users, groups, auth, tokens, plugins, chat, channels, …) | not available | not available |

On top of the route check, the normal group checks apply to whoever the
credential stands for.

### Audit Trail

Non-GET/HEAD requests from plugin credentials are audited as `plugin_api_write`
events, recording:
- **ActorSub**: The credential's subject (user/agent/plugin identity)
- **Target**: `<pluginId> <METHOD> <path>`
- **Outcome**: `success` (status < 400) or `failure`

### Configuration

Plugin credentials are enabled via the `HUB_PLUGIN_CREDENTIAL_SECRET`
environment variable (or `plugins.credentialSecretName` in the Helm chart).
The chart generates a random secret (Secret `ainsel-plugin-credentials`, kept
across upgrades) unless you name your own. When the variable is unset or
shorter than 32 bytes, plugin credentials are disabled: `ainselp_` tokens are
rejected with 401, nothing can be minted, and
`/api/internal/plugins/<id>/credentials` answers 503.

## Internal Service Authentication

Service-to-service communication (webhook-receiver → hub, agent runtimes →
hub, MCP service → hub) uses a shared secret via the `X-Internal-Token`
header. This is scoped to `/api/internal/*` endpoints only and does not
grant access to the user-facing API.
