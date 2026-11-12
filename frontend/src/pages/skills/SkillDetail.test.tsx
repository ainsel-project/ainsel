import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes } from 'react-router-dom'
import { SkillDetail } from './SkillDetail'
import { renderWithProviders } from '../../test/renderWithProviders'

const SKILL = {
  id: 'code-review',
  name: 'Code Review',
  description: 'reviews PRs',
  body: '# Code Review\n\nYou are a **code reviewer**.',
  createdAt: '2026-05-01T00:00:00Z',
  updatedAt: '2026-05-01T00:00:00Z',
}

const SKILL_NO_BODY = {
  id: 'empty-skill',
  name: 'Empty Skill',
  description: '',
  body: '',
  createdAt: '2026-05-01T00:00:00Z',
  updatedAt: '2026-05-01T00:00:00Z',
}

function skillFetch(skill: typeof SKILL, deleteStatus: number = 204, deleteBody?: unknown) {
  return vi.fn((url: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET'
    if (url.match(/\/api\/v1\/skills\/code-review$/) && method === 'GET') {
      return Promise.resolve(new Response(JSON.stringify(skill), { status: 200 }))
    }
    if (url.match(/\/api\/v1\/skills\/empty-skill$/) && method === 'GET') {
      return Promise.resolve(new Response(JSON.stringify(skill), { status: 200 }))
    }
    if (url.match(/\/api\/v1\/skills\/code-review$/) && method === 'DELETE') {
      return Promise.resolve(
        deleteBody !== undefined
          ? new Response(JSON.stringify(deleteBody), { status: deleteStatus })
          : new Response(null, { status: deleteStatus }),
      )
    }
    return Promise.resolve(new Response('{}', { status: 200 }))
  })
}

