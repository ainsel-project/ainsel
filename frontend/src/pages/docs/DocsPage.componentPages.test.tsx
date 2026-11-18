import { fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { DocsPage } from './DocsPage'
import type { ComponentPage } from './componentPages'

// A registered component page replaces the markdown fetch for its slug while
// the sidebar, search, TOC and prev/next keep working around it.

const SIDEBAR = ['## Overview', '- [Glance](glance)', '- [Arch](architecture)'].join('\n')

const DOCS: Record<string, string> = {
  architecture: '# Architecture\n\nMarkdown architecture body.',
}

function GlanceBody() {
  return (
    <>
      <h2 id="first">First</h2>
      <p>Glance component body.</p>
      <h2 id="second">Second</h2>
      <h2 id="third">Third</h2>
      <h2 id="fourth">Fourth</h2>
    </>
  )
}

const PAGES: Record<string, ComponentPage> = {
  glance: {
    component: GlanceBody,
    searchText: 'plugins everywhere',
    headings: [
      { level: 2, text: 'First', id: 'first' },
      { level: 2, text: 'Second', id: 'second' },
      { level: 2, text: 'Third', id: 'third' },
      { level: 2, text: 'Fourth', id: 'fourth' },
    ],
  },
}

let fetchMock: ReturnType<typeof vi.fn>
let releaseSidebar: () => void = () => {}

function mockFetch({ holdSidebar = false } = {}) {
  const sidebarGate = new Promise<void>((resolve) => {
    releaseSidebar = resolve
  })
  if (!holdSidebar) releaseSidebar()
  fetchMock = vi.fn(async (url: string) => {
    const p = String(url)
    if (p.endsWith('_sidebar.md')) {
      await sidebarGate
      return { ok: true, status: 200, text: async () => SIDEBAR } as Response
    }
    const match = p.match(/\/docs\/(.+?)\.md$/)
    const slug = match ? match[1] : ''
    if (slug in DOCS) {
      return { ok: true, status: 200, text: async () => DOCS[slug] } as Response
    }
    return { ok: false, status: 404, text: async () => 'not found' } as Response
  })
  vi.stubGlobal('fetch', fetchMock)
}

beforeEach(() => mockFetch())

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/docs" element={<DocsPage pages={PAGES} />} />
        <Route path="/docs/*" element={<DocsPage pages={PAGES} />} />
      </Routes>
    </MemoryRouter>,
  )
}

function fetchedGlanceMarkdown() {
  return fetchMock.mock.calls.some(([url]) => String(url).endsWith('glance.md'))
}

describe('DocsPage component pages', () => {
  it('renders a registered component instead of fetching markdown', async () => {
    renderAt('/docs/glance')
    expect(await screen.findByText('Glance component body.')).toBeInTheDocument()
    expect(fetchedGlanceMarkdown()).toBe(false)
  })

  it('defaults to a component page when it is first in the sidebar', async () => {
    renderAt('/docs')
    expect(await screen.findByText('Glance component body.')).toBeInTheDocument()
  })

  it('deep link renders without redirect before the sidebar loads', async () => {
    vi.unstubAllGlobals()
    mockFetch({ holdSidebar: true })
    renderAt('/docs/glance')
    expect(await screen.findByText('Glance component body.')).toBeInTheDocument()
    releaseSidebar()
    expect(await screen.findByText('Glance component body.')).toBeInTheDocument()
  })

  it('markdown pages still render', async () => {
    renderAt('/docs/architecture')
    expect(await screen.findByText('Markdown architecture body.')).toBeInTheDocument()
  })

  it('search matches component searchText', async () => {
    renderAt('/docs/architecture')
    await screen.findByText('Markdown architecture body.')
    fireEvent.change(screen.getByPlaceholderText('Search docs…'), { target: { value: 'plugin' } })
    const sidebar = document.querySelector('.docs-sidebar') as HTMLElement
    expect(await within(sidebar).findByRole('link', { name: 'Glance' })).toBeInTheDocument()
    expect(within(sidebar).queryByRole('link', { name: 'Arch' })).toBeNull()
    expect(fetchedGlanceMarkdown()).toBe(false)
  })

  it('toc comes from component headings', async () => {
    renderAt('/docs/glance')
    await screen.findByText('Glance component body.')
    const toc = document.querySelector('.docs-toc') as HTMLElement
    expect(toc).not.toBeNull()
    for (const text of ['First', 'Second', 'Third', 'Fourth']) {
      expect(within(toc).getByRole('link', { name: text })).toHaveAttribute(
        'href',
        `#${text.toLowerCase()}`,
      )
    }
  })

  it('prev/next works across component and markdown pages', async () => {
    renderAt('/docs/glance')
    await screen.findByText('Glance component body.')
    const nav = document.querySelector('.docs-prev-next') as HTMLElement
    expect(await within(nav).findByText('Arch')).toBeInTheDocument()
  })
})
