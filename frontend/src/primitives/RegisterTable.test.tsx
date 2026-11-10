import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { RegisterTable } from './RegisterTable'

type Row = { id: string; name: string; status: 'ok' | 'err' }

const columns = [
  { key: 'name', header: 'Name', cell: (r: Row) => r.name },
  { key: 'status', header: 'Status', cell: (r: Row) => r.status },
] as const

describe('RegisterTable', () => {
  it('renders headers and rows with row numbers', () => {
    const rows: Row[] = [
      { id: 'a', name: 'alpha', status: 'ok' },
      { id: 'b', name: 'beta', status: 'err' },
    ]
    render(<RegisterTable rows={rows} columns={columns} rowKey={(r) => r.id} />)
    expect(screen.getByText('Name')).toBeInTheDocument()
    expect(screen.getByText('alpha')).toBeInTheDocument()
    expect(screen.getByText('beta')).toBeInTheDocument()
    expect(screen.getByText('01')).toBeInTheDocument()
    expect(screen.getByText('02')).toBeInTheDocument()
  })

  it('renders empty-state message when no rows', () => {
    render(
      <RegisterTable
        rows={[] as Row[]}
        columns={columns}
        rowKey={(r) => r.id}
        emptyLabel="No connectors configured."
      />,
    )
    expect(screen.getByText('No connectors configured.')).toBeInTheDocument()
  })

  it('applies row-err class when rowClassName returns it', () => {
    const rows: Row[] = [{ id: 'x', name: 'broken', status: 'err' }]
    const { container } = render(
      <RegisterTable
        rows={rows}
        columns={columns}
        rowKey={(r) => r.id}
        rowClassName={(r) => (r.status === 'err' ? 'row-err' : undefined)}
      />,
    )
    const tr = within(container).getByText('broken').closest('tr') as HTMLElement
    expect(tr).toHaveClass('row-err')
  })

  it('skips the rowno column when withRowNumbers={false}', () => {
    const rows: Row[] = [{ id: 'a', name: 'alpha', status: 'ok' }]
    const { container } = render(
      <RegisterTable rows={rows} columns={columns} rowKey={(r) => r.id} withRowNumbers={false} />,
    )
    expect(container.querySelector('.rowno')).toBeNull()
  })

  describe('expandable rows (renderDetail)', () => {
    const rows: Row[] = [
      { id: 'a', name: 'alpha', status: 'ok' },
      { id: 'b', name: 'beta', status: 'err' },
    ]

    it('renders no toggle column or buttons when renderDetail is absent', () => {
      const { container } = render(
        <RegisterTable rows={rows} columns={columns} rowKey={(r) => r.id} />,
      )
      expect(container.querySelector('.rowtoggle')).toBeNull()
      expect(container.querySelector('button')).toBeNull()
    })

    it('renders a toggle per row and expands the detail row on click', async () => {
      const user = userEvent.setup()
      const { container } = render(
        <RegisterTable
          rows={rows}
          columns={columns}
          rowKey={(r) => r.id}
          renderDetail={(r) => <span>detail of {r.name}</span>}
        />,
      )
      const toggles = container.querySelectorAll<HTMLButtonElement>('.rowtoggle-btn')
      expect(toggles).toHaveLength(2)
      expect(toggles[0]).toHaveAttribute('aria-expanded', 'false')
      expect(screen.queryByText('detail of alpha')).not.toBeInTheDocument()

      await user.click(toggles[0])
      expect(toggles[0]).toHaveAttribute('aria-expanded', 'true')
      expect(screen.getByText('detail of alpha')).toBeInTheDocument()
      // The toggle points at the rendered detail region.
      const detailId = toggles[0]!.getAttribute('aria-controls')
      expect(detailId).toBeTruthy()
      const detailCell = document.getElementById(detailId as string)
      expect(detailCell).toBe(screen.getByText('detail of alpha').closest('td'))
      expect(detailCell).toHaveAttribute('role', 'region')

      await user.click(toggles[0])
      expect(toggles[0]).toHaveAttribute('aria-expanded', 'false')
      expect(toggles[0]).not.toHaveAttribute('aria-controls')
      expect(screen.queryByText('detail of alpha')).not.toBeInTheDocument()
    })

    it('does not offer a toggle for rows whose detail renders nothing', () => {
      const { container } = render(
        <RegisterTable
          rows={rows}
          columns={columns}
          rowKey={(r) => r.id}
          renderDetail={(r) => (r.status === 'ok' ? <span>ok detail</span> : null)}
        />,
      )
      const toggles = container.querySelectorAll('.rowtoggle-btn')
      expect(toggles).toHaveLength(1)
    })

    it('detail row spans the toggle, row-number, and data columns', async () => {
      const user = userEvent.setup()
      const { container } = render(
        <RegisterTable
          rows={rows}
          columns={columns}
          rowKey={(r) => r.id}
          renderDetail={(r) => <span>detail of {r.name}</span>}
        />,
      )
      await user.click(container.querySelector('.rowtoggle-btn') as HTMLButtonElement)
      const detailCell = container.querySelector('tr.reg-detail td') as HTMLTableCellElement
      // toggle + rowno + 2 data columns
      expect(detailCell).toHaveAttribute('colspan', '4')
      // The detail row must not consume a row number.
      expect(detailCell.querySelector('.rowno')).toBeNull()
      expect(container.querySelectorAll('td.rowno')).toHaveLength(2)
    })

    it('keeps row numbering of data rows unaffected by the toggle column', () => {
      render(
        <RegisterTable
          rows={rows}
          columns={columns}
          rowKey={(r) => r.id}
          renderDetail={() => <span>x</span>}
        />,
      )
      expect(screen.getByText('01')).toBeInTheDocument()
      expect(screen.getByText('02')).toBeInTheDocument()
    })
  })
})
