import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AgentResponse, SignInSession } from '../../../api/agents'
import { renderWithProviders } from '../../../test/renderWithProviders'
import { ChatGPTSignInDialog } from './ChatGPTSignInDialog'
import { SignInBanner } from './SignInBanner'

const AUTH_URL = 'https://auth.openai.com/api/accounts/authorize?client_id=dynamic_agent_client'
const PASTED = 'http://127.0.0.1:1455/auth/callback?code=c&state=s'

/**
 * A hub stand-in for one agent's sign-in: POST starts it, the pod "posts" the
 * link on the next GET, a pasted address moves it to exchanging, and the GET
 * after that reports success.
 */
function signInHub(opts: { rejectPaste?: string } = {}) {
  let session: SignInSession | null = null
  let pasted = false
  const calls: { method: string; url: string; body?: unknown }[] = []
  const fetchMock = vi.fn((url: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET'
    const body = init?.body ? JSON.parse(String(init.body)) : undefined
    calls.push({ method, url, body })
    const json = (v: unknown, status = 200) =>
      Promise.resolve(new Response(JSON.stringify(v), { status }))
    if (!url.includes('/chatgpt-sign-in')) return json({})
    if (url.endsWith('/redirect') && method === 'POST') {
      if (opts.rejectPaste) return json({ error: opts.rejectPaste }, 400)
      pasted = true
      if (session) session = { ...session, state: 'exchanging' }
      return Promise.resolve(new Response(null, { status: 202 }))
    }
    if (method === 'POST') {
      session = { id: 'S1', state: 'waiting_for_pod', expiresAt: '2026-11-13T12:00:00Z' }
      return json(session, 201)
    }
    if (method === 'DELETE') {
      if (session) session = { ...session, state: 'cancelled' }
      return Promise.resolve(new Response(null, { status: 204 }))
    }
    if (!session) return json({ error: 'no sign-in session' }, 404)
    if (session.state === 'waiting_for_pod') {
      session = { ...session, state: 'awaiting_redirect', authUrl: AUTH_URL }
    } else if (session.state === 'exchanging' && pasted) {
      session = { ...session, state: 'succeeded' }
    }
    return json(session)
  })
  return { fetchMock, calls }
}

describe('ChatGPTSignInDialog', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('dialog walks start → link → paste → success', async () => {
    const hub = signInHub()
    vi.stubGlobal('fetch', hub.fetchMock)
    const onClose = vi.fn()
    const user = userEvent.setup()
    renderWithProviders(<ChatGPTSignInDialog agentId="a1" open onClose={onClose} pollMs={20} />)

    const link = await screen.findByRole('link', { name: /open the openai sign-in page/i })
    expect(link).toHaveAttribute('href', AUTH_URL)
    expect(link).toHaveAttribute('target', '_blank')
    await user.type(screen.getByLabelText(/address the browser landed on/i), PASTED)
    await user.click(screen.getByRole('button', { name: /finish sign-in/i }))

    expect(await screen.findByText(/signed in/i)).toBeInTheDocument()
    expect(hub.calls.find((c) => c.url.endsWith('/redirect'))?.body).toEqual({
      redirectUrl: PASTED,
    })
    await user.click(screen.getByRole('button', { name: /done/i }))
    expect(onClose).toHaveBeenCalled()
  })

  it("dialog shows the hub's 400 message for a bad paste and stays open", async () => {
    const hub = signInHub({
      rejectPaste: 'the pasted address must start with http://127.0.0.1:1455/auth/callback',
    })
    vi.stubGlobal('fetch', hub.fetchMock)
    const user = userEvent.setup()
    renderWithProviders(<ChatGPTSignInDialog agentId="a1" open onClose={vi.fn()} pollMs={20} />)

    await screen.findByRole('link', { name: /open the openai sign-in page/i })
    await user.type(screen.getByLabelText(/address the browser landed on/i), 'https://chatgpt.com/')
    await user.click(screen.getByRole('button', { name: /finish sign-in/i }))

    expect(
      await screen.findByText(/must start with http:\/\/127\.0\.0\.1:1455/),
    ).toBeInTheDocument()
    expect(screen.getByLabelText(/address the browser landed on/i)).toBeInTheDocument()
  })

  it('says it waits for a running task before the link appears', async () => {
    const hub = signInHub()
    // Keep the session waiting: the pod has not started.
    hub.fetchMock.mockImplementation((_url: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      const s = { id: 'S1', state: 'waiting_for_pod', expiresAt: '2026-11-13T12:00:00Z' }
      return Promise.resolve(
        new Response(JSON.stringify(s), { status: method === 'POST' ? 201 : 200 }),
      )
    })
    vi.stubGlobal('fetch', hub.fetchMock)
    renderWithProviders(
      <ChatGPTSignInDialog agentId="a1" open onClose={vi.fn()} pollMs={20} taskRunning />,
    )
    expect(await screen.findByText(/waiting for the running task to finish/i)).toBeInTheDocument()
  })

  it('cancel stops the live sign-in', async () => {
    const hub = signInHub()
    vi.stubGlobal('fetch', hub.fetchMock)
    const onClose = vi.fn()
    const user = userEvent.setup()
    renderWithProviders(<ChatGPTSignInDialog agentId="a1" open onClose={onClose} pollMs={20} />)
    await screen.findByRole('link', { name: /open the openai sign-in page/i })
    await user.click(screen.getByRole('button', { name: /cancel sign-in/i }))
    await waitFor(() => expect(hub.calls.some((c) => c.method === 'DELETE')).toBe(true))
    expect(onClose).toHaveBeenCalled()
  })
})

describe('SignInBanner', () => {
  beforeEach(() => vi.stubGlobal('fetch', signInHub().fetchMock))
  afterEach(() => vi.unstubAllGlobals())

  const signInAgent = {
    id: 'a1',
    name: 'writer',
    paused: false,
    llm: { model: 'gpt-5', provider: 'openai' },
    openAI: { authMethod: 'ChatGPTSignIn', keyStored: false },
    status: { ready: false, signInRequired: true, signInReason: 'SignInExpired' },
  } as AgentResponse

  it('banner shows for signInRequired and opens the dialog', async () => {
    const user = userEvent.setup()
    renderWithProviders(<SignInBanner agent={signInAgent} />)
    expect(screen.getByText(/chatgpt sign-in expired/i)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /sign in with chatgpt/i }))
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
  })

  it('shows nothing for a signed-in or API-key agent', () => {
    const { container, unmount } = renderWithProviders(
      <SignInBanner
        agent={
          {
            ...signInAgent,
            status: { ready: true, signInRequired: false, signInReason: 'SignedIn' },
          } as AgentResponse
        }
      />,
    )
    expect(container).toBeEmptyDOMElement()
    unmount()
    const keyed = renderWithProviders(
      <SignInBanner
        agent={
          {
            ...signInAgent,
            openAI: { authMethod: 'APIKey' },
            status: { ready: true },
          } as AgentResponse
        }
      />,
    )
    expect(keyed.container).toBeEmptyDOMElement()
  })
})
