import { useEffect, useMemo } from 'react'
import { Navigate, Outlet, useParams, useSearchParams } from 'react-router-dom'
import { useAgent } from '../../../api/agents'
import { usePlugins } from '../../../api/plugins'
import { recordAgentView, recentsScope } from '../../../agentRecents'
import { useAuth } from '../../../auth/AuthProvider'
import { activePlugins, slotContributions } from '../../../plugins/registry'
import { AgentList } from '../AgentList'
import { AgentSidebar } from './AgentSidebar'
import { SectionNav } from './SectionNav'
import { parseSection, sectionPath, type SectionId } from './sections'
import { WorkspaceContext } from './workspaceAgent'
import './AgentWorkspace.css'

/** The /agents index: the roster sidebar beside the agent list. */
export function AgentListPage() {
  return (
    <div className="agent-workspace">
      <AgentSidebar />
      <div className="agent-workspace-main">
        <div className="agent-workspace-content">
          <AgentList />
        </div>
      </div>
    </div>
  )
}

/**
 * Shell for one agent: roster sidebar, section nav, and the active section
 * (the nested route's outlet). Loads the agent once for every section.
 */
export function AgentWorkspace() {
  const { id = '', section } = useParams()
  const [searchParams] = useSearchParams()
  const { user } = useAuth()
  const { data: agent, isLoading, error } = useAgent(id)
  const { data: pluginsList } = usePlugins()

  const plugins = useMemo(
    () =>
      slotContributions(activePlugins(pluginsList ?? []), 'agent.panel').map((c) => ({
        id: c.plugin.id,
        label: c.plugin.displayName,
      })),
    [pluginsList],
  )

  // Feed the nav's recent-agents list. Keyed on the loaded agent's id, not the
  // raw route param, so an unresolvable URL cannot be remembered as "viewed".
  useEffect(() => {
    if (agent?.id) recordAgentView(recentsScope(user), agent.id)
  }, [agent?.id, user])

  // Old deep links (/agents/:id?tab=...) move to the matching section route.
  const legacyTab = searchParams.get('tab')
  if (legacyTab !== null) {
    const target: SectionId = parseSection(legacyTab) ?? 'overview'
    return <Navigate to={sectionPath(id, target)} replace />
  }

  const current: SectionId = (section && parseSection(section)) || 'overview'

  return (
    <div className="agent-workspace">
      <AgentSidebar selectedId={id} />
      <div className="agent-workspace-main">
        <SectionNav agentId={id} current={current} plugins={plugins} />
        <div className="agent-workspace-content">
          {isLoading ? (
            <p className="label" style={{ padding: '28px 32px' }}>
              Loading…
            </p>
          ) : null}
          {error ? (
            <p className="label" style={{ padding: '28px 32px', color: 'var(--signal)' }}>
              Failed to load agent.
            </p>
          ) : null}
          {agent ? (
            <WorkspaceContext.Provider value={{ agent, plugins }}>
              <Outlet />
            </WorkspaceContext.Provider>
          ) : null}
        </div>
      </div>
    </div>
  )
}
