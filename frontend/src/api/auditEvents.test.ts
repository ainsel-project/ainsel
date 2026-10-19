import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { listAuthEvents } from './auditEvents'

const mockFetch = () => globalThis.fetch as ReturnType<typeof vi.fn>

describe('api/auditEvents', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('listAuthEvents GETs /audit/auth-events with filters', async () => {
    mockFetch().mockResolvedValue(
      new Response(
        JSON.stringify({
          items: [
            {
              id: 1,
              occurredAt: '2026-10-19T06:00:00Z',
              actorSub: 'local:alice',
              eventType: 'auth_login',
              target: 'local:alice',
              sourceIp: '10.0.0.1',
              outcome: 'success',
            },
          ],
          total: 1,
          page: 2,
          pageSize: 50,
          totalPages: 1,
        }),
        { status: 200 },
      ),
    )
    const out = await listAuthEvents({
      user: 'local:alice',
      type: 'auth_login',
      from: '2026-10-01T00:00:00Z',
      page: 2,
    })
    const [url] = mockFetch().mock.calls[0]
    expect(url).toContain('/audit/auth-events')
    expect(url).toContain('user=local%3Aalice')
    expect(url).toContain('type=auth_login')
    expect(url).toContain('from=2026-10-01T00%3A00%3A00Z')
    expect(url).toContain('page=2')
    expect(out.items[0].eventType).toBe('auth_login')
    expect(out.total).toBe(1)
  })

  it('omits unset filters', async () => {
    mockFetch().mockResolvedValue(
      new Response(JSON.stringify({ items: [], total: 0, page: 1, pageSize: 50, totalPages: 0 }), {
        status: 200,
      }),
    )
    await listAuthEvents({ page: 1 })
    const [url] = mockFetch().mock.calls[0]
    expect(url).not.toContain('user=')
    expect(url).not.toContain('type=')
    expect(url).not.toContain('from=')
  })
})
