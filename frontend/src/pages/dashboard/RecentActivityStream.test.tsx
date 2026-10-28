import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { RecentActivityStream } from './RecentActivityStream'
import { renderWithProviders } from '../../test/renderWithProviders'

const sampleEvents = [
  {
    id: 'evt-1',
    timestamp: new Date().toISOString(),
    connector: 'c-f5cc476e',
    status: 'matched',
    matches: [{ trigger: 't1', agent: 'review-bot' }],
  },
  {
    id: 'evt-2',
    timestamp: new Date().toISOString(),
    connector: 'c-deleted',
    status: 'unmatched',
  },
]

function mockFetch(connectors: unknown[]) {
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string) => {
      if (url.includes('/connectors')) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              items: connectors,
              total: connectors.length,
              page: 1,
              pageSize: 200,
              totalPages: 1,
            }),
            { status: 200 },
          ),
        )
      }
      if (url.includes('/events')) {
        return Promise.resolve(
          new Response(JSON.stringify({ events: sampleEvents, total: sampleEvents.length }), {
            status: 200,
          }),
        )
      }
      return Promise.resolve(new Response('{}', { status: 200 }))
    }),
  )
}

describe('RecentActivityStream', () => {
  beforeEach(() => mockFetch([]))
  afterEach(() => vi.unstubAllGlobals())

  // #152 — the stream labels connectors by name, not by raw id.
  it('shows the connector name when it resolves', async () => {
    mockFetch([
      { id: 'c-f5cc476e', name: 'forgejo-main', signatureHeader: 'x-sig', disabled: false },
    ])
    renderWithProviders(<RecentActivityStream />)
    await waitFor(() => expect(screen.getByText('forgejo-main')).toBeInTheDocument())
    expect(screen.queryByText('c-f5cc476e')).toBeNull()
  })

  it('falls back to the connector id with a title for an unknown connector', async () => {
    renderWithProviders(<RecentActivityStream />)
    // no blank cell: the unknown connector still renders its id, as a
    // secondary fallback carrying the full id in `title`
    const el = await waitFor(() => screen.getByText('c-deleted'))
    expect(el).toHaveAttribute('title', 'c-deleted')
    // the resolvable connector is unaffected by the unknown one
    expect(screen.getByText('c-f5cc476e')).toBeInTheDocument()
  })
})
