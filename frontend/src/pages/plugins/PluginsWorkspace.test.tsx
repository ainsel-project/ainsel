import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { PluginsWorkspace } from './PluginsWorkspace'
import { pluginIdFromPath } from './pluginRoute'

function pluginsResponse(items: unknown[]) {
  return new Response(
    JSON.stringify({
      items,
      total: items.length,
      page: 1,
      pageSize: 200,
      totalPages: 1,
    }),
    { status: 200 },
  )
}

const memoryPlugin = {
  id: 'memory',
  displayName: 'Memory',
  ready: true,
  manifest: {
    id: 'memory',
    displayName: 'Memory',
    pluginApi: '1.1',
    ui: {
      entry: '/static/ui/index.js',
      pages: [
        { path: '/', label: 'Memories', section: 'Memory' },
        { path: '/setup', label: 'Setup', section: 'Memory', visibility: 'admin' },
      ],
    },
  },
}

const forgejoPlugin = {
  id: 'forgejo',
  displayName: 'Forgejo',
  ready: true,
  manifest: {
    id: 'forgejo',
    displayName: 'Forgejo',
    pluginApi: '1.1',
    ui: {
      entry: '/static/ui/index.js',
      pages: [{ path: '/work', label: 'Work', section: 'Forgejo' }],
    },
  },
}

const disabledPlugin = {
  id: 'aaa-disabled',
  displayName: 'Disabled One',
  ready: true,
  disabled: true,
  manifest: {
    id: 'aaa-disabled',
    displayName: 'Disabled One',
    pluginApi: '1.1',
    ui: { entry: '/static/ui/index.js', pages: [] },
  },
}

const notReadyPlugin = {
  id: 'beta',
  displayName: 'Not Ready',
  ready: false,
  manifest: {
    id: 'beta',
    displayName: 'Not Ready',
    pluginApi: '1.1',
    ui: { entry: '/static/ui/index.js', pages: [] },
  },
}

function stubPlugins(items: unknown[], isAdmin = false) {
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string) => {
      if (url.includes('plugin-context')) {
        return Promise.resolve(
          new Response(
            JSON.stringify({ isAdmin, sub: 'u', username: 'kim', groups: [] }),
            { status: 200 },
          ),
        )
      }
      return Promise.resolve(pluginsResponse(items))
    }),
  )
}

function renderWorkspace(route: string) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[route]}>
        <Routes>
          <Route element={<PluginsWorkspace />}>
            <Route path="/plugins" element={<div>Plugin list</div>} />
            <Route path="/plugins/:id" element={<div>Plugin detail</div>} />
            <Route path="/p/:pluginId/*" element={<div>Plugin page</div>} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  stubPlugins([memoryPlugin, forgejoPlugin, disabledPlugin, notReadyPlugin])
})
afterEach(() => {
  vi.unstubAllGlobals()
})

describe('pluginIdFromPath', () => {
  it('reads the id from a management path and a plugin page path', () => {
    expect(pluginIdFromPath('/plugins/memory')).toBe('memory')
    expect(pluginIdFromPath('/p/memory/setup')).toBe('memory')
    expect(pluginIdFromPath('/plugins/memory%2Fweird')).toBe('memory/weird')
  })

  it('has no plugin on the all-plugins list', () => {
    expect(pluginIdFromPath('/plugins')).toBeUndefined()
    expect(pluginIdFromPath('/dashboard')).toBeUndefined()
  })

  it('survives an undecodable segment instead of throwing', () => {
    expect(pluginIdFromPath('/plugins/%zz')).toBe('%zz')
  })
})

describe('PluginsWorkspace', () => {
  it('lists active plugins sorted by id, hiding disabled and not-ready ones', async () => {
    renderWorkspace('/plugins')
    await screen.findByRole('link', { name: /Memory/i })
    const names = Array.from(
      document.querySelectorAll('.plugin-sidebar-link .plugin-sidebar-name'),
    ).map((el) => el.textContent)
    // Sorted by id: forgejo before memory; the disabled and not-ready
    // plugins never appear.
    expect(names).toEqual(['Forgejo', 'Memory'])
  })

  it('filters the roster through the search box', async () => {
    const user = userEvent.setup()
    renderWorkspace('/plugins')
    await screen.findByRole('link', { name: /Memory/i })
    await user.type(screen.getByRole('searchbox', { name: /Search plugins/i }), 'for')
    expect(screen.getByRole('link', { name: /Forgejo/i })).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /Memory/i })).not.toBeInTheDocument()
  })

  it('shows an empty message when no plugin is active', async () => {
    stubPlugins([disabledPlugin, notReadyPlugin])
    renderWorkspace('/plugins')
    expect(await screen.findByText('No plugins.')).toBeInTheDocument()
  })

  it('links the roster to the plugin detail and offers a Manage link', async () => {
    renderWorkspace('/plugins')
    const memory = await screen.findByRole('link', { name: /Memory/i })
    expect(memory).toHaveAttribute('href', '/plugins/memory')
    expect(screen.getByRole('link', { name: /Manage plugins/i })).toHaveAttribute(
      'href',
      '/plugins',
    )
  })

  it('marks the current plugin active on its detail page', async () => {
    renderWorkspace('/plugins/memory')
    const memory = await screen.findByRole('link', { name: /Memory/i })
    expect(memory).toHaveAttribute('aria-current', 'page')
    const forgejo = screen.getByRole('link', { name: /Forgejo/i })
    expect(forgejo).not.toHaveAttribute('aria-current')
  })

  it('keeps the roster visible and the plugin active on plugin page deep links', async () => {
    renderWorkspace('/p/memory/setup')
    expect(await screen.findByText('Plugin page')).toBeInTheDocument()
    const memory = await screen.findByRole('link', { name: /Memory/i })
    expect(memory).toHaveAttribute('aria-current', 'page')
  })

  it('has no roster selection on the all-plugins list', async () => {
    renderWorkspace('/plugins')
    await screen.findByRole('link', { name: /Memory/i })
    expect(
      document.querySelector('.plugin-sidebar-link[aria-current]'),
    ).toBeNull()
  })
})
