import { describe, expect, it } from 'vitest'
import { permissionCatalog, permissionInfo } from './permissionCatalog'

// The closed permission vocabulary documented in docs/plugin-reference.md
// ("Permission vocabulary" + "Route table"), enforced server-side via
// services/hub/internal/api/plugin_routes.go. If the vocabulary changes
// there, this list and the catalog must change with it.
const documentedPermissions = [
  'agents:read',
  'agents:write',
  'agent-images:read',
  'agent-images:write',
  'connectors:read',
  'connectors:write',
  'invocations:read',
  'llm:check',
  'mcp-servers:read',
  'mcp-servers:write',
  'observability:read',
  'personas:read',
  'personas:write',
  'skills:read',
  'skills:write',
  'triggers:read',
  'triggers:write',
]

describe('permissionCatalog', () => {
  it('contains exactly the documented permission names', () => {
    expect(Object.keys(permissionCatalog).sort()).toEqual([...documentedPermissions].sort())
  })

  it('gives every entry a label and at least one route', () => {
    for (const [name, info] of Object.entries(permissionCatalog)) {
      expect(info.label, name).toBeTruthy()
      expect(info.routes.length, name).toBeGreaterThan(0)
      for (const route of info.routes) {
        expect(route, name).toMatch(/^\/api\/v1\//)
      }
    }
  })

  it('marks exactly the read-only permissions', () => {
    const readOnly = Object.entries(permissionCatalog)
      .filter(([, info]) => info.readOnly)
      .map(([name]) => name)
    expect(readOnly.sort()).toEqual(['invocations:read', 'observability:read'])
  })

  it('permissionInfo returns undefined for unknown names', () => {
    expect(permissionInfo('perm1')).toBeUndefined()
    expect(permissionInfo('agents:read')).toBeDefined()
  })
})
