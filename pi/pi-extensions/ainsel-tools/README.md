# pi-ainsel-tools

Enforces the agent's own tool grant at runtime.

The `Agent` CR carries `spec.enabledTools` — the tools this agent may use. The
operator passes it to the pod as `AGENT_TOOLS`. The image only declares what it
ships (its capability catalog, `AgentImage.Spec.Tools`); **this extension is where
the selection becomes real.** Before it existed the field reached the pod and
nothing read it.

## Behaviour

`AGENT_TOOLS` unset or empty → **no tool grant**: the agent runs with no tools.
Every declared tool is withdrawn, every call is blocked, and every catalog
binary is withheld from the shell. A grant has to be explicit — the empty state
is the restrictive one, not the permissive one (issue #368). `hub
migrate-enabled-tools` (a chart `pre-upgrade` hook) spells out each agent's
effective set before this flips, so an upgrade never takes tools away.

With a grant in effect:

| Guard | What it does |
|-------|--------------|
| Declared tool set | `pi.setActiveTools()` is narrowed to the selection, on `session_start` **and** `before_agent_start` — registering a tool activates it, so a late registration must not widen the agent's tools. |
| `tool_call` (any tool) | A call naming a tool outside the selection is blocked, even if something activated it behind our back. |
| `tool_call` (shell) | A shell command whose command position runs a catalog tool (`AINSEL_TOOLS_DIR` executable) that the selection does not name is blocked. |

### Name matching

| Selection entry | Matches |
|-----------------|---------|
| `read_file` | the registered tool `read_file`, and the catalog tool `read_file` |
| `mcp__github__list_issues` | that MCP tool |
| `mcp__github` | every tool of the `github` MCP server (also the bare `github`) |

Entries are exact tool names — the same names as `AgentImage.spec.tools[].name`,
which is what the hub validates `spec.enabledTools` against. For a single MCP
tool that means the full `mcp__<server>__<tool>` name. A **server-level** entry
(`mcp__github`, or the bare `github` the matcher also honours) grants that
server's tools, present and future: the hub accepts it when the server is in the
agent's effective MCP set (its own `spec.mcp.servers`, or the image's when the
agent pins none) and rejects it otherwise. That is what
`hub migrate-enabled-tools` writes for an agent whose tools came from MCP, and
what the agent Tools page offers, so the CR path can express it.

## The shell guard is capability hygiene, not a sandbox

Catalog tools are executables the model calls through `bash`. The guard checks
command positions (`forgejo …`, `/usr/local/bin/tools/forgejo …`, and after
`sudo` / `env` / `FOO=bar` / `&&` / `|` / `$( … )`), so a withheld tool cannot be
run by accident or by a prompt that "forgets" the selection. It does not stop a
caller that copies the binary or reaches it through an interpretation layer
(`bash -c`, `python -c`) — the agent already has a shell, and the pod is the
security boundary. Say so in docs rather than pretending otherwise.

## Failure mode: a selection that names nothing

Applied anyway — the agent ends up with no tools — plus a loud line on stderr:

```
ainsel-tools: AGENT_TOOLS names nothing registered: forgeeo (from session_start); those tools stay unavailable
```

Silently falling back to "everything" would turn a typo in the Agent CR into a
*wider* tool set than the operator asked for. An empty grant is the same shape —
applied, and announced:

```
ainsel-tools: no tool grant (AGENT_TOOLS unset or empty); the agent runs with no tools
```

## Test

```bash
npm test   # node --experimental-strip-types --test index.test.ts
```
