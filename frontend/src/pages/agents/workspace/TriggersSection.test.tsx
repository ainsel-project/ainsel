import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes } from 'react-router-dom'
import { AgentWorkspace } from './AgentWorkspace'
import { OverviewSection } from './OverviewSection'
import { SectionRoute } from './sectionPages'
import { agentTestFetch } from './agentTestFetch'
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

const page = <T,>(items: T[]) =>
  json({ items, total: items.length, page: 1, pageSize: 200, totalPages: 1 })

function stubTriggers() {
  const fetchMock = vi.fn((url: string, init?: RequestInit) => {
    if (/\/agents\/a1(\?.*)?$/.test(url) && (init?.method ?? 'GET') === 'GET') {
      return Promise.resolve(json(agent))
    }
    if (url.includes('/cron-triggers') && (init?.method ?? 'GET') === 'GET') {
      return Promise.resolve(
        page([
          {
            id: 'cr-1',
            name: 'nightly-review',
            agentRef: 'a1',
            schedule: '0 2 * * *',
            prompt: 'review',
            enabled: true,
          },
        ]),
      )
    }
    if (url.includes('/triggers') && (init?.method ?? 'GET') === 'GET') {
      return Promise.resolve(
        page([{ id: 't-1', name: 'on-doc-issue', agentRef: 'a1', connectorRef: 'c-1', filters: [] }]),
      )
    }
    return Promise.resolve(agentTestFetch(url, init))
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

function renderTriggers() {
  return renderWithProviders(
    <Routes>
      <Route path="/agents/:id" element={<AgentWorkspace />}>
        <Route index element={<OverviewSection />} />
        <Route path=":section" element={<SectionRoute />} />
      </Route>
    </Routes>,
    { route: '/agents/a1/triggers' },
  )
}

describe('TriggersSection', () => {
  beforeEach(() => {
    localStorage.clear()
    stubTriggers()
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('lists subscriptions and schedules in one table', async () => {
    renderTriggers()
    const table = await screen.findByRole('table')
    expect(within(table).getByText('on-doc-issue')).toBeInTheDocument()
    expect(within(table).getByText('nightly-review')).toBeInTheDocument()
    expect(within(table).getByRole('row', { name: /on-doc-issue/ })).toHaveTextContent('Subscription')
    expect(within(table).getByRole('row', { name: /nightly-review/ })).toHaveTextContent('0 2 * * *')
    expect(within(table).getByRole('row', { name: /nightly-review/ })).toHaveTextContent('Schedule')
  })

  it('New → Schedule opens the schedule form', async () => {
    renderTriggers()
    await userEvent.click(await screen.findByRole('button', { name: 'New' }))
    await userEvent.click(screen.getByRole('button', { name: 'Schedule' }))
    expect(await screen.findByText('New schedule')).toBeInTheDocument()
  })

  it('pauses a schedule through the cron update endpoint', async () => {
    const fetchMock = stubTriggers()
    renderTriggers()
    const table = await screen.findByRole('table')
    await userEvent.click(within(table).getByRole('button', { name: 'Pause' }))
    await waitFor(() => {
      const put = fetchMock.mock.calls.find(
        ([url, init]) => String(url).includes('/cron-triggers/cr-1') && init?.method === 'PUT',
      )
      expect(put).toBeDefined()
      expect(JSON.parse(String(put?.[1]?.body))).toEqual({ enabled: false })
    })
  })
})
