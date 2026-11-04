import { useMemo } from 'react'
import { Navigate, Outlet, useParams, useSearchParams } from 'react-router-dom'
import { useAgent } from '../../../api/agents'
import { usePlugins } from '../../../api/plugins'
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
              {/* Keyed by agent: a section never outlives the agent it was mounted for,
                  so a dirty form cannot be saved onto another agent after navigation. */}
              <Outlet key={agent.id} />
            </WorkspaceContext.Provider>
          ) : null}
        </div>
      </div>
    </div>
  )
}
