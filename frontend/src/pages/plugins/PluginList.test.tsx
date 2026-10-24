import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PluginList } from './PluginList'
import { renderWithProviders } from '../../test/renderWithProviders'

const mockFetch = () => globalThis.fetch as ReturnType<typeof vi.fn>

describe('PluginList', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders a ready plugin with Ready status', async () => {
    mockFetch().mockImplementation((url: string) => {
      if (url.includes('/plugin-context')) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              isAdmin: true,
              sub: 'user1',
              username: 'admin',
              groups: [],
            }),
            { status: 200 },
          ),
        )
      }
      if (url.includes('/plugins')) {
        return Promise.resolve(
          new Response(
            JSON.stringify({ items: [
              {
                id: 'plugin1',
                displayName: 'Test Plugin',
                version: '1.0.0',
                ready: true,
                disabled: false,
                declaredPermissions: [],
                approvedPermissions: [],
              },
            ], total: 1, page: 1, pageSize: 200, totalPages: 1 }),
            { status: 200 },
          ),
        )
      }
      return Promise.resolve(new Response('{}', { status: 200 }))
    })

    renderWithProviders(<PluginList />, { route: '/plugins' })
    await waitFor(() => expect(screen.getByText('Test Plugin')).toBeInTheDocument())
    expect(screen.getByText('Ready')).toBeInTheDocument()
  })

  it('renders a disabled plugin with Disabled status', async () => {
    mockFetch().mockImplementation((url: string) => {
      if (url.includes('/plugin-context')) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              isAdmin: true,
              sub: 'user1',
              username: 'admin',
              groups: [],
            }),
            { status: 200 },
          ),
        )
      }
      if (url.includes('/plugins')) {
        return Promise.resolve(
          new Response(
            JSON.stringify({ items: [
              {
                id: 'plugin2',
                displayName: 'Disabled Plugin',
                version: '1.0.0',
                ready: true,
                disabled: true,
                declaredPermissions: [],
                approvedPermissions: [],
              },
            ], total: 1, page: 1, pageSize: 200, totalPages: 1 }),
            { status: 200 },
          ),
        )
      }
      return Promise.resolve(new Response('{}', { status: 200 }))
    })

    renderWithProviders(<PluginList />, { route: '/plugins' })
    await waitFor(() => expect(screen.getByText('Disabled Plugin')).toBeInTheDocument())
    expect(screen.getByText('Disabled')).toBeInTheDocument()
  })

  it('shows a notice when user is not admin', async () => {
    mockFetch().mockImplementation((url: string) => {
      if (url.includes('/plugin-context')) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              isAdmin: false,
              sub: 'user1',
              username: 'user',
              groups: [],
            }),
            { status: 200 },
          ),
        )
      }
      return Promise.resolve(new Response('{}', { status: 200 }))
    })

    renderWithProviders(<PluginList />, { route: '/plugins' })
    await waitFor(() =>
      expect(screen.getByText('Plugins are managed by administrators.')).toBeInTheDocument(),
    )
  })

  it('disables a plugin and invalidates the plugins query', async () => {
    let fetchCallCount = 0
    mockFetch().mockImplementation((url: string) => {
      if (url.includes('/plugin-context')) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              isAdmin: true,
              sub: 'user1',
              username: 'admin',
              groups: [],
            }),
            { status: 200 },
          ),
        )
      }
      if (url.includes('/plugins') && url.includes('plugin1/disable')) {
        return Promise.resolve(new Response('{}', { status: 200 }))
      }
      if (url.includes('/plugins')) {
        fetchCallCount++
        return Promise.resolve(
          new Response(
            JSON.stringify({ items: [
              {
                id: 'plugin1',
                displayName: 'Test Plugin',
                version: '1.0.0',
                ready: true,
                disabled: false,
                declaredPermissions: [],
                approvedPermissions: [],
              },
            ], total: 1, page: 1, pageSize: 200, totalPages: 1 }),
            { status: 200 },
          ),
        )
      }
      return Promise.resolve(new Response('{}', { status: 200 }))
    })

    renderWithProviders(<PluginList />, { route: '/plugins' })
    await waitFor(() => expect(screen.getByText('Test Plugin')).toBeInTheDocument())

    const disableButton = screen.getByRole('button', { name: /disable/i })
    await userEvent.click(disableButton)

    // After disable, the list should refetch (queryKey ['plugins'] should be invalidated)
    await waitFor(() => {
      expect(fetchCallCount).toBeGreaterThan(1)
    })
  })
})
