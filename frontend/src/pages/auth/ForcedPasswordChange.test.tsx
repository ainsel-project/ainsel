import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../../auth/AuthProvider'
import { ForcedPasswordChange } from './ForcedPasswordChange'
import { renderWithProviders } from '../../test/renderWithProviders'
import type { ResolvedConfig } from '../../runtime-config'

// The forced change screen (issue #113) is the only thing a local account with an
// issued password can reach: the hub gate 403s everything else. These tests cover
// who the screen shows itself to, and what a successful change leaves behind.

const LOCAL_CFG: ResolvedConfig = {
  authMode: 'local',
  oidcIssuer: 'https://oidc.example.com',
  oidcClientId: 'test-client',
  oidcProjectId: 'test-project',
}

function seedLocalSession(user: Record<string, unknown>) {
  localStorage.setItem('ainsel.local.token', 'local-tok')
  localStorage.setItem('ainsel.local.user', JSON.stringify(user))
  localStorage.setItem('ainsel.local.expiresAt', new Date(Date.now() + 600_000).toISOString())
}

function storedUser(): Record<string, unknown> {
  return JSON.parse(localStorage.getItem('ainsel.local.user') ?? '{}')
}

function renderScreen(route = '/change-password') {
  return renderWithProviders(
    <AuthProvider>
      <Routes>
        <Route path="/change-password" element={<ForcedPasswordChange />} />
        <Route path="/login" element={<div>login</div>} />
        <Route path="/dashboard" element={<div>console</div>} />
      </Routes>
    </AuthProvider>,
    { route },
  )
}

function mockPasswordChange(response: () => Response) {
  const fetchMock = vi.fn((url: string, init?: RequestInit) => {
    if (String(url).includes('/auth/password') && init?.method === 'PUT') {
      return Promise.resolve(response())
    }
    return Promise.resolve(new Response('{}', { status: 200 }))
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

describe('ForcedPasswordChange', () => {
  beforeEach(() => {
    localStorage.clear()
    window.__AINSEL_CONFIG__ = LOCAL_CFG
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('asks a flagged user for a new password', () => {
    seedLocalSession({
      sub: 'local:admin',
      username: 'admin',
      isAdmin: true,
      mustChangePassword: true,
    })
    renderScreen()
    expect(screen.getByText(/your password was issued by an administrator/)).toBeInTheDocument()
    expect(screen.getByLabelText('Current password')).toBeInTheDocument()
    expect(screen.getByLabelText('New password')).toBeInTheDocument()
    expect(screen.getByLabelText('Confirm new password')).toBeInTheDocument()
  })

  it('changes the password, clears the flag and opens the console', async () => {
    const fetchMock = mockPasswordChange(
      () => new Response(JSON.stringify({ status: 'ok' }), { status: 200 }),
    )
    seedLocalSession({
      sub: 'local:admin',
      username: 'admin',
      isAdmin: true,
      mustChangePassword: true,
    })
    renderScreen()

    await userEvent.type(screen.getByLabelText('Current password'), 'bootstrap-pass')
    await userEvent.type(screen.getByLabelText('New password'), 'self-chosen-pass')
    await userEvent.type(screen.getByLabelText('Confirm new password'), 'self-chosen-pass')
    await userEvent.click(screen.getByRole('button', { name: /change password/i }))

    await waitFor(() => expect(screen.getByText('console')).toBeInTheDocument())
    expect(storedUser().mustChangePassword).toBe(false)
    const call = fetchMock.mock.calls.find(
      ([url, init]) => String(url).includes('/auth/password') && init?.method === 'PUT',
    )
    expect(JSON.parse(String(call?.[1]?.body))).toEqual({
      current: 'bootstrap-pass',
      new: 'self-chosen-pass',
    })
  })

  it('keeps the user on the screen when the current password is wrong', async () => {
    mockPasswordChange(
      () =>
        new Response(JSON.stringify({ message: 'current password is incorrect' }), { status: 401 }),
    )
    seedLocalSession({
      sub: 'local:admin',
      username: 'admin',
      isAdmin: true,
      mustChangePassword: true,
    })
    renderScreen()

    await userEvent.type(screen.getByLabelText('Current password'), 'wrong')
    await userEvent.type(screen.getByLabelText('New password'), 'self-chosen-pass')
    await userEvent.type(screen.getByLabelText('Confirm new password'), 'self-chosen-pass')
    await userEvent.click(screen.getByRole('button', { name: /change password/i }))

    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent('current password is incorrect'),
    )
    expect(storedUser().mustChangePassword).toBe(true)
    expect(screen.queryByText('console')).not.toBeInTheDocument()
  })

  it('stays put when the hub rejects the new password', async () => {
    // The gate clears the flag only on a 200, so a policy rejection must not
    // hand out the console.
    mockPasswordChange(
      () =>
        new Response(JSON.stringify({ message: 'password must be at least 8 characters' }), {
          status: 400,
        }),
    )
    seedLocalSession({
      sub: 'local:admin',
      username: 'admin',
      isAdmin: true,
      mustChangePassword: true,
    })
    renderScreen()

    await userEvent.type(screen.getByLabelText('Current password'), 'bootstrap-pass')
    await userEvent.type(screen.getByLabelText('New password'), 'short')
    await userEvent.type(screen.getByLabelText('Confirm new password'), 'short')
    await userEvent.click(screen.getByRole('button', { name: /change password/i }))

    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent('password must be at least 8 characters'),
    )
    expect(screen.queryByText('console')).not.toBeInTheDocument()
  })

  it('lets a user who does not want the change log out', async () => {
    mockPasswordChange(() => new Response(null, { status: 204 }))
    seedLocalSession({
      sub: 'local:admin',
      username: 'admin',
      isAdmin: true,
      mustChangePassword: true,
    })
    renderScreen()

    await userEvent.click(screen.getByRole('button', { name: /log out/i }))

    // The hub is stateless: logging out means discarding the token, which also
    // ends the forced-change loop.
    expect(localStorage.getItem('ainsel.local.token')).toBeNull()
    expect(screen.getByText('login')).toBeInTheDocument()
  })

  it('is not a way into the console for an unflagged account', () => {
    seedLocalSession({
      sub: 'local:admin',
      username: 'admin',
      isAdmin: true,
      mustChangePassword: false,
    })
    renderScreen()
    expect(screen.getByText('console')).toBeInTheDocument()
    expect(screen.queryByLabelText('Current password')).not.toBeInTheDocument()
  })

  it('sends an anonymous visitor to /login', () => {
    renderScreen()
    expect(screen.getByText('login')).toBeInTheDocument()
  })
})
