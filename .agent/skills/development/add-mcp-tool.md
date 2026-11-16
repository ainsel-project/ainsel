# Skill: Add an MCP Tool

**When to use:** When a hub REST route (new or existing) needs an AInsel MCP
tool, or when a tool is being removed. Enforced by the parity gate from
issue #326 — a hub route and its MCP tool cannot drift.

**Sources of truth (do not restate them here):**

- `openapi/hub.yaml` — the route surface; every operation carries
  `x-mcp-tool: <name>` (or a list) or `x-mcp-not-exposed: "<reason>"`.
- `docs/api-reference.md` — the annotation convention, documented in
  "MCP exposure annotations".
- `docs/mcp.md` — the tool table (single source for the tool surface) and the
  "Default payload limits" rules for read tools.
- `docs/access-control.md` — authorization: tools enforce nothing themselves;
  the hub's checks on the forwarded bearer decide.

---

## Process

### 1. Spec first

- [ ] Find the operation in `openapi/hub.yaml`.
- [ ] If the route is new, add the path item/operation first (the hub parity
      test fails a registered route that is missing from the spec).
- [ ] Set the exposure key: `x-mcp-tool: <tool_name>` when this PR lands the
      tool; `x-mcp-not-exposed: "<reason>"` when it does not — the reason is
      either a permanent decision or `planned in #<issue> (<tool_name>)`.

### 2. Tool

- [ ] Implement in the matching family file in
      `services/mcp/internal/tools/` (e.g. `agents.go`), reusing the
      `hubGet`/`hubPost`/`hubPut`/`hubDelete` helpers from `http.go` — they
      forward the caller's bearer and pass hub errors through.
- [ ] No MCP-side permission checks. The hub decides.
- [ ] Emit request fields by **argument presence**, not zero value: absent →
      omit the key; `[]`/`{}` → send (clear, where the hub defines clearing).
- [ ] Secrets: accept on write, never render in results or error text.
- [ ] Destructive tools: exact `name`/`id` required, no confirmation argument,
      description says the delete is final.
- [ ] Read tools: apply the default page sizes / truncation hints from
      `docs/mcp.md`.

### 3. Register

- [ ] Register in `services/mcp/internal/mcp/server.go` in `New`, before
      `StartPluginTools` (which snapshots built-in names from `s.ListTools()`
      to protect them from plugin shadowing).

### 4. Tests

- [ ] Unit test in the family's `*_test.go` against the `httptest` hub stub,
      asserting on the request body the stub received; include a 403
      pass-through case and (for write tools) a secret non-echo case.
- [ ] `services/mcp`: `go test ./...` — `internal/mcp/parity_test.go` now
      fails if the tool named in the spec is not registered, or a registered
      tool is in no `x-mcp-tool` annotation (composite tools that join
      routes go in its `builtinAllowlist`, deliberately).
- [ ] `services/hub`: `go test ./internal/api/` —
      `openapi_mcp_marker_test.go` fails on a spec operation with neither
      exposure key (or both, or a non-snake_case tool name).

### 5. Docs

- [ ] Add a row for the tool to the table in `docs/mcp.md`.
- [ ] Flip the spec row from `x-mcp-not-exposed` to `x-mcp-tool` in this same
      PR — never leave a named-but-unregistered or registered-but-unnamed
      tool on `main`.

## Pitfalls

| Pitfall | Prevention |
|---------|-----------|
| Tool added without a spec annotation | Hub marker test fails the PR |
| Spec names a tool that does not exist | MCP parity test fails the PR |
| `""` treated as "unset" so a field cannot be cleared | Presence-based emission (step 2); test the clear case |
| Secret echoed in a tool result or error | Sentinel-secret test per write tool |
| Read tool returns unbounded payload | Default payload limits (`docs/mcp.md`) |
