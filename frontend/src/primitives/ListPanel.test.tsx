import { vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ListPanel } from './ListPanel'

type Page = { items: { id: string }[]; total: number }

const page: Page = { items: [{ id: 'a' }], total: 1 }

describe('ListPanel', () => {
  it('renders the children over a ready, non-empty page', () => {
    render(
      <ListPanel query={{ data: { items: [{ id: 'a' }], total: 1 } }} empty="No connectors yet.">
        <p>table</p>
      </ListPanel>,
    )
    expect(screen.getByText('table')).toBeInTheDocument()
    expect(screen.queryByText('No connectors yet.')).not.toBeInTheDocument()
  })

  it('says loading while the query is pending', () => {
    render(
      <ListPanel query={{ isLoading: true }} empty="No connectors yet.">
        <p>table</p>
      </ListPanel>,
    )
    expect(screen.getByText('Loading…')).toBeInTheDocument()
    expect(screen.queryByText('table')).not.toBeInTheDocument()
  })

  it('names what failed and offers a retry', async () => {
    const refetch = vi.fn()
    render(
      <ListPanel
        query={{ error: new Error('boom'), refetch }}
        errorTitle="Failed to load connectors."
        empty="No connectors yet."
      >
        <p>table</p>
      </ListPanel>,
    )
    expect(screen.getByText('Failed to load connectors.')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(refetch).toHaveBeenCalled()
  })

  it('shows the empty state instead of the children when the page has no rows', () => {
    render(
      <ListPanel query={{ data: { items: [], total: 0 } }} empty="No connectors yet.">
        <p>table</p>
        <p>pager</p>
      </ListPanel>,
    )
    expect(screen.getByText('No connectors yet.')).toBeInTheDocument()
    expect(screen.queryByText('table')).not.toBeInTheDocument()
    expect(screen.queryByText('pager')).not.toBeInTheDocument()
  })

  it('counts rows in a bare array as well as in a page of items', () => {
    render(
      <ListPanel query={{ data: [] as string[] }} empty="No groups yet.">
        <p>table</p>
      </ListPanel>,
    )
    expect(screen.getByText('No groups yet.')).toBeInTheDocument()
  })

  it('renders the empty-state call to action only with the empty state', () => {
    const { rerender } = render(
      <ListPanel
        query={{ data: [] as string[] }}
        empty="No groups yet."
        emptyAction={<button type="button">＋ New Group</button>}
      >
        <p>table</p>
      </ListPanel>,
    )
    expect(screen.getByRole('button', { name: '＋ New Group' })).toBeInTheDocument()

    rerender(
      <ListPanel
        query={{ data: ['g'] }}
        empty="No groups yet."
        emptyAction={<button type="button">＋ New Group</button>}
      >
        <p>table</p>
      </ListPanel>,
    )
    expect(screen.queryByRole('button', { name: '＋ New Group' })).not.toBeInTheDocument()
    expect(screen.getByText('table')).toBeInTheDocument()
  })

  it('does not claim an empty list before the first result arrives', () => {
    render(
      <ListPanel query={{}} empty="No connectors yet.">
        <p>table</p>
      </ListPanel>,
    )
    expect(screen.queryByText('No connectors yet.')).not.toBeInTheDocument()
  })

  it('takes emptiness from isEmpty when the data shape is neither', () => {
    render(
      <ListPanel query={{ data: { count: 0 } }} isEmpty empty="No users found.">
        <p>table</p>
      </ListPanel>,
    )
    expect(screen.getByText('No users found.')).toBeInTheDocument()
  })

  it('forwards the panel title and className', () => {
    const { container } = render(
      <ListPanel query={{ data: page }} className="cropped" title="Roster">
        <p>table</p>
      </ListPanel>,
    )
    expect(container.firstChild).toHaveClass('panel', 'cropped')
    expect(screen.getByText('Roster')).toBeInTheDocument()
  })
})
