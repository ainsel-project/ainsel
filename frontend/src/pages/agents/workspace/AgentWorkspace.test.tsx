import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Link, Route, Routes, useLocation } from 'react-router-dom'
import { AgentWorkspace } from './AgentWorkspace'
import { SectionRoute } from './sectionPages'
import { OverviewSection } from './OverviewSection'
import { agentTestFetch } from './agentTestFetch'
import { QueryClient } from '@tanstack/react-query'
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

// The plugin list each test wants the nav to see; empty by default.
const installed = vi.hoisted(() => ({ list: [] as unknown[] }))

vi.mock('../../../api/plugins', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../api/plugins')>()
  return { ...actual, usePlugins: () => ({ data: installed.list, isPending: false }) }
})

vi.mock('../../../plugins/PluginSlot', () => ({
  PluginSlot: ({ pluginId }: { pluginId: string }) => <p>{`plugin=${pluginId}`}</p>,
}))

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200 })
}

/** The panel owning a panel heading (h3) — a section can render more than one
 *  picker, and the section page itself carries an h2 of a similar name. */
function panel(heading: string): HTMLElement {
  return screen.getByRole('heading', { level: 3, name: heading }).closest('.panel') as HTMLElement
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

/** Installs an active plugin whose manifest contributes an agent.panel. */
function installForgejo() {
  installed.list = [
    {
      id: 'forgejo',
      displayName: 'Forgejo',
      ready: true,
      disabled: false,
      manifest: {
        id: 'forgejo',
        displayName: 'Forgejo',
        version: '1.0.0',
        pluginApi: '1.1',
        ui: { entry: '/ui/index.js', slots: { 'agent.panel': 'AgentPanel' } },
      },
    },
  ]
}

function LocationProbe() {
  const location = useLocation()
  return <p data-testid="location">{`${location.pathname}${location.search}`}</p>
}

function renderWorkspace(route: string, queryClient?: QueryClient) {
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
    { route, queryClient },
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
    installed.list = []
  })

  it('renders the runtime section on /agents/:id/runtime', async () => {
    renderWorkspace('/agents/a1/runtime')
    expect(await screen.findByRole('heading', { name: 'Runtime & env' })).toBeInTheDocument()
    // a1 has no image linked: the overrides panel is absent and the image
    // picker's no-image hint is the single prompt.
    expect(screen.queryByRole('heading', { name: 'Environment' })).not.toBeInTheDocument()
    expect(await screen.findAllByText(/No image linked yet/i)).toHaveLength(1)
    expect(screen.getByRole('link', { name: 'Runtime & env' })).toHaveAttribute(
      'aria-current',
      'page',
    )
  })

  it('redirects the legacy ?tab=skills URL to /agents/:id/skills', async () => {
    renderWorkspace('/agents/a1/skills')
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
    installForgejo()
    renderWorkspace('/agents/a1/plugin:forgejo')
    expect(await screen.findByText('plugin=forgejo')).toBeInTheDocument()
  })

  it('redirects a plugin:<id> section with no active agent.panel contribution to the overview', async () => {
    installForgejo()
    renderWorkspace('/agents/a1/plugin:gone')
    await waitFor(() => expect(currentPath()).toBe('/agents/a1'))
    expect(screen.queryByText(/plugin=gone/)).toBeNull()
  })

  it('redirects the legacy ?tab=plugin:<id> URL to the plugin section', async () => {
    installForgejo()
    renderWorkspace('/agents/a1?tab=plugin:forgejo')
    await waitFor(() => expect(currentPath()).toBe('/agents/a1/plugin:forgejo'))
  })

  it('lists an active agent.panel plugin in the nav and opens its slot on click', async () => {
    installForgejo()
    renderWorkspace('/agents/a1')
    await userEvent.click(await screen.findByRole('link', { name: 'Forgejo' }))
    await waitFor(() => expect(currentPath()).toBe('/agents/a1/plugin:forgejo'))
    expect(await screen.findByText('plugin=forgejo')).toBeInTheDocument()
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

    // The image is shown read-only, with a link to the image page: no image
    // fields and no image-side save on this page — the shared image is only
    // edited on /agent-images/:id.
    await screen.findByText('ghcr.io/ainsel/claude-tooling:1.4')
    expect(screen.getByRole('link', { name: /edit image/i })).toHaveAttribute(
      'href',
      '/agent-images/claude-tooling-base%3A1.4',
    )
    expect(screen.queryByLabelText('Display Name')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Image URL')).not.toBeInTheDocument()
    // Environment overrides is its own panel with its own save.
    expect(screen.getByRole('heading', { name: 'Environment overrides' })).toBeInTheDocument()
    // Runtime settings is the only plain Save on the page; the overrides panel
    // saves under its own distinct label.
    expect(screen.getByLabelText('Model')).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: /^save$/i })).toHaveLength(1)
    expect(screen.getByRole('button', { name: /save overrides/i })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^cancel$/i })).not.toBeInTheDocument()

    // No tools-side or skills sections leak onto this section.
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

  it("opens the Tools section as the agent's own allowlist over the image catalog", async () => {
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

    // The image ships the catalog; this agent's spec.enabledTools decides which
    // of it it gets — both picker panes carry agent wording.
    const tools = panel('Tools')
    expect(within(tools).getByRole('option', { name: /^read_file$/i })).toBeInTheDocument()
    expect(within(tools).getByRole('option', { name: /^run_shell$/i })).toBeInTheDocument()
    expect(screen.queryByText(/every tool the image ships/i)).not.toBeInTheDocument()

    // MCP is the agent's own selection, inherited from the image until pinned.
    const mcp = panel('MCP Servers')
    expect(within(mcp).getByRole('option', { name: /github/i })).toBeInTheDocument()
    expect(screen.getByText(/inherited from the runtime image/i)).toBeInTheDocument()

    // No image form on this page: no image fields, no image-side save.
    expect(screen.queryByLabelText('Image URL')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^save$/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Skills' })).not.toBeInTheDocument()

    // Dropping a tool writes this agent's spec.enabledTools — the image, and
    // every other agent on it, keeps the tool.
    await userEvent.click(within(tools).getByRole('option', { name: /^read_file$/i }))
    await userEvent.click(within(tools).getByRole('button', { name: /remove selected from/i }))
    await waitFor(() => {
      const call = putCalls.find((c) => c.body?.includes('enabledTools'))
      expect(call).toBeDefined()
      expect(JSON.parse(call!.body!)).toEqual({ name: 'doc-writer', enabledTools: ['run_shell'] })
    })

    // Enabling an MCP server pins the selection to the agent.
    await userEvent.click(within(mcp).getByRole('option', { name: /linear/i }))
    await userEvent.click(within(mcp).getByRole('button', { name: /add selected to/i }))
    await waitFor(() => {
      const call = putCalls.find((c) => c.body?.includes('mcp'))
      expect(call).toBeDefined()
      expect(JSON.parse(call!.body!)).toEqual({ name: 'doc-writer', mcp: { servers: ['linear'] } })
    })
  })

  // An empty allowlist is not "nothing": the agent gets everything the image ships.
  it('says that an agent without a tool selection runs with every tool the image ships', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string, init?: RequestInit) => {
        if (/\/api\/v1\/agents\/a1(\?|$)/.test(url) && (init?.method ?? 'GET') === 'GET') {
          return Promise.resolve(
            json({
              id: 'a1',
              name: 'doc-writer',
              llm: { model: 'claude-opus-4-7', provider: 'ollama-cloud' },
              imageRef: { id: 'claude-tooling-base:1.4', name: 'claude-tooling-base:1.4' },
              replicas: 1,
              status: { ready: true, replicas: 1 },
            }),
          )
        }
        return Promise.resolve(agentTestFetch(url, init))
      }),
    )
    renderWorkspace('/agents/a1/tools')
    await screen.findByRole('heading', { level: 3, name: 'Tools' })
    // The hint sits under the picker: an empty selection is not "nothing".
    expect(await screen.findByText(/every tool the image ships/i)).toBeInTheDocument()
  })

  it("opens the Skills section as the agent's own selection and writes it on change", async () => {
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

    // The picker lists the hub's skill library with agent wording. Nothing is
    // inherited from the runtime image: skills are the agent's own.
    const skills = panel('Skills')
    expect(within(skills).getByRole('option', { name: /pr review/i })).toBeInTheDocument()
    expect(within(skills).getByRole('option', { name: /issue triage/i })).toBeInTheDocument()
    expect(screen.queryByText(/inherited from the runtime image/i)).not.toBeInTheDocument()

    // No image-side sections leak into this section, and no image-side save:
    // the picker PUTs the agent directly.
    expect(screen.queryByLabelText('Image URL')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^save$/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'MCP Servers' })).not.toBeInTheDocument()

    // Selecting a skill writes this agent's spec.skills.
    await userEvent.click(within(skills).getByRole('option', { name: /pr review/i }))
    await userEvent.click(within(skills).getByRole('button', { name: /add selected to/i }))
    await waitFor(() => {
      const call = putCalls.find((c) => c.url.includes('/api/v1/agents/a1'))
      expect(call).toBeDefined()
      expect(JSON.parse(call!.body!)).toEqual({
        name: 'doc-writer',
        skills: ['skill-pr'],
      })
    })
  })

  // Skills are not a property of the runtime image, so an agent that has no
  // image linked can still be given one.
  it('offers skills to an agent with no image linked', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string, init?: RequestInit) => {
        if (/\/api\/v1\/agents\/a1(\?|$)/.test(url) && (init?.method ?? 'GET') === 'GET') {
          return Promise.resolve(json(AGENTS.a1))
        }
        if (url.includes('/skills')) {
          return Promise.resolve(
            json({
              items: [{ id: 'skill-pr', name: 'PR Review', description: 'Review pull requests' }],
              total: 1,
              page: 1,
              pageSize: 200,
              totalPages: 1,
            }),
          )
        }
        return Promise.resolve(agentTestFetch(url, init))
      }),
    )
    renderWorkspace('/agents/a1/skills')
    const option = await screen.findByRole('option', { name: /pr review/i })
    expect(option).toBeInTheDocument()
    // The image-only sections fall back to a hint instead of a picker.
    expect(screen.queryByRole('heading', { name: 'MCP Servers' })).not.toBeInTheDocument()
  })
  it('shows the Channel section edited in place for the agent inbox', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/agents/:id" element={<AgentWorkspace />}>
          <Route index element={<OverviewSection />} />
          <Route path=":section" element={<SectionRoute />} />
        </Route>
      </Routes>,
      { route: '/agents/a1' },
    )
    await within(await screen.findByTestId('overview-meta')).findByText('claude-opus-4-7')
    expect(screen.getByRole('link', { name: /^channel$/i })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('link', { name: /^channel$/i }))
    expect(await screen.findByText('agent inbox')).toBeInTheDocument()
    expect(screen.getByText('The inbox agent doc-writer drains')).toBeInTheDocument()
    // edited in place: the inbox timeline and the 24h reached count are on this section
    expect(screen.getByText('Inbox timeline')).toBeInTheDocument()
    expect(screen.getByText('Reached inbox 24h')).toBeInTheDocument()
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
    await waitFor(() => expect(screen.getByText('Inbox timeline')).toBeInTheDocument())
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
    // Overview-only content (the model in the header meta row) is shown; triggers are not loaded.
    await waitFor(() =>
      expect(
        within(screen.getByTestId('overview-meta')).getByText('claude-opus-4-7'),
      ).toBeInTheDocument(),
    )
    expect(screen.queryByText('on-doc-issue')).not.toBeInTheDocument()
  })
  it('remounts the section when the agent changes, so typed edits do not carry over', async () => {
    // Pre-seed a2 so the workspace keeps rendering the outlet during the switch;
    // without the key the a1 runtime form would stay mounted with its typed edit.
    // gcTime 0 would evict the seeded entry before the switch mounts it.
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: Infinity, staleTime: 0 } },
    })
    queryClient.setQueryData(['agents', 'detail', 'a2'], AGENTS.a2)
    renderWorkspace('/agents/a1/runtime', queryClient)
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
