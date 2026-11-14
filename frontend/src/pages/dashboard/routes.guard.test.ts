import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

/**
 * Route-target guard (issue #275).
 *
 * The dashboard's Triggers plate linked to `/triggers`, which is not a route —
 * and because `<Route path="*" />` bounces unknown paths to /dashboard, the
 * dead click was invisible: no 404, just a silent round trip. Nothing in the
 * suite linked navigation targets to route declarations, so nothing failed.
 *
 * This guard reads the route table and the navigation sources as text (same
 * parsing-guard pattern as `design/tokens.guard.test.ts` — jsdom renders can't
 * see the route table any better than they can see CSS) and asserts every
 * static target the plates and link builders emit resolves to a declared
 * route. The catch-all is deliberately excluded from the pattern set: it
 * matches everything, so counting it would make the guard vacuous.
 *
 * Scope: absolute route literals in App.tsx and absolute path literals in the
 * checked sources. Relative child routes (`:section`, `mcp-servers`) are not
 * composed — no static console link targets one today, and a target that did
 * would have to be added to App.tsx as an absolute route to pass this guard.
 */

const SRC = join(__dirname, '../..')
const read = (path: string) => readFileSync(join(SRC, path), 'utf8')

const appSource = read('App.tsx')
const dashboardSource = read('pages/dashboard/Dashboard.tsx')
const summaryCardsSource = read('pages/observability/SummaryCards.tsx')
const obsLinksSource = read('utils/obsLinks.ts')

/** Every absolute `path="…"` declared in App.tsx, minus the catch-all. */
function declaredRoutePatterns(source: string): string[] {
  const patterns: string[] = []
  for (const m of source.matchAll(/<Route\b[^>]*?path="([^"]*)"/g)) {
    const p = m[1]
    // The catch-all matches every target, which would make the guard vacuous.
    if (p === '*' || !p.startsWith('/')) continue
    patterns.push(p)
  }
  return patterns
}

/** Static `to: '…'` plate targets in a component source. */
function plateTargets(source: string): string[] {
  return [...source.matchAll(/\bto:\s*'([^']+)'/g)].map((m) => m[1])
}

/** Absolute path literals (plain or template prefix) in a link-builder source. */
function linkBuilderPaths(source: string): string[] {
  return [...source.matchAll(/['"`](\/[A-Za-z0-9\-_/]+)(?:['"`$])/g)].map((m) =>
    m[1].replace(/\/$/, ''),
  )
}

/** Segment-wise match against a declared pattern; `:param` matches any segment. */
function resolvesToRoute(target: string, patterns: string[]): boolean {
  const targetSegs = target.split('/').filter(Boolean)
  return patterns.some((pattern) => {
    const segs = pattern.split('/').filter(Boolean)
    if (segs.length !== targetSegs.length) return false
    return segs.every((s, i) => s.startsWith(':') || s === targetSegs[i])
  })
}

const routePatterns = declaredRoutePatterns(appSource)

describe('routes guard (issue #275)', () => {
  it('finds the route table in App.tsx', () => {
    // Sanity: the regex must actually see the routes, or every check below is vacuous.
    expect(routePatterns).toContain('/dashboard')
    expect(routePatterns).toContain('/agents/:id')
    expect(routePatterns).not.toContain('*')
  })

  it('resolves every dashboard plate target to a declared route', () => {
    const targets = plateTargets(dashboardSource)
    expect(targets.length).toBeGreaterThan(0)
    for (const target of targets) {
      expect(target, `dashboard plate links to ${target}, which is not a route`).toEqual(
        expect.stringMatching(/^\/[^?]*/),
      )
      expect(resolvesToRoute(target, routePatterns)).toBe(true)
    }
  })

  it('resolves every summary-card plate target to a declared route', () => {
    for (const target of plateTargets(summaryCardsSource)) {
      expect(resolvesToRoute(target, routePatterns)).toBe(true)
    }
  })

  it('resolves every obsLinks path to a declared route', () => {
    const paths = linkBuilderPaths(obsLinksSource)
    expect(paths.length).toBeGreaterThan(0)
    for (const p of paths) {
      expect(resolvesToRoute(p, routePatterns)).toBe(true)
    }
  })

  it('rejects an undeclared target (the guard can fail)', () => {
    // The self-test that pins the class of bug: /triggers was a plausible path
    // with no route behind it. If the matcher ever says yes to it, the guard
    // is broken.
    expect(resolvesToRoute('/triggers', routePatterns)).toBe(false)
    expect(resolvesToRoute('/agents/doc-writer', routePatterns)).toBe(true)
  })
})
