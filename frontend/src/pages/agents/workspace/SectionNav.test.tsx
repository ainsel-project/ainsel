import { describe, expect, it } from 'vitest'
import { screen, within } from '@testing-library/react'
import { SectionNav } from './SectionNav'
import { renderWithProviders } from '../../../test/renderWithProviders'

describe('SectionNav', () => {
  it('renders the Configure and Wiring group headings', () => {
    renderWithProviders(<SectionNav agentId="x" current="overview" plugins={[]} />)
    expect(screen.getByRole('heading', { name: 'Configure' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Wiring' })).toBeInTheDocument()
  })

  it('marks the current section with aria-current', () => {
    renderWithProviders(<SectionNav agentId="x" current="runtime" plugins={[]} />)
    expect(screen.getByRole('link', { name: 'Runtime & env' })).toHaveAttribute(
      'aria-current',
      'page',
    )
    expect(screen.getByRole('link', { name: 'Persona' })).not.toHaveAttribute('aria-current')
  })

  it('links Overview to the agent index route', () => {
    renderWithProviders(<SectionNav agentId="x" current="runtime" plugins={[]} />)
    expect(screen.getByRole('link', { name: 'Overview' })).toHaveAttribute('href', '/agents/x')
  })

  it('renders plugin entries under a Plugins heading', () => {
    renderWithProviders(
      <SectionNav
        agentId="x"
        current="plugin:forgejo"
        plugins={[{ id: 'forgejo', label: 'Forgejo' }]}
      />,
    )
    const heading = screen.getByRole('heading', { name: 'Plugins' })
    const group = heading.parentElement as HTMLElement
    expect(within(group).getByRole('link', { name: 'Forgejo' })).toHaveAttribute(
      'aria-current',
      'page',
    )
  })
})
