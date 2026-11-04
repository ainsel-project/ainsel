import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes, useLocation } from 'react-router-dom'
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

function LocationProbe() {
  const location = useLocation()
  return <p data-testid="location">{location.pathname}</p>
}

function renderOverview(route = '/agents/a1') {
  return renderWithProviders(
    <>
      <Routes>
        <Route path="/agents/:id" element={<AgentWorkspace />}>
          <Route index element={<OverviewSection />} />
          <Route path=":section" element={<SectionRoute />} />
        </Route>
        <Route path="/agents" element={<div>LIST</div>} />
      </Routes>
      <LocationProbe />
    </>,
    { route },
  )
}

/** Routes the agent endpoint to `agent`, everything else to the shared fixture. */
function stubAgent(
  agent: Record<string, unknown>,
  extra?: (url: string, init?: RequestInit) => Response | undefined,
) {
  const fetchMock = vi.fn((url: string, init?: RequestInit) => {
    const hit = extra?.(url, init)
    if (hit) return Promise.resolve(hit)
    if (/\/agents\/a1(\?.*)?$/.test(url) && (init?.method ?? 'GET') === 'GET') {
      return Promise.resolve(json(agent))
    }
    return Promise.resolve(agentTestFetch(url, init))
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

const baseAgent = {
  id: 'a1',
  name: 'doc-writer',
  description: 'Writes documentation.',
  llm: { model: 'claude-opus-4-7', provider: 'ollama-cloud' },
  imageRef: { id: 'claude-tooling-base:1.4', name: 'claude-tooling-base:1.4' },
  replicas: 3,
  status: { ready: true, replicas: 3 },
}

const currentPath = () => screen.getByTestId('location').textContent

/** The value cell under a `.k` label in the overview's fact and tile grids. */
function valueOf(label: string): HTMLElement {
  const key = Array.from(document.querySelectorAll<HTMLElement>('.k')).find(
    (el) => el.textContent === label,
  )
  if (!key?.nextElementSibling) throw new Error(`no value cell for "${label}"`)
  return key.nextElementSibling as HTMLElement
}

describe('OverviewSection', () => {
  beforeEach(() => {
    localStorage.clear()
    stubAgent(baseAgent)
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders the name and a status · model · image meta line', async () => {
    renderOverview()
    expect(await screen.findByRole('heading', { name: 'doc-writer' })).toBeInTheDocument()
    const meta = screen.getByTestId('overview-meta')
    expect(meta).toHaveTextContent('claude-opus-4-7')
    await waitFor(() =>
      expect(within(meta).getByRole('link', { name: 'claude-tooling-base:1.4' })).toHaveAttribute(
        'href',
        '/agent-images/claude-tooling-base%3A1.4',
      ),
    )
  })

  it('counts the five run states 1:1 with the Activity outcome filters', async () => {
    // Fixtures carry task payloads: under effectiveRunState a bare
    // { status: 'running' } with no task is `unknown`, not a live run.
    const invocations = [
      { id: 'i1', status: 'success' },
      { id: 'i2', status: 'success' },
      { id: 'i3', status: 'failure' },
      { id: 'i4', status: 'timeout' },
      // Hub-provided derived state wins over the raw status.
      { id: 'i5', status: 'running', task: { status: 'pending', attempts: 0, runState: 'running' } },
      // Pre-#422 fallback: claimed task with no runState still reads as running.
      { id: 'i6', status: 'running', task: { status: 'claimed', attempts: 0 } },
      { id: 'i7', status: 'queued', task: { status: 'pending', attempts: 0 } },
      // Stale record: a "running" invocation with no task behind it is
      // unknown — it counts into neither Running nor Queued.
      { id: 'i8', status: 'running' },
    ]
    const invocationsUrls: string[] = []
    stubAgent(baseAgent, (url) => {
      if (!url.includes('/invocations')) return undefined
      invocationsUrls.push(url)
      return json({ invocations, total: 8, capacity: 0, page: 1, pageSize: 200, totalPages: 1 })
    })
    renderOverview()

    await waitFor(() => expect(valueOf('Runs today')).toHaveTextContent('8'))
    expect(valueOf('Failures today')).toHaveTextContent('1')
    expect(valueOf('Timeouts today')).toHaveTextContent('1')
    expect(valueOf('Running now')).toHaveTextContent('2')
    expect(valueOf('Queued now')).toHaveTextContent('1')
    expect(invocationsUrls[0]).toContain('agent=a1')
    expect(invocationsUrls[0]).toContain('pageSize=200')
    expect(invocationsUrls[0]).toContain('since=')
  })

  it('links each stat to the activity view with the matching filter', async () => {
    renderOverview()
    await waitFor(() => expect(valueOf('Runs today').querySelector('a')).not.toBeNull())
    const expected: [string, string][] = [
      ['Runs today', '/activity?agent=a1&since=today'],
      ['Failures today', '/activity?agent=a1&since=today&outcome=failure'],
      ['Timeouts today', '/activity?agent=a1&since=today&outcome=timeout'],
      ['Running now', '/activity?agent=a1&since=today&outcome=running'],
      ['Queued now', '/activity?agent=a1&since=today&outcome=queued'],
    ]
    for (const [label, href] of expected) {
      expect(valueOf(label).querySelector('a')).toHaveAttribute('href', href)
    }
  })

  it('treats a blank agent TURN_TIMEOUT_MS as absent instead of showing 0 min', async () => {
    stubAgent({ ...baseAgent, env: [{ name: 'TURN_TIMEOUT_MS', value: '' }] })
    renderOverview()
    await waitFor(() => expect(valueOf('Timeout')).toHaveTextContent('30 min · default'))
    expect(valueOf('Timeout').textContent).not.toMatch(/^0 min/)
  })

  it('falls through a blank agent TURN_TIMEOUT_MS to the image value', async () => {
    stubAgent({ ...baseAgent, env: [{ name: 'TURN_TIMEOUT_MS', value: ' ' }] }, (url) => {
      if (!url.includes('/agent-images/claude-tooling-base')) return undefined
      return json({
        id: 'claude-tooling-base:1.4',
        displayName: 'Claude Tooling Base',
        imageURL: 'ghcr.io/ainsel/claude-tooling:1.4',
        tools: [],
        env: [{ name: 'TURN_TIMEOUT_MS', value: '900000' }],
        mcpServers: [],
        enabledSkills: [],
      })
    })
    renderOverview()
    await waitFor(() => expect(valueOf('Timeout')).toHaveTextContent('15 min · image'))
  })

  it('shows an ellipsis for the timeout while the image is loading, not the default', async () => {
    const imageRequest = new Promise<Response>(() => {})
    stubAgent(baseAgent, (url) => {
      if (!url.includes('/agent-images/claude-tooling-base')) return undefined
      return imageRequest as unknown as Response
    })
    renderOverview()
    expect(await screen.findByRole('heading', { name: 'doc-writer' })).toBeInTheDocument()
    expect(valueOf('Timeout')).toHaveTextContent('…')
    expect(valueOf('Timeout')).not.toHaveTextContent('default')
  })

  it('reads the timeout from the agent env in minutes, attributed to the agent', async () => {
    stubAgent({ ...baseAgent, env: [{ name: 'TURN_TIMEOUT_MS', value: '3600000' }] })
    renderOverview()
    await waitFor(() => expect(valueOf('Timeout')).toHaveTextContent('60 min · agent'))
  })

  it('falls back to the 30 minute default when neither env sets the timeout', async () => {
    renderOverview()
    await waitFor(() => expect(valueOf('Timeout')).toHaveTextContent('30 min · default'))
  })

  it('reads the timeout from the runtime image when only the image sets it', async () => {
    stubAgent(baseAgent, (url) => {
      if (!url.includes('/agent-images/claude-tooling-base')) return undefined
      return json({
        id: 'claude-tooling-base:1.4',
        displayName: 'Claude Tooling Base',
        imageURL: 'ghcr.io/ainsel/claude-tooling:1.4',
        tools: [],
        env: [{ name: 'TURN_TIMEOUT_MS', value: '900000' }],
        mcpServers: [],
        enabledSkills: [],
      })
    })
    renderOverview()
    await waitFor(() => expect(valueOf('Timeout')).toHaveTextContent('15 min · image'))
  })

  it('edits the name and description inline and saves through the agent update', async () => {
    const putCalls: { url: string; body: string }[] = []
    stubAgent(baseAgent, (url, init) => {
      if (init?.method === 'PUT' && /\/api\/v1\/agents\/a1$/.test(url)) {
        putCalls.push({ url, body: String(init.body ?? '') })
        return json({ ...baseAgent, name: 'doc-writer-2' })
      }
      return undefined
    })
    renderOverview()

    await userEvent.click(await screen.findByRole('button', { name: 'Edit name and description' }))
    const nameInput = screen.getByLabelText('Name')
    await userEvent.clear(nameInput)
    await userEvent.type(nameInput, 'doc-writer-2')
    await userEvent.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(putCalls).toHaveLength(1))
    expect(JSON.parse(putCalls[0].body)).toEqual({
      name: 'doc-writer-2',
      description: 'Writes documentation.',
    })
  })

  it('has no pencil or lifecycle actions for an externally managed agent', async () => {
    stubAgent({ ...baseAgent, managedBy: 'gitops' })
    renderOverview()
    await waitFor(() => expect(screen.getByText('managed by gitops')).toBeInTheDocument())
    expect(
      screen.queryByRole('button', { name: 'Edit name and description' }),
    ).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^delete$/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^pause$/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^chat$/i })).not.toBeInTheDocument()
  })

  it('opens the chat section from the Chat button', async () => {
    renderOverview()
    await userEvent.click(await screen.findByRole('button', { name: /^chat$/i }))
    await waitFor(() => expect(currentPath()).toBe('/agents/a1/chat'))
  })

  it('shows the Triggers fact as the agent triggers plus its schedules', async () => {
    renderOverview()
    // The fixture gives a1 one trigger and no schedules.
    await waitFor(() => expect(valueOf('Triggers')).toHaveTextContent('1'))
  })

  it('shows the agent inbox channel as a link to the channel section', async () => {
    renderOverview()
    await waitFor(() =>
      expect(valueOf('Channel').querySelector('a')).toHaveAttribute('href', '/agents/a1/channel'),
    )
    expect(valueOf('Channel')).toHaveTextContent('doc-writer')
  })

  it('names the linked persona in the Persona fact', async () => {
    renderOverview()
    await waitFor(() => expect(valueOf('Persona')).toHaveTextContent('docs-writer'))
  })

  it('shows the paused tag with the resume time', async () => {
    const until = new Date(Date.now() + 3600_000).toISOString()
    stubAgent({ ...baseAgent, paused: true, pausedUntil: until })
    renderOverview()
    expect(await screen.findByText(/Paused/)).toBeInTheDocument()
  })

  describe('pause / resume', () => {
    it('offers Pause on a running agent and posts the chosen duration', async () => {
      const pauseCalls: { url: string; body: string }[] = []
      stubAgent(baseAgent, (url, init) => {
        if (init?.method === 'POST' && url.includes('/pause')) {
          pauseCalls.push({ url, body: String(init.body ?? '') })
          return json({})
        }
        return undefined
      })
      renderOverview()

      await userEvent.click(await screen.findByRole('button', { name: 'Pause' }))
      // The modal's confirm button shares the trigger's name; scope to the dialog.
      const dialog = await screen.findByRole('dialog')
      await userEvent.click(await within(dialog).findByRole('button', { name: 'Pause' }))

      await waitFor(() => expect(pauseCalls).toHaveLength(1))
      expect(pauseCalls[0].url).toContain('/api/v1/agents/a1/pause')
      expect(JSON.parse(pauseCalls[0].body)).toEqual({ duration: '8h' })
    })

    it('offers the pause presets with the existing confirm copy', async () => {
      renderOverview()
      await userEvent.click(await screen.findByRole('button', { name: 'Pause' }))
      const group = await screen.findByRole('radiogroup', { name: 'Pause duration' })
      for (const label of ['30 minutes', '8 hours', '24 hours', '7 days', 'Indefinite']) {
        expect(within(group).getByRole('radio', { name: label })).toBeInTheDocument()
      }
      expect(screen.getByText('Pause agent?')).toBeInTheDocument()
    })

    it('shows the paused state and resumes via DELETE', async () => {
      const until = new Date(Date.now() + 3600_000).toISOString()
      const delCalls: string[] = []
      stubAgent({ ...baseAgent, paused: true, pausedUntil: until }, (url, init) => {
        if (init?.method === 'DELETE' && url.includes('/pause')) {
          delCalls.push(url)
          return json({})
        }
        return undefined
      })
      renderOverview()

      expect(await screen.findByText(/Paused/)).toBeInTheDocument()
      await userEvent.click(await screen.findByRole('button', { name: 'Resume' }))
      await waitFor(() => expect(delCalls).toHaveLength(1))
      expect(delCalls[0]).toContain('/api/v1/agents/a1/pause')
    })
  })

  describe('delete', () => {
    it('confirms and deletes the agent then navigates to the list', async () => {
      renderOverview()
      await screen.findAllByText('doc-writer')
      await userEvent.click(screen.getByRole('button', { name: /^delete$/i }))
      const dialog = await screen.findByRole('dialog')
      await userEvent.click(within(dialog).getByRole('button', { name: /^delete/i }))
      await waitFor(() => expect(screen.getByText('LIST')).toBeInTheDocument())
    })

    it('surfaces an error when delete fails and keeps the modal open', async () => {
      stubAgent(baseAgent, (_url, init) => {
        if (init?.method === 'DELETE') {
          return new Response(JSON.stringify({ message: 'forbidden' }), { status: 403 })
        }
        return undefined
      })
      renderOverview()
      await screen.findAllByText('doc-writer')
      await userEvent.click(screen.getByRole('button', { name: /^delete$/i }))
      const dialog = await screen.findByRole('dialog')
      await userEvent.click(within(dialog).getByRole('button', { name: /^delete/i }))
      await waitFor(() => expect(screen.getByText('forbidden')).toBeInTheDocument())
      expect(screen.queryByText('LIST')).not.toBeInTheDocument()
      expect(screen.getByRole('dialog')).toBeInTheDocument()
    })
  })

  describe('status', () => {
    function stubStatus(status: Record<string, unknown>) {
      stubAgent({ ...baseAgent, replicas: 3, status: { ready: false, ...status } })
    }

    it('calls an agent with nothing in flight idle rather than broken', async () => {
      stubStatus({ desired: 0, reason: 'JobPerTask', message: '0 task job(s) active' })
      renderOverview()
      await waitFor(() => expect(screen.getByText('Idle')).toBeInTheDocument())
    })

    it('still reports an agent with a task in flight as pending', async () => {
      stubStatus({ desired: 1, ready: false })
      renderOverview()
      await waitFor(() => expect(screen.getByText('Pending')).toBeInTheDocument())
      expect(screen.queryByText('Idle')).not.toBeInTheDocument()
    })
  })
})
