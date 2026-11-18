import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { AtAGlance } from './AtAGlance'
import { atAGlanceHeadings, atAGlanceSearchText } from './atAGlanceMeta'
import { componentPages } from '../componentPages'

function renderPage() {
  return render(
    <MemoryRouter>
      <AtAGlance />
    </MemoryRouter>,
  )
}

const SECTIONS = [
  ['The problem', 'the-problem'],
  ['One loop', 'one-loop'],
  ['Three jobs, same loop', 'three-jobs-same-loop'],
  ['What each team chooses', 'what-each-team-chooses'],
  ["What's already solved", 'whats-already-solved'],
  ['Grow it', 'grow-it'],
  ['Next', 'next'],
]

describe('AtAGlance', () => {
  it('renders the seven sections in order', () => {
    renderPage()
    const h2s = screen.getAllByRole('heading', { level: 2 })
    expect(h2s.map((h) => h.textContent)).toEqual(SECTIONS.map(([text]) => text))
    expect(h2s.map((h) => h.id)).toEqual(SECTIONS.map(([, id]) => id))
    expect(atAGlanceHeadings.map((h) => h.id)).toEqual(SECTIONS.map(([, id]) => id))
    expect(atAGlanceHeadings.map((h) => h.text)).toEqual(SECTIONS.map(([text]) => text))
  })

  it('links are router links to docs routes', () => {
    const { container } = renderPage()
    const hrefs = Array.from(container.querySelectorAll('a')).map((a) => a.getAttribute('href'))
    expect(hrefs.length).toBeGreaterThan(0)
    for (const href of hrefs) expect(href).toMatch(/^\/docs\//)
    for (const target of [
      '/docs/why-ainsel',
      '/docs/quickstart',
      '/docs/architecture',
      '/docs/access-control',
      '/docs/network-policies',
      '/docs/deployment',
      '/docs/administrator-guide',
    ]) {
      expect(hrefs).toContain(target)
    }
  })

  it('has a figure slot for every diagram', () => {
    const { container } = renderPage()
    const names = Array.from(container.querySelectorAll('[data-diagram]')).map((el) =>
      el.getAttribute('data-diagram'),
    )
    expect(names).toEqual(['problem', 'loop', 'three-jobs', 'choose', 'solved', 'grow'])
  })

  it('search text covers the pitch', () => {
    for (const word of ['plugin', 'trigger', 'connector', 'MCP']) {
      expect(atAGlanceSearchText).toContain(word)
    }
  })

  it('is registered as the at-a-glance component page', () => {
    expect(componentPages['at-a-glance'].component).toBe(AtAGlance)
    expect(componentPages['at-a-glance'].headings).toBe(atAGlanceHeadings)
    expect(componentPages['at-a-glance'].searchText).toBe(atAGlanceSearchText)
  })

  it('never sells itself as "generic"', () => {
    const { container } = renderPage()
    expect(container.textContent).not.toMatch(/\bgeneric\b/i)
  })
})
