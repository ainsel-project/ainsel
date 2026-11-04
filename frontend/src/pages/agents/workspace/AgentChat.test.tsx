import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import type { AgentResponse } from '../../../api/agents'
import { AgentChat } from './AgentChat'
import { WorkspaceContext } from './workspaceAgent'

const auth = vi.hoisted(() => ({
  user: { sub: 'user-1', username: 'kim' } as { sub: string; username: string } | undefined,
  mode: 'oidc' as string,
}))

vi.mock('../../../auth/AuthProvider', () => ({
  useAuth: () => ({ user: auth.user, mode: auth.mode }),
}))

const chat = vi.hoisted(() => ({
  listed: [] as unknown[],
  byId: {} as Record<string, unknown>,
  listCalls: [] as { params: unknown; options: { enabled?: boolean } | undefined }[],
  create: vi.fn(),
  send: vi.fn(),
  sending: false,
  resetSend: vi.fn(),
}))

vi.mock('../../../api/chat', () => ({
  // Like the real hook, a disabled query stays pending and never fetches.
  useChatSessions: (params: unknown, options?: { enabled?: boolean }) => {
    chat.listCalls.push({ params, options })
    if (options?.enabled === false) {
      return { data: undefined, isPending: true, isLoading: false, error: null }
    }
    return {
      data: { items: chat.listed, total: chat.listed.length },
      isPending: false,
      isLoading: false,
      error: null,
    }
  },
  useChatSession: (id: string | undefined) => ({
    data: id ? chat.byId[id] : undefined,
    isLoading: false,
  }),
  useCreateChatSession: () => ({ mutateAsync: chat.create, isPending: false, error: null }),
  useSendChatMessage: () => ({ mutate: chat.send, isPending: chat.sending, reset: chat.resetSend }),
}))

const agent = { id: 'a-dev', name: 'dev' } as AgentResponse

function session(
  id: string,
  reply: string,
  createdAt = '2026-11-01T00:00:00Z',
  updatedAt = createdAt,
) {
  return {
    id,
    name: id,
    agentName: 'dev',
    userId: 'user-1',
    createdAt,
    updatedAt,
    messages: [
      { id: 1, sessionId: id, role: 'assistant', content: reply, tokens: 1, createdAt: updatedAt },
    ],
  }
}

function renderChat() {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter>
        <WorkspaceContext.Provider value={{ agent, plugins: [] }}>
          <AgentChat />
        </WorkspaceContext.Provider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  chat.listed = [
    session('s-new', 'new answer', '2026-11-03T00:00:00Z'),
    session('s-old', 'old answer', '2026-11-01T00:00:00Z'),
  ]
  chat.byId = {
    's-new': session('s-new', 'new answer', '2026-11-03T00:00:00Z'),
    's-old': session('s-old', 'old answer', '2026-11-01T00:00:00Z'),
    's-fresh': session('s-fresh', 'fresh answer', '2026-11-04T00:00:00Z'),
  }
  chat.listCalls = []
  auth.user = { sub: 'user-1', username: 'kim' }
  auth.mode = 'oidc'
  chat.create
    .mockReset()
    .mockResolvedValue(session('s-fresh', 'fresh answer', '2026-11-04T00:00:00Z'))
  chat.send.mockReset().mockImplementation(() => {
    chat.sending = true
  })
  chat.sending = false
  chat.resetSend.mockReset().mockImplementation(() => {
    chat.sending = false
  })
})

afterEach(() => {
  vi.clearAllMocks()
})

