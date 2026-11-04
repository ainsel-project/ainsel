import { useEffect, useReducer, useMemo } from 'react'
import { NavLink, Link, useLocation } from 'react-router-dom'
import { useAgents } from '../api/agents'
import { usePlugins, usePluginCallerContext } from '../api/plugins'
import { getAgentRecents, RECENT_AGENT_LIMIT } from '../agentRecents'
import { PREF_CHANGE_EVENT } from '../prefs'
import { Dot } from '../primitives/Dot'
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
  /** Recents bucket for the signed-in account; see recentsScope(). */
  recentsScope?: string
  /** Mobile drawer state; on desktop the spine is always visible. */
  open?: boolean
  onClose?: () => void
  /**
   * Collapse the spine to a ~48px icon rail (used in the agent workspace):
   * each entry shows only its idx, with the name carried by `title` and
   * `aria-label`; section labels and the recent-agents block are dropped.
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

/**
 * Quick links under the Agents nav entry: the agents this browser opened
 * most recently, oldest-dropped, up to RECENT_AGENT_LIMIT. Before you have
 * clicked anything — or if you have clicked fewer than the limit — the
 * remaining slots are filled by the hub's recently-updated agents, so the
 * section is a stable set of shortcuts rather than something that appears and
 * disappears as you browse. Filler is rendered dimmer (see .padded) to keep
 * "yours" distinguishable from "whatever else is there".
 *
 * The list API is already scoped to what the caller can read, so "of the user"
 * falls out of the backend's access filtering.
 *
 * These sublinks keep NavLink's prefix matching on purpose: they are detail
 * links (`/agents/<id>`), not top-level nav entries, and the parent *Agents*
 * entry staying highlighted on an agent page is intended — no nav entry is a
 * prefix of a sublink, so the most-specific rule has nothing to disambiguate
 * here.
 */
function RecentAgents({ scope }: { scope: string }) {
  const { data } = useAgents({ pageSize: 200 })
  // Recents live in localStorage, which React has no reason to re-render for;
  // the write side broadcasts PREF_CHANGE_EVENT, so subscribe to it.
  const [, rerender] = useReducer((n: number) => n + 1, 0)
  useEffect(() => {
    const handler = () => rerender()
    window.addEventListener(PREF_CHANGE_EVENT, handler)
    return () => window.removeEventListener(PREF_CHANGE_EVENT, handler)
  }, [])

  const items = data?.items ?? []
  const byId = new Map(items.map((a) => [a.id, a]))
  // Storage holds ids, but the hub decides what this caller can read: resolve
  // every remembered id through `byId` so an agent that was deleted, or that
  // you have lost access to since, can never render a link to a 404.
  const clicked = getAgentRecents(scope).flatMap((id) => {
    const agent = byId.get(id)
    return agent ? [{ agent, padded: false }] : []
  })
  const seen = new Set(clicked.map((entry) => entry.agent.id))
  const filler = [...items]
    .sort((a, b) => (b.updatedAt ?? '').localeCompare(a.updatedAt ?? ''))
    .filter((a) => !seen.has(a.id))
    .slice(0, Math.max(0, RECENT_AGENT_LIMIT - clicked.length))
    .map((agent) => ({ agent, padded: true }))

  const recent = [...clicked, ...filler]
  if (recent.length === 0) return null
  return (
    <div className="nav-recent" aria-label="Recent agents">
      {recent.map(({ agent: a, padded }) => (
        <NavLink
          key={a.id}
          to={`/agents/${encodeURIComponent(a.id)}`}
          className={({ isActive }) => {
            const classes = ['nav-sublink']
            if (isActive) classes.push('active')
            if (padded) classes.push('padded')
            return classes.join(' ')
          }}
        >
          <Dot state={a.status?.ready ? 'ok' : 'warn'} aria-label={a.name + ' status'} />
          <span className="name">{a.name}</span>
        </NavLink>
      ))}
    </div>
  )
}

export function Spine({
  operator,
  recentsScope = 'anon',
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
                {item.to === '/agents' && !collapsed ? <RecentAgents scope={recentsScope} /> : null}
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
