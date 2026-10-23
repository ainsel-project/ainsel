import type { ActivePlugin } from './registry'
import { moduleUrl } from './registry'

// Cache loaded modules per plugin id
const moduleCache = new Map<string, Promise<Record<string, unknown>>>()

/**
 * Load a plugin module. Cached per plugin id.
 * @param plugin The active plugin to load
 * @param importer Optional custom importer (for testing); defaults to dynamic import
 */
export async function loadPluginModule(
  plugin: ActivePlugin,
  importer?: (url: string) => Promise<Record<string, unknown>>,
): Promise<Record<string, unknown>> {
  if (moduleCache.has(plugin.id)) {
    return moduleCache.get(plugin.id)!
  }

  const url = moduleUrl(plugin)
  const promise = (importer ?? ((u: string) => import(/* @vite-ignore */ u)))(url)
  moduleCache.set(plugin.id, promise)
  return promise
}

/**
 * Clear the module cache (for testing).
 */
export function clearModuleCache() {
  moduleCache.clear()
}
