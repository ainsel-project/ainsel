import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { describe, expect, it } from 'vitest'

import { THEMES, parseColor, paletteFor, readCss, resolveVar, stripComments } from './cssThemeKit'

/**
 * Design-token guard.
 *
 * The Fjord overhaul (af55f3d) renamed the palette but left a residue of
 * `var(--old-token, #hardcoded)` references behind. Those tokens no longer
 * exist, so the fallback renders instead — off-palette colours that no theme
 * can reach, and in the dual-list picker a near-white selected row that is
 * unreadable in every dark theme.
 *
 * This spec makes that class of bug impossible to reintroduce: any
 * `var(--x)` whose `--x` is not declared in `src/design/` fails the build.
 *
 * Files that predate the guard and are not part of this change are listed in
 * BASELINE. It is a ratchet — shrink it, never grow it. When you migrate a
 * file, delete its entry; adding one back is a review-time red flag.
 */

const SRC = join(__dirname, '..')

/** Files still carrying pre-Fjord phantom tokens. Remove entries as you migrate. */
const BASELINE: Record<string, string[]> = {
  'pages/agents/AgentList.tsx': ['--accent'],
  'pages/agents/AgentWizard.tsx': ['--rule'],
  'pages/agents/ScheduleForm.tsx': ['--rule'],
  'pages/agents/TriggerForm.tsx': ['--rule'],
  'pages/chat/ChatView.tsx': ['--accent', '--border', '--font-body', '--ink-1', '--surface-2'],
  'pages/chat/chatBusy.css': ['--accent'],
  'pages/connectors/ConnectorForm.tsx': ['--rule'],
  'pages/connectors/ConnectorList.tsx': ['--accent'],
  'pages/groups/GroupDetail.tsx': ['--chrome-1'],
  'pages/groups/GroupList.tsx': ['--accent'],
  'pages/images/ImageList.tsx': ['--accent'],
  'pages/mcp-servers/MCPServerList.tsx': ['--accent'],
  'pages/observability/events/EventView.tsx': ['--border'],
  'pages/personas/PersonaList.tsx': ['--accent'],
  'pages/profile/TokenManager.tsx': ['--border', '--danger', '--surface-2'],
  'pages/skills/SkillList.tsx': ['--accent'],
  'pages/users/UserList.tsx': ['--accent', '--muted'],
}

const SCAN_EXT = new Set(['.css', '.ts', '.tsx'])

/**
 * Specs are skipped: they quote `var(--…)` as fixtures and assertions, never
 * as styling, so a token name in a test file renders nothing.
 */
function isSpec(file: string): boolean {
  return /\.test\.tsx?$/.test(file)
}

function walk(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) {
      if (entry === 'node_modules' || entry === 'dist') continue
      walk(full, out)
    } else if (SCAN_EXT.has(entry.slice(entry.lastIndexOf('.'))) && !isSpec(entry)) {
      out.push(full)
    }
  }
  return out
}

/**
 * Every custom property declared by any stylesheet under src/.
 *
 * Not just src/design/: a component may legitimately declare its own token
 * (`.shell` sets `--chrome-top` in Layout.css), and such a token is not a
 * phantom — it resolves, and the cascade reaches it.
 */
function declaredTokens(): Set<string> {
  const declared = new Set<string>()
  for (const file of walkCss(SRC)) {
    const css = stripComments(readFileSync(file, 'utf8'))
    for (const m of css.matchAll(/(--[a-zA-Z0-9-]+)\s*:/g)) declared.add(m[1])
  }
  return declared
}

function walkCss(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) {
      if (entry === 'node_modules' || entry === 'dist') continue
      walkCss(full, out)
    } else if (entry.endsWith('.css')) {
      out.push(full)
    }
  }
  return out
}

/**
 * Blank out comments while leaving string and template literals intact.
 *
 * A `var(--x)` in a comment renders nothing, so counting it would force prose
 * into BASELINE and erode the ratchet. Quote tracking keeps a `//` inside a URL
 * from being read as a comment start. Characters become spaces rather than
 * being deleted so offsets stay aligned with the source.
 *
 * A regex literal containing a quote character could still confuse this; to
 * bound the damage, `'` and `"` strings are terminated at a newline, which real
 * JS strings cannot cross anyway. Failing open here hides a token reference,
 * so the scanner is asserted directly below.
 */
