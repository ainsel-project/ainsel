/**
 * Static catalog for the plugin permission vocabulary, used by the plugin
 * detail page to explain what each permission grants.
 *
 * The vocabulary is a closed set: a manifest naming anything else is rejected
 * server-side. The source of truth is the route table in
 * `services/hub/internal/api/plugin_routes.go`; the entries below mirror the
 * "Permission vocabulary" and "Route table" sections of
 * `docs/plugin-reference.md`. If either changes, update this map (a unit test
 * asserts it contains exactly the documented names).
 */

export type PermissionInfo = {
  /** Human label for the resource family, e.g. "Agents". */
  label: string
  /** Route prefixes the permission unlocks (first matching prefix wins). */
  routes: string[]
  /** True for `invocations:read` / `observability:read`: writes answer 403. */
  readOnly?: boolean
}

export const permissionCatalog: Record<string, PermissionInfo> = {
  'agents:read': {
    label: 'Agents',
    routes: ['/api/v1/agents'],
  },
  'agents:write': {
    label: 'Agents',
    routes: ['/api/v1/agents'],
  },
  'agent-images:read': {
    label: 'Agent images',
    routes: ['/api/v1/agent-images'],
  },
  'agent-images:write': {
    label: 'Agent images',
    routes: ['/api/v1/agent-images'],
  },
  'connectors:read': {
    label: 'Connectors',
    routes: ['/api/v1/connectors'],
  },
  'connectors:write': {
    label: 'Connectors',
    routes: ['/api/v1/connectors'],
  },
  'invocations:read': {
    label: 'Invocations',
    routes: ['/api/v1/invocations', '/api/v1/events', '/api/v1/errors'],
    readOnly: true,
  },
  'mcp-servers:read': {
    label: 'MCP servers',
    routes: ['/api/v1/mcp-servers'],
  },
  'mcp-servers:write': {
    label: 'MCP servers',
    routes: ['/api/v1/mcp-servers'],
  },
  'observability:read': {
    label: 'Observability',
    routes: ['/api/v1/observability', '/api/v1/stats', '/api/v1/queue'],
    readOnly: true,
  },
  'personas:read': {
    label: 'Personas',
    routes: ['/api/v1/personas'],
  },
  'personas:write': {
    label: 'Personas',
    routes: ['/api/v1/personas'],
  },
  'skills:read': {
    label: 'Skills',
    routes: ['/api/v1/skills'],
  },
  'skills:write': {
    label: 'Skills',
    routes: ['/api/v1/skills'],
  },
  'triggers:read': {
    label: 'Triggers',
    routes: ['/api/v1/triggers', '/api/v1/cron-triggers'],
  },
  'triggers:write': {
    label: 'Triggers',
    routes: ['/api/v1/triggers', '/api/v1/cron-triggers'],
  },
}

/** Look up a permission; unknown names (fixtures, older data) return undefined. */
export function permissionInfo(name: string): PermissionInfo | undefined {
  return permissionCatalog[name]
}
