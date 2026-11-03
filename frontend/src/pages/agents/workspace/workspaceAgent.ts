import { createContext, useContext } from 'react'
import type { AgentResponse } from '../../../api/agents'

export type WorkspaceValue = {
  agent: AgentResponse
  plugins: { id: string; label: string }[]
}

export const WorkspaceContext = createContext<WorkspaceValue | null>(null)

function useWorkspace(): WorkspaceValue {
  const value = useContext(WorkspaceContext)
  if (!value) throw new Error('Agent section rendered outside AgentWorkspace')
  return value
}

/** The agent loaded by the enclosing AgentWorkspace. */
export function useWorkspaceAgent(): AgentResponse {
  return useWorkspace().agent
}

/** Plugin entries with an agent.panel contribution, for nav labels. */
export function useWorkspacePlugins(): { id: string; label: string }[] {
  return useWorkspace().plugins
}
