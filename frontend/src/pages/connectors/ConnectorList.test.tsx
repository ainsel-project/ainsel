import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ConnectorList } from './ConnectorList'
import { renderWithProviders } from '../../test/renderWithProviders'

const mockFetch = () => globalThis.fetch as ReturnType<typeof vi.fn>

describe('ConnectorList', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders rows', async () => {
    mockFetch().mockResolvedValue(
      new Response(
        JSON.stringify({
          items: [
            {
              id: 'c1',
              name: 'insel-monorepo',
              signatureHeader: 'X-Hub-Signature-256',
              webhookEndpoint: 'https://hub.example/wh/c1',
              disabled: false,
              status: { ready: true },
            },
          ],
          total: 1,
          page: 1,
          pageSize: 20,
          totalPages: 1,
        }),
        { status: 200 },
      ),
    )
    renderWithProviders(<ConnectorList />, { route: '/connectors' })
    await waitFor(() => expect(screen.getByText('insel-monorepo')).toBeInTheDocument())
  })

  it('shows the New Connector header action', async () => {
    renderWithProviders(<ConnectorList />, { route: '/connectors' })
    expect(await screen.findByRole('button', { name: /new connector/i })).toBeInTheDocument()
  })

  it('shows disabled state for a paused connector', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => {
        if (url.includes('/connectors')) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                items: [
                  {
                    id: 'c1',
                    name: 'paused-connector',
                    signatureHeader: 'X-Forgejo-Signature',
                    disabled: true,
                    status: { ready: false },
                  },
                ],
                total: 1,
                page: 1,
                pageSize: 20,
                totalPages: 1,
              }),
              { status: 200 },
            ),
          )
        }
        return Promise.resolve(new Response('{}', { status: 200 }))
      }),
    )
    renderWithProviders(<ConnectorList />, { route: '/connectors' })
    expect(await screen.findByText('Disabled')).toBeInTheDocument()
  })

  it('names what failed and lets the reader retry it', async () => {
    mockFetch().mockResolvedValue(
      new Response(JSON.stringify({ message: 'hub down' }), { status: 500 }),
    )
    renderWithProviders(<ConnectorList />, { route: '/connectors' })
    expect(await screen.findByText('Failed to load connectors.')).toBeInTheDocument()

    const callsBefore = mockFetch().mock.calls.length
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(mockFetch().mock.calls.length).toBeGreaterThan(callsBefore))
  })

  it('shows the empty state without a pager over an empty catalogue', async () => {
    mockFetch().mockResolvedValue(
      new Response(JSON.stringify({ items: [], total: 0, page: 1, pageSize: 20, totalPages: 0 }), {
        status: 200,
      }),
    )
    renderWithProviders(<ConnectorList />, { route: '/connectors' })
    expect(await screen.findByText('No connectors yet.')).toBeInTheDocument()
    expect(screen.queryByRole('combobox', { name: /rows per page/i })).not.toBeInTheDocument()
  })

  it('asks the hub for the page and page size the URL names', async () => {
    mockFetch().mockResolvedValue(
      new Response(JSON.stringify({ items: [], total: 0, page: 3, pageSize: 50, totalPages: 0 }), {
        status: 200,
      }),
    )
    renderWithProviders(<ConnectorList />, { route: '/connectors?page=3&pageSize=50' })
    await waitFor(() =>
      expect(
        mockFetch().mock.calls.some(
          ([url]) => String(url).includes('page=3') && String(url).includes('pageSize=50'),
        ),
      ).toBe(true),
    )
  })
})
