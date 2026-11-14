import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { describe, expect, it } from 'vitest'

import {
  THEMES,
  contrast,
  declarationsFor,
  paletteFor,
  readCss,
  resolveColor,
  tokenColor,
} from './cssThemeKit'

/**
 * Row-link guard (issue #281).
 *
 * Every list table used to render its row title as
 * `<b style={{ cursor: 'pointer', color: 'var(--accent)' }} onClick={navigate}>`.
 * That is not a link: it cannot receive keyboard focus, middle-click and
 * ⌘-click cannot open the row in a new tab, a screen reader announces no
 * destination, and `--accent` is a phantom token. Two of those tables then put a
 * second, redundant "View" button in an Actions column for the same navigation.
 *
 * The fix was mechanical — `EntityLink` with `className="rowlink"` — and this
 * spec keeps it mechanical: the `<b onClick>` row title may not come back, the
 * row's single navigation affordance stays a real anchor, and `.rowlink` keeps
 * the token it was solved against.
 */

const SRC = join(__dirname, '..')

/** List pages whose row title must be an EntityLink. */
const ROW_LINK_PAGES = [
  'pages/agents/AgentList.tsx',
  'pages/connectors/ConnectorList.tsx',
  'pages/groups/GroupList.tsx',
  'pages/images/ImageList.tsx',
  'pages/mcp-servers/MCPServerList.tsx',
  'pages/personas/PersonaList.tsx',
  'pages/skills/SkillList.tsx',
]

function walk(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) {
      if (entry === 'node_modules' || entry === 'dist') continue
      walk(full, out)
    } else if (entry.endsWith('.tsx') && !entry.endsWith('.test.tsx')) {
      out.push(full)
    }
  }
  return out
}

const read = (rel: string) => readFileSync(join(SRC, rel), 'utf8')

/**
 * `<b … onClick=…>` across the whole element, comments aside. Deliberately
 * coarse: the point is that a bold element carries navigation, whatever it looks
 * like. A `<b>` inside a JSX comment is not rendered, so comments are blanked.
 */
function stripJsxComments(source: string): string {
  return source.replace(/\{\/\*[\s\S]*?\*\/\}/g, '')
}

describe('row title links', () => {
  const files = walk(SRC).map((f) => relative(SRC, f))

  it('leaves no <b onClick> row titles anywhere in src/', () => {
    const offenders = files.filter((rel) => {
      const source = stripJsxComments(read(rel))
      return /<b\b[^>]*\sonClick=/.test(source)
    })
    expect(
      offenders,
      `row titles must navigate with a link (EntityLink + .rowlink), not <b onClick>: ${offenders.join(', ')}`,
    ).toEqual([])
  })

  it('leaves no pointer-cursor <b> pretending to be a link', () => {
    const offenders = files.filter((rel) => {
      const source = stripJsxComments(read(rel))
      return /<b\b[^>]*cursor:\s*'pointer'/.test(source)
    })
    expect(offenders).toEqual([])
  })

  it.each(ROW_LINK_PAGES)('renders the row title of %s through EntityLink', (rel) => {
    const source = read(rel)
    expect(source, `${rel} does not import EntityLink`).toMatch(
      /import\s*\{[^}]*EntityLink[^}]*\}\s*from\s*'[^']*components\/EntityLink'/,
    )
    expect(source, `${rel} has no rowlink-styled EntityLink`).toMatch(
      /<EntityLink[^>]*className="rowlink"/s,
    )
  })

  it('routes those rows through the shared path builders', () => {
    // A row link that hardcodes its own template string drifts from App.tsx;
    // entityPaths.ts is the single place the routes are spelled out.
    for (const rel of ROW_LINK_PAGES) {
      const source = read(rel)
      expect(source, `${rel} does not import from components/entityPaths`).toMatch(
        /import\s*\{[^}]*\}\s*from\s*'[^']*components\/entityPaths'/,
      )
      expect(source, `${rel} still builds a detail path inline`).not.toMatch(
        /onClick=\{\s*\(\)\s*=>\s*navigate\(`\/(agents|connectors|groups|agent-images|personas|skills)\//,
      )
    }
  })

  it('gives each row exactly one navigation affordance', () => {
    // The "View" buttons duplicated the row link. A trailing Actions column is
    // fine only when it does something the title link cannot (Delete, etc.).
    for (const rel of ROW_LINK_PAGES) {
      const source = read(rel)
      expect(
        source.match(/>\s*View\s*</g) ?? [],
        `${rel} still renders a "View" button next to the row link`,
      ).toEqual([])
    }
  })
})

describe('.rowlink', () => {
  const decls = declarationsFor(readCss('primitives.css'), '.rowlink')
  const ALL_THEMES: (string | null)[] = [null, ...THEMES]
  const label = (t: string | null) => (t === null ? 'default' : t)

  // declarationsFor throws when the selector is absent, so a deleted rule fails
  // every assertion below rather than passing a vacuous one.
  it('paints the row title with --signal-ink, never the phantom --accent', () => {
    expect(decls.color).toBe('var(--signal-ink)')
  })

  it.each(ALL_THEMES.map((t) => [label(t), t] as const))(
    'clears WCAG AA against the panel it sits on in the %s theme',
    (name, theme) => {
      // Every list table is inside a `.panel`, which paints --paper-2.
      const ink = resolveColor(decls.color, paletteFor(theme))
      const bg = tokenColor('--paper-2', theme)
      expect(contrast(ink, bg), `.rowlink in the ${name} theme`).toBeGreaterThanOrEqual(4.5)
    },
  )

  it('stays bold, so replacing the <b> does not lose the row title weight', () => {
    expect(Number.parseInt(decls['font-weight'] ?? '', 10)).toBeGreaterThanOrEqual(600)
  })

  it('keeps a focus/hover affordance and a visited distinction', () => {
    const css = readCss('primitives.css')
    expect(css).toMatch(/\.rowlink:hover/)
    expect(css).toMatch(/\.rowlink:focus-visible/)
    // :visited may only restyle a handful of properties; text-decoration-color
    // is one of them, and it is the one used here.
    expect(css).toMatch(/\.rowlink:visited\s*\{[^}]*text-decoration-color/)
  })
})
