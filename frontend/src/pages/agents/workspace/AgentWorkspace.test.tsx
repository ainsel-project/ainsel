import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Link, Route, Routes, useLocation } from 'react-router-dom'
import { AgentWorkspace } from './AgentWorkspace'
import { SectionRoute } from './sectionPages'
import { OverviewSection } from './OverviewSection'
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

vi.mock('../../../plugins/PluginSlot', () => ({
  PluginSlot: ({ pluginId }: { pluginId: string }) => <p>{`plugin=${pluginId}`}</p>,
}))

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200 })
}

/** Agent payloads for the two agents the tests switch between. */
const AGENTS: Record<string, Record<string, unknown>> = {
  a1: {
    id: 'a1',
    name: 'doc-writer',
    llm: { model: 'claude-opus-4-7', provider: 'ollama-cloud' },
    replicas: 1,
    status: { ready: true, replicas: 1 },
  },
  a2: {
    id: 'a2',
    name: 'ops-triager',
    llm: { model: 'gpt-ops-mini', provider: 'ollama-cloud' },
    replicas: 1,
    status: { ready: true, replicas: 1 },
  },
}

function stubFetch() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      const agent = url.match(/\/agents\/(a[12])(\?.*)?$/)
      if (agent && (init?.method ?? 'GET') === 'GET') return json(AGENTS[agent[1]])
      return agentTestFetch(url, init)
    }),
  )
}

function LocationProbe() {
  const location = useLocation()
  return <p data-testid="location">{`${location.pathname}${location.search}`}</p>
}

function renderWorkspace(route: string) {
  return renderWithProviders(
    <>
      <Routes>
        <Route path="/agents/:id" element={<AgentWorkspace />}>
          <Route index element={<OverviewSection />} />
          <Route path=":section" element={<SectionRoute />} />
        </Route>
      </Routes>
      <Link to="/agents/a2/runtime">switch to a2</Link>
      <LocationProbe />
    </>,
    { route },
  )
}

const currentPath = () => screen.getByTestId('location').textContent

