import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useLocation } from 'react-router-dom'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Activity } from './Activity'
import { renderWithProviders } from '../../test/renderWithProviders'

const sampleConnectors = [
  { id: 'c-111', name: 'Monorepo Connector' },
  { id: 'c-222', name: 'Infra Tooling' },
  { id: 'c-333', name: 'Docs Public' },
]

const sampleAgents = [
  { id: 'doc-writer', name: 'Doc Writer' },
  { id: 'infra-bot', name: 'Infra Bot' },
  { id: 'sec-bot', name: 'Sec Bot' },
]

const sampleTriggers = [
  { id: 't1', name: 'On doc issue' },
  { id: 't2', name: 'On infra issue' },
  { id: 't3', name: 'On sec issue' },
]

const now = Date.now()

// sampleEvents are intentionally out of order so we can verify sort behaviour
const sampleEvents = [
  {
    id: 'e1',
    timestamp: new Date(now - 30_000).toISOString(), // newest
    connector: 'c-111',
    match: 'matched',
    matches: [{ trigger: 't1', agent: 'doc-writer' }],
    payload: { action: 'opened', pull_request: { number: 123 } },
  },
  {
    id: 'e2',
    timestamp: new Date(now - 240_000).toISOString(), // oldest
    connector: 'c-222',
    match: 'error',
    payload: { action: 'push', ref: 'refs/heads/main' },
  },
  {
    id: 'e3',
    timestamp: new Date(now - 120_000).toISOString(), // middle
    connector: 'c-333',
    match: 'unmatched',
    matches: [
      { trigger: 't2', agent: 'infra-bot' },
      { trigger: 't3', agent: 'sec-bot' },
    ],
  },
]

// mockFetch simulates the hub /events API: it applies the match, run state,
// connector and agent filters server-side, sorts newest-first, and paginates
// via limit/offset. total reflects all filtered events, not just the page.
function mockFetch(events: Record<string, unknown>[] = sampleEvents) {
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string) => {
      if (url.includes('/events')) {
        const q = new URL(url, 'http://localhost').searchParams
        const match = q.get('match') ?? ''
        const connector = q.get('connector') ?? ''
        const agent = q.get('agent') ?? ''
        const runState = q.get('runState') ?? ''
        const since = q.get('since') ?? ''
        const limit = Number(q.get('limit') ?? '100')
        const offset = Number(q.get('offset') ?? '0')

        const filtered = events
          .filter((e) => {
            if (match && e.match !== match) return false
            if (connector && e.connector !== connector) return false
            if (since && new Date(e.timestamp as string).getTime() < Date.parse(since)) return false
            if (agent) {
              const matches = (e.matches ?? []) as { agent: string }[]
              if (!matches.some((m) => m.agent === agent)) return false
            }
            if (runState) {
              const matches = (e.matches ?? []) as { invocationStatus?: string }[]
              if (!matches.some((m) => m.invocationStatus === runState)) return false
            }
            return true
          })
          .sort(
            (a, b) =>
              new Date(b.timestamp as string).getTime() - new Date(a.timestamp as string).getTime(),
          )
        const page = filtered.slice(offset, offset + limit)

        return Promise.resolve(
          new Response(JSON.stringify({ events: page, total: filtered.length }), { status: 200 }),
        )
      }
      if (url.includes('/connectors')) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              items: sampleConnectors,
              total: sampleConnectors.length,
            }),
            { status: 200 },
          ),
        )
      }
      if (url.includes('/triggers')) {
        return Promise.resolve(
          new Response(JSON.stringify({ items: sampleTriggers, total: sampleTriggers.length }), {
            status: 200,
          }),
        )
      }
      if (url.includes('/agents')) {
        return Promise.resolve(
          new Response(JSON.stringify({ items: sampleAgents, total: sampleAgents.length }), {
            status: 200,
          }),
        )
      }
      return Promise.resolve(new Response('{}', { status: 200 }))
    }),
  )
}

