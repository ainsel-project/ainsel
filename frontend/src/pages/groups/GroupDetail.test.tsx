import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { Route, Routes } from 'react-router-dom'
import { GroupDetail } from './GroupDetail'
import { renderWithProviders } from '../../test/renderWithProviders'

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const GROUP = {
  id: 'g1',
  name: 'Platform',
  description: 'Platform team group',
  createdAt: '2026-01-01T00:00:00Z',
  updatedAt: '2026-01-01T00:00:00Z',
}

const MEMBERS = [
  {
    user: {
      id: 'u-1',
      email: 'alice@test',
      username: 'alice',
      isAdmin: false,
      disabled: false,
      createdAt: '2026-01-01T00:00:00Z',
      updatedAt: '2026-01-01T00:00:00Z',
    },
    role: 'owner',
  },
]

const RESOURCES = {
  items: [
    { type: 'agent', id: 'a-1', name: 'Review Bot', public: false },
    { type: 'agent', id: 'a-2', name: 'a-2', public: true },
    { type: 'agent-image', id: 'img-1', name: 'Base Image', public: false },
    { type: 'trigger', id: 't-1', name: 'On issues', public: false, agentRef: 'a-1' },
    { type: 'cron-trigger', id: 'ct-1', name: 'Nightly', public: false, agentRef: 'a-1' },
    { type: 'mcp-server', id: 'mcp-1', name: 'Docs MCP', public: false },
  ],
}

function mockFetch(resources: { items: unknown[] } = RESOURCES) {
  vi.stubGlobal('fetch', vi.fn((url: string) => {
    const path = String(url).split('?')[0]
    const ok = (payload: unknown) =>
      Promise.resolve(new Response(JSON.stringify(payload), { status: 200 }))
    if (path.endsWith('/groups/g1/resources')) return ok(resources)
    if (path.endsWith('/groups/g1')) return ok({ group: GROUP, members: MEMBERS })
    if (path.endsWith('/users')) return ok([])
    return ok({})
  }))
}

function renderPage() {
  return renderWithProviders(
    <Routes>
      <Route path="/groups/:id" element={<GroupDetail />} />
    </Routes>,
    { route: '/groups/g1' },
  )
}

// ---------------------------------------------------------------------------

describe('GroupDetail resources section', () => {
  it('renders one panel per type with counts and working links', async () => {
    mockFetch()
    renderPage()

    await waitFor(() => expect(screen.getByText('Agents · 2')).toBeInTheDocument())

    // Panel titles carry the count of items of that type.
    expect(screen.getByText('Agents · 2')).toBeInTheDocument()
    expect(screen.getByText('Agent images · 1')).toBeInTheDocument()
    expect(screen.getByText('Triggers · 1')).toBeInTheDocument()
    expect(screen.getByText('Cron triggers · 1')).toBeInTheDocument()
    expect(screen.getByText('MCP servers · 1')).toBeInTheDocument()
    // Types with no items still render with an empty-state row.
    expect(screen.getByText('No personas in this group.')).toBeInTheDocument()

    // Names, not ids.
    expect(screen.getByText('Review Bot')).toBeInTheDocument()
    expect(screen.getByText('Base Image')).toBeInTheDocument()
    expect(screen.getByText('Docs MCP')).toBeInTheDocument()

    // Detail links per type.
    expect(screen.getByRole('link', { name: 'Review Bot' })).toHaveAttribute(
      'href',
      '/agents/a-1',
    )
    expect(screen.getByRole('link', { name: 'Base Image' })).toHaveAttribute(
      'href',
      '/agent-images/img-1',
    )
    expect(screen.getByRole('link', { name: 'Docs MCP' })).toHaveAttribute(
      'href',
      '/settings/mcp-servers/mcp-1',
    )
    // Triggers link to the owning agent's trigger tab.
    for (const name of ['On issues', 'Nightly']) {
      expect(screen.getByRole('link', { name })).toHaveAttribute(
        'href',
        '/agents/a-1?tab=triggers',
      )
    }
  })

  it('labels public resources', async () => {
    mockFetch()
    renderPage()

    await waitFor(() => expect(screen.getByText('Review Bot')).toBeInTheDocument())
    const publicItem = screen.getByText('a-2').closest('span')
    expect(publicItem).not.toBeNull()
    expect(publicItem).toHaveTextContent('Public')
  })

  it('summarises when the group owns nothing', async () => {
    mockFetch({ items: [] })
    renderPage()

    await waitFor(() => expect(screen.getByText('This group owns no resources.')).toBeInTheDocument())
    expect(screen.queryByText('Agents · 0')).not.toBeInTheDocument()
  })

  it('renders the members view even while resources load', async () => {
    mockFetch()
    renderPage()

    await waitFor(() => expect(screen.getByText('Members · 1')).toBeInTheDocument())
    expect(screen.getByText('alice')).toBeInTheDocument()
  })
})
