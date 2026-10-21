import { useState } from 'react'
import { changePassword } from '../api/auth'
import { usePasswordPolicyHint } from '../api/passwordPolicy'
import { Button } from '../primitives/Button'
import { Field } from '../primitives/Field'
import { Input } from '../primitives/Input'
import { useAuth } from './AuthProvider'

/**
 * Change-password form for local accounts (PUT /auth/password): proves the
 * current password and applies a new one. Shared by the profile panel and by the
 * forced change screen (issue #113), which is the same operation with no choice
 * attached to it.
 *
 * The call goes through api/auth's raw-fetch client on purpose — a 401 (wrong
 * current password) is a normal outcome here and must reach the form instead of
 * triggering the shared client's session-recovery handling. Server messages are
 * surfaced verbatim: they carry both the 401 "current password is incorrect" and
 * the 400 password-policy errors, and the hub owns the policy text.
 */
export function ChangePasswordForm({
  submitLabel = 'Change password',
  onSuccess,
}: {
  submitLabel?: string
  onSuccess?: () => void
}) {
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
      await changePassword(token, current, next)
      setDone(true)
      setCurrent('')
      setNext('')
      setConfirm('')
      onSuccess?.()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Password change failed.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} style={{ padding: '14px 16px', display: 'grid', gap: 12 }}>
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
        {busy ? 'Saving…' : submitLabel}
      </Button>
    </form>
  )
}
