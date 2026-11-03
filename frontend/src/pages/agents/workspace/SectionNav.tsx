import { Link } from 'react-router-dom'
import { buildSections, sectionPath, type SectionId, type SectionGroup } from './sections'
import './AgentWorkspace.css'

type SectionNavProps = {
  agentId: string
  current: SectionId
  plugins: { id: string; label: string }[]
}

/** Section links for one agent, grouped as Configure / Wiring / Plugins. */
export function SectionNav({ agentId, current, plugins }: SectionNavProps) {
  const items = buildSections(plugins)
  const groups: SectionGroup[] = ['top', 'Configure', 'Wiring', 'Plugins']

  return (
    <nav className="section-nav" aria-label="Agent sections">
      {groups.map((group) => {
        const groupItems = items.filter((item) => item.group === group)
        if (groupItems.length === 0) return null
        return (
          <div key={group} className="section-nav-group">
            {group !== 'top' && <h3 className="section-nav-heading">{group}</h3>}
            <ul className="section-nav-list">
              {groupItems.map((item) => (
                <li key={item.id}>
                  <Link
                    to={sectionPath(agentId, item.id)}
                    className="section-nav-link"
                    aria-current={item.id === current ? 'page' : undefined}
                  >
                    {item.label}
                  </Link>
                </li>
              ))}
            </ul>
          </div>
        )
      })}
    </nav>
  )
}
