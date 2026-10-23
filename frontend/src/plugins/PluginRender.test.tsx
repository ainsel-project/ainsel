import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { usePluginContext } from '@ainsel/plugin-sdk'
import { PluginSlot } from './PluginSlot'
import { PluginPage } from './PluginPage'
import type { ActivePlugin } from './registry'

vi.mock('./loader', () => ({ loadPluginModule: vi.fn() }))
vi.mock('../api/plugins', () => ({ usePlugins: vi.fn(), usePluginCallerContext: vi.fn() }))
import { loadPluginModule } from './loader'
import { usePlugins, usePluginCallerContext } from '../api/plugins'

const manifest = (id: string, extra: object = {}) => ({
  id,
  displayName: id.toUpperCase(),
  version: '1.0.0',
  pluginApi: '1.1',
  ui: {
    entry: '/ui/index.js',
    pages: [
      { path: '/', label: 'Home' },
      { path: '/memories', label: 'Memories', component: 'MemoriesPage' },
    ],
    slots: { 'agent.panel': 'AgentPanel' },
    ...extra,
  },
})
const active = (id: string): ActivePlugin => ({ id, displayName: id.toUpperCase(), manifest: manifest(id) })

function wrap(ui: React.ReactElement, route = '/') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[route]}>
        <Routes>
          <Route path="/p/:pluginId/*" element={<PluginPage />} />
          <Route path="/dashboard" element={<div>Dashboard</div>} />
          <Route path="*" element={ui} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

function PageWithContext({ label }: { label: string }) {
  const ctx = usePluginContext()
  return <p>{`${label} for ${ctx.pluginId}`}</p>
}

describe('plugin rendering', () => {
  beforeEach(() => {
    vi.mocked(loadPluginModule).mockReset()
    vi.spyOn(console, 'error').mockImplementation(() => {})
    vi.mocked(usePlugins).mockReturnValue({ data: [], isPending: false } as unknown as ReturnType<typeof usePlugins>)
    vi.mocked(usePluginCallerContext).mockReturnValue({
      data: { isAdmin: false, sub: 'u1', username: 'ursula', groups: [] },
      isPending: false,
    } as unknown as ReturnType<typeof usePluginCallerContext>)
  })

  it('renders slot components with exactly the given props, one failure isolated', async () => {
    vi.mocked(loadPluginModule).mockImplementation(async (p) => {
      if (p.id === 'broken') throw new Error('boom')
      return { AgentPanel: (props: object) => <p>{`${p.id} ${JSON.stringify(props)}`}</p> }
    })
    wrap(<PluginSlot plugins={[active('mem0'), active('broken')]} slot="agent.panel" props={{ agentName: 'reviewer' }} />)
    expect(await screen.findByText('mem0 {"agentName":"reviewer"}')).toBeInTheDocument()
    expect(await screen.findByText(/Plugin BROKEN failed to load/)).toBeInTheDocument()
  })

  it('renders a page by its component export with the SDK context', async () => {
    vi.mocked(usePlugins).mockReturnValue({
      data: [{ id: 'mem0', displayName: 'MEM0', manifest: manifest('mem0') }],
      isPending: false,
    } as unknown as ReturnType<typeof usePlugins>)
    vi.mocked(loadPluginModule).mockResolvedValue({
      default: () => <PageWithContext label="home" />,
      MemoriesPage: () => <PageWithContext label="memories" />,
    })
    wrap(<div />, '/p/mem0/memories')
    expect(await screen.findByText('memories for mem0')).toBeInTheDocument()
  })

  it('uses the default export when the page names no component', async () => {
    vi.mocked(usePlugins).mockReturnValue({
      data: [{ id: 'mem0', displayName: 'MEM0', manifest: manifest('mem0') }],
      isPending: false,
    } as unknown as ReturnType<typeof usePlugins>)
    vi.mocked(loadPluginModule).mockResolvedValue({ default: () => <PageWithContext label="home" /> })
    wrap(<div />, '/p/mem0')
    expect(await screen.findByText('home for mem0')).toBeInTheDocument()
  })

  it('waits for the plugin list instead of redirecting a deep link', () => {
    vi.mocked(usePlugins).mockReturnValue({ data: undefined, isPending: true } as unknown as ReturnType<typeof usePlugins>)
    wrap(<div />, '/p/mem0/memories')
    expect(screen.getByText('Loading plugin…')).toBeInTheDocument()
    expect(screen.queryByText('Dashboard')).not.toBeInTheDocument()
  })
})
