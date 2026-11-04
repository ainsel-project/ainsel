import { useMemo } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { usePlugins, usePluginCallerContext } from '../api/plugins'
import { activeNavPath, activePlugins, navEntries } from '../plugins/registry'
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

const BUILT_IN_SECTIONS = new Set(SECTIONS.map((s) => s.label))

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
 * One top-level nav entry, rendered identically in all three render paths
 * (built-in item, plugin entry inside a built-in section, plugin-owned
 * section). In rail mode the visible text collapses to the idx alone, so the
 * name moves to `title` + `aria-label` to keep the link nameable.
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
  const { data: pluginItems } = usePlugins()
  const { data: callerContext } = usePluginCallerContext()
  const location = useLocation()
  // The rail is a desktop-canvas affordance: with the mobile drawer open the
  // user has explicitly asked for the full navigation, so never collapse it.
  const collapsed = rail && !open

  const availablePlugins = useMemo(
    () => activePlugins(Array.isArray(pluginItems) ? pluginItems : []),
    [pluginItems],
  )
  const pluginNavEntries = useMemo(
    () => navEntries(availablePlugins, callerContext?.isAdmin ?? false),
    [availablePlugins, callerContext?.isAdmin],
  )
  // Most-specific-match-wins, mirroring findPage()'s routing rule, applied to
  // every top-level nav entry — built-in and plugin alike. NavLink's default
  // prefix matching reports *every* prefix as active, which would light up a
  // root page (e.g. /p/memory) on every nested path (/p/memory/setup). The
  // rule is uniform so a built-in path can never double-highlight with
  // another entry (built-in or plugin) the day one becomes a prefix of it.
  // Deep links still keep their entry highlighted (/docs/mcp → Docs).
  const allNavEntries = useMemo(
    () => [...SECTIONS.flatMap((section) => section.items), ...pluginNavEntries],
    [pluginNavEntries],
  )
  const activePath = useMemo(
    () => activeNavPath(allNavEntries, location.pathname),
    [allNavEntries, location.pathname],
  )

  // Group plugin entries by section and compute Plugins section
  const pluginsBySection = useMemo(() => {
    const grouped = new Map<string, typeof pluginNavEntries>()
    for (const entry of pluginNavEntries) {
      if (!grouped.has(entry.section)) {
        grouped.set(entry.section, [])
      }
      grouped.get(entry.section)!.push(entry)
    }
    return grouped
  }, [pluginNavEntries])

  // Render sections with plugin entries
  const renderSections = () => {
    const sectionElements: React.ReactNode[] = []

    for (let i = 0; i < SECTIONS.length; i++) {
      const section = SECTIONS[i]
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
          {/* Plugin entries share the built-in entries' matching rule: plain
              links whose active state comes from the combined activeNavPath. */}
          {pluginsBySection.get(section.label)?.map((entry, idx) => {
            const active = entry.to === activePath
            return (
              <div key={entry.to}>
                <EntryLink
                  to={entry.to}
                  idx={`P·${idx + 1}`}
                  name={entry.label}
                  active={active}
                  collapsed={collapsed}
                />
              </div>
            )
          })}
        </div>,
      )

      // Plugin-owned headings (e.g. "Forgejo", or "Plugins" for pages that
      // name no section) go after Library, in the order plugins are listed.
      if (i === 2) {
        for (const [label, entries] of pluginsBySection) {
          if (BUILT_IN_SECTIONS.has(label)) continue
          sectionElements.push(
            <div key={`plugin-section-${label}`}>
              {collapsed ? null : (
                <div className="nav-section">
                  <span className="label">{label}</span>
                </div>
              )}
              {entries.map((entry, idx) => {
                const active = entry.to === activePath
                return (
                  <div key={entry.to}>
                    <EntryLink
                      to={entry.to}
                      idx={`P·${idx + 1}`}
                      name={entry.label}
                      active={active}
                      collapsed={collapsed}
                    />
                  </div>
                )
              })}
            </div>,
          )
        }
      }
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