export function blankComments(source: string): string {
  const out: string[] = []
  const n = source.length
  let i = 0
  while (i < n) {
    const c = source[i]
    if (c === '/' && source[i + 1] === '/') {
      while (i < n && source[i] !== '\n') {
        out.push(' ')
        i++
      }
      continue
    }
    if (c === '/' && source[i + 1] === '*') {
      out.push('  ')
      i += 2
      while (i < n && !(source[i] === '*' && source[i + 1] === '/')) {
        out.push(source[i] === '\n' ? '\n' : ' ')
        i++
      }
      if (i < n) {
        out.push('  ')
        i += 2
      }
      continue
    }
    if (c === '"' || c === "'" || c === '`') {
      out.push(c)
      i++
      while (i < n) {
        if (source[i] === '\\') {
          out.push(source.slice(i, i + 2))
          i += 2
          continue
        }
        const ch = source[i]
        out.push(ch)
        i++
        if (ch === c) break
        // An unterminated quote means we mis-detected a regex literal; stop
        // swallowing code at the end of the line rather than blanking the file.
        if (c !== '`' && ch === '\n') break
      }
      continue
    }
    out.push(c)
    i++
  }
  return out.join('')
}

/** `var(--x)` references per file, excluding declarations and comments. */
function referencedTokens(file: string): Set<string> {
  const raw = readFileSync(file, 'utf8')
  const text = file.endsWith('.css') ? stripComments(raw) : blankComments(raw)
  const refs = new Set<string>()
  for (const m of text.matchAll(/var\(\s*(--[a-zA-Z0-9-]+)/g)) refs.add(m[1])
  return refs
}

function phantomTokens(): Map<string, string[]> {
  const declared = declaredTokens()
  const found = new Map<string, string[]>()
  for (const file of walk(SRC)) {
    const rel = relative(SRC, file)
    const phantoms = [...referencedTokens(file)].filter((t) => !declared.has(t)).sort()
    if (phantoms.length > 0) found.set(rel, phantoms)
  }
  return found
}

describe('design tokens', () => {
  it('declares a non-trivial palette (sanity check on the scanner)', () => {
    expect(declaredTokens().size).toBeGreaterThan(30)
    expect(declaredTokens()).toContain('--signal')
    expect(declaredTokens()).toContain('--paper')
  })

  it('has no var(--x) references to undeclared tokens beyond the baseline', () => {
    const actual = phantomTokens()

    // New offenders: a file that is not in the baseline, or a token that the
    // baseline does not excuse for that file.
    const introduced: string[] = []
    for (const [file, tokens] of actual) {
      const excused = new Set(BASELINE[file] ?? [])
      const fresh = tokens.filter((t) => !excused.has(t))
      if (fresh.length > 0) introduced.push(`${file}: ${fresh.join(', ')}`)
    }

    expect(
      introduced,
      'Undeclared design tokens resolve to their hardcoded fallback, which no ' +
        'theme can override. Declare the token in src/design/tokens.css or use ' +
        'an existing one.',
    ).toEqual([])

    // Ratchet: a baseline entry that no longer matches reality means the file
    // was migrated (good) or renamed (stale). Either way, update the baseline.
    const stale: string[] = []
    for (const [file, tokens] of Object.entries(BASELINE)) {
      const now = new Set(actual.get(file) ?? [])
      const gone = tokens.filter((t) => !now.has(t))
      if (gone.length > 0) stale.push(`${file}: ${gone.join(', ')}`)
    }
    expect(
      stale,
      'These baseline entries no longer match the source. Delete them so the ' +
        'ratchet stays tight.',
    ).toEqual([])
  })
})

/**
 * Theme coverage.
 *
 * Issue #204: the `--spine*` tokens were declared only in `:root`, so every
 * theme rendered the light theme's cool-navy sidebar. That is the same defect
 * class as the phantom fallbacks above, one layer up: the token resolves, the
 * build is green, and nothing is wrong except that no theme can reach it.
 *
 * The rule is about *resolved values*, not declaration sites. A `:root` token
 * reached through a late-bound alias (`--code-bg: var(--spine)`) is just as
 * unthemeable as a literal, and key-presence checks would false-positive on
 * the deliberately theme-invariant scale and shadow tokens. So: every colour
 * token `:root` declares must resolve differently in at least one theme
 * palette — unless it is listed in THEME_INVARIANT.
 */

/** Colour tokens that must never change per theme. Empty today — everything
 *  the console ships is themed. Adding to this set is a review-time question. */
const THEME_INVARIANT = new Set<string>([])

/** `:root` is the light theme, so it is covered alongside the named ones. */
const ALL_PALETTES: (string | null)[] = [null, ...THEMES]

function isColor(value: string): boolean {
  try {
    parseColor(value)
    return true
  } catch {
    return false
  }
}

/**
 * Tokens `palettes[0]` declares that resolve to the same colour in every
 * palette. Pure over palettes so the meta-test below can feed it a synthetic
 * stylesheet; the real check passes the palettes read off disk.
 */
function unthemedColorTokens(
  palettes: Map<string, string>[],
  invariant: Set<string> = THEME_INVARIANT,
): string[] {
  const [root] = palettes
  const out: string[] = []
  for (const token of root.keys()) {
    if (invariant.has(token)) continue
    let resolved: string[]
    try {
      resolved = palettes.map((p) => resolveVar(`var(${token})`, p))
    } catch {
      continue // unresolvable chain — the phantom-token check above owns that
    }
    if (!isColor(resolved[0])) continue // scale and shadow tokens are not colours
    if (resolved.some((v) => v !== resolved[0])) continue
    out.push(token)
  }
  return out.sort()
}

describe('theme coverage', () => {
  it('themes every colour token :root declares', () => {
    const unthemed = unthemedColorTokens(ALL_PALETTES.map(paletteFor))
    expect(
      unthemed,
      'These colour tokens resolve to the same value in every theme, so every ' +
        'theme renders the light theme value. Give each html[data-theme=…] ' +
        'block in tokens.css its own value, or add the token to THEME_INVARIANT ' +
        'if it must genuinely never change.',
    ).toEqual([])
  })

  it('catches a :root-only colour token (sanity check on the guard)', () => {
    // A guard whose failure mode is untested is a guard that silently passes.
    const root = new Map([
      ['--themed', '#111111'],
      ['--never-themed', '#123456'],
      ['--u', '4px'],
      ['--shadow', '0 1px 2px color-mix(in srgb, var(--themed) 6%, transparent)'],
    ])
    const palettes = ['#222222', '#333333', '#444444'].map(
      (v) => new Map([...root, ['--themed', v]]),
    )
    // Only the colour that no theme overrides; the scale and shadow tokens are
    // not colours and must not be reported.
    expect(unthemedColorTokens([root, ...palettes])).toEqual(['--never-themed'])
    // The opt-out opts out.
    expect(unthemedColorTokens([root, ...palettes], new Set(['--never-themed']))).toEqual([])
  })

  it('keeps the theme list pinned at both ends', () => {
    // THEMES is a hand-maintained literal: a fifth theme added to theme.ts
    // without a THEMES entry would be silently unguarded by every spec here,
    // and a theme block in tokens.css missing from THEMES would be unmeasured.
    const css = readCss('tokens.css')
    const blocks = [...css.matchAll(/html\[data-theme="([^"]+)"\]/g)].map((m) => m[1]).sort()
    expect(blocks, 'tokens.css theme blocks vs cssThemeKit THEMES').toEqual([...THEMES].sort())

    // theme.ts is semicolon-free, so the union ends at the newline, not a `;`.
    const decl = /export type Theme = ([^\n]+)/.exec(readFileSync(join(SRC, 'theme.ts'), 'utf8'))
    expect(decl, 'theme.ts has no `export type Theme = …`').toBeTruthy()
    const union = [...decl![1].matchAll(/'([^']+)'/g)]
      .map((m) => m[1])
      .filter((t) => t !== 'light')
      .sort()
    expect(union, 'theme.ts Theme union vs cssThemeKit THEMES').toEqual([...THEMES].sort())
  })
})

// blankComments fails open by construction — a mis-detected string could hide a
// real reference — so it is asserted directly rather than trusted.
describe('blankComments', () => {
  const refs = (source: string): string[] =>
    [...blankComments(source).matchAll(/var\(\s*(--[a-zA-Z0-9-]+)/g)].map((m) => m[1])

  it('keeps references that actually render', () => {
    expect(refs(`<p style={{ color: 'var(--signal)' }} />`)).toEqual(['--signal'])
    expect(refs('color: var(--paper);')).toEqual(['--paper'])
    expect(refs('const c = "var(--ink)"')).toEqual(['--ink'])
    expect(refs('const c = `var(--ink-2)`')).toEqual(['--ink-2'])
  })

  it('ignores references in prose', () => {
    expect(refs('// migrate var(--accent) to --signal')).toEqual([])
    expect(refs('/* var(--accent) */')).toEqual([])
    expect(refs('/**\n * `var(--surface-2, #eef)` was undeclared.\n */')).toEqual([])
    expect(refs('// var(--a)\ncolor: var(--b);')).toEqual(['--b'])
  })

  it('does not mistake a URL or a regex for a comment', () => {
    expect(refs(`const url = "https://x.dev/a" // var(--gone)`)).toEqual([])
    expect(refs(`const url = "https://x.dev/var(--kept)"`)).toEqual(['--kept'])
    // An unterminated quote (a regex literal we mis-read) must not swallow the
    // rest of the file.
    expect(refs(`const re = /'/\ncolor: var(--kept);`)).toEqual(['--kept'])
  })

  it('preserves length and line structure', () => {
    const src = 'a\n/* x\ny */\nb // c\n'
    const out = blankComments(src)
    expect(out).toHaveLength(src.length)
    expect(out.split('\n')).toHaveLength(src.split('\n').length)
  })
})
