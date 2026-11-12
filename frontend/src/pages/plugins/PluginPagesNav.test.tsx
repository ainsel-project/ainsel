import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import type { PluginManifest } from '@ainsel/plugin-sdk'
import { PluginPagesNav } from './PluginPagesNav'

const manifest = {
  id: 'memory',
  displayName: 'Memory',
  pluginApi: '1.1',
  ui: {
    entry: '/static/ui/index.js',
    pages: [
      { path: '/', label: 'Memories', section: 'Memory' },
      { path: '/setup', label: 'Setup', section: 'Memory', visibility: 'admin' },
    ],
  },
} as unknown as PluginManifest

const plugin = { id: 'memory', manifest }

const barePlugin = {
  id: 'bare',
  manifest: {
    id: 'bare',
    displayName: 'Bare',
    pluginApi: '1.1',
    ui: { entry: '/static/ui/index.js', pages: [] },
  },
} as unknown as { id: string; manifest: PluginManifest }

function renderNav(path: string, isAdmin: boolean, p = plugin) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <PluginPagesNav plugin={p} isAdmin={isAdmin} />
    </MemoryRouter>,
  )
}

describe('PluginPagesNav', () => {
  it('renders a labelled tablist of link-backed tabs', () => {
    renderNav('/p/memory', true)
    const tablist = screen.getByRole('tablist', { name: 'Plugin sections' })
    const setup = within(tablist).getByRole('tab', { name: /Setup/i })
    // Link-backed: deep-linkable and openable in a new tab.
    expect(setup).toHaveAttribute('href', '/p/memory/setup')
  })

  it('selects the most specific tab, never two', () => {
    renderNav('/p/memory/setup', true)
    const setup = screen.getByRole('tab', { name: /Setup/i })
    const memories = screen.getByRole('tab', { name: /Memories/i })
    // The nested page wins over the root page — never both (the old spine
    // regression, re-established for the workspace tab strip).
    expect(setup).toHaveAttribute('aria-selected', 'true')
    expect(memories).toHaveAttribute('aria-selected', 'false')
  })

  it('keeps the page tab selected on deep links under its path', () => {
    renderNav('/p/memory/setup/42', true)
    expect(screen.getByRole('tab', { name: /Setup/i })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('tab', { name: /Memories/i })).toHaveAttribute('aria-selected', 'false')
  })

  it('selects the root page tab on its own path', () => {
    renderNav('/p/memory', true)
    expect(screen.getByRole('tab', { name: /Memories/i })).toHaveAttribute('aria-selected', 'true')
  })

  it('leads with a Plugin home tab and selects it on the detail page', () => {
    renderNav('/plugins/memory', true)
    const home = screen.getByRole('tab', { name: 'Plugin' })
    expect(home).toHaveAttribute('href', '/plugins/memory')
    expect(home).toHaveAttribute('aria-selected', 'true')
  })

  it('hides the Plugin home tab and admin pages from non-admins', () => {
    renderNav('/p/memory', false)
    expect(screen.getByRole('tab', { name: /Memories/i })).toBeInTheDocument()
    expect(screen.queryByRole('tab', { name: /Setup/i })).not.toBeInTheDocument()
    // /plugins/:id is admin-only — never offer non-admins a dead end.
    expect(screen.queryByRole('tab', { name: 'Plugin' })).not.toBeInTheDocument()
  })

  it('renders just the Plugin home tab for a zero-page plugin (admin)', () => {
    renderNav('/plugins/bare', true, barePlugin)
    const tabs = screen.getAllByRole('tab')
    expect(tabs).toHaveLength(1)
    expect(tabs[0]).toHaveTextContent('Plugin')
    expect(tabs[0]).toHaveAttribute('aria-selected', 'true')
  })

  it('renders nothing for a zero-page plugin as a non-admin', () => {
    const { container } = renderNav('/p/bare', false, barePlugin)
    expect(container).toBeEmptyDOMElement()
  })
})
