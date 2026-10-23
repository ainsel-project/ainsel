// Loads the fixture plugin's real UI module (services/hub/internal/plugintest/hello)
// through the console runtime. vitest resolves the module's bare imports
// (react/jsx-runtime, @ainsel/plugin-sdk) to the console's own instances —
// the same objects installPluginHost exposes to the browser's import map.
import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { copyFileSync, mkdirSync, readFileSync } from 'fs'
import { dirname, join } from 'path'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import type { PluginManifest } from '@ainsel/plugin-sdk'
import { installPluginHost } from './host'
import { PluginSlot } from './PluginSlot'
import { PluginPage } from './PluginPage'
import type { ActivePlugin } from './registry'

const fixtureDir = join(process.cwd(), '..', 'services', 'hub', 'internal', 'plugintest', 'hello', 'static')
const manifest = JSON.parse(readFileSync(join(fixtureDir, 'ainsel-plugin.json'), 'utf-8')) as PluginManifest

// The module's bare imports must resolve to the console's packages, as the
// import map makes them in the browser; vite resolves bare imports relative to
// the importing file, so load a copy that sits inside the frontend package.
const moduleCopy = join(process.cwd(), 'node_modules', '.cache', 'fixture-plugin-hello', 'index.js')
mkdirSync(dirname(moduleCopy), { recursive: true })
copyFileSync(join(fixtureDir, 'ui', 'index.js'), moduleCopy)

vi.mock('./loader', () => ({
  loadPluginModule: vi.fn(() => import(/* @vite-ignore */ moduleCopy)),
}))
vi.mock('../api/plugins', () => ({ usePlugins: vi.fn(), usePluginCallerContext: vi.fn() }))
import { usePlugins, usePluginCallerContext } from '../api/plugins'

const plugin: ActivePlugin = { id: manifest.id, displayName: manifest.displayName, manifest }

function renderAt(route: string, ui: React.ReactElement = <div />) {
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

describe('fixture plugin in the console', () => {
  beforeAll(() => installPluginHost())

  beforeEach(() => {
    vi.mocked(usePlugins).mockReturnValue({
      data: [{ id: manifest.id, displayName: manifest.displayName, manifest }],
      isPending: false,
    } as unknown as ReturnType<typeof usePlugins>)
    vi.mocked(usePluginCallerContext).mockReturnValue({
      data: { isAdmin: false, sub: 'u1', username: 'ursula', groups: [] },
      isPending: false,
    } as unknown as ReturnType<typeof usePluginCallerContext>)
  })

  it('renders the agent.panel slot with the slot props', async () => {
    renderAt('/', <PluginSlot plugins={[plugin]} slot="agent.panel" props={{ agentName: 'reviewer' }} />)
    expect(await screen.findByText('Hello agent reviewer')).toBeInTheDocument()
  })

  it('renders the default page with the SDK context', async () => {
    renderAt('/p/hello')
    expect(await screen.findByText('Hello from hello')).toBeInTheDocument()
  })

  it('keeps a non-admin out of the admin page', async () => {
    renderAt('/p/hello/admin')
    expect(await screen.findByText('Dashboard')).toBeInTheDocument()
    expect(screen.queryByText('Hello admin')).not.toBeInTheDocument()
  })

  it('shows the admin page to an admin', async () => {
    vi.mocked(usePluginCallerContext).mockReturnValue({
      data: { isAdmin: true, sub: 'u0', username: 'admin', groups: [] },
      isPending: false,
    } as unknown as ReturnType<typeof usePluginCallerContext>)
    renderAt('/p/hello/admin')
    expect(await screen.findByText('Hello admin')).toBeInTheDocument()
  })
})
