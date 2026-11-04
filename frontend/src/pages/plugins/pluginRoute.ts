/**
 * The plugin id addressed by a plugins-workspace URL, or undefined on the
 * all-plugins list. Covers both surfaces: management (`/plugins/<id>`) and
 * plugin pages (`/p/<id>/...`). Paths are already basename-stripped by
 * react-router.
 */
export function pluginIdFromPath(pathname: string): string | undefined {
  const match = /^\/(?:plugins|p)\/([^/]+)/.exec(pathname)
  if (!match) return undefined
  try {
    return decodeURIComponent(match[1])
  } catch {
    return match[1]
  }
}
