import { describe, it, expect } from 'vitest'
import type { PluginManifest } from '@ainsel/plugin-sdk'
import type { PluginListItem } from '../api/plugins'
import {
  activePlugins,
  navEntries,
  pluginPages,
  pluginTabs,
  findPage,
  activeNavPath,
  slotContributions,
  moduleUrl,
} from './registry'

const mockManifest = (overrides?: Partial<PluginManifest>): PluginManifest => ({
  id: 'test',
  displayName: 'Test Plugin',
  version: '1.0.0',
  pluginApi: '1.1',
  ui: {
    entry: '/static/ui/index.js',
    pages: [],
    slots: {},
  },
  ...overrides,
})

const mockItem = (overrides?: Partial<PluginListItem>): PluginListItem => ({
  id: 'test',
  displayName: 'Test Plugin',
  version: '1.0.0',
  ready: true,
  manifest: mockManifest(),
  ...overrides,
})

describe('registry', () => {
  describe('activePlugins', () => {
    it('filters out plugins that are not ready', () => {
      const items: PluginListItem[] = [
        mockItem({ id: 'a', ready: true }),
        mockItem({ id: 'b', ready: false }),
        mockItem({ id: 'c' }), // ready defaults to true
      ]
      const active = activePlugins(items)
      expect(active.map((p) => p.id)).toEqual(['a', 'c'])
    })

    it('filters out disabled plugins', () => {
      const items: PluginListItem[] = [
        mockItem({ id: 'a', disabled: false }),
        mockItem({ id: 'b', disabled: true }),
      ]
      const active = activePlugins(items)
      expect(active.map((p) => p.id)).toEqual(['a'])
    })

    it('drops plugins without a manifest and falls back to the manifest name', () => {
      const items: PluginListItem[] = [
        mockItem({ id: 'a' }),
        mockItem({ id: 'b', manifest: undefined }),
        mockItem({ id: 'c', displayName: undefined }),
      ]
      const active = activePlugins(items)
      expect(active.map((p) => p.id)).toEqual(['a', 'c'])
      expect(active[1].displayName).toBe(items[2].manifest!.displayName || 'c')
    })

    it('matches page paths on whole segments only', () => {
      const plugin = {
        id: 'mem0',
        displayName: 'Mem0',
        manifest: {
          id: 'mem0',
          displayName: 'Mem0',
          version: '1.0.0',
          pluginApi: '1.1',
          ui: { entry: '/ui/index.js', pages: [{ path: '/mem', label: 'Mem' }] },
        },
      }
      expect(findPage(plugin, 'memories')).toBeUndefined()
      expect(findPage(plugin, 'mem/42')?.path).toBe('/mem')
    })

    it('sorts plugins by id', () => {
      const items: PluginListItem[] = [mockItem({ id: 'b' }), mockItem({ id: 'a' })]
      const active = activePlugins(items)
      expect(active.map((p) => p.id)).toEqual(['a', 'b'])
    })
  })

  describe('navEntries', () => {
    it('maps pages to nav entries with correct routes', () => {
      const plugins = [
        {
          id: 'mem0',
          displayName: 'Mem0',
          manifest: mockManifest({
            ui: {
              entry: '/static/ui/index.js',
              pages: [
                { path: '/', label: 'Home', section: 'Library' },
                { path: '/memories', label: 'Memories', section: 'Library' },
              ],
            },
          }),
        },
      ]
      const entries = navEntries(plugins, true)
      expect(entries).toEqual([
        { section: 'Library', label: 'Home', to: '/p/mem0' },
        { section: 'Library', label: 'Memories', to: '/p/mem0/memories' },
      ])
    })

    it("keeps a section of the plugin's own and puts pages without one under Plugins", () => {
      const plugins = [
        {
          id: 'test',
          displayName: 'Test',
          manifest: mockManifest({
            ui: {
              entry: '/static/ui/index.js',
              pages: [
                { path: '/', label: 'Home', section: 'Memory' },
                { path: '/other', label: 'Other' },
                { path: '/blank', label: 'Blank', section: '  ' },
              ],
            },
          }),
        },
      ]
      const entries = navEntries(plugins, true)
      expect(entries.map((e) => e.section)).toEqual(['Memory', 'Plugins', 'Plugins'])
    })

    it('omits admin pages when isAdmin is false', () => {
      const plugins = [
        {
          id: 'test',
          displayName: 'Test',
          manifest: mockManifest({
            ui: {
              entry: '/static/ui/index.js',
              pages: [
                { path: '/', label: 'Public', section: 'Library' },
                { path: '/admin', label: 'Admin', section: 'Admin', visibility: 'admin' },
              ],
            },
          }),
        },
      ]
      const entries = navEntries(plugins, false)
      expect(entries.map((e) => e.label)).toEqual(['Public'])
    })

    it('includes admin pages when isAdmin is true', () => {
      const plugins = [
        {
          id: 'test',
          displayName: 'Test',
          manifest: mockManifest({
            ui: {
              entry: '/static/ui/index.js',
              pages: [{ path: '/admin', label: 'Admin', section: 'Admin', visibility: 'admin' }],
            },
          }),
        },
      ]
      const entries = navEntries(plugins, true)
      expect(entries.map((e) => e.label)).toEqual(['Admin'])
    })
  })

  describe('pluginPages', () => {
    it("returns one plugin's page entries, admin-filtered", () => {
      const plugin = {
        id: 'mem0',
        manifest: mockManifest({
          ui: {
            entry: '/ui/index.js',
            pages: [
              { path: '/', label: 'Mem' },
              { path: '/setup', label: 'Setup', visibility: 'admin' },
            ],
          },
        }),
      }
      expect(pluginPages(plugin, true).map((e) => e.to)).toEqual(['/p/mem0', '/p/mem0/setup'])
      expect(pluginPages(plugin, false).map((e) => e.to)).toEqual(['/p/mem0'])
    })
  })

  describe('pluginTabs', () => {
    const tabsPlugin = {
      id: 'mem0',
      manifest: mockManifest({
        ui: {
          entry: '/ui/index.js',
          pages: [
            { path: '/', label: 'Mem' },
            { path: '/setup', label: 'Setup', visibility: 'admin' },
          ],
        },
      }),
    }

    it('leads with the Plugin home tab for admins', () => {
      const entries = pluginTabs(tabsPlugin, true)
      expect(entries[0]).toMatchObject({ label: 'Plugin', to: '/plugins/mem0' })
      expect(entries.map((e) => e.to)).toEqual(['/plugins/mem0', '/p/mem0', '/p/mem0/setup'])
    })

    it('omits the Plugin home tab for non-admins', () => {
      // /plugins/:id is the admin management view; a non-admin would only
      // reach the "managed by administrators" dead end.
      expect(pluginTabs(tabsPlugin, false).map((e) => e.to)).toEqual(['/p/mem0'])
    })

    it('keeps the plugin home tab even with no declared pages', () => {
      const bare = {
        id: 'bare',
        manifest: mockManifest({ ui: { entry: '/ui/index.js', pages: [] } }),
      }
      const entries = pluginTabs(bare, true)
      expect(entries.map((e) => e.to)).toEqual(['/plugins/bare'])
      expect(pluginTabs(bare, false)).toEqual([])
    })

    it('preserves pluginPages order behind the home tab', () => {
      const ordered = {
        id: 'ord',
        manifest: mockManifest({
          ui: {
            entry: '/ui/index.js',
            pages: [
              { path: '/b', label: 'B', section: 'Second' },
              { path: '/a', label: 'A', section: 'First' },
            ],
          },
        }),
      }
      expect(pluginTabs(ordered, true).map((e) => e.label)).toEqual(['Plugin', 'B', 'A'])
    })
  })

  describe('findPage', () => {
    it('matches root path', () => {
      const plugin = {
        id: 'test',
        displayName: 'Test',
        manifest: mockManifest({
          ui: {
            entry: '/static/ui/index.js',
            pages: [{ path: '/', label: 'Home' }],
          },
        }),
      }
      const page = findPage(plugin, '')
      expect(page?.label).toEqual('Home')
    })

    it('matches specific path', () => {
      const plugin = {
        id: 'test',
        displayName: 'Test',
        manifest: mockManifest({
          ui: {
            entry: '/static/ui/index.js',
            pages: [
              { path: '/', label: 'Home' },
              { path: '/memories', label: 'Memories' },
            ],
          },
        }),
      }
      const page = findPage(plugin, 'memories')
      expect(page?.label).toEqual('Memories')
    })

    it('matches longest prefix', () => {
      const plugin = {
        id: 'test',
        displayName: 'Test',
        manifest: mockManifest({
          ui: {
            entry: '/static/ui/index.js',
            pages: [
              { path: '/memories', label: 'Memories' },
              { path: '/memories/detail', label: 'Detail' },
            ],
          },
        }),
      }
      const page = findPage(plugin, 'memories/detail/42')
      expect(page?.label).toEqual('Detail')
    })

    it('returns undefined for non-matching path', () => {
      const plugin = {
        id: 'test',
        displayName: 'Test',
        manifest: mockManifest({
          ui: {
            entry: '/static/ui/index.js',
            pages: [{ path: '/memories', label: 'Memories' }],
          },
        }),
      }
      const page = findPage(plugin, 'settings')
      expect(page).toBeUndefined()
    })
  })

  describe('slotContributions', () => {
    it('returns contributions ordered by plugin id', () => {
      const plugins = [
        {
          id: 'b-plugin',
          displayName: 'B Plugin',
          manifest: mockManifest({
            ui: {
              entry: '/static/ui/index.js',
              slots: { 'agent.panel': 'AgentPanel' },
            },
          }),
        },
        {
          id: 'a-plugin',
          displayName: 'A Plugin',
          manifest: mockManifest({
            ui: {
              entry: '/static/ui/index.js',
              slots: { 'agent.panel': 'AgentPanel' },
            },
          }),
        },
      ]
      const contributions = slotContributions(plugins, 'agent.panel')
      expect(contributions.map((c) => c.plugin.id)).toEqual(['a-plugin', 'b-plugin'])
    })

    it('uses export name from manifest', () => {
      const plugins = [
        {
          id: 'test',
          displayName: 'Test',
          manifest: mockManifest({
            ui: {
              entry: '/static/ui/index.js',
              slots: { 'agent.panel': 'CustomExport' },
            },
          }),
        },
      ]
      const contributions = slotContributions(plugins, 'agent.panel')
      expect(contributions[0].exportName).toEqual('CustomExport')
    })

    it('uses default export when slot value is empty', () => {
      const plugins = [
        {
          id: 'test',
          displayName: 'Test',
          manifest: mockManifest({
            ui: {
              entry: '/static/ui/index.js',
              slots: { 'agent.panel': '' },
            },
          }),
        },
      ]
      const contributions = slotContributions(plugins, 'agent.panel')
      expect(contributions[0].exportName).toEqual('default')
    })

    it('returns empty array for slot with no contributors', () => {
      const plugins = [
        {
          id: 'test',
          displayName: 'Test',
          manifest: mockManifest({
            ui: {
              entry: '/static/ui/index.js',
              slots: {},
            },
          }),
        },
      ]
      const contributions = slotContributions(plugins, 'agent.panel')
      expect(contributions).toEqual([])
    })
  })

  describe('activeNavPath', () => {
    const nested = [{ to: '/p/memory' }, { to: '/p/memory/setup' }]

    it('lets the nested entry win on its own path', () => {
      expect(activeNavPath(nested, '/p/memory/setup')).toBe('/p/memory/setup')
    })

    it('keeps the root entry active on the root path', () => {
      expect(activeNavPath(nested, '/p/memory')).toBe('/p/memory')
    })

    it('keeps the nested entry active on a deep link under it', () => {
      expect(activeNavPath(nested, '/p/memory/setup/42')).toBe('/p/memory/setup')
    })

    it('keeps a page entry active on deep links under its path', () => {
      const entries = [{ to: '/p/forgejo' }, { to: '/p/forgejo/work' }]
      expect(activeNavPath(entries, '/p/forgejo/work/42')).toBe('/p/forgejo/work')
    })

    it('never returns two entries for one pathname', () => {
      const entries = [
        { to: '/p/memory' },
        { to: '/p/memory/setup' },
        { to: '/p/memory/setup/detail' },
      ]
      expect(activeNavPath(entries, '/p/memory/setup/detail')).toBe('/p/memory/setup/detail')
    })

    it('does not cross-activate sibling entries', () => {
      const entries = [{ to: '/p/forgejo/now' }, { to: '/p/forgejo/work' }]
      expect(activeNavPath(entries, '/p/forgejo/now')).toBe('/p/forgejo/now')
      expect(activeNavPath(entries, '/p/forgejo/work')).toBe('/p/forgejo/work')
    })

    it('matches whole segments only', () => {
      // A sibling of the nested entry must not activate it; the root entry
      // still matches as its own prefix (mirrors findPage's whole-segment rule).
      expect(activeNavPath(nested, '/p/memory/set')).toBe('/p/memory')
      expect(activeNavPath(nested, '/p/memory/setupx')).toBe('/p/memory')
      const onlyNested = [{ to: '/p/x/setup' }]
      expect(activeNavPath(onlyNested, '/p/x/set')).toBeUndefined()
    })

    it('returns undefined when nothing matches', () => {
      expect(activeNavPath(nested, '/dashboard')).toBeUndefined()
      expect(activeNavPath([], '/p/memory')).toBeUndefined()
    })

    it('resolves entries across sections from one flattened set', () => {
      const entries = [{ to: '/agents' }, { to: '/p/memory' }, { to: '/p/memory/setup' }]
      expect(activeNavPath(entries, '/p/memory/setup')).toBe('/p/memory/setup')
      expect(activeNavPath(entries, '/agents/a-1')).toBe('/agents')
    })
  })

  describe('moduleUrl', () => {
    it('constructs correct URL for plugin entry', () => {
      const plugin = {
        id: 'mem0',
        displayName: 'Mem0',
        manifest: mockManifest({
          ui: { entry: '/static/ui/index.js' },
        }),
      }
      const url = moduleUrl(plugin)
      expect(url).toContain('api/v1/plugins/mem0/ui/index.js')
    })

    it('throws error if no ui.entry', () => {
      const plugin = {
        id: 'test',
        displayName: 'Test',
        manifest: { ...mockManifest(), ui: undefined },
      }
      expect(() => moduleUrl(plugin)).toThrow()
    })
  })
})
