import { useLocation } from 'react-router-dom'
import type { PluginManifest } from '@ainsel/plugin-sdk'
import { activeNavPath, pluginTabs } from '../../plugins/registry'
import { Tabs } from '../../primitives/Tabs'

type PluginPagesNavProps = {
  plugin: { id: string; manifest: PluginManifest }
  isAdmin: boolean
}

/**
 * The tab strip of one plugin's area — its own pages plus, for admins, a
 * built-in `Plugin` tab back to the plugin's detail page. Rendered at the
 * top of the plugin detail and above every plugin page, so both surfaces
 * share one navigation metaphor and a subpage is never a dead end.
 *
 * Tabs are link-backed (`Tabs` renders `<Link role="tab">` for entries with
 * a `to`), so they stay deep-linkable and openable in a new tab. Active
 * state uses the same most-specific-match rule the sidebar entries have
 * (activeNavPath): a root page never double-highlights on a nested path,
 * and deep links keep their page highlighted.
 */
export function PluginPagesNav({ plugin, isAdmin }: PluginPagesNavProps) {
  const location = useLocation()
  const entries = pluginTabs(plugin, isAdmin)
  if (entries.length === 0) return null
  const activePath = activeNavPath(entries, location.pathname)
  const tabs = entries.map((entry) => ({ value: entry.to, label: entry.label, to: entry.to }))
  return <Tabs value={activePath ?? ''} tabs={tabs} aria-label="Plugin sections" />
}
