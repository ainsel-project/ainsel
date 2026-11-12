import type { PluginManifest, PluginPage, SlotId } from '@ainsel/plugin-sdk'
import type { PluginListItem } from '../api/plugins'

export interface ActivePlugin {
  id: string
  displayName: string
  manifest: PluginManifest
}

/**
 * Filter plugins to those that are ready and enabled.
 * Sorted by plugin id.
 */
export function activePlugins(items: PluginListItem[]): ActivePlugin[] {
  return items
    .filter((item) => (item.ready ?? true) && !item.disabled)
    .filter((item) => item.manifest)
    .map((item) => ({
      id: item.id,
      displayName: item.displayName || item.manifest!.displayName || item.id,
      manifest: item.manifest!,
    }))
    .sort((a, b) => a.id.localeCompare(b.id))
}

/**
 * Compute navigation entries for active plugins.
 * Pages appear under their declared section: a built-in one (Operations,
 * Fleet, Library, Docs, Admin) or a heading of the plugin's own, such as
 * "Forgejo". A page without a section goes under Plugins.
 * Admin pages are omitted when isAdmin is false.
 */
export function navEntries(
  plugins: readonly { id: string; manifest: PluginManifest }[],
  isAdmin: boolean,
) {
  const entries: { section: string; label: string; to: string }[] = []

  for (const plugin of plugins) {
    const pages = plugin.manifest.ui?.pages ?? []
    for (const page of pages) {
      if (page.visibility === 'admin' && !isAdmin) {
        continue
      }
      const section = page.section?.trim() || 'Plugins'
      const path = page.path === '/' ? `/p/${plugin.id}` : `/p/${plugin.id}${page.path}`
      entries.push({
        section,
        label: page.label,
        to: path,
      })
    }
  }

  return entries
}

/**
 * The navigable pages of one plugin — the same entries navEntries() produces,
 * restricted to a single plugin. Feeds the plugin workspace's pages tab strip
 * (the plugin's own sections, mirroring the agent workspace's section nav).
 */
export function pluginPages(
  plugin: { id: string; manifest: PluginManifest },
  isAdmin: boolean,
): { section: string; label: string; to: string }[] {
  return navEntries([plugin], isAdmin)
}

/**
 * The tab strip of one plugin's area: a built-in `Plugin` home tab pointing
 * at the plugin's detail page (`/plugins/<id>`), followed by the plugin's
 * declared pages in `pluginPages()` order. The home tab is admin-only —
 * `/plugins/:id` is the admin management view and a non-admin would only
 * reach the "managed by administrators" dead end. Feeds `PluginPagesNav`,
 * which renders both plugin surfaces (`/plugins/:id`, `/p/<id>/*`) with one
 * navigation metaphor and always offers a way back to the plugin's home.
 */
export function pluginTabs(
  plugin: { id: string; manifest: PluginManifest },
  isAdmin: boolean,
): { section: string; label: string; to: string }[] {
  const pages = pluginPages(plugin, isAdmin)
  if (!isAdmin) return pages
  return [{ section: '', label: 'Plugin', to: `/plugins/${plugin.id}` }, ...pages]
}

/**
 * The nav entry whose page is rendered for `pathname`, or undefined if none
 * matches. Mirrors findPage()'s routing rule: an entry matches on whole
 * segments (`pathname === entry.to` or `pathname.startsWith(entry.to + '/')`)
 * and the *most specific* match wins — at most one entry is ever active.
 *
 * This exists because react-router's default NavLink matching is prefix-based
 * and reports *every* prefix as active: an entry with a root page (`/p/x`) plus
 * a nested page (`/p/x/setup`) would highlight both entries on `/p/x/setup`.
 * Deep links must keep highlighting their page (`/p/x/work/42` → the `WorkPage`
 * entry), which exact matching alone would break. The hub applies this rule to
 * every top-level nav entry — built-in and plugin alike — so the sidebar uses
 * one uniform matching rule and stays correct if a built-in path ever becomes
 * a prefix of another entry.
 *
 * Operates on the full flattened entry set, not per section, so the rule
 * stays correct if sections ever share path prefixes.
 */
export function activeNavPath(
  entries: readonly { to: string }[],
  pathname: string,
): string | undefined {
  let best: string | undefined
  for (const entry of entries) {
    const to = entry.to
    if (pathname === to || pathname.startsWith(to + '/')) {
      if (best === undefined || to.length > best.length) {
        best = to
      }
    }
  }
  return best
}

/**
 * Find a page by its path segments.
 * Matches the longest prefix of the path against page paths.
 */
export function findPage(plugin: ActivePlugin, splat: string): PluginPage | undefined {
  const pages = plugin.manifest.ui?.pages ?? []

  // Exact match for root
  if (splat === '' || splat === '/') {
    return pages.find((p) => p.path === '/')
  }

  // Add leading slash if not present
  const splatPath = splat.startsWith('/') ? splat : `/${splat}`

  // Find the longest matching page path
  let bestMatch: PluginPage | undefined
  let bestLength = 0

  for (const page of pages) {
    if (page.path === '/') continue // Already handled above
    // Check if splatPath starts with page.path
    const pagePath = page.path.replace(/\/+$/, '')
    // Whole segments only: /mem must not match /memories.
    if (splatPath === pagePath || splatPath.startsWith(pagePath + '/')) {
      if (page.path.length > bestLength) {
        bestMatch = page
        bestLength = page.path.length
      }
    }
  }

  return bestMatch
}

/**
 * Get contributions to a slot from active plugins.
 * Ordered by plugin id.
 */
export function slotContributions(
  plugins: ActivePlugin[],
  slot: SlotId,
): { plugin: ActivePlugin; exportName: string }[] {
  const contributions: { plugin: ActivePlugin; exportName: string }[] = []

  for (const plugin of plugins) {
    const slots = plugin.manifest.ui?.slots ?? {}
    if (slot in slots) {
      const exportName = (slots[slot] as string) || 'default'
      contributions.push({ plugin, exportName })
    }
  }

  // Sort by plugin id
  return contributions.sort((a, b) => a.plugin.id.localeCompare(b.plugin.id))
}

/**
 * Compute the module URL for a plugin's UI entry.
 * entryFile is ui.entry relative to its directory, so we extract just the filename.
 */
export function moduleUrl(plugin: ActivePlugin): string {
  const entry = plugin.manifest.ui?.entry
  if (!entry) {
    throw new Error(`Plugin ${plugin.id} has no ui.entry`)
  }
  // Extract the filename from the entry path
  const entryFile = entry.split('/').pop() || entry
  return `${import.meta.env.BASE_URL}api/v1/plugins/${plugin.id}/ui/${entryFile}`
}
