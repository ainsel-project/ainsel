import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes } from 'react-router-dom'
import { PluginDetail } from './PluginDetail'
import { renderWithProviders } from '../../test/renderWithProviders'

const mockFetch = () => globalThis.fetch as ReturnType<typeof vi.fn>

function panelByTitle(title: string): HTMLElement {
  const heading = screen.getByRole('heading', { name: title })
  const panel = heading.closest('.panel')
  if (!panel) throw new Error(`Panel not found for title: ${title}`)
  return panel as HTMLElement
}

function metadataCard(): HTMLElement {
  const panels = document.querySelectorAll<HTMLElement>('.panel')
  const card = [...panels].find(
    (p) => !p.querySelector('.panel-header') && p.querySelector('.info-grid'),
  )
  if (!card) throw new Error('Metadata card panel not found')
  return card
}

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
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => Promise.resolve(defaultFetch(url))),
    )
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('approves permissions with exact declared set in confirm modal', async () => {
    let approveRequestBody: unknown = null
    mockFetch().mockImplementation((url: string, options?: RequestInit) => {
      if (url.includes('approve-permissions')) {
        approveRequestBody = options?.body ? JSON.parse(options.body as string) : null
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
        screen.getByText(
          /Agents, triggers, connectors and other resources this plugin created stay after uninstall/,
        ),
      ).toBeInTheDocument()
    })
  })

  it('saves group access with chosen ids', async () => {
    let groupAccessRequestBody: unknown = null
    mockFetch().mockImplementation((url: string, options?: RequestInit) => {
      if (url.includes('group-access')) {
        groupAccessRequestBody = options?.body ? JSON.parse(options.body as string) : null
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

  it('shows granted groups in read mode without entering edit mode', async () => {
    const withGroups = { ...defaultPlugin, groupAccess: ['group1'] }
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) =>
        Promise.resolve(
          url.includes('/groups') && !url.includes('group-access')
            ? new Response(
                JSON.stringify([
                  {
                    id: 'group1',
                    name: 'Group 1',
                    description: 'Ops group',
                    createdAt: '',
                    updatedAt: '',
                  },
                  {
                    id: 'group2',
                    name: 'Group 2',
                    description: '',
                    createdAt: '',
                    updatedAt: '',
                  },
                ]),
                { status: 200 },
              )
            : url.includes('/plugins')
              ? new Response(
                  JSON.stringify({
                    items: [withGroups],
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

    // Group name and description are visible in read mode, without clicking Edit groups
    const groupName = await screen.findByText('Group 1')
    expect(groupName).toBeInTheDocument()
    expect(screen.getByText('Ops group')).toBeInTheDocument()
    expect(groupName.closest('a')).toHaveAttribute('href', '/groups/group1')
    expect(screen.queryByText('No groups have access')).not.toBeInTheDocument()
  })

  it('falls back to the raw id for groups that no longer exist', async () => {
    const withDeletedGroup = { ...defaultPlugin, groupAccess: ['deleted-group'] }
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) =>
        Promise.resolve(
          url.includes('/plugins') && !url.includes('plugin-context')
            ? new Response(
                JSON.stringify({
                  items: [withDeletedGroup],
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

    const rawId = await screen.findByText('deleted-group')
    expect(rawId).toBeInTheDocument()
    expect(rawId.closest('a')).toBeNull()
  })

  it('shows a read-mode empty state when no groups have access', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/plugins/:id" element={<PluginDetail />} />
      </Routes>,
      { route: '/plugins/plugin1' },
    )
    await waitFor(() => expect(screen.getByText('Test Plugin')).toBeInTheDocument())

    expect(await screen.findByText('No groups have access')).toBeInTheDocument()
    expect(screen.queryByText('No groups selected')).not.toBeInTheDocument()
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

  it('metadata card shows the image address with digest', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/plugins/:id" element={<PluginDetail />} />
      </Routes>,
      { route: '/plugins/plugin1' },
    )
    await waitFor(() => expect(screen.getByText('Test Plugin')).toBeInTheDocument())

    expect(
      within(metadataCard()).getByText('example.com/plugin@sha256:abc123'),
    ).toBeInTheDocument()
  })

  it('metadata card shows an em-dash fallback when the image repository is missing', async () => {
    const noImage = { ...defaultPlugin, imageRepository: undefined, imageDigest: undefined }
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) =>
        Promise.resolve(
          url.includes('/plugins') && !url.includes('plugin-context')
            ? new Response(
                JSON.stringify({
                  items: [noImage],
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

    expect(within(metadataCard()).getByText('—')).toBeInTheDocument()
  })

  it('metadata card shows Enabled and disables the plugin', async () => {
    let disableUrl: string | null = null
    mockFetch().mockImplementation((url: string) => {
      if (url.includes('/plugins/plugin1/disable')) {
        disableUrl = url
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

    const card = metadataCard()
    expect(card.querySelector('.v .tag.ok')).toHaveTextContent('Enabled')

    await userEvent.click(within(card).getByRole('button', { name: 'Disable' }))
    await waitFor(() => expect(disableUrl).toContain('/plugins/plugin1/disable'))
  })

  it('metadata card shows Disabled and enables the plugin', async () => {
    const disabledPlugin = { ...defaultPlugin, disabled: true, ready: false }
    let enableUrl: string | null = null
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => {
        if (url.includes('/plugins/plugin1/enable')) {
          enableUrl = url
          return Promise.resolve(new Response('{}', { status: 200 }))
        }
        if (url.includes('/plugins') && !url.includes('plugin-context')) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                items: [disabledPlugin],
                total: 1,
                page: 1,
                pageSize: 200,
                totalPages: 1,
              }),
              { status: 200 },
            ),
          )
        }
        return Promise.resolve(defaultFetch(url))
      }),
    )

    renderWithProviders(
      <Routes>
        <Route path="/plugins/:id" element={<PluginDetail />} />
      </Routes>,
      { route: '/plugins/plugin1' },
    )
    await waitFor(() => expect(screen.getByText('Test Plugin')).toBeInTheDocument())

    // Scoped query: "Disabled" also appears in the Status panel tag.
    const card = metadataCard()
    expect(card.querySelector('.v .tag.err')).toHaveTextContent('Disabled')

    await userEvent.click(within(card).getByRole('button', { name: 'Enable' }))
    await waitFor(() => expect(enableUrl).toContain('/plugins/plugin1/enable'))
  })

  it('status panel still shows the readiness message and signature details', async () => {
    renderWithProviders(
      <Routes>
        <Route path="/plugins/:id" element={<PluginDetail />} />
      </Routes>,
      { route: '/plugins/plugin1' },
    )
    await waitFor(() => expect(screen.getByText('Test Plugin')).toBeInTheDocument())

    const statusPanel = panelByTitle('Status')
    expect(within(statusPanel).getByText('Ready')).toBeInTheDocument()
    expect(within(statusPanel).getByText('Plugin is ready')).toBeInTheDocument()
    expect(within(statusPanel).getByText('test-signer')).toBeInTheDocument()
    const checkedAt = new Date('2024-01-01T00:00:00Z').toLocaleString()
    expect(within(statusPanel).getByText(new RegExp(checkedAt))).toBeInTheDocument()
  })
})
