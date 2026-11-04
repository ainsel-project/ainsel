import { Outlet, useLocation } from 'react-router-dom'
import { PluginSidebar } from './PluginSidebar'
import { pluginIdFromPath } from './pluginRoute'
import './PluginsWorkspace.css'

/**
 * Shell for every plugin surface: the roster sidebar stays visible across the
 * plugin list, a plugin's management detail, and the plugin's own pages —
 * mirroring how AgentWorkspace keeps the agent roster visible across sections.
 */
export function PluginsWorkspace() {
  const location = useLocation()
  const selectedId = pluginIdFromPath(location.pathname)
  return (
    <div className="plugin-workspace">
      <PluginSidebar selectedId={selectedId} />
      <div className="plugin-workspace-main">
        <div className="plugin-workspace-content">
          <Outlet />
        </div>
      </div>
    </div>
  )
}
