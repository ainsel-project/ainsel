import { useState } from 'react'
import { Link } from 'react-router-dom'
import { usePlugins } from '../../api/plugins'
import { activePlugins } from '../../plugins/registry'
import { Dot } from '../../primitives/Dot'
import { Input } from '../../primitives/Input'
import './PluginsWorkspace.css'

type PluginSidebarProps = {
  selectedId?: string
}

/**
 * Searchable plugin roster for the plugins workspace (mirrors AgentSidebar).
 * One entry per active plugin — ready and enabled, sorted by id, the same
 * filter the old spine entries used. The roster is never admin-gated: every
 * user may open a plugin's area; only admin-visibility *pages* are filtered
 * (in the pages strip and at render time).
 */
export function PluginSidebar({ selectedId }: PluginSidebarProps) {
  const { data } = usePlugins()
  const [query, setQuery] = useState('')
  const [open, setOpen] = useState(false)

  const needle = query.trim().toLowerCase()
  const plugins = activePlugins(data ?? []).filter(
    (p) =>
      p.displayName.toLowerCase().includes(needle) || p.id.toLowerCase().includes(needle),
  )

  return (
    <div className="plugin-sidebar-shell">
      <button
        type="button"
        className="plugin-sidebar-toggle"
        aria-label="Show plugins"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
      >
        Plugins
      </button>
      <aside className="plugin-sidebar" data-open={open ? 'true' : 'false'}>
        <Input
          type="search"
          className="plugin-sidebar-search"
          placeholder="Search plugins"
          aria-label="Search plugins"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        <ul className="plugin-sidebar-list">
          {plugins.map((p) => (
            <li key={p.id}>
              <Link
                to={`/plugins/${encodeURIComponent(p.id)}`}
                className="plugin-sidebar-link"
                aria-current={p.id === selectedId ? 'page' : undefined}
              >
                <Dot state="ok" />
                <span className="plugin-sidebar-name">{p.displayName}</span>
              </Link>
            </li>
          ))}
        </ul>
        {plugins.length === 0 ? <p className="plugin-sidebar-empty">No plugins.</p> : null}
        <Link to="/plugins" className="plugin-sidebar-manage">
          Manage plugins
        </Link>
      </aside>
    </div>
  )
}
