import { Navigate, useParams } from 'react-router-dom'
import { useAgents } from '../../api/agents'
import { useChatSession } from '../../api/chat'
import { sectionPath } from '../agents/workspace/sections'

/** Old /chat/:id links: opens the session's agent at its chat section. */
export function ChatRedirect() {
  const { id = '' } = useParams()
  const session = useChatSession(id)
  const agents = useAgents({ pageSize: 100 })

  if (session.isError || agents.isError) return <Navigate to="/agents" replace />
  if (!session.data || !agents.data) return null

  const { agentName } = session.data
  // Sessions carry the agent's display name, but the workspace route takes the CR id.
  const agent = agents.data.items.find((a) => a.name === agentName || a.id === agentName)
  return <Navigate to={agent ? sectionPath(agent.id, 'chat') : '/agents'} replace />
}
