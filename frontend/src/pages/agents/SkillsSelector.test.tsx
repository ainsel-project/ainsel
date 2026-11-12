import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes } from 'react-router-dom'
import { SkillsSelector } from './SkillsSelector'
import { renderWithProviders } from '../../test/renderWithProviders'

function listFixture() {
  return {
    items: [
      {
        id: 'git-ops',
        name: 'git-ops',
        description: 'Git operations skill',
        tags: ['git'],
        usedBy: 0,
        createdAt: '',
        updatedAt: '',
      },
      {
        id: 'k8s-deploy',
        name: 'k8s-deploy',
        description: 'Kubernetes deploy skill',
        tags: [],
        usedBy: 0,
        createdAt: '',
        updatedAt: '',
      },
    ],
    total: 2,
    page: 1,
    pageSize: 200,
  }
}

const details: Record<string, unknown> = {
  'git-ops': {
    id: 'git-ops',
    name: 'git-ops',
    description: 'Git operations skill',
    tags: ['git'],
    managedBy: 'gitops',
    createdAt: '',
    updatedAt: '',
    body: '# Git ops\n\nDo the thing.',
  },
  'k8s-deploy': {
    id: 'k8s-deploy',
    name: 'k8s-deploy',
    description: 'Kubernetes deploy skill',
    tags: [],
    createdAt: '',
    updatedAt: '',
    body: '',
  },
}

function fakeFetch(url: string): Response {
  const detail = Object.entries(details).find(([id]) => url.includes(`/skills/${id}`))
  if (detail) return new Response(JSON.stringify(detail[1]), { status: 200 })
  if (url.includes('/skills')) return new Response(JSON.stringify(listFixture()), { status: 200 })
  return new Response('{}', { status: 200 })
}

function renderSelector(props: Partial<React.ComponentProps<typeof SkillsSelector>> = {}) {
  return renderWithProviders(
    <Routes>
      <Route
        path="/"
        element={<SkillsSelector selectedIds={['k8s-deploy']} onChange={() => {}} {...props} />}
      />
      <Route path="/skills/:id" element={<div>skill detail</div>} />
    </Routes>,
    { route: '/' },
  )
}

/** The preview Panel is the panel whose header carries the skill's name. */
async function previewPanel(name: string): Promise<HTMLElement> {
  const heading = await screen.findByRole('heading', { level: 3, name })
  return (heading.closest('.panel') as HTMLElement) ?? heading
}

function enabledPane(): HTMLElement {
  return screen.getByRole('listbox', { name: /enabled on this agent/i })
}

describe('SkillsSelector — read-only preview (issue #216)', () => {
  beforeEach(() => {
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => Promise.resolve(fakeFetch(String(url)))),
    )
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders no preview panel before a row is activated', async () => {
    renderSelector({ previewable: true })
    await waitFor(() => expect(screen.getByText('git-ops')).toBeInTheDocument())
    expect(screen.queryByText('Open skill →')).not.toBeInTheDocument()
  })

  it('shows the rendered skill body below the list when a row is clicked', async () => {
    const user = userEvent.setup()
    renderSelector({ previewable: true })
    await waitFor(() => expect(screen.getByText('git-ops')).toBeInTheDocument())
    await user.click(screen.getByRole('option', { name: /git-ops/ }))

    const preview = await previewPanel('git-ops')
    await waitFor(() => expect(within(preview).getByText('Do the thing.')).toBeInTheDocument())
    // Body goes through the Markdown primitive, not a plain <pre>.
    expect(preview.querySelector('h1')).toHaveTextContent('Git ops')
    expect(within(preview).getByText('Open skill →')).toBeInTheDocument()
  })

  it('marks externally managed skills and links to the detail page', async () => {
    const user = userEvent.setup()
    renderSelector({ previewable: true })
    await waitFor(() => expect(screen.getByText('git-ops')).toBeInTheDocument())
    await user.click(screen.getByRole('option', { name: /git-ops/ }))
    const preview = await previewPanel('git-ops')
    expect(within(preview).getByText(/managed by gitops/i)).toBeInTheDocument()
    expect(within(preview).getByRole('link', { name: 'Open skill →' })).toHaveAttribute(
      'href',
      '/skills/git-ops',
    )
  })

  it('shows the empty-body fallback', async () => {
    const user = userEvent.setup()
    renderSelector({ previewable: true })
    await waitFor(() => expect(screen.getByText('git-ops')).toBeInTheDocument())
    await user.click(within(enabledPane()).getByRole('option', { name: /k8s-deploy/ }))
    const preview = await previewPanel('k8s-deploy')
    expect(within(preview).getByText('This skill has no body yet.')).toBeInTheDocument()
  })

  it('replaces the preview when another row is activated', async () => {
    const user = userEvent.setup()
    renderSelector({ previewable: true })
    await waitFor(() => expect(screen.getByText('git-ops')).toBeInTheDocument())
    await user.click(screen.getByRole('option', { name: /git-ops/ }))
    await previewPanel('git-ops')
    await user.click(within(enabledPane()).getByRole('option', { name: /k8s-deploy/ }))
    await waitFor(() =>
      expect(screen.queryByRole('heading', { level: 3, name: 'git-ops' })).not.toBeInTheDocument(),
    )
    expect(await previewPanel('k8s-deploy')).toBeInTheDocument()
  })

  it('keeps the preview after the skill is moved into the enabled pane', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    renderSelector({ previewable: true, onChange })
    await waitFor(() => expect(screen.getByText('git-ops')).toBeInTheDocument())
    await user.click(screen.getByRole('option', { name: /git-ops/ }))
    await previewPanel('git-ops')
    await user.click(screen.getByRole('button', { name: /add selected/i }))
    expect(onChange).toHaveBeenCalledWith(['k8s-deploy', 'git-ops'])
    expect(screen.getByRole('heading', { level: 3, name: 'git-ops' })).toBeInTheDocument()
  })

  it('stays off when previewable is not set', async () => {
    const user = userEvent.setup()
    renderSelector()
    await waitFor(() => expect(screen.getByText('git-ops')).toBeInTheDocument())
    await user.click(screen.getByRole('option', { name: /git-ops/ }))
    expect(screen.queryByText('Open skill →')).not.toBeInTheDocument()
    expect(document.querySelector('.dual-list-rows--tall')).toBeNull()
  })

  it('enables the tall rows variant in preview mode', async () => {
    renderSelector({ previewable: true })
    await waitFor(() => expect(screen.getByText('git-ops')).toBeInTheDocument())
    expect(document.querySelectorAll('.dual-list-rows--tall')).toHaveLength(2)
  })
})
