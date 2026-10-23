import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes } from 'react-router-dom'
import { PersonaDetail } from './PersonaDetail'
import { renderWithProviders } from '../../test/renderWithProviders'

// Mock usePlugins to return a mem0 plugin with all four slots
vi.mock('../../api/plugins', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/plugins')>()
  return {
    ...actual,
    usePlugins: () => ({
      data: [
        {
          id: 'mem0',
          displayName: 'Mem0',
          ready: true,
          disabled: false,
          manifest: {
            id: 'mem0',
            displayName: 'Mem0',
            version: '1.0.0',
            pluginApi: '1.1',
            ui: {
              entry: '/ui/index.js',
              slots: {
                'agent.panel': 'AgentPanel',
                'persona.panel': 'PersonaPanel',
                'observability.section': 'ObsSection',
                'invocation.panel': 'RunPanel',
              },
            },
          },
        },
      ],
      isPending: false,
    }),
  }
})

// Mock loadPluginModule to return components that render their props
vi.mock('../../plugins/loader', () => {
  return {
    loadPluginModule: vi.fn(async () => ({
      AgentPanel: ({ agentName }: { agentName: string }) => (
        <p>{`agentName=${agentName}`}</p>
      ),
      PersonaPanel: ({ personaId }: { personaId: string }) => (
        <p>{`personaId=${personaId}`}</p>
      ),
      ObsSection: ({ range }: { range: string }) => <p>{`range=${range}`}</p>,
      RunPanel: ({ invocationId, agentName }: { invocationId: string; agentName: string }) => (
        <p>{`invocationId=${invocationId} agentName=${agentName}`}</p>
      ),
    })),
  }
})

describe('PersonaDetail', () => {
  beforeEach(() => {
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => {
        if (url.match(/\/api\/v1\/personas\/01HX1$/)) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                id: '01HX1',
                name: 'code-reviewer',
                description: 'reviews PRs',
                currentVersion: 2,
                text: '# Persona\n\nYou are a thorough code reviewer.',
                createdAt: '2026-05-01T00:00:00Z',
                updatedAt: '2026-05-02T00:00:00Z',
              }),
              { status: 200 },
            ),
          )
        }
        if (url.match(/\/api\/v1\/personas\/01HX1\/versions(\?|$)/)) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                items: [
                  {
                    personaId: '01HX1',
                    versionNumber: 2,
                    createdAt: '2026-05-02T00:00:00Z',
                  },
                  {
                    personaId: '01HX1',
                    versionNumber: 1,
                    createdAt: '2026-05-01T00:00:00Z',
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
        if (url.includes('/agents')) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                items: [
                  {
                    id: 'a1',
                    name: 'code-reviewer-agent',
                    persona: { id: '01HX1' },
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
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders persona identity and markdown', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/personas/:id" element={<PersonaDetail />} />
      </Routes>,
      { route: '/personas/01HX1' },
    )
    await waitFor(() =>
      expect(screen.getAllByText('code-reviewer')[0]).toBeInTheDocument(),
    )
    expect(screen.getByText(/thorough code reviewer/i)).toBeInTheDocument()
  })

  it('lists referring agents', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/personas/:id" element={<PersonaDetail />} />
      </Routes>,
      { route: '/personas/01HX1' },
    )
    await waitFor(() =>
      expect(screen.getByText('code-reviewer-agent')).toBeInTheDocument(),
    )
  })

  it('shows the Edit action', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/personas/:id" element={<PersonaDetail />} />
      </Routes>,
      { route: '/personas/01HX1' },
    )
    expect(await screen.findByRole('button', { name: /^edit$/i })).toBeInTheDocument()
  })

  it('renders the real version history on the History tab', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/personas/:id" element={<PersonaDetail />} />
      </Routes>,
      { route: '/personas/01HX1' },
    )
    await waitFor(() =>
      expect(screen.getAllByText('code-reviewer')[0]).toBeInTheDocument(),
    )

    // The History tab replaces the old "coming soon" placeholder.
    expect(screen.queryByText(/coming soon/i)).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('tab', { name: /^history$/i }))

    expect(await screen.findByText('v2')).toBeInTheDocument()
    expect(screen.getByText('v1')).toBeInTheDocument()
    expect(screen.getByText('current')).toBeInTheDocument()
    expect(screen.queryByText(/coming soon/i)).not.toBeInTheDocument()
  })

  it('does not fetch version history until the History tab is opened', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/personas/:id" element={<PersonaDetail />} />
      </Routes>,
      { route: '/personas/01HX1' },
    )
    await waitFor(() =>
      expect(screen.getAllByText('code-reviewer')[0]).toBeInTheDocument(),
    )
    expect(
      vi.mocked(fetch).mock.calls.some((c) => String(c[0]).includes('/versions')),
    ).toBe(false)

    await userEvent.click(screen.getByRole('tab', { name: /^history$/i }))
    await screen.findByText('v2')
    expect(
      vi.mocked(fetch).mock.calls.some((c) => String(c[0]).includes('/versions')),
    ).toBe(true)
  })

  describe('plugin slots', () => {
    it('adds a Mem0 tab and renders the plugin when selected', async () => {
      renderWithProviders(
        <Routes>
          <Route path="/personas/:id" element={<PersonaDetail />} />
        </Routes>,
        { route: '/personas/01HX1' },
      )
      await screen.findAllByText('code-reviewer')
      expect(screen.getByRole('tab', { name: /mem0/i })).toBeInTheDocument()

      await userEvent.click(screen.getByRole('tab', { name: /mem0/i }))
      await waitFor(() =>
        expect(screen.getByText('personaId=01HX1')).toBeInTheDocument(),
      )
    })
  })
})
