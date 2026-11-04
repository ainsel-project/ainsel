import { useEffect, useRef, useState } from 'react'
import {
  useChatSession,
  useCreateChatSession,
  useSendChatMessage,
  type ChatMessage,
} from '../../api/chat'
import { Button } from '../../primitives/Button'
import { Markdown } from '../../primitives/Markdown'
import { Panel } from '../../primitives/Panel'
import { formatRelative } from '../../utils/time'
import './chatBusy.css'
import './Conversation.css'

type ConversationProps = {
  /** The session to show; undefined until the first send creates one. */
  sessionId?: string
  /** Agent the first send creates its session for. */
  agentName: string
  /** Called with the new session id when the first send creates it. */
  onSessionCreated: (id: string) => void
  /** Bumped by the parent on Reset: clears the composer and any in-flight send state. */
  resetKey?: number
}

export function Conversation({
  sessionId,
  agentName,
  onSessionCreated,
  resetKey = 0,
}: ConversationProps) {
  const { data: session, isLoading } = useChatSession(sessionId)
  const createMutation = useCreateChatSession()
  const sendMutation = useSendChatMessage()
  const [input, setInput] = useState('')
  const scrollRef = useRef<HTMLDivElement>(null)
  const resetSend = sendMutation.reset
  const seenResetKey = useRef(resetKey)

  // A Reset must not leave the old send's "Sending…" or its text on the new chat.
  useEffect(() => {
    if (seenResetKey.current === resetKey) return
    seenResetKey.current = resetKey
    setInput('')
    resetSend()
  }, [resetKey, resetSend])

  // Auto-scroll to bottom on new messages
  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight, behavior: 'smooth' })
  }, [session?.messages])

  const handleSend = async () => {
    const content = input.trim()
    if (!content || sendMutation.isPending || createMutation.isPending) return

    let targetId = sessionId
    if (!targetId) {
      try {
        const created = await createMutation.mutateAsync(agentName)
        targetId = created.id
      } catch {
        return // shown below the composer via createMutation.error
      }
      onSessionCreated(targetId)
    }
    // Clear only the text that was sent, so text typed after a Reset survives.
    sendMutation.mutate(
      { sessionId: targetId, content },
      { onSuccess: () => setInput((current) => (current.trim() === content ? '' : current)) },
    )
  }

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      void handleSend()
    }
  }

  if (sessionId && isLoading) {
    return (
      <div className="chat-conversation">
        <Panel>
          <div className="label" style={{ padding: 14 }}>
            Loading chat…
          </div>
        </Panel>
      </div>
    )
  }

  if (sessionId && !session) {
    return (
      <div className="chat-conversation">
        <Panel>
          <div className="label" style={{ padding: 14, color: 'var(--signal)' }}>
            Chat session not found.
          </div>
        </Panel>
      </div>
    )
  }

  const messages = session?.messages ?? []
  const lastMessage = messages[messages.length - 1]
  const isBusy = sendMutation.isPending || lastMessage?.role === 'user'

  return (
    <div className="chat-conversation">
      <Panel className="cropped chat-panel">
        {isBusy && (
          <div className="chat-busy" role="status" aria-live="polite">
            <span className="chat-busy-dot" />
            {sendMutation.isPending ? 'Sending…' : 'Agent may be busy and will respond when ready…'}
          </div>
        )}
        <div
          ref={scrollRef}
          style={{
            flex: 1,
            minHeight: 0,
            overflow: 'auto',
            padding: '16px 20px',
            display: 'flex',
            flexDirection: 'column',
            gap: '12px',
          }}
        >
          {messages.length === 0 ? (
            <div
              className="label"
              style={{ textAlign: 'center', marginTop: 40, color: 'var(--ink-3)' }}
            >
              No messages yet. Say something to start the conversation.
            </div>
          ) : (
            messages.map((msg) => <MessageBubble key={msg.id} msg={msg} />)
          )}
        </div>
        {createMutation.error && (
          <div className="label" style={{ padding: '8px 20px', color: 'var(--signal)' }}>
            Failed to start chat: {createMutation.error.message}
          </div>
        )}
        <div
          style={{
            borderTop: '2px solid var(--ink)',
            padding: '16px 20px',
            display: 'flex',
            gap: '12px',
            alignItems: 'flex-end',
            background: 'var(--paper-2)',
            flexShrink: 0,
          }}
        >
          <textarea
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="Type a message… (Enter to send, Shift+Enter for newline)"
            rows={1}
            style={{
              flex: 1,
              resize: 'none',
              minHeight: 44,
              maxHeight: 120,
              padding: '10px 14px',
              fontFamily: 'inherit',
              fontSize: 'var(--font-body)',
              background: 'var(--paper)',
              border: '2px solid var(--ink-4)',
              borderRadius: 'var(--radius)',
              color: 'var(--ink)',
              outline: 'none',
              boxShadow: 'inset 0 1px 3px rgba(0,0,0,0.1)',
            }}
          />
          <Button
            variant="primary"
            onClick={() => void handleSend()}
            disabled={!input.trim() || sendMutation.isPending || createMutation.isPending}
          >
            Send
          </Button>
        </div>
      </Panel>
    </div>
  )
}

function MessageBubble({ msg }: { msg: ChatMessage }) {
  if (msg.role === 'status') {
    return (
      <div style={{ textAlign: 'center', color: 'var(--ink-3)', fontSize: '0.85em' }}>
        <span style={{ fontStyle: 'italic' }}>{msg.content}</span>
        <span style={{ marginLeft: 8 }}>{formatRelative(msg.createdAt)}</span>
      </div>
    )
  }

  const isUser = msg.role === 'user'
  return (
    <div
      style={{
        display: 'flex',
        justifyContent: isUser ? 'flex-end' : 'flex-start',
      }}
    >
      <div
        style={{
          maxWidth: '75%',
          padding: '10px 14px',
          borderRadius: 'var(--radius)',
          // Solid invert for user messages (the .tag.solid convention), recessed
          // paper for assistant messages. The old `--accent`/`--surface-2`/
          // `--ink-1`/`--border` tokens do not exist, so these declarations
          // were invalid at computed-value time: user bubbles rendered white
          // text on the white panel in the light theme (issue #212).
          background: isUser ? 'var(--ink)' : 'var(--paper-deep)',
          color: isUser ? 'var(--paper)' : 'var(--ink-2)',
          border: isUser ? 'none' : '1px solid var(--rule-soft)',
        }}
      >
        {isUser ? (
          <div style={{ whiteSpace: 'pre-wrap' }}>{msg.content}</div>
        ) : (
          <Markdown source={msg.content} />
        )}
        <div
          style={{
            fontSize: '0.75em',
            marginTop: 4,
            opacity: 0.6,
            textAlign: isUser ? 'right' : 'left',
          }}
        >
          {formatRelative(msg.createdAt)}
        </div>
      </div>
    </div>
  )
}
