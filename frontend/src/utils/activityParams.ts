// The two activity filter axes are named after the question they answer:
// `match` (did the event match a subscription?) and `runState` (what state is
// this run in right now?). They used to be `status` and `outcome` — the first
// ambiguous against every other "status" in these views, the second simply
// wrong, because `queued` and `running` are not finished at all.
//
// Old links are still out there (bookmarks, hand-edited URLs, the agent
// overview's stat tiles), so the console reads the retired spellings as a
// fallback and rewrites the URL onto the current names. The current name wins
// when both are present, which is also how the hub resolves them.

/** Retired param name → current param name. */
export const LEGACY_ACTIVITY_PARAMS = [
  { from: 'status', to: 'match' },
  { from: 'outcome', to: 'runState' },
] as const

/** Read an activity filter param, falling back to its retired spelling. */
export function readActivityParam(params: URLSearchParams, name: string): string {
  const current = params.get(name)
  if (current !== null && current !== '') return current
  const legacy = LEGACY_ACTIVITY_PARAMS.find((l) => l.to === name)
  return legacy ? (params.get(legacy.from) ?? '') : ''
}

/** Whether `params` still carries a retired activity filter name. */
export function hasLegacyActivityParams(params: URLSearchParams): boolean {
  return LEGACY_ACTIVITY_PARAMS.some((l) => params.get(l.from) !== null)
}

/**
 * Rewrite retired activity filter params onto their current names in `next`
 * (in place) and drop the retired spelling, so a legacy link settles on the
 * current one and clearing a filter cannot re-activate the retired value.
 * Returns whether anything changed.
 */
export function renameLegacyActivityParams(next: URLSearchParams): boolean {
  let changed = false
  for (const { from, to } of LEGACY_ACTIVITY_PARAMS) {
    const v = next.get(from)
    if (v === null) continue
    next.delete(from)
    if (v !== '' && !next.get(to)) next.set(to, v)
    changed = true
  }
  return changed
}
