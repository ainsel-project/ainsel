import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useEffect } from 'react'
import { MemoryRouter, Route, Routes, useNavigate } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { Layout } from './Layout'

vi.mock('../auth/AuthProvider', () => ({
  useAuth: () => ({
    token: 'test-token',
    user: { username: 'kim', email: 'kim@example.com' },
    ready: true,
    signinRedirect: vi.fn(),
    signoutRedirect: vi.fn(),
  }),
}))

// The failure mode from issue #283: a list page handed a response shape it did
// not expect throws during render.
function Boom(): never {
  throw new TypeError('groups.map is not a function')
}

// Throws while `failNext` is set — to exercise the retry path. A plain
// attempt counter is not enough: React retries a failed concurrent render
// synchronously, so the boundary must see the throw on every pass until the
// test releases it.
let failNext = true
function Flaky() {
  if (failNext) throw new TypeError('rows.map is not a function')
  return <div>Recovered page</div>
}

function renderLayout(initialPath: string, queryClient = new QueryClient()) {
  // Captured via an effect (not a render-phase assignment) so the
  // react-hooks/globals rule stays happy and the router hook is used legally.
  const nav: { go?: (to: string) => void } = {}
  const Capture = () => {
    const navigate = useNavigate()
    useEffect(() => {
      nav.go = navigate
    })
    return null
  }
  const utils = render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[initialPath]}>
        <Capture />
        <Routes>
          <Route element={<Layout />}>
            <Route path="/boom" element={<Boom />} />
            <Route path="/flaky" element={<Flaky />} />
            <Route path="/dashboard" element={<div>Dashboard page</div>} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return {
    ...utils,
    goTo: (to: string) => act(() => nav.go!(to)),
  }
}

beforeEach(() => {
  failNext = true
  // The boundary (correctly) logs the caught error; keep the run quiet.
  vi.spyOn(console, 'error').mockImplementation(() => {})
  vi.stubGlobal(
    'fetch',
    vi.fn(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({ items: [], total: 0, page: 1, pageSize: 200, totalPages: 0 }),
          { status: 200 },
        ),
      ),
    ),
  )
})
afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('RouteErrorBoundary (issue #283)', () => {
  it('spine survives: a throwing route shows the error state, navigation stays usable', () => {
    renderLayout('/boom')
    expect(screen.getByText('This page failed to render')).toBeInTheDocument()
    expect(screen.getByText('groups.map is not a function')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
    // The shell and spine are still mounted — the operator can still navigate.
    expect(screen.getByRole('navigation', { name: 'Primary' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Dashboard/i })).toBeInTheDocument()
  })

  it('route change clears the error state', () => {
    const { goTo } = renderLayout('/boom')
    expect(screen.getByText('This page failed to render')).toBeInTheDocument()
    goTo('/dashboard')
    expect(screen.queryByText('This page failed to render')).not.toBeInTheDocument()
    expect(screen.getByText('Dashboard page')).toBeInTheDocument()
  })

  it('retry clears the error and invalidates react-query (no navigation, no reload)', async () => {
    const queryClient = new QueryClient()
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
    renderLayout('/flaky', queryClient)
    expect(screen.getByText('This page failed to render')).toBeInTheDocument()

    failNext = false
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))

    expect(screen.getByText('Recovered page')).toBeInTheDocument()
    expect(screen.queryByText('This page failed to render')).not.toBeInTheDocument()
    expect(invalidate).toHaveBeenCalled()
  })
})
