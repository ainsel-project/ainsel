import { useCallback, useEffect, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  cancelSignIn,
  getSignIn,
  signInIsLive,
  startSignIn,
  submitSignInRedirect,
  type SignInSession,
} from '../../../api/agents'
import { ApiError } from '../../../api/client'
import { ConfirmModal } from '../../../primitives/ConfirmModal'
import { Field } from '../../../primitives/Field'
import { Input } from '../../../primitives/Input'

type ChatGPTSignInDialogProps = {
  agentId: string
  open: boolean
  onClose: () => void
  /** A task is running: the sign-in pod starts once it finishes. */
  taskRunning?: boolean
  /** How often the session is re-read while it is live. */
  pollMs?: number
}

function errorText(err: unknown): string {
  return err instanceof ApiError ? err.message : 'Something went wrong. Please try again.'
}

/**
 * Signs an agent in with a ChatGPT subscription, through OpenAI's official
 * "Sign in with ChatGPT". AInsel starts a short sign-in pod for the agent and
 * shows OpenAI's link; after signing in, the browser lands on a 127.0.0.1 page
 * that does not load, and the user pastes that address here. The pod finishes
 * the sign-in and keeps the credential on the agent's own volume — it never
 * passes through the console or the hub.
 */
export function ChatGPTSignInDialog({
  agentId,
  open,
  onClose,
  taskRunning,
  pollMs = 2000,
}: ChatGPTSignInDialogProps) {
  const qc = useQueryClient()
  const [started, setStarted] = useState(false)
  const [startError, setStartError] = useState<string | null>(null)
  const [pasted, setPasted] = useState('')
  const [pasteError, setPasteError] = useState<string | null>(null)
  const startedFor = useRef<string | null>(null)

  const queryKey = ['agents', 'detail', agentId, 'chatgpt-sign-in']
  const session = useQuery({
    queryKey,
    queryFn: () => getSignIn(agentId),
    enabled: open && started,
    refetchInterval: (q) => (signInIsLive(q.state.data?.state) ? pollMs : false),
  })

  const begin = async () => {
    setStartError(null)
    setPasted('')
    setPasteError(null)
    try {
      const s = await startSignIn(agentId)
      qc.setQueryData<SignInSession>(queryKey, s)
    } catch (err) {
      // A sign-in already in progress (another tab, or a reopened dialog)
      // is simply picked up again.
      if (!(err instanceof ApiError && err.status === 409)) {
        setStartError(errorText(err))
        return
      }
    }
    setStarted(true)
  }

  useEffect(() => {
    if (!open || startedFor.current === agentId) return
    startedFor.current = agentId
    void begin()
    // begin is recreated each render; starting once per opening is the point.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, agentId])

  useEffect(() => {
    if (!open) {
      startedFor.current = null
      setStarted(false)
    }
  }, [open])

  const s = session.data
  const state = s?.state
  const live = signInIsLive(state)

  useEffect(() => {
    if (state === 'succeeded') void qc.invalidateQueries({ queryKey: ['agents'] })
  }, [state, qc])

  const submitPaste = async () => {
    setPasteError(null)
    try {
      await submitSignInRedirect(agentId, pasted.trim())
      void session.refetch()
    } catch (err) {
      setPasteError(errorText(err))
    }
  }

  // Stable across renders: ConfirmModal re-focuses its first control whenever
  // onCancel changes, which would pull focus out of the paste field.
  const liveRef = useRef(live)
  liveRef.current = live
  const onCloseRef = useRef(onClose)
  onCloseRef.current = onClose
  const cancel = useCallback(async () => {
    if (liveRef.current) await cancelSignIn(agentId).catch(() => {})
    onCloseRef.current()
  }, [agentId])
  const onCancel = useCallback(() => void cancel(), [cancel])

  let body: React.ReactNode
  let confirmLabel = 'Hide'
  let onConfirm: () => void = onClose
  if (startError) {
    body = <p>{startError}</p>
    confirmLabel = 'Try again'
    onConfirm = () => void begin()
  } else if (!s || state === 'waiting_for_pod') {
    body = (
      <p>
        {taskRunning
          ? 'Waiting for the running task to finish, then starting the sign-in…'
          : 'Starting the sign-in…'}
      </p>
    )
  } else if (state === 'awaiting_redirect') {
    body = (
      <div style={{ display: 'grid', gap: 12 }}>
        <p style={{ margin: 0 }}>
          1.{' '}
          <a href={s.authUrl} target="_blank" rel="noopener noreferrer">
            Open the OpenAI sign-in page
          </a>{' '}
          and sign in with your ChatGPT account.
        </p>
        <p style={{ margin: 0 }}>
          2. Afterwards the browser shows a page that does not load, at an address starting with{' '}
          <code>http://127.0.0.1:1455</code>. Copy that whole address and paste it here.
        </p>
        <Field
          label="Address the browser landed on"
          htmlFor="signInRedirect"
          error={pasteError ?? undefined}
        >
          <Input
            id="signInRedirect"
            value={pasted}
            onChange={(e) => setPasted(e.target.value)}
            placeholder="http://127.0.0.1:1455/auth/callback?code=…"
            autoComplete="off"
          />
        </Field>
      </div>
    )
    confirmLabel = 'Finish sign-in'
    onConfirm = () => void submitPaste()
  } else if (state === 'exchanging') {
    body = <p>Finishing the sign-in…</p>
  } else if (state === 'succeeded') {
    body = <p>Signed in. Queued tasks start now.</p>
    confirmLabel = 'Done'
  } else {
    body = (
      <p>
        {state === 'failed'
          ? `The sign-in failed: ${s.error || 'unknown error'}`
          : state === 'expired'
            ? 'The sign-in took too long and expired.'
            : 'The sign-in was cancelled.'}
      </p>
    )
    confirmLabel = 'Try again'
    onConfirm = () => void begin()
  }

  return (
    <ConfirmModal
      open={open}
      title="Sign in with ChatGPT"
      body={body}
      confirmLabel={confirmLabel}
      cancelLabel={live ? 'Cancel sign-in' : 'Close'}
      onConfirm={onConfirm}
      onCancel={onCancel}
    />
  )
}
