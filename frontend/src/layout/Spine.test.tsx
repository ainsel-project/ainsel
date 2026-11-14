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

  it('renders exactly one Plugins entry, pointing at the plugins workspace', () => {
    renderAt('/dashboard')
    const links = screen.getAllByRole('link', { name: /Plugins/i })
    expect(links).toHaveLength(1)
    expect(links[0]).toHaveAttribute('href', '/plugins')
  })

  // Plugin navigation lives in the plugins workspace sidebar now (issue #211):
  // the spine renders built-in entries only, even with active plugins that
  // declare pages.
  it('renders no plugin page entries in the spine', () => {
    renderAt('/dashboard')
    expect(screen.queryByRole('link', { name: /Memories/i })).toBeNull()
    expect(screen.queryByRole('link', { name: /Setup/i })).toBeNull()
    expect(screen.queryByText('Memory')).toBeNull()
  })

  it('keeps the Agents entry highlighted on agent detail paths', async () => {
    renderAt('/agents/a-1')

    const agents = screen.getByRole('link', { name: 'Agents' })
    expect(agents).toHaveAttribute('aria-current', 'page')
    expect(agents).toHaveClass('active')
  })

  it('numbers the entries sequentially and marks the numbers decorative', () => {
    renderAt('/dashboard')
    const numbers = Array.from(document.querySelectorAll('.nav-link .idx'))
    expect(numbers.map((el) => el.textContent)).toEqual([
      '01',
      '02',
      '03',
      '04',
      '05',
      '06',
      '07',
      '08',
      '09',
      '10',
      '11',
      '12',
      '13',
      '14',
      '15',
    ])
    // The number is decoration, never a label.
    for (const el of numbers) {
      expect(el).toHaveAttribute('aria-hidden', 'true')
    }
  })

  it('gives every nav entry an icon', () => {
    renderAt('/dashboard')
    const links = Array.from(document.querySelectorAll('.nav-link'))
    expect(links).toHaveLength(15)
    for (const link of links) {
      const icon = link.querySelector('svg.nav-icon')
      expect(icon).not.toBeNull()
      // Decorative: the link is named by its text, not by the glyph.
      expect(icon).toHaveAttribute('aria-hidden', 'true')
    }
  })

  it('keeps the number out of the accessible name', () => {
    // The idx is aria-hidden, so the expanded link is named by its text alone —
    // "Dashboard", not "01Dashboard".
    renderAt('/dashboard')
    expect(screen.getByRole('link', { name: /^Dashboard$/ })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /^Users$/ })).toBeInTheDocument()
  })

  describe('rail mode', () => {
    it('shows the icon and exposes the name as title and aria-label', () => {
      renderAt('/dashboard', { rail: true })
      const agents = screen.getByRole('link', { name: 'Agents' })
      expect(agents).toHaveAttribute('aria-label', 'Agents')
      expect(agents).toHaveAttribute('title', 'Agents')
      expect(agents).toHaveClass('nav-link')
      const icon = agents.querySelector('svg.nav-icon')
      expect(icon).not.toBeNull()
      expect(icon).toHaveAttribute('aria-hidden', 'true')
      // The name text is not rendered in the rail: the icon is the only visible
      // mark (the idx stays in the DOM but `.spine.rail .nav-link .idx` hides it).
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