describe('AgentChat', () => {
  it('shows the newest session the hub lists first', () => {
    renderChat()
    expect(screen.getByText('new answer')).toBeInTheDocument()
    expect(screen.queryByText('old answer')).toBeNull()
  })

  it('shows the most recently created session, even when an older one was updated later', () => {
    // The hub orders by updated_at, so a late reply can float an old session to the top.
    chat.listed = [
      session('s-old', 'old answer', '2026-11-01T00:00:00Z', '2026-11-04T09:00:00Z'),
      session('s-new', 'new answer', '2026-11-03T00:00:00Z', '2026-11-03T00:00:00Z'),
    ]
    renderChat()
    expect(screen.getByText('new answer')).toBeInTheDocument()
    expect(screen.queryByText('old answer')).toBeNull()
  })

  it('lists the agent chat by agent id and the current user id', () => {
    renderChat()
    expect(chat.listCalls[0].params).toEqual({ agent: 'a-dev', user: 'user-1', pageSize: 100 })
  })

  it('does not list sessions until the auth user resolves when auth is enabled', () => {
    auth.user = undefined
    renderChat()
    expect(chat.listCalls[0].options).toEqual({ enabled: false })
    expect(screen.getByText('Loading chat…')).toBeInTheDocument()
    expect(screen.queryByText('new answer')).toBeNull()
  })

  it('lists sessions without a user when auth mode is none', () => {
    auth.user = undefined
    auth.mode = 'none'
    renderChat()
    expect(chat.listCalls[0].options).toEqual({ enabled: true })
    expect(screen.getByText('new answer')).toBeInTheDocument()
  })

  it('Reset asks for confirmation, creates a new session and shows it', async () => {
    const user = userEvent.setup()
    renderChat()

    await user.click(screen.getByRole('button', { name: 'Reset' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByText('Reset chat?')).toBeInTheDocument()
    expect(
      within(dialog).getByText(
        'Starts a new conversation with dev. The current one is no longer shown.',
      ),
    ).toBeInTheDocument()

    await user.click(within(dialog).getByRole('button', { name: 'Reset' }))

    expect(chat.create).toHaveBeenCalledWith('a-dev')
    expect(await screen.findByText('fresh answer')).toBeInTheDocument()
    expect(screen.queryByText('new answer')).toBeNull()
  })

  it('Reset does nothing when cancelled', async () => {
    const user = userEvent.setup()
    renderChat()

    await user.click(screen.getByRole('button', { name: 'Reset' }))
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Cancel' }))

    expect(chat.create).not.toHaveBeenCalled()
    expect(screen.getByText('new answer')).toBeInTheDocument()
  })

  it('after Reset, a remount shows the new session the list now returns first', () => {
    const first = renderChat()
    chat.listed = [
      session('s-fresh', 'fresh answer', '2026-11-04T00:00:00Z'),
      session('s-new', 'new answer', '2026-11-03T00:00:00Z'),
    ]
    first.unmount()

    renderChat()
    expect(screen.getByText('fresh answer')).toBeInTheDocument()
    expect(screen.queryByText('new answer')).toBeNull()
  })

  it('with no session yet, the first send creates one and sends into it', async () => {
    const user = userEvent.setup()
    chat.listed = []
    renderChat()

    await user.type(screen.getByPlaceholderText(/Type a message/), 'hello')
    await user.click(screen.getByRole('button', { name: 'Send' }))

    expect(chat.create).toHaveBeenCalledWith('a-dev')
    expect(chat.send).toHaveBeenCalledWith(
      { sessionId: 's-fresh', content: 'hello' },
      expect.anything(),
    )
  })

  it('Reset during an in-flight send clears the composer and the Sending state', async () => {
    const user = userEvent.setup()
    renderChat()

    await user.type(screen.getByPlaceholderText(/Type a message/), 'hello')
    await user.click(screen.getByRole('button', { name: 'Send' }))
    await user.click(screen.getByRole('button', { name: 'Reset' }))
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Reset' }))

    expect(await screen.findByText('fresh answer')).toBeInTheDocument()
    expect(chat.resetSend).toHaveBeenCalled()
    expect(screen.queryByText(/Sending/)).toBeNull()
    expect(screen.getByPlaceholderText(/Type a message/)).toHaveValue('')
  })
})
