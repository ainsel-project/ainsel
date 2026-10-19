import { screen } from '@testing-library/react'
import { Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it } from 'vitest'
import { AuthProvider, type AuthUser } from './AuthProvider'
import { RequireAuth } from './RequireAuth'
import { renderWithProviders } from '../test/renderWithProviders'
import type { ResolvedConfig } from '../runtime-config'

// RequireAuth decides what a local account sees while it still holds a password
// it did not choose (issue #113). The hub enforces the same rule with a 403, so
// these tests are about the routing, not about security.

const LOCAL_CFG: ResolvedConfig = {
  authMode: 'local',
  oidcIssuer: 'https://oidc.example.com',
  oidcClientId: 'test-client',
  oidcProjectId: 'test-project',
}

function seedLocalSession(user: Partial<AuthUser> & { sub: string }) {
  localStorage.setItem('ainsel.local.token', 'local-tok')
  localStorage.setItem('ainsel.local.user', JSON.stringify(user))
  localStorage.setItem('ainsel.local.expiresAt', new Date(Date.now() + 600_000).toISOString())
}

function seedConfig(cfg: ResolvedConfig) {
  window.__AINSEL_CONFIG__ = cfg
}

function renderGate(route: string) {
  return renderWithProviders(
    <AuthProvider>
      <Routes>
        <Route
          path="/dashboard"
          element={
            <RequireAuth>
              <div>console</div>
            </RequireAuth>
          }
        />
        <Route path="/login" element={<div>login</div>} />
        <Route path="/change-password" element={<div>change-password</div>} />
      </Routes>
    </AuthProvider>,
    { route },
  )
}

describe('RequireAuth (forced password change)', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it('sends a flagged local account to the change screen instead of the console', () => {
    seedConfig(LOCAL_CFG)
    seedLocalSession({
      sub: 'local:admin',
      username: 'admin',
      isAdmin: true,
      mustChangePassword: true,
    })
    renderGate('/dashboard')
    expect(screen.getByText('change-password')).toBeInTheDocument()
    expect(screen.queryByText('console')).not.toBeInTheDocument()
  })

  it('lets a local account that chose its own password through', () => {
    seedConfig(LOCAL_CFG)
    seedLocalSession({
      sub: 'local:admin',
      username: 'admin',
      isAdmin: true,
      mustChangePassword: false,
    })
    renderGate('/dashboard')
    expect(screen.getByText('console')).toBeInTheDocument()
  })

  it('treats a session stored before the flag existed as unflagged', () => {
    // Sessions persist in localStorage across an upgrade, so a missing field
    // must not strand an existing user on the change screen.
    seedConfig(LOCAL_CFG)
    seedLocalSession({ sub: 'local:admin', username: 'admin' })
    renderGate('/dashboard')
    expect(screen.getByText('console')).toBeInTheDocument()
  })

  it('still prefers /login when there is no token to change the password with', () => {
    seedConfig(LOCAL_CFG)
    renderGate('/dashboard')
    expect(screen.getByText('login')).toBeInTheDocument()
  })
})
