import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { Tabs } from './Tabs'

const tabs = [
  { value: 'overview', label: 'Overview' },
  { value: 'status', label: 'Status' },
]

describe('Tabs', () => {
  it('renders all tabs', () => {
    render(<Tabs value="overview" onChange={() => {}} tabs={tabs} />)
    expect(screen.getByRole('tab', { name: 'Overview' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'Status' })).toBeInTheDocument()
  })

  it('marks the active tab', () => {
    render(<Tabs value="status" onChange={() => {}} tabs={tabs} />)
    const status = screen.getByRole('tab', { name: 'Status' })
    expect(status).toHaveAttribute('aria-selected', 'true')
    expect(status).toHaveClass('tab', 'active')
  })

  it('calls onChange when a tab is clicked', async () => {
    const onChange = vi.fn()
    render(<Tabs value="overview" onChange={onChange} tabs={tabs} />)
    await userEvent.click(screen.getByRole('tab', { name: 'Status' }))
    expect(onChange).toHaveBeenCalledWith('status')
  })

  it('renders link-backed tabs for entries with a `to`', () => {
    render(
      <MemoryRouter>
        <Tabs
          value="/plugins/x"
          tabs={[
            { value: '/plugins/x', label: 'Plugin', to: '/plugins/x' },
            { value: '/p/x', label: 'Home', to: '/p/x' },
          ]}
        />
      </MemoryRouter>,
    )
    const active = screen.getByRole('tab', { name: 'Plugin' })
    expect(active).toHaveAttribute('href', '/plugins/x')
    expect(active).toHaveAttribute('aria-selected', 'true')
    expect(active).toHaveClass('tab', 'active')
    const inactive = screen.getByRole('tab', { name: 'Home' })
    expect(inactive).toHaveAttribute('href', '/p/x')
    expect(inactive).toHaveAttribute('aria-selected', 'false')
  })

  it('omits onChange for link tabs', () => {
    // A link tab is selected by navigation, not by a click handler.
    render(
      <MemoryRouter>
        <Tabs value="/a" tabs={[{ value: '/a', label: 'A', to: '/a' }]} />
      </MemoryRouter>,
    )
    expect(screen.getByRole('tab', { name: 'A' })).toBeInTheDocument()
  })
})
