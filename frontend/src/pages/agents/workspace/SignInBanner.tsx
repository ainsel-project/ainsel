import { useState } from 'react'
import type { AgentResponse } from '../../../api/agents'
import { Button } from '../../../primitives/Button'
import { Notice } from '../../../primitives/Notice'
import { isManaged } from '../../../utils/managed'
import { storedAuthMethod } from '../llmProviders'
import { ChatGPTSignInDialog } from './ChatGPTSignInDialog'

/**
 * The agent uses ChatGPT sign-in and has none that works: say so, and offer
 * the sign-in right here. Shown until the operator reports a valid sign-in;
 * a freshly created agent without status yet counts as not signed in.
 */
export function SignInBanner({ agent }: { agent: AgentResponse }) {
  const [open, setOpen] = useState(false)
  if (agent.llm?.provider !== 'openai' || storedAuthMethod(agent) !== 'ChatGPTSignIn') return null
  const status = agent.status
  if (status?.signInRequired === false || status?.signInReason === 'SignedIn') return null

  const expired = status?.signInReason === 'SignInExpired'
  return (
    <>
      <Notice variant="warn">
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
          <span style={{ flex: 1, minWidth: 240 }}>
            {expired
              ? 'The ChatGPT sign-in expired or was revoked. '
              : 'This agent is not signed in to ChatGPT yet. '}
            It runs nothing until someone signs in; tasks that arrive wait.
          </span>
          {isManaged(agent.managedBy) ? null : (
            <Button variant="primary" onClick={() => setOpen(true)}>
              Sign in with ChatGPT
            </Button>
          )}
        </div>
      </Notice>
      <ChatGPTSignInDialog
        agentId={agent.id}
        open={open}
        onClose={() => setOpen(false)}
        taskRunning={(status?.desired ?? 0) > 0}
      />
    </>
  )
}
