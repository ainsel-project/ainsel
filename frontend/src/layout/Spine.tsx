import { useMemo } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { activeNavPath } from '../plugins/registry'
import { NavIcon, type NavIconName } from './SpineIcons'
import './Spine.css'

type NavItem = { name: string; to: string; tag?: string; icon: NavIconName }

const SECTIONS: { label: string; items: NavItem[] }[] = [
  {
    label: 'Operations',
    items: [
      { name: 'Dashboard', to: '/dashboard', icon: 'dashboard' },
      { name: 'Channels', to: '/channels', icon: 'channels' },
      { name: 'Activity', to: '/activity', icon: 'activity' },
      { name: 'Observability', to: '/observability', icon: 'observability' },
    ],
  },
  {
    label: 'Fleet',
    items: [{ name: 'Agents', to: '/agents', icon: 'agents' }],
  },
  {
    // Shared catalogs an agent draws from. Agent-scoped configuration lives on
    // the agent itself; these pages manage the reusable building blocks.
    label: 'Library',
    items: [
      { name: 'Images', to: '/agent-images', icon: 'images' },
      { name: 'Personas', to: '/personas', icon: 'personas' },
      { name: 'Skills', to: '/skills', icon: 'skills' },
      { name: 'MCPs', to: '/settings', icon: 'mcps' },
    ],
  },
  {
    label: 'Docs',
    items: [{ name: 'Docs', to: '/docs', icon: 'docs' }],
  },
  {
    label: 'Admin',
    items: [
      { name: 'Users', to: '/users', icon: 'users' },
      { name: 'Groups', to: '/groups', icon: 'groups' },
      { name: 'Connectors', to: '/connectors', icon: 'connectors' },
      { name: 'Audit', to: '/audit', icon: 'audit' },
      { name: 'Plugins', to: '/plugins', icon: 'plugins' },
    ],
  },
]

// The ledger numbers are decoration (issue #279): they are aria-hidden, and the
// rail hides them in favour of the icon. They used to be hand-written per entry
// and went stale every time an entry moved, so they are derived from the
// flattened order instead — the numbering cannot drift again.
const INDEX_BY_PATH = new Map<string, string>()
for (const section of SECTIONS) {
  for (const item of section.items) {
    INDEX_BY_PATH.set(item.to, String(INDEX_BY_PATH.size + 1).padStart(2, '0'))
  }
}

type SpineProps = {
  operator: string
  /** Mobile drawer state; on desktop the spine is always visible. */
  open?: boolean
  onClose?: () => void
  /**
   * Collapse the spine to a ~48px icon rail (used in the agent workspace):
   * each entry shows only its icon, with the name carried by `title` and
   * `aria-label`; section labels are dropped.
   * Ignored while the mobile drawer is open — the drawer always shows the
   * full navigation.
   */
  rail?: boolean
}

/**
 * One top-level nav entry. Both the icon and the idx are always in the DOM and
 * CSS decides which one is visible: the expanded spine shows the number, the
 * rail shows the icon (see Spine.css). Neither is a label — the idx is
 * aria-hidden and the icon is a decorative SVG — so the accessible name is the
 * entry name, read from `.name` when expanded and from `title` + `aria-label`
 * in the rail, where no name text is rendered.
 */
function EntryLink({
  to,
  name,
  tag,
  icon,
  active,
  collapsed,
}: {
  to: string
  name: string
  tag?: string
  icon: NavIconName
  active: boolean
  collapsed: boolean
}) {
  const idx = INDEX_BY_PATH.get(to) ?? ''
  return (
    <Link
      to={to}
      className={active ? 'nav-link active' : 'nav-link'}
      aria-current={active ? 'page' : undefined}
      aria-label={collapsed ? name : undefined}
      title={collapsed ? name : undefined}
    >
      <NavIcon name={icon} />
      <span className="idx" aria-hidden="true">
        {idx}
      </span>
      {collapsed ? null : (
        <>
          <span className="name">{name}</span>
          {tag ? <span className="nav-tag">{tag}</span> : <span />}
        </>
      )}
    </Link>
  )
}

export function Spine({ operator, open = false, onClose, rail = false }: SpineProps) {
  const location = useLocation()
  // The rail is a desktop-canvas affordance: with the mobile drawer open the
  // user has explicitly asked for the full navigation, so never collapse it.
  const collapsed = rail && !open

  // Most-specific-match-wins: NavLink's default prefix matching reports
  // *every* prefix as active, which would light up a root page on every
  // nested path. The rule keeps at most one entry active, and deep links
  // still keep their entry highlighted (/docs/mcp → Docs).
  const activePath = useMemo(
    () =>
      activeNavPath(
        SECTIONS.flatMap((section) => section.items),
        location.pathname,
      ),
    [location.pathname],
  )

  const renderSections = () => {
    const sectionElements: React.ReactNode[] = []

    for (const section of SECTIONS) {
      sectionElements.push(
        <div key={section.label}>
          {collapsed ? null : (
            <div className="nav-section">
              <span className="label">{section.label}</span>
            </div>
          )}
          {section.items.map((item) => {
            const active = item.to === activePath
            return (
              <div key={item.to}>
                <EntryLink
                  to={item.to}
                  name={item.name}
                  tag={item.tag}
                  icon={item.icon}
                  active={active}
                  collapsed={collapsed}
                />
              </div>
            )
          })}
        </div>,
      )
    }

    return sectionElements
  }

  return (
    <>
      {open ? <div className="spine-backdrop" aria-hidden="true" onClick={onClose} /> : null}
      <aside
        id="spine"
        className={['spine', open ? 'open' : null, collapsed ? 'rail' : null]
          .filter(Boolean)
          .join(' ')}
      >
        <div className="brand">
          <div className="brand-mark">
            <span />
          </div>
          <h1>
            AInsel <em>Hub</em>
          </h1>
          {onClose ? (
            <button className="spine-close" aria-label="Close menu" onClick={onClose}>
              <span aria-hidden="true">&times;</span>
            </button>
          ) : null}
        </div>

        <nav className="nav" aria-label="Primary">
          {renderSections()}
        </nav>

        <div className="spine-footer">
          <Link to="/profile" className="row spine-profile-link">
            <span>Operator</span>
            <b>{operator}</b>
          </Link>
        </div>
      </aside>
    </>
  )
}
