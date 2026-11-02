import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
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
    return new Response(
      JSON.stringify({ items: [defaultPlugin], total: 1, page: 1, pageSize: 200, totalPages: 1 }),
      { status: 200 },
    )
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
            ? new Response(
                JSON.stringify({
                  items: [approved],
                  total: 1,
                  page: 1,
                  pageSize: 200,
                  totalPages: 1,
                }),
                { status: 200 },
              )
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

  function renderPlugin(overrides: Partial<typeof defaultPlugin>) {
    const plugin = { ...defaultPlugin, ...overrides }
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) =>
        Promise.resolve(
          url.includes('/plugins') && !url.includes('plugin-context')
            ? new Response(
                JSON.stringify({
                  items: [plugin],
                  total: 1,
                  page: 1,
                  pageSize: 200,
                  totalPages: 1,
                }),
                { status: 200 },
              )
            : defaultFetch(url),
        ),
      ),
    )
    return renderWithProviders(
      <Routes>
        <Route path="/plugins/:id" element={<PluginDetail />} />
      </Routes>,
      { route: '/plugins/plugin1' },
    )
  }

  function rowFor(name: string): HTMLElement {
    const cell = screen.getByText(name)
    const row = cell.closest('tr')
    if (!row) throw new Error(`no row for ${name}`)
    return row as HTMLElement
  }

  it('marks a pending permission as declared but not approved', async () => {
    // defaultPlugin: declared perm1+perm2, approved perm1 only
    renderPlugin({})
    await waitFor(() => expect(screen.getByText('Test Plugin')).toBeInTheDocument())

    const pending = rowFor('perm2')
    expect(within(pending).getByLabelText('Declared')).toBeInTheDocument()
    expect(within(pending).queryByLabelText('Approved')).not.toBeInTheDocument()

    const live = rowFor('perm1')
    expect(within(live).getByLabelText('Declared')).toBeInTheDocument()
    expect(within(live).getByLabelText('Approved')).toBeInTheDocument()
  })

  it('renders a stale grant (approved but no longer declared)', async () => {
    renderPlugin({ declaredPermissions: ['perm1'], approvedPermissions: ['perm1', 'stale-perm'] })
    await waitFor(() => expect(screen.getByText('Test Plugin')).toBeInTheDocument())

    const stale = rowFor('stale-perm')
    expect(within(stale).queryByLabelText('Declared')).not.toBeInTheDocument()
    expect(within(stale).getByLabelText('Approved')).toBeInTheDocument()
  })

  it('shows the empty label when no permissions exist', async () => {
    renderPlugin({ declaredPermissions: [], approvedPermissions: [] })
    await waitFor(() => expect(screen.getByText('Test Plugin')).toBeInTheDocument())
    expect(screen.getByText('None')).toBeInTheDocument()
  })

  it('expands a known permission to show its catalog description', async () => {
    renderPlugin({
      declaredPermissions: ['agents:read'],
      approvedPermissions: ['agents:read'],
    })
    await waitFor(() => expect(screen.getByText('Test Plugin')).toBeInTheDocument())

    const row = rowFor('agents:read')
    const toggle = within(row).getByRole('button', { name: /show details/i })
    await userEvent.click(toggle)

    expect(screen.getByText(/Grants access to/)).toBeInTheDocument()
    expect(screen.getByText('/api/v1/agents')).toBeInTheDocument()
  })

  it('does not offer expansion for unknown permission names', async () => {
    renderPlugin({})
    await waitFor(() => expect(screen.getByText('Test Plugin')).toBeInTheDocument())

    const row = rowFor('perm1')
    expect(within(row).queryByRole('button', { name: /details/i })).not.toBeInTheDocument()
  })

  it('shows the merged permission table in the approve confirm modal', async () => {
    renderPlugin({})
    await waitFor(() => expect(screen.getByText('Test Plugin')).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: /approve permissions/i }))

    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('Approve the following permissions for this plugin?'))
      .toBeInTheDocument()
    // Merged table over declared + approved: both permissions listed, no expansion in the modal.
    expect(within(dialog).getByText('perm1')).toBeInTheDocument()
    expect(within(dialog).getByText('perm2')).toBeInTheDocument()
    expect(within(dialog).queryByRole('button', { name: /details/i })).not.toBeInTheDocument()
  })
})
