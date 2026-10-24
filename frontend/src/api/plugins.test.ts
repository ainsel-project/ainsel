import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { getPlugins } from './plugins'

const mockFetch = () => globalThis.fetch as ReturnType<typeof vi.fn>

const envelope = (items: unknown[]) =>
  new Response(
    JSON.stringify({ items, total: items.length, page: 1, pageSize: 200, totalPages: 1 }),
    { status: 200 },
  )

describe('api/plugins', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('getPlugins unwraps the paginated envelope', async () => {
    mockFetch().mockResolvedValue(envelope([{ id: 'forgejo', ready: true }]))
    const plugins = await getPlugins()
    expect(plugins).toEqual([{ id: 'forgejo', ready: true }])
    const [url] = mockFetch().mock.calls[0]
    expect(url).toContain('/plugins')
    expect(url).toContain('pageSize=200')
  })

  it('getPlugins returns an empty array when no plugin is installed', async () => {
    mockFetch().mockResolvedValue(envelope([]))
    expect(await getPlugins()).toEqual([])
  })
})
