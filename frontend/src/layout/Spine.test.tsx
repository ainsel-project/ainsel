import { render, screen, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, vi } from 'vitest'
import { Spine } from './Spine'

function emptyAgentsResponse() {
  return new Response(
    JSON.stringify({ items: [], total: 0, page: 1, pageSize: 200, totalPages: 0 }),
    { status: 200 },
  )
}

function renderAt(path: string, opts: { rail?: boolean; open?: boolean } = {}) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[path]}>
        <Spine
          operator="kim"
          rail={opts.rail}
          open={opts.open}
          onClose={opts.open ? () => {} : undefined}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string) => {
      if (url.includes('/plugins')) {
        return Promise.resolve(
          new Response(
            JSON.stringify({ items: [], total: 0, page: 1, pageSize: 200, totalPages: 0 }),
            { status: 200 },
          ),
        )
      }
      if (url.includes('plugin-context')) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              isAdmin: false,
              sub: 'user-1',
              username: 'testuser',
              groups: [],
            }),
            { status: 200 },
          ),
        )
      }
      return Promise.resolve(emptyAgentsResponse())
    }),
  )
})
afterEach(() => {
  vi.unstubAllGlobals()
})

describe('Spine', () => {
  it('renders the brand mark and the operator footer', () => {
    renderAt('/dashboard')
    expect(screen.getByText('AInsel')).toBeInTheDocument()
    expect(screen.getByText('kim')).toBeInTheDocument()
  })

  it('marks the route matching the current path as active', () => {
    renderAt('/agents')
    const agents = screen.getByRole('link', { name: /Agents/i })
    expect(agents).toHaveAttribute('aria-current', 'page')
  })

  it('marks the Docs entry as active when on /docs', () => {
    renderAt('/docs')
    const docs = screen.getByRole('link', { name: /Docs/i })
    expect(docs).toHaveAttribute('aria-current', 'page')
  })

  it('marks the Docs entry as active when on /docs/mcp', () => {
    renderAt('/docs/mcp')
    const docs = screen.getByRole('link', { name: /Docs/i })
    expect(docs).toHaveAttribute('aria-current', 'page')
  })

  it('renders all nav items', () => {
    renderAt('/dashboard')
    const expected = [
      'Dashboard',
      'Channels',
      'Activity',
      'Observability',
      'Agents',
      'Images',
      'Personas',
      'Connectors',
      'Docs',
      'MCPs',
      'Skills',
    ]
    for (const name of expected) {
      expect(screen.getByRole('link', { name: new RegExp(name, 'i') })).toBeInTheDocument()
    }
  })

  it('has no Chat item: chat lives inside each agent', () => {
    renderAt('/dashboard')
    expect(screen.getByRole('link', { name: /Dashboard/i })).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /Chat/i })).toBeNull()
  })

  it('groups the shared catalogs under Library', () => {
    renderAt('/dashboard')

    const section = screen.getByText('Library').closest('div')?.parentElement
    expect(section).not.toBeNull()
    for (const name of ['Images', 'Personas', 'Skills', 'MCPs']) {
      expect(
        within(section as HTMLElement).getByRole('link', { name: new RegExp(name, 'i') }),
      ).toBeInTheDocument()
    }

    // The old Setup section is gone: its entries moved into Library.
    expect(screen.queryByText('Setup')).not.toBeInTheDocument()
  })

  it('links the library entries to their catalog routes', () => {
    renderAt('/dashboard')
    expect(screen.getByRole('link', { name: /Images/i })).toHaveAttribute('href', '/agent-images')
    expect(screen.getByRole('link', { name: /Personas/i })).toHaveAttribute('href', '/personas')
  })

  it('does not render Error Log in nav (redirected to /observability/errors)', () => {
    renderAt('/dashboard')
    expect(screen.queryByRole('link', { name: /Error Log/i })).toBeNull()
  })

  it('no longer renders the Triggers entry (moved under Agent Detail)', () => {
    renderAt('/dashboard')
    expect(screen.queryByRole('link', { name: /^Triggers$/i })).toBeNull()
  })

  it('renders plugin pages under their own section heading, after Library', async () => {
    vi.unstubAllGlobals()
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => {
        if (url.includes('/plugins')) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                items: [
                  {
                    id: 'memory-plugin',
                    displayName: 'Memory Plugin',
                    ready: true,
                    manifest: {
                      id: 'memory-plugin',
                      displayName: 'Memory Plugin',
                      pluginApi: '1.1',
                      ui: {
                        entry: '/static/ui/index.js',
                        pages: [
                          { path: '/', label: 'Memories', section: 'Memory' },
                          { path: '/loose', label: 'Loose' },
                        ],
                      },
                    },
                  },
                ],
              }),
              { status: 200 },
            ),
          )
        }
        if (url.includes('plugin-context')) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                isAdmin: false,
                sub: 'user-1',
                username: 'testuser',
                groups: [],
              }),
              { status: 200 },
            ),
          )
        }
        return Promise.resolve(emptyAgentsResponse())
      }),
    )

    renderAt('/dashboard')

    const memoriesLink = await screen.findByRole('link', { name: /Memories/i })
    const looseLink = screen.getByRole('link', { name: /Loose/i })

    // Each link sits in the block whose heading names its section.
    const headingOf = (link: HTMLElement) =>
      link.closest('div:has(> .nav-section)')!.querySelector('.nav-section .label')!.textContent
    expect(headingOf(memoriesLink)).toBe('Memory')
    expect(headingOf(looseLink)).toBe('Plugins')

    const headings = Array.from(document.querySelectorAll('.nav-section .label')).map(
      (el) => el.textContent,
    )
    expect(headings.indexOf('Memory')).toBe(headings.indexOf('Library') + 1)
  })

  // Regression for ainsel-plugin-memory#9: the memory plugin is the first to
  // combine a root page (/) with a nested page (/setup), and NavLink's
  // default prefix matching lit up *both* entries on /p/memory/setup. Plugin
  // entries must use the most-specific-match-wins rule in both render paths:
  // inside built-in sections and under plugin-owned headings.
  function stubMemoryPlugin(section: string) {
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => {
        if (url.includes('/plugins')) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                items: [
                  {
                    id: 'memory',
                    displayName: 'Memory',
                    ready: true,
                    manifest: {
                      id: 'memory',
                      displayName: 'Memory',
                      pluginApi: '1.1',
                      ui: {
                        entry: '/static/ui/index.js',
                        pages: [
                          { path: '/', label: 'Memories', section },
                          { path: '/setup', label: 'Setup', section, visibility: 'admin' },
                        ],
                      },
                    },
                  },
                ],
              }),
              { status: 200 },
            ),
          )
        }
        if (url.includes('plugin-context')) {
          return Promise.resolve(
            new Response(JSON.stringify({ isAdmin: true, sub: 'u', username: 'kim', groups: [] }), {
              status: 200,
            }),
          )
        }
        return Promise.resolve(emptyAgentsResponse())
      }),
    )
  }

  function activePluginLinks() {
    return Array.from(document.querySelectorAll('.nav-link.active')).filter((el) =>
      ['/p/memory', '/p/memory/setup'].includes(el.getAttribute('href') ?? ''),
    )
  }

  it('highlights only the nested plugin entry, not the root entry (built-in section)', async () => {
    stubMemoryPlugin('Observability')
    renderAt('/p/memory/setup')

    await screen.findByRole('link', { name: /Setup/i })
    expect(activePluginLinks().map((el) => el.getAttribute('href'))).toEqual(['/p/memory/setup'])
    // aria-current follows the same rule as the class, so assistive tech
    // never sees two "current page" links either.
    expect(screen.getByRole('link', { name: /Setup/i })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: /Memories/i })).not.toHaveAttribute('aria-current')
  })

  it('highlights only the nested plugin entry, not the root entry (plugin-owned heading)', async () => {
    stubMemoryPlugin('Memory')
    renderAt('/p/memory/setup')

    await screen.findByRole('link', { name: /Setup/i })
    expect(activePluginLinks().map((el) => el.getAttribute('href'))).toEqual(['/p/memory/setup'])
  })

  it('highlights the root plugin entry on its own path', async () => {
    stubMemoryPlugin('Observability')
    renderAt('/p/memory')

    await screen.findByRole('link', { name: /Memories/i })
    expect(activePluginLinks().map((el) => el.getAttribute('href'))).toEqual(['/p/memory'])
  })

  it('keeps the nested entry highlighted on deep links under its path', async () => {
    stubMemoryPlugin('Observability')
    renderAt('/p/memory/setup/42')

    await screen.findByRole('link', { name: /Setup/i })
    expect(activePluginLinks().map((el) => el.getAttribute('href'))).toEqual(['/p/memory/setup'])
  })

  // The most-specific-match rule is not plugin-only: every top-level nav
  // entry (built-in and plugin) is resolved through one combined
  // activeNavPath, so the sidebar has a single uniform matching rule.
  function activeTopLevelLinks() {
    return Array.from(document.querySelectorAll('a.nav-link.active')).map((el) =>
      el.getAttribute('href'),
    )
  }

  it('keeps the Agents entry highlighted on agent detail paths', async () => {
    renderAt('/agents/a-1')

    const agents = screen.getByRole('link', { name: /^\s*02\s*Agents/i })
    expect(agents).toHaveAttribute('aria-current', 'page')
    expect(agents).toHaveClass('active')
  })

  it('highlights exactly one top-level entry when built-in and plugin entries coexist', async () => {
    stubMemoryPlugin('Observability')
    renderAt('/p/memory/setup')

    await screen.findByRole('link', { name: /Setup/i })
    // /p/memory/setup matches no built-in entry, and among the plugin entries
    // only the nested one wins — never the root entry, never Observability too.
    expect(activeTopLevelLinks()).toEqual(['/p/memory/setup'])
  })

  it('highlights exactly one top-level entry on built-in deep links with plugins present', async () => {
    stubMemoryPlugin('Observability')
    renderAt('/docs/mcp')

    await screen.findByRole('link', { name: /Memories/i })
    expect(activeTopLevelLinks()).toEqual(['/docs'])
  })

  describe('rail mode', () => {
    it('shows only the idx and exposes the name as title and aria-label', () => {
      renderAt('/dashboard', { rail: true })
      const agents = screen.getByRole('link', { name: 'Agents' })
      expect(agents).toHaveAttribute('aria-label', 'Agents')
      expect(agents).toHaveAttribute('title', 'Agents')
      expect(agents).toHaveClass('nav-link')
      expect(agents).toHaveTextContent('02')
      // The name text is not rendered in the rail: the idx is the only label.
      expect(agents.querySelector('.name')).toBeNull()
    })

    it('hides section labels', () => {
      renderAt('/dashboard', { rail: true })
      for (const label of ['Operations', 'Fleet', 'Library', 'Docs', 'Admin']) {
        expect(screen.queryByText(label)).toBeNull()
      }
    })

    it('keeps the full menu while the mobile drawer is open', () => {
      // Rail mode is a desktop-canvas concern; opening the drawer on mobile
      // must still give the full navigation, names and all.
      renderAt('/agents', { rail: true, open: true })
      const spine = document.querySelector('.spine')
      expect(spine).toHaveClass('open')
      expect(spine).not.toHaveClass('rail')
      expect(screen.getByText('Operations')).toBeInTheDocument()
      expect(screen.getByRole('link', { name: /Dashboard/i })).toBeInTheDocument()
    })
  })
})