describe('AgentWorkspace', () => {
  beforeEach(() => {
    localStorage.clear()
    stubFetch()
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders the runtime section and its environment editor on /agents/:id/runtime', async () => {
    renderWorkspace('/agents/a1/runtime')
    expect(await screen.findByRole('heading', { name: 'Runtime & env' })).toBeInTheDocument()
    expect(await screen.findByRole('heading', { name: 'Environment' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Runtime & env' })).toHaveAttribute(
      'aria-current',
      'page',
    )
  })

  it('redirects the legacy ?tab=skills URL to /agents/:id/skills', async () => {
    renderWorkspace('/agents/a1?tab=skills')
    await waitFor(() => expect(currentPath()).toBe('/agents/a1/skills'))
  })

  it('sends an unknown section segment back to the overview', async () => {
    renderWorkspace('/agents/a1/bogus')
    await waitFor(() => expect(currentPath()).toBe('/agents/a1'))
  })

  it('navigates to the triggers section from the section nav', async () => {
    renderWorkspace('/agents/a1')
    await userEvent.click(await screen.findByRole('link', { name: 'Triggers' }))
    await waitFor(() => expect(currentPath()).toBe('/agents/a1/triggers'))
  })

  it('renders the plugin panel for a plugin:<id> section', async () => {
    renderWorkspace('/agents/a1/plugin:forgejo')
    expect(await screen.findByText('plugin=forgejo')).toBeInTheDocument()
  })

  it('records the viewed agent under the signed-in user', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/agents/:id" element={<AgentWorkspace />}>
          <Route index element={<OverviewSection />} />
          <Route path=":section" element={<SectionRoute />} />
        </Route>
      </Routes>,
      { route: '/agents/a1' },
    )
    await waitFor(() =>
      expect(JSON.parse(localStorage.getItem('ainsel-agent-recents:sub-kim') ?? '[]')).toEqual([
        'a1',
      ]),
    )
  })

  it('opens the Runtime section with image identity and env, and switches images', async () => {
    const putCalls: Array<{ url: string; body?: string }> = []
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string, init?: RequestInit) => {
        if (init?.method === 'PUT') {
          putCalls.push({ url, body: init.body ? String(init.body) : undefined })
        }
        return Promise.resolve(agentTestFetch(url, init))
      }),
    )

    renderWithProviders(
      <Routes>
        <Route path="/agents/:id" element={<AgentWorkspace />}>
          <Route index element={<OverviewSection />} />
          <Route path=":section" element={<SectionRoute />} />
        </Route>
      </Routes>,
      { route: '/agents/a1' },
    )
    await screen.findAllByText('doc-writer')
    await userEvent.click(screen.getByRole('link', { name: /^runtime & env$/i }))

    // The embedded editor loads the referenced image's identity and env.
    const urlInput = await screen.findByLabelText('Image URL')
    await waitFor(() => expect(urlInput).toHaveValue('ghcr.io/ainsel/claude-tooling:1.4'))
    expect(screen.getByRole('heading', { name: 'Environment Variables' })).toBeInTheDocument()
    // Runtime settings and the image editor each carry a Save.
    expect(screen.getByLabelText('Model')).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: /^save$/i }).length).toBeGreaterThan(1)
    expect(screen.queryByRole('button', { name: /^cancel$/i })).not.toBeInTheDocument()

    // No tools-side or skills sections leak onto this tab.
    expect(screen.queryByRole('heading', { name: 'MCP Servers' })).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Skills' })).not.toBeInTheDocument()

    // Switching the image patches the agent with the new reference.
    await userEvent.selectOptions(screen.getByLabelText('Agent image'), 'minimal:1.0')
    await waitFor(() => {
      const call = putCalls.find((c) => c.url.includes('/api/v1/agents/a1'))
      expect(call).toBeDefined()
      expect(JSON.parse(call!.body!)).toEqual({
        name: 'doc-writer',
        imageRef: { name: 'minimal:1.0' },
      })
    })
  })

  it('opens the Tools section with the image catalog and an agent-scoped MCP selection', async () => {
    const putCalls: Array<{ url: string; body?: string }> = []
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string, init?: RequestInit) => {
        if (init?.method === 'PUT') {
          putCalls.push({ url, body: init.body ? String(init.body) : undefined })
        }
        if (url.includes('/mcp-servers')) {
          return Promise.resolve(
            new Response(
              JSON.stringify([
                {
                  name: 'github',
                  displayName: 'GitHub',
                  url: 'https://mcp.github.com/sse',
                  tokenFromEnv: 'GITHUB_TOKEN',
                  createdAt: '2026-06-01T00:00:00Z',
                  updatedAt: '2026-06-01T00:00:00Z',
                },
                {
                  name: 'linear',
                  displayName: 'Linear',
                  url: 'https://mcp.linear.app/sse',
                  createdAt: '2026-06-01T00:00:00Z',
                  updatedAt: '2026-06-01T00:00:00Z',
                },
              ]),
              { status: 200 },
            ),
          )
        }
        return Promise.resolve(agentTestFetch(url, init))
      }),
    )

    renderWithProviders(
      <Routes>
        <Route path="/agents/:id" element={<AgentWorkspace />}>
          <Route index element={<OverviewSection />} />
          <Route path=":section" element={<SectionRoute />} />
        </Route>
      </Routes>,
      { route: '/agents/a1' },
    )
    await screen.findAllByText('doc-writer')
    await userEvent.click(screen.getByRole('link', { name: /^tools & mcp$/i }))

    // The image tool catalog still renders (with its own save button),
    // and the MCP section is now the agent's own selection.
    expect((await screen.findAllByText('read_file')).length).toBeGreaterThan(0)
    expect(
      await screen.findByRole('button', {
        name: /add selected to enabled on this agent/i,
      }),
    ).toBeInTheDocument()
    expect(screen.getByRole('option', { name: /github/i })).toBeInTheDocument()
    expect(screen.getByText(/inherited from the runtime image/i)).toBeInTheDocument()

    // No image-side, skills, or picker sections on this tab.
    expect(screen.queryByLabelText('Image URL')).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Environment Variables' })).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Skills' })).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Agent image')).not.toBeInTheDocument()

    // Enabling an MCP server pins the selection to the agent.
    await userEvent.click(screen.getByRole('option', { name: /github/i }))
    await userEvent.click(
      screen.getByRole('button', { name: /add selected to enabled on this agent/i }),
    )
    await waitFor(() => {
      const call = putCalls.find((c) => c.url.includes('/api/v1/agents/a1'))
      expect(call).toBeDefined()
      expect(JSON.parse(call!.body!)).toEqual({
        name: 'doc-writer',
        mcp: { servers: ['github'] },
      })
    })
  })

  it('opens the Skills section as an agent-scoped selection and pins it on change', async () => {
    const putCalls: Array<{ url: string; body?: string }> = []
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string, init?: RequestInit) => {
        if (init?.method === 'PUT') {
          putCalls.push({ url, body: init.body ? String(init.body) : undefined })
        }
        if (url.includes('/skills?') || url.includes('/skills')) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                items: [
                  {
                    id: 'skill-pr',
                    name: 'PR Review',
                    description: 'Review pull requests',
                  },
                  {
                    id: 'skill-triage',
                    name: 'Issue Triage',
                    description: 'Triage issues',
                  },
                ],
                total: 2,
                page: 1,
                pageSize: 200,
                totalPages: 1,
              }),
              { status: 200 },
            ),
          )
        }
        return Promise.resolve(agentTestFetch(url, init))
      }),
    )

    renderWithProviders(
      <Routes>
        <Route path="/agents/:id" element={<AgentWorkspace />}>
          <Route index element={<OverviewSection />} />
          <Route path=":section" element={<SectionRoute />} />
        </Route>
      </Routes>,
      { route: '/agents/a1' },
    )
    await screen.findAllByText('doc-writer')
    await userEvent.click(screen.getByRole('link', { name: /^skills$/i }))

    // The agent-scoped picker renders with agent wording, and the
    // inherit-from-image hint shows while spec.skills is unset.
    expect(
      await screen.findByRole('button', {
        name: /add selected to enabled on this agent/i,
      }),
    ).toBeInTheDocument()
    expect(screen.getByRole('option', { name: /pr review/i })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: /issue triage/i })).toBeInTheDocument()
    expect(screen.getByText(/inherited from the runtime image/i)).toBeInTheDocument()

    // No image-side sections leak onto this tab, and no embedded form save:
    // the picker PUTs the agent directly.
    expect(screen.queryByLabelText('Image URL')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^save$/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'MCP Servers' })).not.toBeInTheDocument()

    // Enabling a skill pins the selection to the agent.
    await userEvent.click(screen.getByRole('option', { name: /pr review/i }))
    await userEvent.click(
      screen.getByRole('button', { name: /add selected to enabled on this agent/i }),
    )
    await waitFor(() => {
      const call = putCalls.find((c) => c.url.includes('/api/v1/agents/a1'))
      expect(call).toBeDefined()
      expect(JSON.parse(call!.body!)).toEqual({
        name: 'doc-writer',
        skills: { items: ['skill-pr'] },
      })
    })
  })

  it('shows the Channel section with the agent inbox summary', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/agents/:id" element={<AgentWorkspace />}>
          <Route index element={<OverviewSection />} />
          <Route path=":section" element={<SectionRoute />} />
        </Route>
      </Routes>,
      { route: '/agents/a1' },
    )
    await screen.findByText('claude-opus-4-7')
    expect(screen.getByRole('link', { name: /^channel$/i })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('link', { name: /^channel$/i }))
    expect(await screen.findByText('agent inbox')).toBeInTheDocument()
    expect(screen.getByText('The inbox agent doc-writer drains')).toBeInTheDocument()
    // addressed by the hub's channel id, not a synthesized kind:name key
    expect(
      screen.getByText((_, el) => el?.textContent === '7 events reached this inbox in 24h'),
    ).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /open channel/i })).toHaveAttribute(
      'href',
      '/channels/ch-inbox-a1',
    )
  })

  it('opens the Channel section directly via the ?tab=channel legacy link', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/agents/:id" element={<AgentWorkspace />}>
          <Route index element={<OverviewSection />} />
          <Route path=":section" element={<SectionRoute />} />
        </Route>
      </Routes>,
      { route: '/agents/a1?tab=channel' },
    )
    // Channel panel content loads without clicking the link.
    await waitFor(() => expect(screen.getByText('agent inbox')).toBeInTheDocument())
  })

  it('falls back to the Overview section for an invalid ?tab value', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/agents/:id" element={<AgentWorkspace />}>
          <Route index element={<OverviewSection />} />
          <Route path=":section" element={<SectionRoute />} />
        </Route>
      </Routes>,
      { route: '/agents/a1?tab=bogus' },
    )
    // Overview-only content (the model) is shown; triggers are not loaded.
    await waitFor(() => expect(screen.getByText('claude-opus-4-7')).toBeInTheDocument())
    expect(screen.queryByText('on-doc-issue')).not.toBeInTheDocument()
  })
  it('remounts the section when the agent changes, so typed edits do not carry over', async () => {
    renderWorkspace('/agents/a1/runtime')
    const model = await screen.findByLabelText('Model')
    await waitFor(() => expect(model).toHaveValue('claude-opus-4-7'))
    await userEvent.clear(model)
    await userEvent.type(model, 'typed-on-a1')

    await userEvent.click(screen.getByRole('link', { name: 'switch to a2' }))
    await waitFor(() => expect(currentPath()).toBe('/agents/a2/runtime'))

    const next = await screen.findByLabelText('Model')
    await waitFor(() => expect(next).toHaveValue('gpt-ops-mini'))
    expect(screen.queryByDisplayValue('typed-on-a1')).not.toBeInTheDocument()
  })
})
