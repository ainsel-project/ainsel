import { render, screen } from '@testing-library/react'
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

function renderNav(path: string, isAdmin: boolean) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <PluginPagesNav plugin={plugin} isAdmin={isAdmin} />
    </MemoryRouter>,
  )
}

describe('PluginPagesNav', () => {
  it('links the plugin pages and highlights the current one most specifically', () => {
    renderNav('/p/memory/setup', true)
    const setup = screen.getByRole('link', { name: /Setup/i })
    const memories = screen.getByRole('link', { name: /Memories/i })
    // The nested page wins over the root page — never both (the old spine
    // regression, re-established for the workspace strip).
    expect(setup).toHaveAttribute('aria-current', 'page')
    expect(memories).not.toHaveAttribute('aria-current')
  })

  it('keeps the page highlighted on deep links under its path', () => {
    renderNav('/p/memory/setup/42', true)
    expect(screen.getByRole('link', { name: /Setup/i })).toHaveAttribute('aria-current', 'page')
  })

  it('highlights the root page on its own path', () => {
    renderNav('/p/memory', true)
    expect(screen.getByRole('link', { name: /Memories/i })).toHaveAttribute(
      'aria-current',
      'page',
    )
  })

  it('hides admin pages from non-admins', () => {
    renderNav('/p/memory', false)
    expect(screen.getByRole('link', { name: /Memories/i })).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /Setup/i })).not.toBeInTheDocument()
  })

  it('renders nothing when the plugin has no visible pages', () => {
    const empty = {
      id: 'bare',
      manifest: {
        id: 'bare',
        displayName: 'Bare',
        pluginApi: '1.1',
        ui: { entry: '/static/ui/index.js', pages: [] },
      },
    } as unknown as { id: string; manifest: PluginManifest }
    const { container } = render(
      <MemoryRouter initialEntries={['/p/bare']}>
        <PluginPagesNav plugin={empty} isAdmin />
      </MemoryRouter>,
    )
    expect(container).toBeEmptyDOMElement()
  })
})
