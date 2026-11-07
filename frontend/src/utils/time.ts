/** Local midnight today, as an RFC3339 instant for `since` query filters. */
export function startOfToday(): string {
  const d = new Date()
  d.setHours(0, 0, 0, 0)
  return d.toISOString()
}

// The only accepted `since` URL value. Hand-edited or stale URLs with any
// other value fall back to "any", mirroring the enum guards on the Activity
// page — arbitrary ISO values in links are not promised stability.
export const SINCE_SENTINEL = 'today'

/**
 * Resolve a `since` URL parameter to an RFC3339 instant for the events API.
 * Only the `today` sentinel is valid (resolved to local midnight, the same
 * instant the agent overview counts its daily stats from); anything else
 * yields undefined so the query stays unbounded.
 */
export function resolveSinceParam(v: string | undefined): string | undefined {
  return v === SINCE_SENTINEL ? startOfToday() : undefined
}

export function formatRelative(
  input: string | null | undefined,
  nowMs: number = Date.now(),
): string {
  if (!input) return '—'
  const t = Date.parse(input)
  if (Number.isNaN(t)) return '—'
  const delta = Math.max(0, nowMs - t)
  const s = Math.floor(delta / 1000)
  if (s < 5) return 'just now'
  if (s < 60) return `${s}s ago`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  const remMin = m % 60
  if (h < 24) return `${h}h ${remMin}m`
  const d = Math.floor(h / 24)
  const remHour = h % 24
  return `${d}d ${remHour}h`
}

export function formatISO(input: string | null | undefined): string {
  if (!input) return '—'
  const t = Date.parse(input)
  if (Number.isNaN(t)) return '—'
  const d = new Date(t)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())} ${pad(
    d.getUTCHours(),
  )}:${pad(d.getUTCMinutes())}:${pad(d.getUTCSeconds())}Z`
}
