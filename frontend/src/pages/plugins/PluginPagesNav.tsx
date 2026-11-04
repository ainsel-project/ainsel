import { Link, useLocation } from 'react-router-dom'
import type { PluginManifest } from '@ainsel/plugin-sdk'
import { activeNavPath, pluginPages } from '../../plugins/registry'
import './PluginsWorkspace.css'

type PluginPagesNavProps = {
  plugin: { id: string; manifest: PluginManifest }
  isAdmin: boolean
}

/**
 * Links to one plugin's visible pages — the sections of the plugin's own
 * area, mirroring the agent workspace's SectionNav. Rendered on the plugin
 * detail and above every plugin page, so a user can move between a plugin's
 * pages (and back to its detail) without ever losing the roster sidebar.
 *
 * Active state uses the same most-specific-match rule the old spine entries
 * had (activeNavPath): a root page never double-highlights on a nested path,
 * and deep links keep their page highlighted.
 */
export function PluginPagesNav({ plugin, isAdmin }: PluginPagesNavProps) {
  const location = useLocation()
  const entries = pluginPages(plugin, isAdmin)
  if (entries.length === 0) return null
  const activePath = activeNavPath(entries, location.pathname)
  return (
    <nav className="plugin-pages-nav" aria-label="Plugin pages">
      <ul className="plugin-pages-list">
        {entries.map((entry) => (
          <li key={entry.to}>
            <Link
              to={entry.to}
              className="plugin-pages-link"
              aria-current={entry.to === activePath ? 'page' : undefined}
            >
              {entry.label}
            </Link>
          </li>
        ))}
      </ul>
    </nav>
  )
}
