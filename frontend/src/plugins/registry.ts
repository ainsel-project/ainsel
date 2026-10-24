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
export function navEntries(plugins: ActivePlugin[], isAdmin: boolean) {
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
