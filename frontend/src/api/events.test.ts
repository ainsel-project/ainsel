import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createElement, type ReactNode } from 'react'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { listEvents, listEventsPage, useEventsPage } from './events'

const mockFetch = () => globalThis.fetch as ReturnType<typeof vi.fn>

describe('api/events', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('listEvents GETs /events with limit', async () => {
    mockFetch().mockResolvedValue(
      new Response(JSON.stringify({ events: [], total: 0 }), { status: 200 }),
    )
    await listEvents({ limit: 5 })
    const [url] = mockFetch().mock.calls[0]
    expect(url).toContain('/events')
    expect(url).toContain('limit=5')
  })

  it('unwraps the events array from the envelope', async () => {
    const events = [
      {
        id: 'e1',
        timestamp: '2026-05-20T12:42:00Z',
        connector: 'insel-monorepo',
        status: 'matched',
      },
    ]
    mockFetch().mockResolvedValue(
      new Response(JSON.stringify({ events, total: 1 }), { status: 200 }),
    )
    const out = await listEvents({ limit: 5 })
    expect(out).toEqual(events)
  })

  it('listEventsPage passes pagination and filter params', async () => {
    mockFetch().mockResolvedValue(
      new Response(JSON.stringify({ events: [], total: 0 }), { status: 200 }),
    )
    await listEventsPage({
      limit: 25,
      offset: 50,
      match: 'error',
      connector: 'c-1',
      agent: 'agent-a',
    })
    const [url] = mockFetch().mock.calls[0]
    expect(url).toContain('/events')
    expect(url).toContain('limit=25')
    expect(url).toContain('offset=50')
    expect(url).toContain('match=error')
    expect(url).toContain('connector=c-1')
    expect(url).toContain('agent=agent-a')
  })

  // The two activity axes were renamed `status`/`outcome` → `match`/`runState`
  // (#328). A query key that still named the old params would keep serving the
  // previous page instead of failing, so each axis must be its own identity.
  it('useEventsPage keys the cache on match and runState', async () => {
    const calls: string[] = []
    mockFetch().mockImplementation((url: string) => {
      calls.push(String(url))
      return Promise.resolve(
        new Response(JSON.stringify({ events: [], total: 0 }), { status: 200 }),
      )
    })
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const wrapper = ({ children }: { children: ReactNode }) =>
      createElement(QueryClientProvider, { client: qc }, children)

    type MatchFilter = 'matched' | 'unmatched'
    const { result, rerender } = renderHook(
      ({ match }: { match?: MatchFilter }) => useEventsPage({ limit: 25, match }),
      { wrapper, initialProps: { match: 'matched' } as { match?: MatchFilter } },
    )
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(calls.at(-1)).toContain('match=matched')

    rerender({ match: 'unmatched' })
    await waitFor(() => expect(calls.length).toBe(2))
    expect(calls.at(-1)).toContain('match=unmatched')

    // A different axis is a different cache entry, not a reuse of the last one.
    const second = renderHook(() => useEventsPage({ limit: 25, runState: 'failure' }), { wrapper })
    await waitFor(() => expect(second.result.current.isSuccess).toBe(true))
    expect(calls.length).toBe(3)
    expect(calls.at(-1)).toContain('runState=failure')
  })

  it('listEventsPage returns the page and the match total', async () => {
    const events = [
      {
        id: 'e1',
        timestamp: '2026-05-20T12:42:00Z',
        connector: 'insel-monorepo',
        status: 'matched',
      },
    ]
    mockFetch().mockResolvedValue(
      new Response(JSON.stringify({ events, total: 4711 }), { status: 200 }),
    )
    const out = await listEventsPage({ limit: 25, offset: 0 })
    expect(out.events).toEqual(events)
    expect(out.total).toBe(4711)
  })
})