describe('SkillDetail', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders skill identity and body', async () => {
    vi.stubGlobal('fetch', skillFetch(SKILL))
    renderWithProviders(
      <Routes>
        <Route path="/skills/:id" element={<SkillDetail />} />
      </Routes>,
      { route: '/skills/code-review' },
    )
    // Wait for the description, which only renders after load (the ID slug
    // also appears in the breadcrumb while still loading).
    await waitFor(() => expect(screen.getByText('reviews PRs')).toBeInTheDocument())
    expect(screen.getByText('code-review')).toBeInTheDocument()
    // Markdown body rendered (strong text is unique).
    expect(screen.getByText('code reviewer')).toBeInTheDocument()
  })

  it('renders an empty-body state when the skill has no body', async () => {
    vi.stubGlobal('fetch', skillFetch(SKILL_NO_BODY))
    renderWithProviders(
      <Routes>
        <Route path="/skills/:id" element={<SkillDetail />} />
      </Routes>,
      { route: '/skills/code-review' },
    )
    await waitFor(() => expect(screen.getByText(/no body yet/i)).toBeInTheDocument())
  })

  it('shows the Edit action', async () => {
    vi.stubGlobal('fetch', skillFetch(SKILL))
    renderWithProviders(
      <Routes>
        <Route path="/skills/:id" element={<SkillDetail />} />
      </Routes>,
      { route: '/skills/code-review' },
    )
    expect(await screen.findByRole('button', { name: /^edit$/i })).toBeInTheDocument()
  })

  it('deletes the skill and navigates back to the list', async () => {
    vi.stubGlobal('fetch', skillFetch(SKILL, 204))
    renderWithProviders(
      <Routes>
        <Route path="/skills/:id" element={<SkillDetail />} />
        <Route path="/skills" element={<div>SKILL_LIST</div>} />
      </Routes>,
      { route: '/skills/code-review' },
    )
    // Header Delete is the only /delete/ button while the modal is closed.
    await waitFor(() => expect(screen.getByText('reviews PRs')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /^delete$/i }))
    // Modal confirm is the second /delete/ button now visible.
    const deleteButtons = screen.getAllByRole('button', { name: /^delete$/i })
    await userEvent.click(deleteButtons[deleteButtons.length - 1])
    await waitFor(() => expect(screen.getByText('SKILL_LIST')).toBeInTheDocument())
  })

  it('shows the in-use referrers when delete is refused', async () => {
    vi.stubGlobal(
      'fetch',
      skillFetch(SKILL, 409, {
        error: 'skill in use',
        referrers: [{ agentName: 'reviewer' }],
      }),
    )
    renderWithProviders(
      <Routes>
        <Route path="/skills/:id" element={<SkillDetail />} />
        <Route path="/skills" element={<div>SKILL_LIST</div>} />
      </Routes>,
      { route: '/skills/code-review' },
    )
    await waitFor(() => expect(screen.getByText('reviews PRs')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /^delete$/i }))
    const deleteButtons = screen.getAllByRole('button', { name: /^delete$/i })
    await userEvent.click(deleteButtons[deleteButtons.length - 1])
    await waitFor(() => expect(screen.getByText('reviewer')).toBeInTheDocument())
    expect(screen.getByRole('button', { name: /cannot delete/i })).toBeInTheDocument()
  })

  // #154: the delete-409 referrer list links each agent to its detail page
  // when the payload carries the agent id and the agent list resolves it.
  it('links delete-409 referrers that resolve in the agent list', async () => {
    const base = skillFetch(SKILL, 409, {
      error: 'skill in use',
      referrers: [
        { agentName: 'reviewer', agentId: 'reviewer' },
        { agentName: 'gone-agent', agentId: 'gone-agent' },
      ],
    })
    vi.stubGlobal('fetch', (url: string, init?: RequestInit) => {
      if (url.includes('/api/v1/agents')) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              items: [{ id: 'reviewer', name: 'reviewer' }],
              total: 1,
              page: 1,
              pageSize: 200,
              totalPages: 1,
            }),
            { status: 200 },
          ),
        )
      }
      return base(url, init)
    })
    renderWithProviders(
      <Routes>
        <Route path="/skills/:id" element={<SkillDetail />} />
        <Route path="/skills" element={<div>SKILL_LIST</div>} />
      </Routes>,
      { route: '/skills/code-review' },
    )
    await waitFor(() => expect(screen.getByText('reviews PRs')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /^delete$/i }))
    const deleteButtons = screen.getAllByRole('button', { name: /^delete$/i })
    await userEvent.click(deleteButtons[deleteButtons.length - 1])
    const link = await screen.findByRole('link', { name: 'reviewer' })
    expect(link).toHaveAttribute('href', '/agents/reviewer')
    // An id the side list does not resolve stays plain text.
    expect(screen.getByText('gone-agent')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'gone-agent' })).not.toBeInTheDocument()
  })

  // Skills are selected on the agent, so assigning from a skill's page writes
  // that agent's own spec.skills — never a shared image's list.
  it('assigns the skill to an agent through the assignments picker', async () => {
    const puts: string[] = []
    const base = skillFetch(SKILL)
    vi.stubGlobal('fetch', (url: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      if (url.endsWith('/assignments') && method === 'GET') {
        return Promise.resolve(
          new Response(JSON.stringify({ items: [{ agentName: 'reviewer' }] }), { status: 200 }),
        )
      }
      if (url.includes('/assignments/') && method === 'PUT') {
        puts.push(url)
        return Promise.resolve(new Response(null, { status: 204 }))
      }
      if (url.includes('/api/v1/agents') && method === 'GET') {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              items: [
                { id: 'reviewer', name: 'reviewer' },
                { id: 'doc-writer', name: 'doc-writer' },
              ],
              total: 2,
              page: 1,
              pageSize: 200,
              totalPages: 1,
            }),
            { status: 200 },
          ),
        )
      }
      return base(url, init)
    })
    renderWithProviders(
      <Routes>
        <Route path="/skills/:id" element={<SkillDetail />} />
      </Routes>,
      { route: '/skills/code-review' },
    )
    // doc-writer sits in the available pane: it does not select the skill yet.
    await userEvent.click(await screen.findByRole('option', { name: /doc-writer/ }))
    await userEvent.click(screen.getByRole('button', { name: /add selected to/i }))
    await waitFor(() => expect(puts).toContain('/api/v1/skills/code-review/assignments/doc-writer'))
  })
})

// Issue #146: an externally managed skill is read-only in the console.
describe('SkillDetail (externally managed)', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('hides Edit and Delete and marks the skill as managed', async () => {
    vi.stubGlobal(
      'fetch',
      skillFetch({ ...SKILL, managedBy: 'gitops' } as typeof SKILL & { managedBy: string }),
    )
    renderWithProviders(
      <Routes>
        <Route path="/skills/:id" element={<SkillDetail />} />
      </Routes>,
      { route: '/skills/code-review' },
    )
    await waitFor(() => expect(screen.getByText('managed by gitops')).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: /^edit$/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^delete$/i })).not.toBeInTheDocument()
  })

  it('keeps Edit and Delete for a console-managed skill', async () => {
    vi.stubGlobal(
      'fetch',
      skillFetch({ ...SKILL, managedBy: 'user' } as typeof SKILL & { managedBy: string }),
    )
    renderWithProviders(
      <Routes>
        <Route path="/skills/:id" element={<SkillDetail />} />
      </Routes>,
      { route: '/skills/code-review' },
    )
    await waitFor(() => expect(screen.getByText('reviews PRs')).toBeInTheDocument())
    expect(screen.queryByText('managed by user')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^edit$/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^delete$/i })).toBeInTheDocument()
  })
})
