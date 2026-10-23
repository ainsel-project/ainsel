import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { PluginPage } from './PluginPage'

const mockQueryFetch = vi.fn()

function renderPluginPage(initialRoute = '/p/mem0') {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  })

  // Mock the fetch for API calls
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

  it('redirects to dashboard when plugin is not found', async () => {
    mockQueryFetch.mockImplementation((url: string) => {
      if (url.includes('/plugins')) {
        return Promise.resolve(
          new Response(JSON.stringify([]), { status: 200 }),
        )
      }
      if (url.includes('plugin-context')) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              isAdmin: false,
              sub: 'user-1',
              username: 'testuser',
              groups: [],
            }),
            { status: 200 },
          ),
        )
      }
      return Promise.reject(new Error('Not found'))
    })

    renderPluginPage('/p/unknown-plugin')

    await waitFor(() => {
      expect(screen.getByText('Dashboard')).toBeInTheDocument()
    }, { timeout: 3000 })
  })

  it('redirects to dashboard when page is not found', async () => {
    mockQueryFetch.mockImplementation((url: string) => {
      if (url.includes('/plugins')) {
        return Promise.resolve(
          new Response(
            JSON.stringify([
              {
                id: 'mem0',
                displayName: 'Mem0',
                manifest: {
                  id: 'mem0',
                  displayName: 'Mem0',
                  pluginApi: '1.1',
                  ui: {
                    entry: '/static/ui/index.js',
                    pages: [{ path: '/', label: 'Home' }],
                  },
                },
              },
            ]),
            { status: 200 },
          ),
        )
      }
      if (url.includes('plugin-context')) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              isAdmin: false,
              sub: 'user-1',
              username: 'testuser',
              groups: [],
            }),
            { status: 200 },
          ),
        )
      }
      return Promise.reject(new Error('Not found'))
    })

    renderPluginPage('/p/mem0/unknown-page')

    await waitFor(() => {
      expect(screen.getByText('Dashboard')).toBeInTheDocument()
    }, { timeout: 3000 })
  })

  it('redirects to dashboard when admin page accessed by non-admin', async () => {
    mockQueryFetch.mockImplementation((url: string) => {
      if (url.includes('/plugins')) {
        return Promise.resolve(
          new Response(
            JSON.stringify([
              {
                id: 'mem0',
                displayName: 'Mem0',
                manifest: {
                  id: 'mem0',
                  displayName: 'Mem0',
                  pluginApi: '1.1',
                  ui: {
                    entry: '/static/ui/index.js',
                    pages: [
                      {
                        path: '/admin',
                        label: 'Admin',
                        visibility: 'admin' as const,
                      },
                    ],
                  },
                },
              },
            ]),
            { status: 200 },
          ),
        )
      }
      if (url.includes('plugin-context')) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              isAdmin: false,
              sub: 'user-1',
              username: 'testuser',
              groups: [],
            }),
            { status: 200 },
          ),
        )
      }
      return Promise.reject(new Error('Not found'))
    })

    renderPluginPage('/p/mem0/admin')

    await waitFor(() => {
      expect(screen.getByText('Dashboard')).toBeInTheDocument()
    }, { timeout: 3000 })
  })

})
