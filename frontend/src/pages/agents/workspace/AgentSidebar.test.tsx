import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AgentSidebar } from './AgentSidebar'
import { renderWithProviders } from '../../../test/renderWithProviders'

const agents = [
  { id: 'a1', name: 'doc-writer', status: { ready: true } },
  { id: 'a2', name: 'reviewer-apps', status: { ready: false }, paused: true },
]

describe('AgentSidebar', () => {
  beforeEach(() => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          new Response(
            JSON.stringify({ items: agents, total: 2, page: 1, pageSize: 200, totalPages: 1 }),
            { status: 200 },
          ),
        ),
      ),
    )
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('lists the agents from the agents query', async () => {
    renderWithProviders(<AgentSidebar />, { route: '/agents' })
    expect(await screen.findByText('doc-writer')).toBeInTheDocument()
    expect(screen.getByText('reviewer-apps')).toBeInTheDocument()
  })

  it('filters agents by name as the search text is typed', async () => {
    renderWithProviders(<AgentSidebar />, { route: '/agents' })
    await screen.findByText('doc-writer')
    await userEvent.type(screen.getByLabelText('Search agents'), 'rev')
    await waitFor(() => expect(screen.queryByText('doc-writer')).not.toBeInTheDocument())
    expect(screen.getByText('reviewer-apps')).toBeInTheDocument()
  })

  it('marks the selected agent link with aria-current', async () => {
    renderWithProviders(<AgentSidebar selectedId="a1" />, { route: '/agents/a1' })
    const link = (await screen.findByText('doc-writer')).closest('a')
    expect(link).toHaveAttribute('href', '/agents/a1')
    expect(link).toHaveAttribute('aria-current', 'page')
  })

  it('keeps the toggle named Show agents and flips aria-expanded on click', async () => {
    renderWithProviders(<AgentSidebar />, { route: '/agents' })
    const toggle = screen.getByRole('button', { name: 'Show agents' })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    await userEvent.click(toggle)
    expect(screen.getByRole('button', { name: 'Show agents' })).toHaveAttribute(
      'aria-expanded',
      'true',
    )
    await userEvent.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(toggle).toHaveAccessibleName('Show agents')
  })

  it('links New agent to the wizard route', async () => {
    renderWithProviders(<AgentSidebar />, { route: '/agents' })
    expect(await screen.findByRole('link', { name: '+ New agent' })).toHaveAttribute(
      'href',
      '/agents/new',
    )
  })
})
