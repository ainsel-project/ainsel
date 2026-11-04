import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { Conversation } from './Conversation'

const mockUseChatSession = vi.fn()
const mockUseSendChatMessage = vi.fn()

vi.mock('../../api/chat', () => ({
  useChatSession: (...args: unknown[]) => mockUseChatSession(...args),
  useCreateChatSession: () => ({ mutateAsync: vi.fn(), isPending: false, error: null }),
  useSendChatMessage: (...args: unknown[]) => mockUseSendChatMessage(...args),
}))

const message = (role: 'user' | 'assistant', content = 'x') => ({
  id: 1,
  sessionId: 'sess-abc',
  role,
  content,
  tokens: 1,
  createdAt: '2026-06-22T00:00:00Z',
})

// Default session data: last message is `assistant` (not busy)
const defaultSession = {
  id: 'sess-abc',
  name: 'sess-abc',
  agentName: 'dev',
  userId: 'user-1',
  createdAt: '2026-06-22T00:00:00Z',
  updatedAt: '2026-06-22T00:00:00Z',
  messages: [message('user', 'Hello'), message('assistant', 'Hi there!')],
}

function renderConversation() {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <Conversation sessionId="sess-abc" agentName="a-dev" onSessionCreated={vi.fn()} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  mockUseChatSession.mockReturnValue({ data: defaultSession, isLoading: false })
  mockUseSendChatMessage.mockReturnValue({ mutate: vi.fn(), isPending: false })
})

afterEach(() => {
  vi.clearAllMocks()
})

describe('Conversation — busy notification', () => {
  it('hides the busy notification when the last message is from the assistant', () => {
    renderConversation()
    expect(screen.queryByRole('status')).toBeNull()
  })

  it('shows the busy notification when the last message is from the user', () => {
    mockUseChatSession.mockReturnValue({
      data: { ...defaultSession, messages: [message('user', 'Hello')] },
      isLoading: false,
    })
    renderConversation()
    expect(screen.getByRole('status')).toBeInTheDocument()
    expect(screen.getByText(/Agent may be busy and will respond when ready/i)).toBeInTheDocument()
  })

  it('shows "Sending…" when sendMutation is pending', () => {
    mockUseSendChatMessage.mockReturnValue({ mutate: vi.fn(), isPending: true })
    renderConversation()
    expect(screen.getByRole('status')).toBeInTheDocument()
    expect(screen.getByText(/Sending/i)).toBeInTheDocument()
  })

  it('renders the busy notification inside the chat panel so it cannot push the input row out of view', () => {
    mockUseChatSession.mockReturnValue({
      data: { ...defaultSession, messages: [message('user', 'Hello')] },
      isLoading: false,
    })
    const { container } = renderConversation()
    const panel = container.querySelector('.chat-panel')
    expect(panel).toContainElement(screen.getByRole('status'))
  })
})

describe('Conversation — Reset during an in-flight send', () => {
  it('clears the composer and the Sending state when the reset key changes', async () => {
    const user = userEvent.setup()
    // A stateful stand-in for the send mutation: mutate starts a send, reset ends it.
    const send = { pending: false }
    const reset = vi.fn(() => {
      send.pending = false
    })
    mockUseSendChatMessage.mockImplementation(() => ({
      mutate: vi.fn(() => {
        send.pending = true
      }),
      isPending: send.pending,
      reset,
    }))
    const client = new QueryClient()
    const tree = (resetKey: number) => (
      <QueryClientProvider client={client}>
        <Conversation
          sessionId="sess-abc"
          agentName="a-dev"
          onSessionCreated={vi.fn()}
          resetKey={resetKey}
        />
      </QueryClientProvider>
    )
    const { rerender } = render(tree(0))

    await user.type(screen.getByPlaceholderText(/Type a message/), 'hello')
    await user.click(screen.getByRole('button', { name: 'Send' }))
    expect(send.pending).toBe(true)

    rerender(tree(1))

    expect(reset).toHaveBeenCalledTimes(1)
    expect(screen.queryByText(/Sending/)).toBeNull()
    expect(screen.getByPlaceholderText(/Type a message/)).toHaveValue('')
  })

  it('keeps the composer when the reset key does not change', async () => {
    const user = userEvent.setup()
    const reset = vi.fn()
    mockUseSendChatMessage.mockReturnValue({ mutate: vi.fn(), isPending: false, reset })
    renderConversation()
    await user.type(screen.getByPlaceholderText(/Type a message/), 'draft')
    expect(reset).not.toHaveBeenCalled()
    expect(screen.getByPlaceholderText(/Type a message/)).toHaveValue('draft')
  })
})
