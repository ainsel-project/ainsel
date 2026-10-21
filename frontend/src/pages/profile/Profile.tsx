import { useCallback, useState } from 'react'
import { useAuth } from '../../auth/AuthProvider'
import { changePassword } from '../../api/auth'
import { usePasswordPolicyHint } from '../../api/passwordPolicy'
import { useCurrentUser } from '../../hooks/useCurrentUser'
import { userDisplayName, useSyncMe } from '../../api/users'
import { Titleblock } from '../../layout/Titleblock'
import { Button } from '../../primitives/Button'
import { Field } from '../../primitives/Field'
import { Input } from '../../primitives/Input'
import { Panel } from '../../primitives/Panel'
import { Select } from '../../primitives/Select'
import { getStoredTheme, setStoredTheme, type Theme } from '../../theme'
import { TokenManager } from './TokenManager'

const THEME_OPTIONS: { value: Theme; label: string }[] = [
  { value: 'light', label: 'Light' },
  { value: 'dark', label: 'Dark' },
  { value: 'peat', label: 'Peat' },
  { value: 'tallow', label: 'Tallow' },
]

export function Profile() {
  const { signoutRedirect, mode } = useAuth()
  const { data: user, isLoading } = useCurrentUser()
  const [theme, setTheme] = useState<Theme>(() => getStoredTheme() ?? 'light')
  const syncMe = useSyncMe()

  const handleThemeChange = useCallback((next: string) => {
    const t = next as Theme
    setTheme(t)
    setStoredTheme(t)
  }, [])

  return (
    <>
      <Titleblock
        crumbs={<b>Profile</b>}
        title={
          <>
            Your <em>Profile</em>
          </>
        }
      />
      <div style={{ padding: '28px 32px', display: 'grid', gap: 24, maxWidth: 700 }}>
        <Panel title="Account">
          {isLoading ? (
            <div className="label" style={{ padding: 14 }}>
              Loading…
            </div>
          ) : user ? (
            <div style={{ padding: '14px 16px', display: 'grid', gap: 12 }}>
              <Row
                label="Username"
                value={userDisplayName(user)}
                action={
                  mode === 'oidc' ? (
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => syncMe.mutate()}
                      disabled={syncMe.isPending}
                      aria-label="Sync username from identity provider"
                    >
                      {syncMe.isPending ? '…' : 'Sync'}
                    </Button>
                  ) : undefined
                }
              />
              {syncMe.isError && (
                <div style={{ color: 'var(--signal)', fontSize: '0.85em', textAlign: 'right' }}>
                  Sync failed. Please try again.
                </div>
              )}
              <Row label="Email" value={user.email || '—'} />
              <Row label="Role" value={user.isAdmin ? 'Admin' : 'Member'} />
              <Row label="Member since" value={new Date(user.createdAt).toLocaleDateString()} />
            </div>
          ) : null}
        </Panel>

        <Panel title="Appearance">
          <div style={{ padding: '14px 16px' }}>
            <div style={{ display: 'flex', alignItems: 'baseline', gap: 12 }}>
              <span className="label">Theme</span>
              <Select
                id="theme-select"
                value={theme}
                onChange={handleThemeChange}
                options={THEME_OPTIONS}
                aria-label="Theme"
              />
            </div>
          </div>
        </Panel>

        <TokenManager />

        {mode !== 'oidc' && <ChangePasswordPanel />}

        <Panel title="Session">
          <div style={{ padding: '14px 16px' }}>
            <Button variant="danger" onClick={() => signoutRedirect()}>
              Log out
            </Button>
          </div>
        </Panel>
      </div>
    </>
  )
}

/** Local-auth only: the hub's PUT /auth/password requires proving the
 *  current password and rejects non-local accounts, so this panel is hidden
 *  in OIDC mode where the identity provider owns the password. The call goes
 *  through api/auth's raw-fetch client on purpose — a 401 (wrong current
 *  password) is a normal outcome here and must reach the form instead of
 *  triggering the shared client's session-recovery reload. */
function ChangePasswordPanel() {
  const { token } = useAuth()
  // Hub-served policy hint; the hub's 400 stays the contract, this is advisory.
  const passwordHint = usePasswordPolicyHint()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState(false)
  const [busy, setBusy] = useState(false)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!token) return
    setError(null)
    setDone(false)
    if (next !== confirm) {
      setError('New passwords do not match.')
      return
    }
    setBusy(true)
    try {
      // Server messages are surfaced verbatim: they carry both the 401
      // "current password is incorrect" and the 400 password-policy errors.
      await changePassword(token, current, next)
      setDone(true)
      setCurrent('')
      setNext('')
      setConfirm('')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Password change failed.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Panel title="Change password">
      <form
        onSubmit={submit}
        style={{ padding: '14px 16px', display: 'grid', gap: 12 }}
      >
        <Field label="Current password" htmlFor="change-password-current">
          <Input
            id="change-password-current"
            type="password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
            autoComplete="current-password"
            required
          />
        </Field>
        <Field label="New password" htmlFor="change-password-next" hint={passwordHint}>
          <Input
            id="change-password-next"
            type="password"
            value={next}
            onChange={(e) => setNext(e.target.value)}
            autoComplete="new-password"
            required
          />
        </Field>
        <Field label="Confirm new password" htmlFor="change-password-confirm">
          <Input
            id="change-password-confirm"
            type="password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            autoComplete="new-password"
            required
          />
        </Field>
        {error && (
          <div role="alert" style={{ color: 'var(--signal)', fontSize: '0.85em' }}>
            {error}
          </div>
        )}
        {done && (
          <div style={{ color: 'var(--ok)', fontSize: '0.85em' }} role="status">
            Password changed.
          </div>
        )}
        <Button type="submit" variant="primary" disabled={busy || !token}>
          {busy ? 'Saving…' : 'Change password'}
        </Button>
      </form>
    </Panel>
  )
}

function Row({
  label,
  value,
  action,
}: {
  label: string
  value: string
  action?: React.ReactNode
}) {
  return (
    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline' }}>
      <span className="label">{label}</span>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}>
        <b>{value}</b>
        {action}
      </div>
    </div>
  )
}
