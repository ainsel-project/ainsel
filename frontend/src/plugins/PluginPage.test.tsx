import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { PluginPage } from './PluginPage'

const mockQueryFetch = vi.fn()

type Page = { path: string; label: string; section?: string; visibility?: 'admin' }

function mem0(pages: Page[]) {
  return {
    id: 'mem0',
    displayName: 'Mem0',
    ready: true,
    manifest: {
      id: 'mem0',
      displayName: 'Mem0',
      pluginApi: '1.1',
      ui: { entry: '/static/ui/index.js', pages },
    },
  }
}

// mockHub answers the two calls PluginPage makes: the plugin list, in the
// hub's paginated envelope, and the caller context.
function mockHub(plugins: unknown[], isAdmin = false) {
  mockQueryFetch.mockImplementation((url: string) => {
    if (url.includes('plugin-context')) {
      return Promise.resolve(
        new Response(
          JSON.stringify({ isAdmin, sub: 'user-1', username: 'testuser', groups: [] }),
          { status: 200 },
        ),
      )
    }
    if (url.includes('/plugins')) {
      return Promise.resolve(
        new Response(
          JSON.stringify({ items: plugins, total: plugins.length, page: 1, pageSize: 200, totalPages: 1 }),
          { status: 200 },
        ),
      )
    }
    return Promise.reject(new Error('Not found'))
  })
}

function renderPluginPage(initialRoute = '/p/mem0') {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  })

  vi.stubGlobal('fetch', mockQueryFetch)

  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[initialRoute]}>
        <Routes>
          <Route path="/p/:pluginId/*" element={<PluginPage />} />
          <Route path="/dashboard" element={<div>Dashboard</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('PluginPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockQueryFetch.mockReset()
  })

  it('frames the page with a title block naming its section and label', async () => {
    mockHub([mem0([{ path: '/', label: 'Memories', section: 'Mem0 Tools' }])])

    renderPluginPage('/p/mem0')

    const heading = await screen.findByRole('heading', { level: 2, name: 'Memories' })
    const header = heading.closest('header')!
    expect(header).toHaveClass('titleblock')
    expect(header).toHaveTextContent('Mem0 Tools / Memories')
  })

  it('falls back to the plugin name in the crumbs when the page has no section', async () => {
    mockHub([mem0([{ path: '/', label: 'Home' }])])

    renderPluginPage('/p/mem0')

    const heading = await screen.findByRole('heading', { level: 2, name: 'Home' })
    expect(heading.closest('header')).toHaveTextContent('Mem0 / Home')
  })

  it('redirects to dashboard when plugin is not found', async () => {
    mockHub([mem0([{ path: '/', label: 'Home' }])])

    renderPluginPage('/p/unknown-plugin')

    await waitFor(() => {
      expect(screen.getByText('Dashboard')).toBeInTheDocument()
    }, { timeout: 3000 })
  })

  it('redirects to dashboard when page is not found', async () => {
    mockHub([mem0([{ path: '/', label: 'Home' }])])

    renderPluginPage('/p/mem0/unknown-page')

    await waitFor(() => {
      expect(screen.getByText('Dashboard')).toBeInTheDocument()
    }, { timeout: 3000 })
  })

  it('redirects to dashboard when admin page accessed by non-admin', async () => {
    mockHub([mem0([{ path: '/admin', label: 'Admin', visibility: 'admin' }])])

    renderPluginPage('/p/mem0/admin')

    await waitFor(() => {
      expect(screen.getByText('Dashboard')).toBeInTheDocument()
    }, { timeout: 3000 })
  })
})
