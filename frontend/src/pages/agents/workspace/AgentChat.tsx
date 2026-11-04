import { useState } from 'react'
import { useAuth } from '../../../auth/AuthProvider'
import { useChatSessions, useCreateChatSession } from '../../../api/chat'
import { Button } from '../../../primitives/Button'
import { ConfirmModal } from '../../../primitives/ConfirmModal'
import { Panel } from '../../../primitives/Panel'
import { Conversation } from '../../chat/Conversation'
import { SectionPage } from './SectionPage'
import { useWorkspaceAgent } from './workspaceAgent'

/** The most recently created session; ties keep the hub's order (first wins). */
function newestCreated<T extends { createdAt: string }>(items: T[]): T | undefined {
  let best: T | undefined
  for (const item of items) {
    if (!best || Date.parse(item.createdAt) > Date.parse(best.createdAt)) best = item
  }
  return best
}

/**
 * The agent's one chat: the caller's newest session with this agent. Reset
 * starts a new session, which gives the agent a fresh conversation context.
 */
export function AgentChat() {
  const agent = useWorkspaceAgent()
  const { user } = useAuth()
  // Sessions are stored under the agent id the chat has always used, and the
  // hub stamps each one with the caller's sub, so both filters must match that.
  // The hub orders by updated_at, which late agent replies bump, so the current
  // chat is picked by createdAt below rather than taken from the first row.
  const sessions = useChatSessions({ agent: agent.id, user: user?.sub, pageSize: 100 })
  const create = useCreateChatSession()
  // Set by Reset or by a first send; overrides the list until the section remounts.
  const [pinnedId, setPinnedId] = useState<string>()
  const [confirmReset, setConfirmReset] = useState(false)

  const sessionId = pinnedId ?? newestCreated(sessions.data?.items ?? [])?.id

  const onReset = async () => {
    try {
      const session = await create.mutateAsync(agent.id)
      setPinnedId(session.id)
      setConfirmReset(false)
    } catch {
      // The modal shows create.error and stays open for a retry.
    }
  }

  const resetButton = (
    <Button onClick={() => setConfirmReset(true)} disabled={create.isPending}>
      Reset
    </Button>
  )

  if (sessions.error) {
    return (
      <SectionPage title="Chat" actions={resetButton}>
        <Panel>
          <div className="label" style={{ padding: 14, color: 'var(--signal)' }}>
            Failed to load chat.
          </div>
        </Panel>
      </SectionPage>
    )
  }

  if (sessions.isLoading) {
    return (
      <SectionPage title="Chat" actions={resetButton}>
        <Panel>
          <div className="label" style={{ padding: 14 }}>
            Loading chat…
          </div>
        </Panel>
      </SectionPage>
    )
  }

  return (
    <SectionPage title="Chat" actions={resetButton}>
      <Conversation
        sessionId={sessionId}
        agentName={agent.id}
        onSessionCreated={setPinnedId}
      />
      <ConfirmModal
        open={confirmReset}
        title="Reset chat?"
        body={`Starts a new conversation with ${agent.name}. The current one is no longer shown.`}
        confirmLabel={create.isPending ? 'Resetting…' : 'Reset'}
        error={create.error ? create.error.message : null}
        onConfirm={onReset}
        onCancel={() => setConfirmReset(false)}
      />
    </SectionPage>
  )
}
