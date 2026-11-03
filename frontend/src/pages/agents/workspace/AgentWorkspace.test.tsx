import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes, useLocation } from 'react-router-dom'
import { AgentWorkspace } from './AgentWorkspace'
import { SectionRoute } from './sectionPages'
import { OverviewSection } from '../AgentDetail'
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

function stubFetch() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (/\/agents\/a1(\?.*)?$/.test(url)) {
        return json({
          id: 'a1',
          name: 'doc-writer',
          llm: { model: 'claude-opus-4-7', provider: 'ollama-cloud' },
          replicas: 1,
          status: { ready: true, replicas: 1 },
        })
      }
      return json({ items: [], total: 0, page: 1, pageSize: 200, totalPages: 0 })
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
})
