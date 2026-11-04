import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { Route, Routes } from 'react-router-dom'
import { AgentWorkspace } from './AgentWorkspace'
import { SectionRoute } from './sectionPages'
import { renderWithProviders } from '../../../test/renderWithProviders'

vi.mock('../../../auth/AuthProvider', () => ({
  useAuth: () => ({
    token: 'test-token',
    user: { sub: 'sub-kim', username: 'kim', email: 'kim@example.com' },
    mode: 'password',
    ready: true,
    signinRedirect: vi.fn(),
    signoutRedirect: vi.fn(),
  }),
}))

vi.mock('../../../api/plugins', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../api/plugins')>()
  return { ...actual, usePlugins: () => ({ data: [], isPending: false }) }
})

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200 })
}

const agent = {
  id: 'a1',
  name: 'doc-writer',
  description: 'Writes documentation.',
  llm: { model: 'claude-opus-4-7', provider: 'ollama-cloud' },
  imageRef: { id: 'claude-tooling-base:1.4', name: 'claude-tooling-base:1.4' },
  replicas: 3,
  status: { ready: true, replicas: 3 },
}

const inbox = {
  id: 'ch-inbox-a1',
  kind: 'agent',
  name: 'doc-writer',
  description: 'The inbox agent doc-writer drains',
  entityRef: 'a1',
  counts: { events: 7, unmatched: 0, failed: 1 },
  bridges: 0,
  subscriptions: 1,
}

function stubFetch(channels: unknown[] = [inbox]) {
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      if (/\/agents\/a1(\?.*)?$/.test(url) && method === 'GET') return Promise.resolve(json(agent))
      if (/\/channels\/ch-inbox-a1$/.test(url)) {
        return Promise.resolve(json({ ...inbox, incoming: [], outgoing: [] }))
      }
      if (/\/channels\/ch-inbox-a1\/events/.test(url)) {
        return Promise.resolve(json({ events: [], total: 0 }))
      }
      if (/\/api\/v1\/channels(\?.*)?$/.test(url)) {
        return Promise.resolve(
          json({ items: channels, total: channels.length, page: 1, pageSize: 500, totalPages: 1 }),
        )
      }
      if (/\/channel-subscriptions/.test(url)) {
        return Promise.resolve(json({ items: [], total: 0 }))
      }
      if (/\/invocations/.test(url)) {
        return Promise.resolve(
          json({ items: [], total: 0, capacity: 0, page: 1, pageSize: 20, totalPages: 0 }),
        )
      }
      if (/\/cron-triggers/.test(url)) {
        return Promise.resolve(json({ items: [], total: 0, page: 1, pageSize: 200, totalPages: 0 }))
      }
      if (/\/triggers/.test(url)) {
        return Promise.resolve(json({ items: [], total: 0, page: 1, pageSize: 200, totalPages: 0 }))
      }
      return Promise.resolve(json({ items: [], total: 0, page: 1, pageSize: 200, totalPages: 0 }))
    }),
  )
}

function renderChannelSection() {
  return renderWithProviders(
    <Routes>
      <Route path="/agents/:id" element={<AgentWorkspace />}>
        <Route path=":section" element={<SectionRoute />} />
      </Route>
    </Routes>,
    { route: '/agents/a1/channel' },
  )
}

describe('ChannelSection', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('edits the agent inbox in place: header facts, relation graph, timeline, runs', async () => {
    stubFetch()
    renderChannelSection()
    await waitFor(() => expect(screen.getByText('Inbox timeline')).toBeInTheDocument())
    expect(screen.getByText('Reached inbox 24h')).toBeInTheDocument()
    expect(screen.getByText(/Relations/i)).toBeInTheDocument()
    expect(screen.getByText('What the agent did')).toBeInTheDocument()
  })

  it('offers no bridge editor on an agent inbox: the hub rejects bridges onto inboxes', async () => {
    stubFetch()
    renderChannelSection()
    await waitFor(() => expect(screen.getByText('Inbox timeline')).toBeInTheDocument())
    expect(screen.queryByText(/Bridges ·/)).toBeNull()
  })

  it('shows no Subscriptions or Schedules panel: those live in the Triggers section', async () => {
    stubFetch()
    renderChannelSection()
    await waitFor(() => expect(screen.getByText('Inbox timeline')).toBeInTheDocument())
    expect(screen.queryByText('Subscriptions')).not.toBeInTheDocument()
    expect(screen.queryByText('Schedules')).not.toBeInTheDocument()
  })

  it('says so when the agent has no inbox channel yet', async () => {
    stubFetch([])
    renderChannelSection()
    await waitFor(() =>
      expect(screen.getByText(/This agent has no channel yet/)).toBeInTheDocument(),
    )
  })
})
