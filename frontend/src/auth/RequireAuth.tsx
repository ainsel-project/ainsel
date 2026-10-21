import { useEffect, type ReactNode } from 'react'
import { Navigate } from 'react-router-dom'
import { useAuth } from './AuthProvider'

// RequireAuth gates the authenticated app shell per mode:
//   - none:       everyone passes (there is no auth),
//   - local:      without a valid token the user is sent to /login,
//   - oidc+local: same as local — /login renders the chooser (issue #114);
//                 no auto-redirect to the IdP,
//   - oidc:       unauthenticated users are redirected to the IdP (legacy flow).
//
// On top of that, a local account still holding a password it did not choose
// is sent to /change-password instead of the console (issue #113). The flag
// decides what is rendered, nothing more: the hub enforces the same rule with
// a 403 `password_change_required` on every endpoint the change screen does not
// need, so a client that ignored this gate still could not use the API. The
// hub only ever arms the flag for local accounts, so checking the flag alone
// is correct in every mode.
export function RequireAuth({ children }: { children: ReactNode }) {
  const auth = useAuth()

  const oidcNeedsRedirect = auth.mode === 'oidc' && auth.ready && !auth.token
  useEffect(() => {
    if (oidcNeedsRedirect) auth.signinRedirect()
  }, [oidcNeedsRedirect, auth])

  if (auth.mode === 'none') return <>{children}</>
  if (!auth.ready) return null
  if (!auth.token) {
    if (auth.mode === 'local' || auth.mode === 'oidc+local') {
      return <Navigate to="/login" replace />
    }
    return null // oidc: signinRedirect is in flight
  }
  if (auth.user?.mustChangePassword) {
    return <Navigate to="/change-password" replace />
  }
  return <>{children}</>
}
