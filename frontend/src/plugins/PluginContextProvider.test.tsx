import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { usePluginContext } from '@ainsel/plugin-sdk'
import { PluginContextProvider } from './PluginContextProvider'
import type { ActivePlugin } from './registry'

const mockPlugin = (): ActivePlugin => ({
  id: 'mem0',
  displayName: 'Mem0',
  manifest: {
    id: 'mem0',
    displayName: 'Mem0',
    version: '1.0.0',
    pluginApi: '1.1',
    ui: {
      entry: '/static/ui/index.js',
    },
  },
})

function TestComponent() {
  const ctx = usePluginContext()
  return (
    <div>
      <div data-testid="plugin-id">{ctx.pluginId}</div>
      <div data-testid="username">{ctx.user.username}</div>
      <div data-testid="is-admin">{ctx.user.isAdmin ? 'admin' : 'user'}</div>
    </div>
  )
}

function renderWithContext(plugin: ActivePlugin, contextData?: unknown) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  })

  // Mock fetch for plugin-context
  const mockFetch = vi.fn((url: string) => {
    if (url.includes('plugin-context')) {
      return Promise.resolve(
        new Response(
          JSON.stringify(
            contextData || {
              isAdmin: false,
              sub: 'user-123',
              username: 'testuser',
              groups: [],
            },
          ),
          { status: 200 },
        ),
      )
    }
    return Promise.reject(new Error(`Unexpected fetch: ${url}`))
  })

  vi.stubGlobal('fetch', mockFetch)

  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <PluginContextProvider plugin={plugin}>
          <TestComponent />
        </PluginContextProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('PluginContextProvider', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    document.documentElement.dataset.theme = 'light'
  })

  it('provides plugin id in context', async () => {
    const plugin = mockPlugin()
    renderWithContext(plugin)

    await waitFor(() => {
      expect(screen.getByTestId('plugin-id')).toHaveTextContent('mem0')
    })
  })

  it('provides user info from plugin-context endpoint', async () => {
    const plugin = mockPlugin()
    renderWithContext(plugin, {
      isAdmin: false,
      sub: 'user-123',
      username: 'john',
      groups: [],
    })

    await waitFor(() => {
      expect(screen.getByTestId('username')).toHaveTextContent('john')
      expect(screen.getByTestId('is-admin')).toHaveTextContent('user')
    })
  })

  it('provides admin status', async () => {
    const plugin = mockPlugin()
    renderWithContext(plugin, {
      isAdmin: true,
      sub: 'user-123',
      username: 'admin',
      groups: [],
    })

    await waitFor(() => {
      expect(screen.getByTestId('is-admin')).toHaveTextContent('admin')
    })
  })
})
