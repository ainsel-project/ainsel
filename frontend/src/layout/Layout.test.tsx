import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, vi } from 'vitest'
import { Layout } from './Layout'

vi.mock('../auth/AuthProvider', () => ({
  useAuth: vi.fn(() => ({
    token: 'test-token',
    user: { username: 'kim', email: 'kim@example.com' },
    ready: true,
    signinRedirect: vi.fn(),
    signoutRedirect: vi.fn(),
  })),
}))

function renderLayout(route = '/dashboard') {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[route]}>
        <Routes>
          <Route element={<Layout />}>
            <Route path="/dashboard" element={<div>Dashboard page</div>} />
            <Route path="/agents" element={<div>Agents page</div>} />
            <Route path="/agents/:id" element={<div>Agent workspace</div>} />
            <Route path="/agentsx" element={<div>Lookalike page</div>} />
            <Route path="/plugins" element={<div>Plugins page</div>} />
            <Route path="/plugins/:id" element={<div>Plugin detail</div>} />
            <Route path="/p/:pluginId/*" element={<div>Plugin page</div>} />
            <Route path="/pluginsx" element={<div>Lookalike page</div>} />
            <Route path="/profile" element={<div>Profile page</div>} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.stubGlobal(
    'fetch',
    vi.fn(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({ items: [], total: 0, page: 1, pageSize: 200, totalPages: 0 }),
          { status: 200 },
        ),
      ),
    ),
  )
})
afterEach(() => {
  vi.unstubAllGlobals()
})

const menuButton = () => screen.getByRole('button', { name: 'Menu' })

describe('Layout', () => {
  it('renders the mobile menu button in collapsed state', () => {
    renderLayout()
    expect(menuButton()).toHaveAttribute('aria-expanded', 'false')
    expect(document.querySelector('.spine')).not.toHaveClass('open')
    expect(document.querySelector('.spine-backdrop')).toBeNull()
  })

  it('opens the sidebar drawer when the menu button is clicked', async () => {
    const user = userEvent.setup()
    renderLayout()
    await user.click(menuButton())
    expect(menuButton()).toHaveAttribute('aria-expanded', 'true')
    expect(document.querySelector('.spine')).toHaveClass('open')
    expect(document.querySelector('.spine-backdrop')).toBeInTheDocument()
  })

  it('closes the drawer when the backdrop is clicked', async () => {
    const user = userEvent.setup()
    renderLayout()
    await user.click(menuButton())
    const backdrop = document.querySelector('.spine-backdrop') as HTMLElement
    await user.click(backdrop)
    expect(document.querySelector('.spine')).not.toHaveClass('open')
  })

  it('closes the drawer on Escape', async () => {
    const user = userEvent.setup()
    renderLayout()
    await user.click(menuButton())
    expect(document.querySelector('.spine')).toHaveClass('open')
    await user.keyboard('{Escape}')
    expect(document.querySelector('.spine')).not.toHaveClass('open')
  })

  it('closes the drawer after navigating to a page', async () => {
    const user = userEvent.setup()
    renderLayout()
    await user.click(menuButton())
    await user.click(screen.getByRole('link', { name: /Agents/i }))
    expect(await screen.findByText('Agents page')).toBeInTheDocument()
    expect(document.querySelector('.spine')).not.toHaveClass('open')
  })

  it('locks body scroll while the drawer is open and restores it', async () => {
    const user = userEvent.setup()
    renderLayout()
    await user.click(menuButton())
    expect(document.body.style.overflow).toBe('hidden')
    await user.keyboard('{Escape}')
    expect(document.body.style.overflow).toBe('')
  })

  it('collapses the spine to a rail inside the agent workspace', () => {
    renderLayout('/agents/a-1')
    const spine = document.querySelector('.spine')
    expect(spine).toHaveClass('rail')
    expect(screen.getByRole('link', { name: 'Agents' })).toHaveAttribute('aria-label', 'Agents')
  })

  it('keeps the full spine outside the agent workspace', () => {
    renderLayout('/dashboard')
    expect(document.querySelector('.spine')).not.toHaveClass('rail')
    expect(screen.getByText('Operations')).toBeInTheDocument()
  })

  it('narrows the shell grid to the rail inside the agent workspace', () => {
    renderLayout('/agents/a-1')
    // The .shell grid track must shrink with the 48px rail, otherwise the
    // canvas keeps a 236px left offset (see .shell.rail in Layout.css).
    expect(document.querySelector('.shell')).toHaveClass('rail')
  })

  it('keeps the full shell grid outside the agent workspace', () => {
    renderLayout('/dashboard')
    expect(document.querySelector('.shell')).not.toHaveClass('rail')
  })

  it('uses the rail on the agents list page', () => {
    renderLayout('/agents')
    expect(document.querySelector('.shell')).toHaveClass('rail')
  })

  it('does not use the rail for a path that only starts with /agents', () => {
    renderLayout('/agentsx')
    expect(document.querySelector('.shell')).not.toHaveClass('rail')
    expect(document.querySelector('.spine')).not.toHaveClass('rail')
  })

  it('uses the rail in the plugins workspace', () => {
    renderLayout('/plugins')
    expect(document.querySelector('.shell')).toHaveClass('rail')
    expect(document.querySelector('.spine')).toHaveClass('rail')
    renderLayout('/plugins/memory')
    expect(document.querySelector('.shell')).toHaveClass('rail')
    renderLayout('/p/memory/setup')
    expect(document.querySelector('.shell')).toHaveClass('rail')
  })

  it('does not use the rail for paths that only start with /plugins or /p', () => {
    renderLayout('/pluginsx')
    expect(document.querySelector('.shell')).not.toHaveClass('rail')
    expect(document.querySelector('.spine')).not.toHaveClass('rail')
    renderLayout('/profile')
    expect(document.querySelector('.shell')).not.toHaveClass('rail')
    expect(document.querySelector('.spine')).not.toHaveClass('rail')
  })
})
