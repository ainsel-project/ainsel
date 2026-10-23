import { describe, it, expect, beforeEach, vi } from 'vitest'
import { loadPluginModule, clearModuleCache } from './loader'
import type { ActivePlugin } from './registry'

const mockPlugin = (id: string): ActivePlugin => ({
  id,
  displayName: `Plugin ${id}`,
  manifest: {
    id,
    displayName: `Plugin ${id}`,
    version: '1.0.0',
    pluginApi: '1.1',
    ui: {
      entry: '/static/ui/index.js',
    },
  },
})

describe('loader', () => {
  beforeEach(() => {
    // Clear module cache between tests
    clearModuleCache()
  })

  it('loads a plugin module', async () => {
    const plugin = mockPlugin('test')
    const mockModule = { default: () => null }
    const importer = vi.fn(async () => mockModule)

    const result = await loadPluginModule(plugin, importer)
    expect(result).toBe(mockModule)
    expect(importer).toHaveBeenCalledOnce()
  })

  it('caches module per plugin id', async () => {
    const plugin = mockPlugin('test')
    const mockModule = { default: () => null }
    const importer = vi.fn(async () => mockModule)

    const result1 = await loadPluginModule(plugin, importer)
    const result2 = await loadPluginModule(plugin, importer)

    expect(result1).toBe(result2)
    expect(importer).toHaveBeenCalledOnce()
  })

  it('caches separately per plugin id', async () => {
    const plugin1 = mockPlugin('test1')
    const plugin2 = mockPlugin('test2')
    const module1 = { default: () => null }
    const module2 = { default: () => null }
    const importer = vi.fn(async (url: string) => {
      if (url.includes('test1')) return module1
      return module2
    })

    const result1 = await loadPluginModule(plugin1, importer)
    const result2 = await loadPluginModule(plugin2, importer)

    expect(result1).toBe(module1)
    expect(result2).toBe(module2)
    expect(importer).toHaveBeenCalledTimes(2)
  })
})

describe('loader retry', () => {
  beforeEach(() => clearModuleCache())

  it('does not cache a failed import', async () => {
    const plugin = mockPlugin('flaky')
    const importer = vi
      .fn<(url: string) => Promise<Record<string, unknown>>>()
      .mockRejectedValueOnce(new Error('network'))
      .mockResolvedValueOnce({ default: () => null })

    await expect(loadPluginModule(plugin, importer)).rejects.toThrow('network')
    await expect(loadPluginModule(plugin, importer)).resolves.toHaveProperty('default')
    expect(importer).toHaveBeenCalledTimes(2)
  })
})
