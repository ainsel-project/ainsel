import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { TokensDetail } from './TokensDetail'
import { renderWithProviders } from '../../../test/renderWithProviders'

const TOKEN_ROWS = [
  {
    agent: 'doc-writer',
    repo: 'ainsel-project/ainsel',
    eventType: 'pull_request.opened',
    model: 'kimi-k2.6',
    inputTokens: 500_000,
    outputTokens: 50_000,
    totalTokens: 550_000,
  },
  {
    agent: 'triage-bot',
    repo: 'ainsel-project/ainsel',
    model: 'kimi-k2.6',
    inputTokens: 200_000,
    outputTokens: 40_000,
    totalTokens: 240_000,
  },
]

function defaultFetch(url: string): Response {
  if (url.includes('/tokens/summary')) {
    return new Response(
      JSON.stringify({ totalTokens: 1_000_000, inputTokens: 700_000, outputTokens: 300_000 }),
      { status: 200 },
    )
  }
  if (url.includes('/tokens/by-subject')) {
    return new Response(
      JSON.stringify({ range: '24h', rows: TOKEN_ROWS }),
      { status: 200 },
    )
  }
  return new Response('{}', { status: 200 })
}

describe('TokensDetail', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn((url: string) => Promise.resolve(defaultFetch(url))))
  })
  afterEach(() => { vi.unstubAllGlobals() })

  it('renders the page heading', () => {
    renderWithProviders(<TokensDetail />, { route: '/observability/tokens' })
    expect(screen.getByRole('heading', { name: /token\s+detail/i })).toBeInTheDocument()
  })

  it('renders the breadcrumb link to Observability', () => {
    renderWithProviders(<TokensDetail />, { route: '/observability/tokens' })
    expect(screen.getByRole('link', { name: /observability/i })).toBeInTheDocument()
  })

  it('renders KPI cards for total, input and output', async () => {
    renderWithProviders(<TokensDetail />, { route: '/observability/tokens' })
    await waitFor(() => expect(screen.getByText('1M')).toBeInTheDocument())
    expect(screen.getByText('Total Tokens (all time)')).toBeInTheDocument()
  })

  it('renders token rows sorted by total descending', async () => {
    renderWithProviders(<TokensDetail />, { route: '/observability/tokens' })
    await waitFor(() => expect(screen.getByText('doc-writer')).toBeInTheDocument())
    expect(screen.getByText('triage-bot')).toBeInTheDocument()
    const cells = screen.getAllByText(/doc-writer|triage-bot/)
    expect(cells[0].textContent).toBe('doc-writer')
  })

  it('renders I/O ratio column', async () => {
    renderWithProviders(<TokensDetail />, { route: '/observability/tokens' })
    await waitFor(() => expect(screen.getByText('doc-writer')).toBeInTheDocument())
    expect(screen.getByText('10.0×')).toBeInTheDocument()
  })

  it('renders cache and cost columns so the visible columns sum to the total', async () => {
    // Total includes cache traffic; without the cache columns the displayed
    // columns do not add up for a cache-aware provider (issue #281).
    vi.stubGlobal('fetch', vi.fn((url: string) => {
      if (url.includes('/tokens/by-subject')) {
        return Promise.resolve(new Response(JSON.stringify({
          range: '24h',
          rows: [{
            agent: 'cached-bot', repo: 'r', model: 'm',
            inputTokens: 1000, outputTokens: 500,
            cacheReadTokens: 8000, cacheWriteTokens: 100,
            totalTokens: 9600, costUsd: 1.25,
          }],
        }), { status: 200 }))
      }
      if (url.includes('/tokens/summary')) {
        return Promise.resolve(new Response(JSON.stringify({
          totalTokens: 9600, inputTokens: 1000, outputTokens: 500, costUsd: 1.25,
        }), { status: 200 }))
      }
      return Promise.resolve(new Response('{}', { status: 200 }))
    }))
    renderWithProviders(<TokensDetail />, { route: '/observability/tokens' })
    await waitFor(() => expect(screen.getByText('cached-bot')).toBeInTheDocument())
    expect(screen.getByText('8K')).toBeInTheDocument()  // cache read
    expect(screen.getByText('100')).toBeInTheDocument()  // cache write
    // $1.25 appears both as the row cost and the Cost KPI.
    expect(screen.getAllByText('$1.25').length).toBeGreaterThanOrEqual(2)
  })

  it('labels the table with the backend that answered', async () => {
    // Records-first since #411: the default answer comes from the hub's own
    // conversation rows, and the panel says so.
    vi.stubGlobal('fetch', vi.fn((url: string) => {
      if (url.includes('/tokens/by-subject')) {
        return Promise.resolve(new Response(JSON.stringify({
          range: '24h', source: 'postgres', rows: TOKEN_ROWS,
        }), { status: 200 }))
      }
      return Promise.resolve(defaultFetch(url))
    }))
    renderWithProviders(<TokensDetail />, { route: '/observability/tokens' })
    await waitFor(() => expect(screen.getByText('doc-writer')).toBeInTheDocument())
    expect(screen.getByText('hub records')).toBeInTheDocument()
  })

  it('renders pre-migration rows with an empty subject as a dash', async () => {
    // Rows written before migration 0024 have no repo/event_type. They are
    // still consumption and must render — as "—", not a blank cell (#411).
    vi.stubGlobal('fetch', vi.fn((url: string) => {
      if (url.includes('/tokens/by-subject')) {
        return Promise.resolve(new Response(JSON.stringify({
          range: '24h', source: 'postgres',
          rows: [{
            agent: 'legacy-bot', repo: '', eventType: '', model: 'kimi',
            inputTokens: 40, outputTokens: 8, totalTokens: 48,
          }],
        }), { status: 200 }))
      }
      return Promise.resolve(defaultFetch(url))
    }))
    renderWithProviders(<TokensDetail />, { route: '/observability/tokens' })
    await waitFor(() => expect(screen.getByText('legacy-bot')).toBeInTheDocument())
    expect(screen.getByText('—')).toBeInTheDocument()
  })

  it('shows the hub reason on a 503 without naming Prometheus as required', async () => {
    // The panel is records-backed now, so the unavailable copy names the
    // general condition, not Prometheus specifically (#411).
    vi.stubGlobal('fetch', vi.fn((url: string) => {
      if (url.includes('/tokens/by-subject')) {
        return Promise.resolve(new Response(
          JSON.stringify({ error: 'no metrics source configured: set observability.prometheus.url or the hub database' }),
          { status: 503, headers: { 'Content-Type': 'application/json' } },
        ))
      }
      return Promise.resolve(defaultFetch(url))
    }))
    renderWithProviders(<TokensDetail />, { route: '/observability/tokens' })
    await waitFor(() =>
      expect(screen.getByText('No metrics source configured')).toBeInTheDocument(),
    )
    expect(screen.queryByText(/Token metrics need Prometheus/)).not.toBeInTheDocument()
    expect(
      screen.getByText(/no metrics source configured: set observability\.prometheus\.url/),
    ).toBeInTheDocument()
  })

  it('renders view events links', async () => {
    renderWithProviders(<TokensDetail />, { route: '/observability/tokens' })
    await waitFor(() => {
      const links = screen.getAllByText(/view events →/i)
      expect(links.length).toBeGreaterThan(0)
    })
  })

  it('view events link includes agent and range in URL', async () => {
    renderWithProviders(<TokensDetail />, { route: '/observability/tokens' })
    await waitFor(() => {
      const link = screen.getAllByText(/view events →/i)[0]
      const href = link.closest('a')?.getAttribute('href') ?? ''
      expect(href).toContain('/observability/events')
      expect(href).toContain('agent=doc-writer')
    })
  })
})
