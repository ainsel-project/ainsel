import { useState } from 'react'
import { Link, NavLink } from 'react-router-dom'
import { useAgents } from '../../../api/agents'
import { Dot } from '../../../primitives/Dot'
import { Input } from '../../../primitives/Input'
import { sectionPath } from './sections'
import './AgentWorkspace.css'

type AgentSidebarProps = {
  selectedId?: string
}

/** Searchable agent roster for the agent workspace. */
export function AgentSidebar({ selectedId }: AgentSidebarProps) {
  const { data } = useAgents({ pageSize: 200 })
  const [query, setQuery] = useState('')
  const [open, setOpen] = useState(false)

  const needle = query.trim().toLowerCase()
  const agents = (data?.items ?? []).filter((a) => a.name.toLowerCase().includes(needle))

  return (
    <div className="agent-sidebar-shell">
      <button
        type="button"
        className="agent-sidebar-toggle"
        aria-label={open ? 'Hide agents' : 'Show agents'}
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
      >
        Agents
      </button>
      <aside className="agent-sidebar" data-open={open ? 'true' : 'false'}>
        <Input
          type="search"
          className="agent-sidebar-search"
          placeholder="Search agents"
          aria-label="Search agents"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        <ul className="agent-sidebar-list">
          {agents.map((a) => (
            <li key={a.id}>
              <NavLink
                to={sectionPath(a.id, 'overview')}
                className="agent-sidebar-link"
                aria-current={a.id === selectedId ? 'page' : undefined}
              >
                <Dot state={!a.paused && a.status?.ready ? 'ok' : 'warn'} />
                <span className="agent-sidebar-name">{a.name}</span>
              </NavLink>
            </li>
          ))}
        </ul>
        <Link to="/agents/new" className="agent-sidebar-new">
          + New agent
        </Link>
      </aside>
    </div>
  )
}
