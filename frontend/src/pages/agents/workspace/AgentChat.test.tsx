import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import type { AgentResponse } from '../../../api/agents'
import { AgentChat } from './AgentChat'
import { WorkspaceContext } from './workspaceAgent'

vi.mock('../../../auth/AuthProvider', () => ({
  useAuth: () => ({ user: { sub: 'user-1', username: 'kim' } }),
}))

const chat = vi.hoisted(() => ({
  listed: [] as unknown[],
  byId: {} as Record<string, unknown>,
  listCalls: [] as unknown[],
  create: vi.fn(),
  send: vi.fn(),
}))

vi.mock('../../../api/chat', () => ({
  useChatSessions: (params: unknown) => {
    chat.listCalls.push(params)
    return { data: { items: chat.listed, total: chat.listed.length }, isLoading: false }
  },
  useChatSession: (id: string | undefined) => ({
    data: id ? chat.byId[id] : undefined,
    isLoading: false,
  }),
  useCreateChatSession: () => ({ mutateAsync: chat.create, isPending: false, error: null }),
  useSendChatMessage: () => ({ mutate: chat.send, isPending: false }),
}))

const agent = { id: 'a-dev', name: 'dev' } as AgentResponse

function session(id: string, reply: string) {
  return {
    id,
    name: id,
    agentName: 'dev',
    userId: 'user-1',
    createdAt: '2026-11-01T00:00:00Z',
    updatedAt: '2026-11-01T00:00:00Z',
    messages: [{ id: 1, sessionId: id, role: 'assistant', content: reply, tokens: 1, createdAt: '2026-11-01T00:00:00Z' }],
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
  chat.listed = [session('s-new', 'new answer'), session('s-old', 'old answer')]
  chat.byId = {
    's-new': session('s-new', 'new answer'),
    's-old': session('s-old', 'old answer'),
    's-fresh': session('s-fresh', 'fresh answer'),
  }
  chat.listCalls = []
  chat.create.mockReset().mockResolvedValue(session('s-fresh', 'fresh answer'))
  chat.send.mockReset()
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

  it('lists the agent chat by agent id and the current user id', () => {
    renderChat()
    expect(chat.listCalls[0]).toEqual({ agent: 'a-dev', user: 'user-1', pageSize: 1 })
  })

  it('Reset asks for confirmation, creates a new session and shows it', async () => {
    const user = userEvent.setup()
    renderChat()

    await user.click(screen.getByRole('button', { name: 'Reset' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByText('Reset chat?')).toBeInTheDocument()
    expect(
      within(dialog).getByText('Starts a new conversation with dev. The current one is no longer shown.'),
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
    chat.listed = [session('s-fresh', 'fresh answer'), session('s-new', 'new answer')]
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
})
