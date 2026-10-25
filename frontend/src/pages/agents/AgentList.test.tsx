import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AgentList } from './AgentList'
import { renderWithProviders } from '../../test/renderWithProviders'

describe('AgentList', () => {
  beforeEach(() => {
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => {
        if (url.includes('/agents')) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                items: [
                  {
                    id: 'a1',
                    name: 'doc-writer',
                    llm: { model: 'claude-opus-4-7', provider: 'ollama-cloud' },
                    imageRef: { name: 'claude-tooling-base:1.4' },
                    status: { ready: true, replicas: 3 },
                  },
                  {
                    id: 'a2',
                    name: 'triage-bot',
                    llm: { model: 'claude-sonnet-4-6', provider: 'alibaba-cloud' },
                    imageRef: { name: 'claude-tooling-base:1.4' },
                    status: { ready: false, replicas: 0 },
                  },
                ],
                total: 2,
                page: 1,
                pageSize: 20,
                totalPages: 1,
              }),
              { status: 200 },
            ),
          )
        }
        return Promise.resolve(new Response('[]', { status: 200 }))
      }),
    )
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders rows from the agents query', async () => {
    renderWithProviders(<AgentList />, { route: '/agents' })
    await waitFor(() => expect(screen.getByText('doc-writer')).toBeInTheDocument())
    expect(screen.getByText('triage-bot')).toBeInTheDocument()
    expect(screen.getByText('claude-opus-4-7')).toBeInTheDocument()
  })

  it('renders each agent’s provider label next to its model', async () => {
    renderWithProviders(<AgentList />, { route: '/agents' })
    expect(await screen.findByText('Ollama Cloud')).toBeInTheDocument()
    expect(screen.getByText('Alibaba Token Plan')).toBeInTheDocument()
  })

  it('falls back to None when a row has no provider', async () => {
    // Only a1 is returned this run, stripped of its provider.
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => {
        if (url.includes('/agents')) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                items: [
                  {
                    id: 'a1',
                    name: 'doc-writer',
                    llm: { model: 'claude-opus-4-7' },
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
        }
        return Promise.resolve(new Response('[]', { status: 200 }))
      }),
    )
    renderWithProviders(<AgentList />, { route: '/agents' })
    expect(await screen.findByText('None')).toBeInTheDocument()
  })

  it('shows the New Agent header action', async () => {
    renderWithProviders(<AgentList />, { route: '/agents' })
    expect(await screen.findByRole('button', { name: /new agent/i })).toBeInTheDocument()
  })

  it('shows pager info for the current page', async () => {
    renderWithProviders(<AgentList />, { route: '/agents' })
    await waitFor(() => expect(screen.getByText('doc-writer')).toBeInTheDocument())
    const info = screen.getByText(/of/).parentElement as HTMLElement
    expect(info.textContent).toMatch(/01/)
    expect(info.textContent).toMatch(/02/)
    expect(info.textContent).toMatch(/of\s+2/)
  })

  it('changing page size updates URL', async () => {
    renderWithProviders(<AgentList />, { route: '/agents' })
    await screen.findByText('doc-writer')
    const select = screen.getByRole('combobox', { name: /rows per page/i })
    await userEvent.selectOptions(select, '50')
    await waitFor(() =>
      expect(
        (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.some(
          ([url]) => String(url).includes('pageSize=50'),
        ),
      ).toBe(true),
    )
  })

  it('calls a drained queue-scaled agent asleep instead of pending', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string, init?: RequestInit) => {
        if ((init?.method ?? 'GET') === 'GET' && url.includes('/api/v1/agents')) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                items: [
                  {
                    id: 'a1',
                    name: 'doc-writer',
                    imageRef: { name: 'claude-tooling-base:1.4' },
                    persona: { id: 'p1' },
                    llm: { model: 'glm-5.1:cloud' },
                    replicas: 2,
                    status: {
                      ready: true,
                      desired: 0,
                      reason: 'JobPerTask',
                    },
                    updatedAt: '2026-06-08T00:00:00Z',
                  },
                  {
                    id: 'a2',
                    name: 'crash-bot',
                    imageRef: { name: 'claude-tooling-base:1.4' },
                    persona: { id: 'p2' },
                    llm: { model: 'glm-5.1:cloud' },
                    replicas: 1,
                    status: { ready: false, desired: 1 },
                    updatedAt: '2026-06-08T00:00:00Z',
                  },
                ],
                total: 2,
                page: 1,
                pageSize: 20,
                totalPages: 1,
              }),
              { status: 200 },
            ),
          )
        }
        return Promise.resolve(new Response('[]', { status: 200 }))
      }),
    )

    renderWithProviders(<AgentList />, { route: '/agents' })

    // Both agents run nothing or fail — an idle agent (nothing in flight) is
    // deliberately at rest, while one with a task in flight that never becomes
    // ready is broken. The fleet view must not blur them together.
    const row = await screen.findByText('doc-writer')
    expect(row).toBeInTheDocument()
    expect(await screen.findByText('Idle')).toBeInTheDocument()
    expect(screen.getByText('Pending')).toBeInTheDocument()
  })

  it('marks a paused agent as Paused, not Ready or broken', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => {
        if (url.includes('/agents')) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                items: [
                  {
                    id: 'a1',
                    name: 'doc-writer',
                    llm: { model: 'claude-opus-4-7', provider: 'ollama-cloud' },
                    imageRef: { name: 'claude-tooling-base:1.4' },
                    paused: true,
                    pausedUntil: new Date(Date.now() + 3600_000).toISOString(),
                    status: { ready: true, replicas: 1 },
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
        return Promise.resolve(new Response('[]', { status: 200 }))
      }),
    )

    renderWithProviders(<AgentList />, { route: '/agents' })

    expect(await screen.findByText('doc-writer')).toBeInTheDocument()
    expect(screen.getByText('Paused')).toBeInTheDocument()
    expect(screen.queryByText('Ready')).not.toBeInTheDocument()
  })

  // --- roster filters (#153) ---------------------------------------------

  const groupsResponse = [
    { id: 'g-platform', name: 'Platform', description: '', createdAt: '', updatedAt: '' },
    { id: 'g-data', name: 'Data', description: '', createdAt: '', updatedAt: '' },
  ]

  const stubAgentFetch = () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => {
        if (url.includes('/groups')) {
          return Promise.resolve(new Response(JSON.stringify(groupsResponse), { status: 200 }))
        }
        if (url.includes('/agents')) {
          const filtered = url.includes('search=triage')
          return Promise.resolve(
            new Response(
              JSON.stringify({
                items: filtered
                  ? [
                      {
                        id: 'a2',
                        name: 'triage-bot',
                        groupId: 'g-platform',
                        groupName: 'Platform',
                        llm: { model: 'claude-sonnet-4-6' },
                        status: { ready: true },
                      },
                    ]
                  : [
                      {
                        id: 'a1',
                        name: 'doc-writer',
                        groupId: 'g-platform',
                        groupName: 'Platform',
                        llm: { model: 'claude-opus-4-7' },
                        status: { ready: true },
                      },
                      {
                        id: 'a2',
                        name: 'triage-bot',
                        // Group name unavailable: the id is the fallback.
                        groupId: 'g-data',
                        llm: { model: 'claude-sonnet-4-6' },
                        status: { ready: true },
                      },
                    ],
                total: filtered ? 1 : 2,
                page: 1,
                pageSize: 20,
                totalPages: 1,
              }),
              { status: 200 },
            ),
          )
        }
        return Promise.resolve(new Response('[]', { status: 200 }))
      }),
    )
  }

  it('renders a Group column with the group name, falling back to the id', async () => {
    stubAgentFetch()
    renderWithProviders(<AgentList />, { route: '/agents' })
    await screen.findByText('doc-writer')
    // The row shows the group name, never the bare id, when a name exists;
    // a row whose group name is unavailable falls back to the id.
    expect(screen.queryByText('g-platform')).not.toBeInTheDocument()
    expect(screen.getAllByText('Platform').length).toBeGreaterThan(0)
    expect(screen.getByText('g-data')).toBeInTheDocument()
  })

  it('typing in the search field fetches with the search param and narrows the roster', async () => {
    stubAgentFetch()
    renderWithProviders(<AgentList />, { route: '/agents' })
    await screen.findByText('doc-writer')

    await userEvent.type(screen.getByLabelText('Search agents'), 'triage')

    await waitFor(() =>
      expect(
        (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.some(
          ([url]) => String(url).includes('search=triage'),
        ),
      ).toBe(true),
    )
    await waitFor(() => expect(screen.queryByText('doc-writer')).not.toBeInTheDocument())
    expect(screen.getByText('triage-bot')).toBeInTheDocument()
  })

  it('selecting a group fetches with the groupId param and resets the page', async () => {
    stubAgentFetch()
    renderWithProviders(<AgentList />, { route: '/agents?page=2' })
    await screen.findByText('doc-writer')

    const select = screen.getByRole('combobox', { name: /filter by group/i })
    expect(select).toHaveValue('')
    await userEvent.selectOptions(select, 'g-platform')

    await waitFor(() => {
      const agentCalls = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls
        .map(([url]) => String(url))
        .filter((url) => url.includes('/agents'))
      const last = agentCalls[agentCalls.length - 1]
      expect(last).toContain('groupId=g-platform')
      // Filter changes drop `page`: page 1 of the filtered set, not page 2.
      expect(last).not.toContain('page=2')
    })
  })

  it('restores filters from the URL so a filtered view is linkable', async () => {
    stubAgentFetch()
    renderWithProviders(<AgentList />, { route: '/agents?search=triage&groupId=g-platform' })

    expect(await screen.findByText('triage-bot')).toBeInTheDocument()
    expect(screen.getByLabelText('Search agents')).toHaveValue('triage')
    expect(screen.getByRole('combobox', { name: /filter by group/i })).toHaveValue('g-platform')
    const firstCall = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls
      .map(([url]) => String(url))
      .find((url) => url.includes('/agents'))
    expect(firstCall).toContain('search=triage')
    expect(firstCall).toContain('groupId=g-platform')
  })
})