describe('Activity', () => {
  beforeEach(() => mockFetch())
  afterEach(() => vi.unstubAllGlobals())

  // SearchSpy records the current location.search so tests can assert the URL
  // without a full Routes setup (useLocation works anywhere under MemoryRouter).
  function renderActivity(route: string) {
    const search = { current: '' }
    function SearchSpy() {
      search.current = useLocation().search
      return null
    }
    const utils = renderWithProviders(
      <>
        <Activity />
        <SearchSpy />
      </>,
      { route },
    )
    return { search, ...utils }
  }

  const lastEventsUrl = () => {
    const calls = (fetch as ReturnType<typeof vi.fn>).mock.calls as [string][]
    const last = calls.filter(([u]) => u.includes('/events')).at(-1)
    return new URL(last![0], 'http://localhost').searchParams
  }

  describe('URL-persisted filters', () => {
    it('restores dropdown filters from the URL and sends them to the events API', async () => {
      const { container } = renderActivity(
        '/activity?match=unmatched&connector=c-333&agent=infra-bot',
      )
      await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())

      expect(screen.getByLabelText(/filter by match/i)).toHaveValue('unmatched')
      expect(screen.getByLabelText(/filter by connector/i)).toHaveValue('c-333')
      expect(screen.getByLabelText(/filter by agent/i)).toHaveValue('infra-bot')

      const q = lastEventsUrl()
      expect(q.get('match')).toBe('unmatched')
      expect(q.get('connector')).toBe('c-333')
      expect(q.get('agent')).toBe('infra-bot')

      // Only e3 matches all three filters.
      const rows = container.querySelectorAll('tbody tr.activity-row')
      expect(rows.length).toBe(1)
      expect(rows[0].textContent).toContain('Docs Public')
    })

    it('restores the state filter from the URL', async () => {
      renderActivity('/activity?runState=failure')
      await waitFor(() => expect(screen.getByLabelText(/filter by state/i)).toHaveValue('failure'))
      expect(lastEventsUrl().get('runState')).toBe('failure')
    })

    // `status`/`outcome` were the old names of these two axes (#328). Old links
    // keep working, and the URL settles on the current spelling so a later
    // clear cannot re-activate the retired one.
    it('reads retired status/outcome params, filters with them, and rewrites the URL', async () => {
      const { search } = renderActivity('/activity?status=matched&outcome=failure')
      await waitFor(() => expect(screen.getByLabelText(/filter by match/i)).toHaveValue('matched'))
      expect(screen.getByLabelText(/filter by state/i)).toHaveValue('failure')

      // The request already carries the current names — no silently widened filter.
      const q = lastEventsUrl()
      expect(q.get('match')).toBe('matched')
      expect(q.get('runState')).toBe('failure')
      expect(q.get('status')).toBeNull()
      expect(q.get('outcome')).toBeNull()

      await waitFor(() => {
        expect(search.current).toBe('?match=matched&runState=failure')
      })
    })

    it('prefers the current param name when both spellings are in the URL', async () => {
      const { search } = renderActivity('/activity?match=unmatched&status=matched')
      await waitFor(() =>
        expect(screen.getByLabelText(/filter by match/i)).toHaveValue('unmatched'),
      )
      expect(lastEventsUrl().get('match')).toBe('unmatched')
      await waitFor(() => expect(search.current).toBe('?match=unmatched'))
    })

    it('does not resurrect a retired param when a folded filter is cleared', async () => {
      const { search } = renderActivity('/activity?status=error')
      await waitFor(() => expect(screen.getByLabelText(/filter by match/i)).toHaveValue('error'))

      await userEvent.selectOptions(screen.getByLabelText(/filter by match/i), '')
      await waitFor(() => {
        expect(search.current).not.toContain('status')
        expect(search.current).not.toContain('match')
      })
    })

    it('resolves since=today to local midnight for the events API', async () => {
      const { container } = renderActivity('/activity?since=today')
      await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())
      const d = new Date(lastEventsUrl().get('since')!)
      expect([d.getHours(), d.getMinutes(), d.getSeconds()]).toEqual([0, 0, 0])
      expect(d.toDateString()).toBe(new Date().toDateString())
    })

    it('bounds the list to today when since=today', async () => {
      // e2 is a week old; the other two samples are from the last minute.
      const aged = sampleEvents.map((e) =>
        e.id === 'e2'
          ? { ...e, timestamp: new Date(Date.now() - 7 * 86_400_000).toISOString() }
          : e,
      )
      mockFetch(aged)
      const { container } = renderActivity('/activity?since=today')
      await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())
      expect(container.querySelectorAll('tbody tr.activity-row').length).toBe(2)
      expect(screen.getByText('Activity · 2 total')).toBeInTheDocument()
    })

    it('ignores an unknown since value in the URL', async () => {
      const { container } = renderActivity('/activity?since=yesterday')
      await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())
      expect(lastEventsUrl().get('since')).toBeNull()
      expect(container.querySelectorAll('tbody tr.activity-row').length).toBe(3)
    })

    it('reports a filtered-empty list as a filter result when only since is active', async () => {
      // All samples are older than today: the bound empties the list.
      const aged = sampleEvents.map((e) => ({
        ...e,
        timestamp: new Date(Date.now() - 7 * 86_400_000).toISOString(),
      }))
      mockFetch(aged)
      renderActivity('/activity?since=today')
      await waitFor(() =>
        expect(screen.getByText('No events match the filter.')).toBeInTheDocument(),
      )
    })

    it('keeps unrelated URL params when filters change', async () => {
      const { search } = renderActivity('/activity?q=pull_request&page=2&pageSize=50')
      await waitFor(() => expect(screen.getByLabelText(/filter by match/i)).toBeInTheDocument())
      await userEvent.selectOptions(screen.getByLabelText(/filter by match/i), 'error')
      await waitFor(() => expect(search.current).toContain('match=error'))
      expect(search.current).toContain('q=pull_request')
      expect(search.current).toContain('pageSize=50')
    })

    it('resets page to 1 when a filter changes', async () => {
      const { search } = renderActivity('/activity?page=3')
      await waitFor(() => expect(screen.getByLabelText(/filter by match/i)).toBeInTheDocument())
      await userEvent.selectOptions(screen.getByLabelText(/filter by match/i), 'matched')
      await waitFor(() => expect(search.current).toContain('match=matched'))
      expect(search.current).toContain('page=1')
    })

    it('removes the filter param from the URL when cleared', async () => {
      const { container, search } = renderActivity('/activity?match=error')
      await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())
      expect(screen.getByLabelText(/filter by match/i)).toHaveValue('error')

      await userEvent.selectOptions(screen.getByLabelText(/filter by match/i), '')
      await waitFor(() => {
        expect(container.querySelectorAll('tbody tr.activity-row').length).toBe(3)
      })
      expect(search.current).not.toContain('match')
    })

    it('falls back to "any" for invalid enum values in the URL', async () => {
      const { container } = renderActivity('/activity?match=bogus&runState=nope')
      await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())

      expect(screen.getByLabelText(/filter by match/i)).toHaveValue('')
      expect(screen.getByLabelText(/filter by state/i)).toHaveValue('')

      const q = lastEventsUrl()
      expect(q.get('match')).toBeNull()
      expect(q.get('runState')).toBeNull()
      // All events load since no filter is applied.
      expect(container.querySelectorAll('tbody tr.activity-row').length).toBe(3)
    })
  })

  it('renders all three events with status tags', async () => {
    const { container } = renderWithProviders(<Activity />, { route: '/activity' })
    await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())
    const body = container.querySelector('tbody') as HTMLElement
    expect(body.textContent).toContain('Monorepo Connector')
    expect(body.textContent).toContain('Infra Tooling')
    expect(body.textContent).toContain('Docs Public')
    expect(screen.getByText('MATCH')).toBeInTheDocument()
    expect(screen.getByText('ERR')).toBeInTheDocument()
    expect(screen.getByText('SKIP')).toBeInTheDocument()
  })

  it('shows newest event first', async () => {
    const { container } = renderWithProviders(<Activity />, { route: '/activity' })
    await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())
    const rows = container.querySelectorAll('tbody tr.activity-row')
    // e1 is newest (30s ago), e3 is middle (120s ago), e2 is oldest (240s ago)
    expect(rows[0].textContent).toContain('Monorepo Connector')
    expect(rows[1].textContent).toContain('Docs Public')
    expect(rows[2].textContent).toContain('Infra Tooling')
  })

  it('expands a row to show details when the chevron is clicked', async () => {
    const { container } = renderWithProviders(<Activity />, { route: '/activity' })
    await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())
    const chevron = screen.getAllByRole('button', { name: /expand event details/i })[0]
    await userEvent.click(chevron)
    const details = container.querySelector('.activity-details')
    expect(details).not.toBeNull()
    const matchRow = container.querySelector('.activity-matches .match')!
    // The default mock provides the trigger/agent registries, so ids resolve
    // to display names.
    expect(matchRow.textContent).toMatch(/Doc Writer/)
  })

  it('filters by match via the events API', async () => {
    const { container } = renderWithProviders(<Activity />, { route: '/activity' })
    await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())
    await userEvent.selectOptions(screen.getByLabelText(/filter by match/i), 'error')
    await waitFor(() => {
      const body = container.querySelector('tbody') as HTMLElement
      expect(body.textContent).toContain('Infra Tooling')
      expect(body.textContent).not.toContain('Monorepo Connector')
    })
  })

  it('renders resolved connector names in table and filter dropdown', async () => {
    const { container } = renderWithProviders(<Activity />, { route: '/activity' })
    await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())
    const body = container.querySelector('tbody') as HTMLElement
    expect(body.textContent).toContain('Monorepo Connector')
    expect(body.textContent).toContain('Infra Tooling')
    expect(body.textContent).toContain('Docs Public')

    const connectorSelect = screen.getByLabelText(/filter by connector/i)
    expect(connectorSelect).toBeInTheDocument()
    const options = Array.from(connectorSelect.querySelectorAll('option'))
    const labels = options.map((o) => o.textContent)
    expect(labels).toContain('Monorepo Connector')
    expect(labels).toContain('Infra Tooling')
    expect(labels).toContain('Docs Public')
    expect(labels).not.toContain('c-111')
  })

  it('filters by connector id while showing names in UI', async () => {
    const { container } = renderWithProviders(<Activity />, { route: '/activity' })
    await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())
    await userEvent.selectOptions(screen.getByLabelText(/filter by connector/i), 'c-111')
    await waitFor(() => {
      const body = container.querySelector('tbody') as HTMLElement
      expect(body.textContent).toContain('Monorepo Connector')
      expect(body.textContent).not.toContain('Infra Tooling')
    })
  })

  it('renders trigger and agent columns with comma-separated matches', async () => {
    const { container } = renderWithProviders(<Activity />, { route: '/activity' })
    await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())
    const rows = container.querySelectorAll('tbody tr.activity-row')
    // e1: single match, resolved to registry names
    expect(rows[0].textContent).toContain('On doc issue')
    expect(rows[0].textContent).toContain('Doc Writer')
    // e3: multiple matches, comma-separated
    expect(rows[1].textContent).toContain('On infra issue, On sec issue')
    expect(rows[1].textContent).toContain('Infra Bot, Sec Bot')
    // e2: no matches → dash
    expect(rows[2].textContent).toContain('—')
  })

  it('resolves trigger and agent names to links in the activity table', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => {
        if (url.includes('/events')) {
          return Promise.resolve(
            new Response(JSON.stringify({ events: sampleEvents, total: sampleEvents.length }), {
              status: 200,
            }),
          )
        }
        if (url.includes('/connectors')) {
          return Promise.resolve(
            new Response(
              JSON.stringify({ items: sampleConnectors, total: sampleConnectors.length }),
              { status: 200 },
            ),
          )
        }
        if (url.includes('/triggers')) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                items: [
                  { id: 't1', name: 'On doc issue' },
                  { id: 't2', name: 'On infra issue' },
                  { id: 't3', name: 'On sec issue' },
                ],
                total: 3,
              }),
              { status: 200 },
            ),
          )
        }
        if (url.includes('/agents')) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                items: [
                  { id: 'doc-writer', name: 'Doc Writer' },
                  { id: 'infra-bot', name: 'Infra Bot' },
                  { id: 'sec-bot', name: 'Sec Bot' },
                ],
                total: 3,
              }),
              { status: 200 },
            ),
          )
        }
        return Promise.resolve(new Response('{}', { status: 200 }))
      }),
    )

    const { container } = renderWithProviders(<Activity />, { route: '/activity' })
    await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())

    // e1 (newest): single match resolves to names
    const triggerLink = await screen.findByRole('link', { name: 'Open subscription On doc issue' })
    expect(triggerLink).toHaveAttribute('href', '/agents/doc-writer/triggers')
    const agentLink = screen.getByRole('link', { name: 'Open agent Doc Writer' })
    expect(agentLink).toHaveAttribute('href', '/agents/doc-writer')

    // e3: multiple matches resolve to names
    expect(screen.getByRole('link', { name: 'Open agent Infra Bot' })).toHaveAttribute(
      'href',
      '/agents/infra-bot',
    )
    expect(screen.getByRole('link', { name: 'Open agent Sec Bot' })).toHaveAttribute(
      'href',
      '/agents/sec-bot',
    )
  })

  it('paginates and shows the pager when there are more rows than the page size', async () => {
    const manyEvents = Array.from({ length: 30 }, (_, i) => ({
      id: `e${i}`,
      timestamp: new Date(now - i * 10_000).toISOString(),
      connector: 'c-111',
      match: 'matched' as const,
    }))
    mockFetch(manyEvents)

    const { container } = renderWithProviders(<Activity />, {
      route: '/activity?pageSize=25',
    })
    await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())

    const rows = container.querySelectorAll('tbody tr.activity-row')
    expect(rows.length).toBe(25)

    expect(screen.getByLabelText('Next page')).toBeInTheDocument()
    expect(screen.getByLabelText('Previous page')).toBeDisabled()
    expect(screen.getByLabelText('Next page')).not.toBeDisabled()
  })

  it('fetches the next page via offset when navigating', async () => {
    const manyEvents = Array.from({ length: 30 }, (_, i) => ({
      id: `e${i}`,
      timestamp: new Date(now - i * 10_000).toISOString(),
      connector: 'c-111',
      match: 'matched' as const,
    }))
    mockFetch(manyEvents)

    const { container } = renderWithProviders(<Activity />, {
      route: '/activity?pageSize=25',
    })
    await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())
    expect(container.querySelectorAll('tbody tr.activity-row').length).toBe(25)

    await userEvent.click(screen.getByLabelText('Next page'))
    await waitFor(() => {
      // Page 2 holds the remaining 5 events (total 30).
      expect(container.querySelectorAll('tbody tr.activity-row').length).toBe(5)
    })
    expect(screen.getByLabelText('Previous page')).not.toBeDisabled()
    expect(screen.getByLabelText('Next page')).toBeDisabled()
  })

  it('filters events by search query matching payload content', async () => {
    const { container } = renderWithProviders(<Activity />, { route: '/activity' })
    await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())

    const searchInput = screen.getByLabelText(/search events/i)
    await userEvent.type(searchInput, 'pull_request')

    await waitFor(() => {
      const body = container.querySelector('tbody') as HTMLElement
      expect(body.textContent).toContain('Monorepo Connector')
      expect(body.textContent).not.toContain('Infra Tooling')
      expect(body.textContent).not.toContain('Docs Public')
    })
  })

  it('narrows results with multiple AND terms', async () => {
    const { container } = renderWithProviders(<Activity />, { route: '/activity' })
    await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())

    const searchInput = screen.getByLabelText(/search events/i)
    await userEvent.type(searchInput, 'pull_request 123')

    await waitFor(() => {
      const body = container.querySelector('tbody') as HTMLElement
      expect(body.textContent).toContain('Monorepo Connector')
      expect(body.textContent).not.toContain('Infra Tooling')
    })

    // A term that matches nothing should show empty state
    await userEvent.clear(searchInput)
    await userEvent.type(searchInput, 'pull_request 999')

    await waitFor(() => {
      expect(container.querySelector('tbody tr')).toBeNull()
      expect(screen.getByText('No events match the filter.')).toBeInTheDocument()
    })
  })

  it('search is case-insensitive', async () => {
    const { container } = renderWithProviders(<Activity />, { route: '/activity' })
    await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())

    const searchInput = screen.getByLabelText(/search events/i)
    await userEvent.type(searchInput, 'PULL_REQUEST')

    await waitFor(() => {
      const body = container.querySelector('tbody') as HTMLElement
      expect(body.textContent).toContain('Monorepo Connector')
      expect(body.textContent).not.toContain('Infra Tooling')
    })
  })

  it('empty or whitespace-only query shows all rows', async () => {
    const { container } = renderWithProviders(<Activity />, { route: '/activity' })
    await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())

    const searchInput = screen.getByLabelText(/search events/i)
    await userEvent.type(searchInput, '   ')

    const rows = container.querySelectorAll('tbody tr.activity-row')
    expect(rows.length).toBe(3)
  })

  it('search matches connector names and the match result', async () => {
    const { container } = renderWithProviders(<Activity />, { route: '/activity' })
    await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())

    const searchInput = screen.getByLabelText(/search events/i)
    await userEvent.type(searchInput, 'infra tooling')

    await waitFor(() => {
      const body = container.querySelector('tbody') as HTMLElement
      expect(body.textContent).toContain('Infra Tooling')
      expect(body.textContent).not.toContain('Monorepo Connector')
    })
  })

  it('search combines with the match filter', async () => {
    const { container } = renderWithProviders(<Activity />, { route: '/activity' })
    await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())

    // Filter by match=matched first
    await userEvent.selectOptions(screen.getByLabelText(/filter by match/i), 'matched')
    // Then search for something in the error event's payload
    const searchInput = screen.getByLabelText(/search events/i)
    await userEvent.type(searchInput, 'push')

    await waitFor(() => {
      expect(container.querySelector('tbody tr')).toBeNull()
      expect(screen.getByText('No events match the filter.')).toBeInTheDocument()
    })
  })

  it('persists search query in the URL q param', async () => {
    const { container } = renderWithProviders(<Activity />, { route: '/activity?q=pull_request' })
    await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())

    const body = container.querySelector('tbody') as HTMLElement
    expect(body.textContent).toContain('Monorepo Connector')
    expect(body.textContent).not.toContain('Infra Tooling')

    const searchInput = screen.getByLabelText(/search events/i) as HTMLInputElement
    expect(searchInput.value).toBe('pull_request')
  })

  it('renders agent filter dropdown with options from the agent registry', async () => {
    const { container } = renderWithProviders(<Activity />, { route: '/activity' })
    await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())

    const agentSelect = screen.getByLabelText(/filter by agent/i)
    expect(agentSelect).toBeInTheDocument()
    const options = Array.from(agentSelect.querySelectorAll('option'))
    const values = options.map((o) => o.value).filter(Boolean)
    // Should contain agent IDs from the agent registry
    expect(values).toContain('doc-writer')
    expect(values).toContain('infra-bot')
    expect(values).toContain('sec-bot')
  })

  it('filters by agent to show only events with that agent', async () => {
    const { container } = renderWithProviders(<Activity />, { route: '/activity' })
    await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())

    // Filter by doc-writer — only e1 has this agent
    await userEvent.selectOptions(screen.getByLabelText(/filter by agent/i), 'doc-writer')
    await waitFor(() => {
      const body = container.querySelector('tbody') as HTMLElement
      expect(body.textContent).toContain('Monorepo Connector')
      expect(body.textContent).not.toContain('Infra Tooling')
      expect(body.textContent).not.toContain('Docs Public')
    })
  })

  it('agent filter shows events with multiple matches when any match has the agent', async () => {
    const { container } = renderWithProviders(<Activity />, { route: '/activity' })
    await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())

    // Filter by infra-bot — only e3 has this agent (in a multi-match)
    await userEvent.selectOptions(screen.getByLabelText(/filter by agent/i), 'infra-bot')
    await waitFor(() => {
      const body = container.querySelector('tbody') as HTMLElement
      expect(body.textContent).toContain('Docs Public')
      expect(body.textContent).not.toContain('Monorepo Connector')
      expect(body.textContent).not.toContain('Infra Tooling')
    })
  })

  it('agent filter excludes unmatched events', async () => {
    const { container } = renderWithProviders(<Activity />, { route: '/activity' })
    await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())

    // Filter by sec-bot — e3 matches, e2 (unmatched/error) should be excluded
    await userEvent.selectOptions(screen.getByLabelText(/filter by agent/i), 'sec-bot')
    await waitFor(() => {
      const rows = container.querySelectorAll('tbody tr.activity-row')
      expect(rows.length).toBe(1)
      expect(rows[0].textContent).toContain('Docs Public')
    })
  })

  it('selecting Any agent resets the filter', async () => {
    const { container } = renderWithProviders(<Activity />, { route: '/activity' })
    await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())

    // Apply agent filter
    await userEvent.selectOptions(screen.getByLabelText(/filter by agent/i), 'doc-writer')
    await waitFor(() => {
      const rows = container.querySelectorAll('tbody tr.activity-row')
      expect(rows.length).toBe(1)
    })

    // Reset to "Any agent"
    await userEvent.selectOptions(screen.getByLabelText(/filter by agent/i), '')
    await waitFor(() => {
      const rows = container.querySelectorAll('tbody tr.activity-row')
      expect(rows.length).toBe(3)
    })
  })

  it('agent filter combines with the match filter using AND logic', async () => {
    const { container } = renderWithProviders(<Activity />, { route: '/activity' })
    await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())

    // Filter by agent=doc-writer AND match=error → no results (e1 is matched, not error)
    await userEvent.selectOptions(screen.getByLabelText(/filter by agent/i), 'doc-writer')
    await userEvent.selectOptions(screen.getByLabelText(/filter by match/i), 'error')

    await waitFor(() => {
      expect(container.querySelector('tbody tr')).toBeNull()
      expect(screen.getByText('No events match the filter.')).toBeInTheDocument()
    })
  })

  describe('state filter', () => {
    // o1: single successful run; o2: single failed run; o3: mixed run states in
    // one event; o4: unmatched event without any runs.
    const stateEvents = [
      {
        id: 'o1',
        timestamp: new Date(now - 30_000).toISOString(),
        connector: 'c-111',
        match: 'matched',
        matches: [
          { trigger: 't1', agent: 'doc-writer', invocationStatus: 'success', durationMs: 1200 },
        ],
      },
      {
        id: 'o2',
        timestamp: new Date(now - 60_000).toISOString(),
        connector: 'c-222',
        match: 'error',
        matches: [
          { trigger: 't2', agent: 'infra-bot', invocationStatus: 'failure', error: 'boom' },
        ],
      },
      {
        id: 'o3',
        timestamp: new Date(now - 90_000).toISOString(),
        connector: 'c-333',
        match: 'matched',
        matches: [
          { trigger: 't3', agent: 'sec-bot', invocationStatus: 'success' },
          { trigger: 't4', agent: 'perf-bot', invocationStatus: 'failure', error: 'oom' },
        ],
      },
      {
        id: 'o4',
        timestamp: new Date(now - 120_000).toISOString(),
        connector: 'c-111',
        match: 'unmatched',
      },
    ]

    beforeEach(() => mockFetch(stateEvents))

    it('renders the state filter dropdown with all run states', async () => {
      renderWithProviders(<Activity />, { route: '/activity' })
      const stateSelect = await screen.findByLabelText(/filter by state/i)
      const options = Array.from(stateSelect.querySelectorAll('option'))
      expect(options.map((o) => o.textContent)).toContain('Any state')
      // queued and running are separate states (#422): no worker yet vs
      // being worked on.
      expect(
        options
          .map((o) => o.value)
          .filter(Boolean)
          .sort(),
      ).toEqual(['failure', 'queued', 'running', 'success', 'timeout'])
    })

    it('filters by success to show only events with a successful run', async () => {
      const { container } = renderWithProviders(<Activity />, { route: '/activity' })
      await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())
      await userEvent.selectOptions(screen.getByLabelText(/filter by state/i), 'success')
      await waitFor(() => {
        const rows = container.querySelectorAll('tbody tr.activity-row')
        expect(rows.length).toBe(2)
        expect(rows[0].textContent).toContain('Monorepo Connector') // o1
        expect(rows[1].textContent).toContain('Docs Public') // o3 (mixed)
      })
      const body = container.querySelector('tbody') as HTMLElement
      expect(body.textContent).not.toContain('Infra Tooling') // o2
    })

    it('filters by failure to show only events with a failed run', async () => {
      const { container } = renderWithProviders(<Activity />, { route: '/activity' })
      await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())
      await userEvent.selectOptions(screen.getByLabelText(/filter by state/i), 'failure')
      await waitFor(() => {
        const rows = container.querySelectorAll('tbody tr.activity-row')
        expect(rows.length).toBe(2)
        expect(rows[0].textContent).toContain('Infra Tooling') // o2
        expect(rows[1].textContent).toContain('Docs Public') // o3 (mixed)
      })
      const body = container.querySelector('tbody') as HTMLElement
      expect(body.textContent).not.toContain('Monorepo Connector') // o1
    })

    it('sends the state filter to the events API', async () => {
      renderWithProviders(<Activity />, { route: '/activity' })
      await waitFor(() => expect(screen.getByLabelText(/filter by state/i)).toBeInTheDocument())
      await userEvent.selectOptions(screen.getByLabelText(/filter by state/i), 'failure')
      await waitFor(() => {
        const calls = (fetch as ReturnType<typeof vi.fn>).mock.calls as [string][]
        const lastEventsCall = calls.filter(([u]) => u.includes('/events')).at(-1)
        expect(new URL(lastEventsCall![0], 'http://localhost').searchParams.get('runState')).toBe(
          'failure',
        )
      })
    })

    it('finds a failure on page 2 of the unfiltered history', async () => {
      // 30 successful runs fill the default 25-row first page; the single
      // failure is the OLDEST event and would never appear there. The
      // client-side filter this page used to run hid it entirely — the pager
      // paged over successes, so "failure" showed nothing. Server-side the
      // filter must walk the whole history.
      const history = [
        ...Array.from({ length: 30 }, (_, i) => ({
          id: `ok-${i}`,
          timestamp: new Date(now - i * 10_000).toISOString(),
          connector: 'c-111',
          match: 'matched' as const,
          matches: [{ trigger: 't1', agent: 'doc-writer', invocationStatus: 'success' }],
        })),
        {
          id: 'fail-deep-in-history',
          timestamp: new Date(now - 400_000).toISOString(),
          connector: 'c-111',
          match: 'error' as const,
          matches: [
            { trigger: 't2', agent: 'infra-bot', invocationStatus: 'failure', error: 'boom' },
          ],
        },
      ]
      mockFetch(history)

      const { container } = renderWithProviders(<Activity />, { route: '/activity' })
      await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())
      expect(screen.queryByText('No events match the filter.')).toBeNull()

      await userEvent.selectOptions(screen.getByLabelText(/filter by state/i), 'failure')
      await waitFor(() => {
        const rows = container.querySelectorAll('tbody tr.activity-row')
        expect(rows.length).toBe(1)
        // The failure is the only event routed to infra-bot in this history.
        expect(rows[0].textContent).toContain('Infra Bot')
      })
    })

    it('includes an event when any of its matches has the selected state', async () => {
      const { container } = renderWithProviders(<Activity />, { route: '/activity' })
      await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())
      // o3 has one success and one failure match, so it appears for both filters
      await userEvent.selectOptions(screen.getByLabelText(/filter by state/i), 'success')
      await waitFor(() => {
        const body = container.querySelector('tbody') as HTMLElement
        expect(body.textContent).toContain('Docs Public')
      })
      await userEvent.selectOptions(screen.getByLabelText(/filter by state/i), 'failure')
      await waitFor(() => {
        const body = container.querySelector('tbody') as HTMLElement
        expect(body.textContent).toContain('Docs Public')
      })
    })

    it('excludes events without runs (e.g. unmatched) when a state is selected', async () => {
      const { container } = renderWithProviders(<Activity />, { route: '/activity' })
      await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())
      await userEvent.selectOptions(screen.getByLabelText(/filter by state/i), 'success')
      await waitFor(() => {
        const rows = container.querySelectorAll('tbody tr.activity-row')
        for (const row of rows) expect(row.textContent).not.toContain('SKIP')
      })
    })

    it('selecting Any state resets the filter', async () => {
      const { container } = renderWithProviders(<Activity />, { route: '/activity' })
      await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())
      await userEvent.selectOptions(screen.getByLabelText(/filter by state/i), 'failure')
      await waitFor(() => {
        expect(container.querySelectorAll('tbody tr.activity-row').length).toBe(2)
      })
      await userEvent.selectOptions(screen.getByLabelText(/filter by state/i), '')
      await waitFor(() => {
        expect(container.querySelectorAll('tbody tr.activity-row').length).toBe(4)
      })
    })

    it('state filter combines with the match filter using AND logic', async () => {
      const { container } = renderWithProviders(<Activity />, { route: '/activity' })
      await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())
      // o2 is the only error-match event; it has a failure run but no success run
      await userEvent.selectOptions(screen.getByLabelText(/filter by state/i), 'success')
      await userEvent.selectOptions(screen.getByLabelText(/filter by match/i), 'error')
      await waitFor(() => {
        expect(container.querySelector('tbody tr')).toBeNull()
        expect(screen.getByText('No events match the filter.')).toBeInTheDocument()
      })
    })

    it('search matches run state terms', async () => {
      const { container } = renderWithProviders(<Activity />, { route: '/activity' })
      await waitFor(() => expect(container.querySelector('tbody tr')).not.toBeNull())
      const searchInput = screen.getByLabelText(/search events/i)
      await userEvent.type(searchInput, 'failure')
      await waitFor(() => {
        const rows = container.querySelectorAll('tbody tr.activity-row')
        expect(rows.length).toBe(2)
        expect(rows[0].textContent).toContain('Infra Tooling')
        expect(rows[1].textContent).toContain('Docs Public')
      })
    })
  })
})
