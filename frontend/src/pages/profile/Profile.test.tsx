import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Profile } from './Profile'
import { renderWithProviders } from '../../test/renderWithProviders'

const { authState } = vi.hoisted(() => ({
  authState: {
    mode: 'oidc' as string,
    ready: true,
    token: 'tok',
    user: { sub: 'user-123', username: 'kim' },
    isAdmin: true,
    login: vi.fn(),
    logout: vi.fn(),
    signinRedirect: vi.fn(),
    signoutRedirect: vi.fn(),
  },
}))

vi.mock('../../auth/AuthProvider', () => ({
  useAuth: () => authState,
}))

function defaultFetch(url: string): Response {
  if (url.includes('/users/user-123')) {
    return new Response(
      JSON.stringify({
        id: 'user-123',
        username: 'kim',
        email: 'kim@example.com',
        isAdmin: true,
        createdAt: '2026-05-22T00:00:00Z',
        updatedAt: '2026-05-22T00:00:00Z',
      }),
      { status: 200 },
    )
  }
  if (url.includes('/user-tokens')) {
    return new Response(JSON.stringify([]), { status: 200 })
  }
  return new Response('{}', { status: 200 })
}

describe('Profile', () => {
  beforeEach(() => {
    authState.mode = 'oidc'
    authState.token = 'tok'
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => Promise.resolve(defaultFetch(url))),
    )
    vi.stubGlobal(
      'matchMedia',
      vi.fn((query: string) => ({
        matches: query === '(prefers-color-scheme: dark)',
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      })),
    )
    localStorage.clear()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    localStorage.clear()
  })

  it('renders the Account panel with user details', async () => {
    renderWithProviders(<Profile />, { route: '/profile' })
    await waitFor(() => expect(screen.getByText('kim')).toBeInTheDocument())
    expect(screen.getByText('kim@example.com')).toBeInTheDocument()
    expect(screen.getByText('Admin')).toBeInTheDocument()
  })

  it('renders the Appearance panel with a theme selector', async () => {
    renderWithProviders(<Profile />, { route: '/profile' })
    expect(screen.getByText('Appearance')).toBeInTheDocument()
    const select = screen.getByLabelText('Theme')
    expect(select).toBeInTheDocument()
    expect(select).toHaveValue('light')
  })

  it('stores the selected theme in localStorage', async () => {
    renderWithProviders(<Profile />, { route: '/profile' })
    const select = screen.getByLabelText('Theme')
    await userEvent.selectOptions(select, 'dark')
    await waitFor(() => {
      expect(localStorage.getItem('ainsel-theme')).toBe('dark')
    })
    expect(document.documentElement.dataset.theme).toBe('dark')
  })

  it('renders a Sync button in the Account panel', async () => {
    renderWithProviders(<Profile />, { route: '/profile' })
    await waitFor(() => expect(screen.getByText('kim')).toBeInTheDocument())
    expect(screen.getByRole('button', { name: /sync username/i })).toBeInTheDocument()
  })

  it('calls POST /users/me/sync when Sync is clicked', async () => {
    let syncCalled = false
    const fetchMock = vi.fn((url: string, init?: RequestInit) => {
      if (url.includes('/users/me/sync') && init?.method === 'POST') {
        syncCalled = true
        return Promise.resolve(
          new Response(
            JSON.stringify({
              id: 'user-123',
              username: 'dpinsel',
              email: 'kim@example.com',
              isAdmin: true,
              createdAt: '2026-05-22T00:00:00Z',
              updatedAt: '2026-05-22T00:00:00Z',
            }),
            { status: 200 },
          ),
        )
      }
      if (url.includes('/users/user-123')) {
        if (syncCalled) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                id: 'user-123',
                username: 'dpinsel',
                email: 'kim@example.com',
                isAdmin: true,
                createdAt: '2026-05-22T00:00:00Z',
                updatedAt: '2026-05-22T00:00:00Z',
              }),
              { status: 200 },
            ),
          )
        }
      }
      return Promise.resolve(defaultFetch(url))
    })
    vi.stubGlobal('fetch', fetchMock)

    renderWithProviders(<Profile />, { route: '/profile' })
    await waitFor(() => expect(screen.getByText('kim')).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: /sync username/i }))

    await waitFor(() => {
      const call = fetchMock.mock.calls.find(
        ([url, init]) => url.includes('/users/me/sync') && init?.method === 'POST',
      )
      expect(call).toBeDefined()
    })

    await waitFor(() => expect(screen.getByText('dpinsel')).toBeInTheDocument())
  })

  it('lists peat and tallow as theme options', async () => {
    renderWithProviders(<Profile />, { route: '/profile' })
    const select = screen.getByLabelText('Theme')
    const options = Array.from(select.querySelectorAll('option')).map(o => o.value)
    expect(options).toContain('peat')
    expect(options).toContain('tallow')
  })

  it('stores peat in localStorage when selected', async () => {
    renderWithProviders(<Profile />, { route: '/profile' })
    const select = screen.getByLabelText('Theme')
    await userEvent.selectOptions(select, 'peat')
    await waitFor(() => {
      expect(localStorage.getItem('ainsel-theme')).toBe('peat')
    })
    expect(document.documentElement.dataset.theme).toBe('peat')
  })

  it('stores tallow in localStorage when selected', async () => {
    renderWithProviders(<Profile />, { route: '/profile' })
    const select = screen.getByLabelText('Theme')
    await userEvent.selectOptions(select, 'tallow')
    await waitFor(() => {
      expect(localStorage.getItem('ainsel-theme')).toBe('tallow')
    })
    expect(document.documentElement.dataset.theme).toBe('tallow')
  })

  function passwordFetch(response: Response) {
    return vi.fn((url: string, init?: RequestInit) => {
      if (url.includes('/auth/password') && init?.method === 'PUT') {
        return Promise.resolve(response.clone())
      }
      return Promise.resolve(defaultFetch(url))
    })
  }

  async function fillPasswordForm(current: string, next: string, confirm: string) {
    await userEvent.type(screen.getByLabelText('Current password'), current)
    await userEvent.type(screen.getByLabelText('New password'), next)
    await userEvent.type(screen.getByLabelText('Confirm new password'), confirm)
    await userEvent.click(screen.getByRole('button', { name: /change password/i }))
  }

  it('does not render the Change password panel in OIDC mode', async () => {
    renderWithProviders(<Profile />, { route: '/profile' })
    await waitFor(() => expect(screen.getByText('kim')).toBeInTheDocument())
    expect(screen.queryByText('Change password')).not.toBeInTheDocument()
  })

  it('renders the Change password panel in local mode', async () => {
    authState.mode = 'local'
    renderWithProviders(<Profile />, { route: '/profile' })
    await waitFor(() => expect(screen.getByText('kim')).toBeInTheDocument())
    expect(screen.getByLabelText('Current password')).toBeInTheDocument()
    expect(screen.getByLabelText('New password')).toBeInTheDocument()
    expect(screen.getByLabelText('Confirm new password')).toBeInTheDocument()
  })

  it('shows the server error when the current password is wrong', async () => {
    authState.mode = 'local'
    vi.stubGlobal(
      'fetch',
      passwordFetch(
        new Response(JSON.stringify({ message: 'current password is incorrect' }), {
          status: 401,
        }),
      ),
    )
    renderWithProviders(<Profile />, { route: '/profile' })
    await waitFor(() => expect(screen.getByText('kim')).toBeInTheDocument())
    await fillPasswordForm('wrong', 'newsecret1', 'newsecret1')
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent('current password is incorrect'),
    )
  })

  it('shows the policy error when the new password is too short', async () => {
    authState.mode = 'local'
    vi.stubGlobal(
      'fetch',
      passwordFetch(
        new Response(JSON.stringify({ message: 'password must be at least 8 characters' }), {
          status: 400,
        }),
      ),
    )
    renderWithProviders(<Profile />, { route: '/profile' })
    await waitFor(() => expect(screen.getByText('kim')).toBeInTheDocument())
    await fillPasswordForm('secret123', 'short', 'short')
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        'password must be at least 8 characters',
      ),
    )
  })

  it('does not call the API when the new passwords do not match', async () => {
    authState.mode = 'local'
    const fetchMock = passwordFetch(new Response('{}', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    renderWithProviders(<Profile />, { route: '/profile' })
    await waitFor(() => expect(screen.getByText('kim')).toBeInTheDocument())
    await fillPasswordForm('secret123', 'newsecret1', 'othersecret')
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent('New passwords do not match.'),
    )
    expect(
      fetchMock.mock.calls.some(([url, init]) => url.includes('/auth/password') && init?.method === 'PUT'),
    ).toBe(false)
  })

  it('confirms on a successful password change', async () => {
    authState.mode = 'local'
    vi.stubGlobal(
      'fetch',
      passwordFetch(new Response(JSON.stringify({ status: 'ok' }), { status: 200 })),
    )
    renderWithProviders(<Profile />, { route: '/profile' })
    await waitFor(() => expect(screen.getByText('kim')).toBeInTheDocument())
    await fillPasswordForm('secret123', 'newsecret1', 'newsecret1')
    await waitFor(() => expect(screen.getByText('Password changed.')).toBeInTheDocument())
    expect((screen.getByLabelText('Current password') as HTMLInputElement).value).toBe('')
    expect((screen.getByLabelText('New password') as HTMLInputElement).value).toBe('')
  })

  it('shows the hub-configured password policy hint on the change form', async () => {
    authState.mode = 'local'
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => {
        if (url.includes('/auth/password-policy')) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                minLength: 10,
                maxLength: 128,
                requireCharacterClasses: ['symbol'],
              }),
              { status: 200 },
            ),
          )
        }
        return Promise.resolve(defaultFetch(url))
      }),
    )
    renderWithProviders(<Profile />, { route: '/profile' })
    await waitFor(() => expect(screen.getByText('kim')).toBeInTheDocument())
    await waitFor(() =>
      expect(screen.getByText('At least 10 characters; 1 symbol.')).toBeInTheDocument(),
    )
  })
})
