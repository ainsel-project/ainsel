import { describe, it, expect } from 'vitest'
import type { PluginManifest } from '@ainsel/plugin-sdk'
import type { PluginListItem } from '../api/plugins'
import {
  activePlugins,
  navEntries,
  findPage,
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
      const items: PluginListItem[] = [
        mockItem({ id: 'b' }),
        mockItem({ id: 'a' }),
      ]
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

    it('keeps a section of the plugin\'s own and puts pages without one under Plugins', () => {
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
              pages: [
                { path: '/admin', label: 'Admin', section: 'Admin', visibility: 'admin' },
              ],
            },
          }),
        },
      ]
      const entries = navEntries(plugins, true)
      expect(entries.map((e) => e.label)).toEqual(['Admin'])
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
