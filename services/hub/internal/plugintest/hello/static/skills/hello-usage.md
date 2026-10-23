# hello-usage

The `hello` plugin is the fixture plugin: it shows what a plugin's MCP tools look
like from an agent's side. It keeps no state, so calling a tool twice gives the
same answer twice.

Use these tools when:

- Call `greet` with `name` when you want a greeting for that name. It answers
  `hello, <name>` and nothing else.
- Call `remember` when you want to keep something from this invocation. It
  answers `remembered for <invocation id>`, which confirms the invocation id
  reached the plugin — if it answers with an error instead, your runtime is not
  sending `X-Ainsel-Invocation-Id`.

`reset` is an operator tool: it is not offered to agents, and an agent that asks
for it should not expect it to work.
