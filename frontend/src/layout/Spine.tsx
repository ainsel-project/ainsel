import { useMemo } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { activeNavPath } from '../plugins/registry'
import './Spine.css'

type NavItem = { idx: string; name: string; to: string; tag?: string }

const SECTIONS: { label: string; items: NavItem[] }[] = [
  {
    label: 'Operations',
    items: [
      { idx: '01', name: 'Dashboard', to: '/dashboard' },
      { idx: '12', name: 'Channels', to: '/channels' },
      { idx: '13', name: 'Activity', to: '/activity' },
      { idx: '14', name: 'Observability', to: '/observability' },
    ],
  },
  {
    label: 'Fleet',
    items: [{ idx: '02', name: 'Agents', to: '/agents' }],
  },
  {
    // Shared catalogs an agent draws from. Agent-scoped configuration lives on
    // the agent itself; these pages manage the reusable building blocks.
    label: 'Library',
    items: [
      { idx: '15', name: 'Images', to: '/agent-images' },
      { idx: '16', name: 'Personas', to: '/personas' },
      { idx: '17', name: 'Skills', to: '/skills' },
      { idx: '18', name: 'MCPs', to: '/settings' },
    ],
  },
  {
    label: 'Docs',
    items: [{ idx: '10', name: 'Docs', to: '/docs' }],
  },
  {
    label: 'Admin',
    items: [
      { idx: '20', name: 'Users', to: '/users' },
      { idx: '21', name: 'Groups', to: '/groups' },
      { idx: '22', name: 'Connectors', to: '/connectors' },
      { idx: '23', name: 'Audit', to: '/audit' },
      { idx: '24', name: 'Plugins', to: '/plugins' },
    ],
  },
]

type SpineProps = {
  operator: string
  /** Mobile drawer state; on desktop the spine is always visible. */
  open?: boolean
  onClose?: () => void
  /**
   * Collapse the spine to a ~48px icon rail (used in the agent workspace):
   * each entry shows only its idx, with the name carried by `title` and
   * `aria-label`; section labels are dropped.
   * Ignored while the mobile drawer is open — the drawer always shows the
   * full navigation.
   */
  rail?: boolean
}

/**
 * One top-level nav entry. In rail mode the visible text collapses to the
 * idx alone, so the name moves to `title` + `aria-label` to keep the link
 * nameable.
 */
function EntryLink({
  to,
  idx,
  name,
  tag,
  active,
  collapsed,
}: {
  to: string
  idx: string
  name: string
  tag?: string
  active: boolean
  collapsed: boolean
}) {
  return (
    <Link
      to={to}
      className={active ? 'nav-link active' : 'nav-link'}
      aria-current={active ? 'page' : undefined}
      aria-label={collapsed ? name : undefined}
      title={collapsed ? name : undefined}
    >
      <span className="idx">{idx}</span>
      {collapsed ? null : (
        <>
          <span className="name">{name}</span>
          {tag ? <span className="nav-tag">{tag}</span> : <span />}
        </>
      )}
    </Link>
  )
}

export function Spine({
  operator,
  open = false,
  onClose,
  rail = false,
}: SpineProps) {
  const location = useLocation()
  // The rail is a desktop-canvas affordance: with the mobile drawer open the
  // user has explicitly asked for the full navigation, so never collapse it.
  const collapsed = rail && !open

  // Most-specific-match-wins: NavLink's default prefix matching reports
  // *every* prefix as active, which would light up a root page on every
  // nested path. The rule keeps at most one entry active, and deep links
  // still keep their entry highlighted (/docs/mcp → Docs).
  const activePath = useMemo(
    () => activeNavPath(SECTIONS.flatMap((section) => section.items), location.pathname),
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
                  idx={item.idx}
                  name={item.name}
                  tag={item.tag}
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
