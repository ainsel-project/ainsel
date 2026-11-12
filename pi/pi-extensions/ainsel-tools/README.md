# pi-ainsel-tools

Enforces the agent's own tool selection at runtime.

The `Agent` CR carries `spec.enabledTools` — the tools this agent may use. The
operator passes it to the pod as `AGENT_TOOLS`. The image only declares what it
ships (its capability catalog, `AgentImage.Spec.Tools`); **this extension is where
the selection becomes real.** Before it existed the field reached the pod and
nothing read it.

## Behaviour

`AGENT_TOOLS` unset or empty → the extension does nothing. An agent with no
selection runs with everything its image and MCP servers provide.

With a selection in effect:

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

Entries are exact tool names — the same names as `AgentImage.spec.tools[].name`,
which is what the hub validates `spec.enabledTools` against. For an MCP tool that
means the full `mcp__<server>__<tool>` name; a bare server name is not a catalog
entry and the hub rejects it (`400 enabledTools not in image`). The matcher does
also grant a bare server name over that server's tools — a defensive rule for a
hand-set `AGENT_TOOLS`, not a supported configuration: never author selections
that way, since the CR path cannot express them.

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
*wider* tool set than the operator asked for.

## Test

```bash
npm test   # node --experimental-strip-types --test index.test.ts
```
