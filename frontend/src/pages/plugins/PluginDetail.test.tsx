import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes } from 'react-router-dom'
import { PluginDetail } from './PluginDetail'
import { renderWithProviders } from '../../test/renderWithProviders'

const mockFetch = () => globalThis.fetch as ReturnType<typeof vi.fn>

const defaultPlugin = {
  id: 'plugin1',
  displayName: 'Test Plugin',
  version: '1.0.0',
  ready: true,
  disabled: false,
  declaredPermissions: ['perm1', 'perm2'],
  approvedPermissions: ['perm1'],
  groupAccess: [],
  message: 'Plugin is ready',
  signature: {
    verified: true,
    signer: 'test-signer',
    checkedAt: '2024-01-01T00:00:00Z',
  },
  imageRepository: 'example.com/plugin',
  imageDigest: 'sha256:abc123',
}

function defaultFetch(url: string): Response {
  if (url.includes('/plugin-context')) {
    return new Response(
      JSON.stringify({
        isAdmin: true,
        sub: 'user1',
        username: 'admin',
        groups: [],
      }),
      { status: 200 },
    )
  }
  if (url.includes('/groups') && !url.includes('group-access')) {
    return new Response(
      JSON.stringify([
        { id: 'group1', name: 'Group 1', description: '', createdAt: '', updatedAt: '' },
        { id: 'group2', name: 'Group 2', description: '', createdAt: '', updatedAt: '' },
      ]),
      { status: 200 },
    )
  }
  if (url.includes('/plugins')) {
    return new Response(JSON.stringify({ items: [defaultPlugin] }), { status: 200 })
  }
  return new Response('{}', { status: 200 })
}

describe('PluginDetail', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn((url: string) => Promise.resolve(defaultFetch(url))))
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('approves permissions with exact declared set in confirm modal', async () => {
    let approveRequestBody: unknown = null
    mockFetch().mockImplementation((url: string, options?: RequestInit) => {
      if (url.includes('approve-permissions')) {
        approveRequestBody = options?.body
          ? JSON.parse(options.body as string)
          : null
        return Promise.resolve(new Response('{}', { status: 200 }))
      }
      return Promise.resolve(defaultFetch(url))
    })

    renderWithProviders(
      <Routes>
        <Route path="/plugins/:id" element={<PluginDetail />} />
      </Routes>,
      { route: '/plugins/plugin1' },
    )
    await waitFor(() => expect(screen.getByText('Test Plugin')).toBeInTheDocument())

    const approveButton = screen.getByRole('button', { name: /approve permissions/i })
    await userEvent.click(approveButton)

    // Find and click the approve button in the modal
    const approveModalButton = await screen.findByRole('button', { name: /^Approve$/i })
    await userEvent.click(approveModalButton)

    await waitFor(() => {
      expect(approveRequestBody).toEqual({
        permissions: ['perm1', 'perm2'],
      })
    })
  })

  it('shows uninstall confirm modal with required text', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/plugins/:id" element={<PluginDetail />} />
      </Routes>,
      { route: '/plugins/plugin1' },
    )
    await waitFor(() => expect(screen.getByText('Test Plugin')).toBeInTheDocument())

    const uninstallButton = screen.getByRole('button', { name: /uninstall/i })
    await userEvent.click(uninstallButton)

    await waitFor(() => {
      expect(
        screen.getByText(/Agents, triggers, connectors and other resources this plugin created stay after uninstall/),
      ).toBeInTheDocument()
    })
  })

  it('saves group access with chosen ids', async () => {
    let groupAccessRequestBody: unknown = null
    mockFetch().mockImplementation((url: string, options?: RequestInit) => {
      if (url.includes('group-access')) {
        groupAccessRequestBody = options?.body
          ? JSON.parse(options.body as string)
          : null
        return Promise.resolve(new Response('{}', { status: 200 }))
      }
      return Promise.resolve(defaultFetch(url))
    })

    renderWithProviders(
      <Routes>
        <Route path="/plugins/:id" element={<PluginDetail />} />
      </Routes>,
      { route: '/plugins/plugin1' },
    )
    await waitFor(() => expect(screen.getByText('Test Plugin')).toBeInTheDocument())

    // Find the Edit groups button and click it to open the editor
    const editGroupsButton = screen.getByRole('button', { name: /edit groups/i })
    await userEvent.click(editGroupsButton)

    // The dual list picker will have an available pane and an enabled pane
    // We need to find group1 in the available pane and move it to enabled
    const group1Item = await screen.findByText('Group 1')
    await userEvent.click(group1Item)

    // Click the arrow button to move it to the enabled pane
    const moveButton = screen.getByRole('button', { name: /add selected/i })
    await userEvent.click(moveButton)

    // Now click the save button to save the group access
    const saveButton = screen.getByRole('button', { name: /save/i })
    await userEvent.click(saveButton)

    await waitFor(() => {
      expect(groupAccessRequestBody).toEqual({
        groups: ['group1'],
      })
    })
  })

  it('keeps showing approved permissions without an approve button', async () => {
    const approved = { ...defaultPlugin, approvedPermissions: ['perm1', 'perm2'] }
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) =>
        Promise.resolve(
          url.includes('/plugins') && !url.includes('plugin-context')
            ? new Response(JSON.stringify({ items: [approved] }), { status: 200 })
            : defaultFetch(url),
        ),
      ),
    )
    renderWithProviders(
      <Routes>
        <Route path="/plugins/:id" element={<PluginDetail />} />
      </Routes>,
      { route: '/plugins/plugin1' },
    )
    await waitFor(() => expect(screen.getByText('Test Plugin')).toBeInTheDocument())
    expect(screen.getAllByText('perm2').length).toBeGreaterThan(0)
    expect(screen.queryByRole('button', { name: 'Approve permissions' })).not.toBeInTheDocument()
  })
})
