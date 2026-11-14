import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { describe, expect, it } from 'vitest'

import { stripComments } from './cssThemeKit'

/**
 * Responsive-layout guard (issue #261).
 *
 * jsdom has no layout engine and does not evaluate media queries, so a
 * component test cannot see any of this. What it *can* see is the stylesheet:
 * these specs assert the structure that makes the phone tier work, on the same
 * parsing-guard pattern as `tokens.guard.test.ts`.
 *
 * What gets pinned:
 *
 *  1. The breakpoint scale. Custom properties cannot be used inside a media
 *     query, so the scale is a documented constant (see the BREAKPOINTS header
 *     in tokens.css) rather than a token, and this spec is what keeps it from
 *     drifting into per-screen magic numbers.
 *  2. Every multi-column primitive collapses. A primitive that keeps a fixed
 *     track at 360px does not merely look wrong: `.canvas` is
 *     `overflow-x: hidden`, so the overflow is *clipped*, and the content past
 *     the viewport edge becomes unreachable rather than scrollable.
 *  3. Page padding goes through `.page`. Inline `padding: '28px 32px'` is
 *     invisible to a media query, which is how 43 containers ended up unable to
 *     shrink. The ratchet below is shrink-only, same shape as the token one.
 *     Inline `gridTemplateColumns` on a collapsing primitive is the same defect
 *     one level down (issue #284) and gets the same ratchet.
 *  4. The two things a phone layout still needs that no primitive covers: the
 *     register scroll stays scrollable and legible, and the agent workspace's
 *     section nav becomes a strip instead of a 170px column.
 *
 * The rules themselves live in `design/responsive.css`, not in primitives.css:
 * three specs read primitives.css through `cssThemeKit.parseRules`, which refuses
 * any conditional at-rule, and that refusal is pinned by cssThemeKit.test.ts. So a
 * fourth thing gets pinned from the other side — that primitives.css stays flat
 * enough for those specs to read it (see 'leaves primitives.css flat').
 *
 * `cssThemeKit.parseRules` is deliberately not used: it throws on `@media` (its
 * body is real style rules) and it folds duplicate properties into one map
 * entry, which would erase exactly the `vh`/`dvh` fallback pairs checked here.
 * So this spec brings its own parser, which keeps at-rules and declaration
 * order — the two things a responsive check needs.
 */

const SRC = join(__dirname, '..')

/** Every width a `@media` rule may query, documented in tokens.css. */
const BREAKPOINTS = [480, 760, 899, 1000, 1100] as const

/**
 * Widths that are a breakpoint's own complement rather than a tier of their
 * own: `min-width: 1001px` is "not the 1000px drawer", nothing more.
 */
const COMPLEMENTS: Record<number, number> = { 1001: 1000 }

/** Widths used to sample the cascade. 600 sits between the two new tiers. */
const DESKTOP = 1400
const NARROW = 600
const PHONE = 360

// ---------------------------------------------------------------- parser ----

type Decl = [property: string, value: string]

/** One style rule, with its declarations in source order and duplicates kept. */
type StyleRule = { selectors: string[]; declarations: Decl[] }

/** A style rule plus the `@media` query it is nested in, in source order. */
type Block = { media: string | null; rule: StyleRule }

/**
 * Every style rule in a stylesheet, in source order, tagged with its enclosing
 * media query. One level of nesting is all the repo uses; a nested `@media`
 * fails loudly instead of being read as a selector.
 */
function parseBlocks(css: string, media: string | null = null): Block[] {
  const src = stripComments(css)
  const out: Block[] = []
  let i = 0
  while (i < src.length) {
    const open = src.indexOf('{', i)
    if (open === -1) break
    const prelude = src.slice(i, open).trim()
    const end = endOfBlock(src, open)
    const body = src.slice(open + 1, end - 1)
    if (prelude.startsWith('@media')) {
      if (media) throw new Error('nested @media is not supported by this guard')
      out.push(...parseBlocks(body, prelude.slice('@media'.length).trim()))
    } else if (prelude.startsWith('@')) {
      // @keyframes / @font-face / @import: bodies are not selector rules.
    } else if (prelude) {
      out.push({
        media,
        rule: {
          selectors: prelude.split(',').map((s) => s.trim()),
          declarations: body
            .split(';')
            .map((decl) => decl.trim())
            .filter(Boolean)
            .map((decl) => {
              const colon = decl.indexOf(':')
              return [decl.slice(0, colon).trim(), decl.slice(colon + 1).trim()] as Decl
            }),
        },
      })
    }
    i = end
  }
  return out
}

/** Index just past the balanced `{…}` that starts at `open`. */
function endOfBlock(src: string, open: number): number {
  let depth = 0
  for (let i = open; i < src.length; i++) {
    if (src[i] === '{') depth++
    else if (src[i] === '}' && --depth === 0) return i + 1
  }
  throw new Error('unbalanced { in stylesheet')
}

/** Does a media query apply at `width`? Non-width queries (motion, contrast) always do. */
function appliesAt(query: string | null, width: number): boolean {
  if (query === null) return true
  if (!/\b(min|max)-width\b/.test(query)) return true
  const max = /max-width:\s*(\d+)px/.exec(query)
  const min = /min-width:\s*(\d+)px/.exec(query)
  if (max && Number(max[1]) < width) return false
  if (min && Number(min[1]) > width) return false
  return true
}

/** The value a property has at `width`: the last applicable declaration, as CSS does. */
function valueAt(blocks: Block[], selector: string, property: string, width: number): string {
  let found = ''
  for (const block of blocks) {
    if (!block.rule.selectors.includes(selector)) continue
    if (!appliesAt(block.media, width)) continue
    for (const [prop, value] of block.rule.declarations) {
      if (prop === property) found = value
    }
  }
  return found
}

/** Every value a property is given for `selector` at `width`, in source order —
 *  what a fallback pair looks like from the parser's side. */
function valuesAt(blocks: Block[], selector: string, property: string, width: number): string[] {
  const found: string[] = []
  for (const block of blocks) {
    if (!block.rule.selectors.includes(selector)) continue
    if (!appliesAt(block.media, width)) continue
    for (const [prop, value] of block.rule.declarations) {
      if (prop === property) found.push(value)
    }
  }
  return found
}

/** Column count of a `grid-template-columns` value; null when there is none. */
function columns(value: string): number | null {
  if (!value) return null
  const repeat = /^repeat\(\s*(\d+)\s*,/.exec(value)
  if (repeat) return Number(repeat[1])
  return value.trim().split(/\s+/).length
}

// ----------------------------------------------------------------- files ----

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

function walkTsx(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) {
      if (entry === 'node_modules' || entry === 'dist') continue
      walkTsx(full, out)
    } else if (entry.endsWith('.tsx') && !entry.endsWith('.test.tsx')) {
      // Specs quote the padding string as fixtures; counting them would force
      // prose into the baseline and erode the ratchet (same reasoning as in
      // tokens.guard.test.ts).
      out.push(full)
    }
  }
  return out
}

const read = (path: string): string => readFileSync(join(SRC, path), 'utf8')

/**
 * The design cascade in import order: the flat primitives, then the responsive
 * layer over them. `valueAt` takes the last applicable declaration, which is
 * what the cascade does, so the two files must be concatenated in the order
 * main.tsx imports them.
 */
const designBlocks = (): Block[] => [
  ...parseBlocks(read(join('design', 'primitives.css'))),
  ...parseBlocks(read(join('design', 'responsive.css'))),
]

/**
 * Component CSS for the agent workspace. Its `.section-nav` rules are the desktop
 * shape the phone tier in responsive.css has to beat, so a nav check has to read
 * both files — and it is the reason the phone selectors are qualified with `nav.`:
 * component CSS is bundled after responsive.css, so an equal-specificity override
 * there would be dead on arrival.
 */
const workspaceBlocks = (): Block[] =>
  parseBlocks(read(join('pages', 'agents', 'workspace', 'AgentWorkspace.css')))

// ---------------------------------------------------------------- specs ----

describe('breakpoint scale', () => {
  /** Every width a `@media` preludes on, per stylesheet. Read off the parsed
   *  queries rather than a regex over the file: `min-width` is also a
   *  declaration (`table.reg { min-width: 960px }`), and that is not a tier. */
  function queriedWidths(): Map<string, number[]> {
    const found = new Map<string, number[]>()
    for (const file of walkCss(SRC)) {
      const widths = new Set<number>()
      for (const block of parseBlocks(readFileSync(file, 'utf8'))) {
        if (block.media === null) continue
        for (const m of block.media.matchAll(/\b(?:min|max)-width:\s*(\d+)px/g)) {
          widths.add(Number(m[1]))
        }
      }
      if (widths.size > 0)
        found.set(
          relative(SRC, file),
          [...widths].sort((a, b) => a - b),
        )
    }
    return found
  }

  it('is respected by every stylesheet', () => {
    const allowed = new Set<number>([...BREAKPOINTS, ...Object.keys(COMPLEMENTS).map(Number)])
    const offScale: string[] = []
    for (const [file, widths] of queriedWidths()) {
      for (const w of widths) if (!allowed.has(w)) offScale.push(`${file}: ${w}px`)
    }
    expect(
      offScale,
      'A width outside the scale in tokens.css means a new ad-hoc breakpoint. ' +
        'Use one of the documented tiers, or add it to both tokens.css and the ' +
        'BREAKPOINTS list here — with a failure mode behind it, not a taste.',
    ).toEqual([])
  })

  it('is actually used, so a deleted tier cannot pass by being absent', () => {
    const all = new Set([...queriedWidths().values()].flat())
    for (const tier of [480, 760, 1000]) {
      expect(all.has(tier), `no stylesheet queries ${tier}px any more`).toBe(true)
    }
  })

  it('rejects a width off the scale (sanity check on the guard)', () => {
    const allowed = new Set<number>([...BREAKPOINTS, ...Object.keys(COMPLEMENTS).map(Number)])
    const widths = (css: string): number[] =>
      parseBlocks(css)
        .filter((b) => b.media !== null)
        .flatMap((b) => [...b.media!.matchAll(/(\d+)px/g)].map((m) => Number(m[1])))
        .filter((w) => !allowed.has(w))
    expect(widths('@media (max-width: 720px) { .x { color: red } }')).toEqual([720])
    // A declaration that looks like a breakpoint feature is not one.
    expect(widths('table.reg { min-width: 960px }')).toEqual([])
  })
})

/**
 * Every primitive that lays out more than one column, and what it must collapse
 * to. `narrow`/`phone` omitted means "must not change at that tier" — pinned
 * because `.dual-list` is deliberately left side-by-side until 480px, and a
 * silent collapse there would be a behaviour change nobody asked for.
 */
type Collapse = {
  selector: string
  /** Columns the primitive declares with no media query in play. */
  base: number
  /** Columns at the narrow tier; omitted means "unchanged from `base`". */
  narrow?: number
  /** Columns at the phone tier; omitted means "unchanged from `narrow`". */
  phone?: number
}

const COLLAPSES: Collapse[] = [
  { selector: '.kpi-row', base: 4, narrow: 2, phone: 1 },
  { selector: '.info-grid', base: 4, narrow: 2, phone: 1 },
  // The column-count variants a detail page uses instead of an inline
  // `gridTemplateColumns` (issue #284). They are separate selectors precisely so
  // that they can be pinned here: a counted value carried by an inline style is
  // invisible to this table, which is how the phone tier went unnoticed.
  { selector: '.info-grid.cols-5', base: 5, narrow: 2, phone: 1 },
  { selector: '.info-grid.cols-7', base: 7, narrow: 2, phone: 1 },
  { selector: '.form-grid.cols-2', base: 2, phone: 1 },
  { selector: '.form-grid.cols-3', base: 3, narrow: 2, phone: 1 },
  { selector: '.form-section > .form-grid.cols-2', base: 2, phone: 1 },
  { selector: '.form-section > .form-grid.cols-3', base: 3, narrow: 2, phone: 1 },
  // Two tracks, one of them a fixed 320px: wider than a 360px viewport minus
  // padding, and clipped rather than scrolled by .canvas.
  { selector: '.md-grid', base: 2, narrow: 1, phone: 1 },
  { selector: '.dual-list', base: 3, narrow: 3, phone: 1 },
]

describe('primitive collapses', () => {
  const blocks = designBlocks()

  it.each(COLLAPSES)('$selector keeps its column count on desktop', ({ selector, base }) => {
    expect(columns(valueAt(blocks, selector, 'grid-template-columns', DESKTOP)), selector).toBe(
      base,
    )
  })

  it.each(COLLAPSES)('$selector is $narrow columns at the narrow tier', (entry) => {
    const at = columns(valueAt(blocks, entry.selector, 'grid-template-columns', NARROW))
    expect(at, `${entry.selector} at ${NARROW}px`).toBe(entry.narrow ?? entry.base)
  })

  it.each(COLLAPSES)('$selector is $phone column at the phone tier', (entry) => {
    const at = columns(valueAt(blocks, entry.selector, 'grid-template-columns', PHONE) || '1fr')
    expect(at, `${entry.selector} at ${PHONE}px`).toBe(entry.phone ?? entry.narrow ?? entry.base)
  })

  it('reports a primitive that never collapses (sanity check on the guard)', () => {
    const blocks = parseBlocks(`
      .stub-grid { display: grid; grid-template-columns: repeat(4, 1fr); }
      @media (max-width: 760px) { .other { grid-template-columns: 1fr; } }
    `)
    expect(columns(valueAt(blocks, '.stub-grid', 'grid-template-columns', NARROW))).toBe(4)
    // The same selector *does* collapse when the rule is there — the check is
    // not simply reading the base rule back.
    const fixed = parseBlocks(`
      .stub-grid { display: grid; grid-template-columns: repeat(4, 1fr); }
      @media (max-width: 760px) { .stub-grid { grid-template-columns: repeat(2, 1fr); } }
    `)
    expect(columns(valueAt(fixed, '.stub-grid', 'grid-template-columns', NARROW))).toBe(2)
  })

  it('swaps the info-grid cell borders to match the rows it draws', () => {
    // Two columns: the base rule drops the border only on the last cell, so the
    // right-hand column needs its own rule or every odd row shows a stray rule.
    expect(valueAt(designBlocks(), '.info-grid > div:nth-child(2n)', 'border-right', NARROW)).toBe(
      '0',
    )
    // One column: no vertical rules at all, and no bottom rule after the last.
    expect(valueAt(designBlocks(), '.info-grid > div', 'border-right', PHONE)).toBe('0')
    expect(valueAt(designBlocks(), '.info-grid > div', 'border-bottom', PHONE)).toContain(
      'rule-soft',
    )
    expect(valueAt(designBlocks(), '.info-grid > div:last-child', 'border-bottom', PHONE)).toBe('0')
  })

  it('stacks the dual-list arrows between the panes instead of beside them', () => {
    const blocks = designBlocks()
    expect(valueAt(blocks, '.dual-list-arrows', 'flex-direction', PHONE)).toBe('row')
    // The vertical rule that separated the columns would be a stray line across
    // the stacked layout.
    expect(valueAt(blocks, '.dual-list-arrows', 'border-left', PHONE)).toBe('0')
    expect(valueAt(blocks, '.dual-list-arrows', 'border-top', PHONE)).toContain('rule-soft')
  })

  it('keeps the stacked master-detail left pane scrollable', () => {
    const blocks = designBlocks()
    expect(valueAt(blocks, '.md-grid > .md-left', 'max-height', NARROW)).toBe('320px')
    expect(valueAt(blocks, '.md-grid > .md-left', 'overflow-y', NARROW)).toBe('auto')
    expect(valueAt(blocks, '.md-grid > .md-left', 'border-right', NARROW)).toBe('0')
  })

  it('keeps the register table scrollable and legible at every tier', () => {
    // The scroll is the documented answer to a wide table on a narrow screen
    // (docs/conventions.md: "reading a register on a phone is a scroll, not a
    // reflow"). It was never pinned, which is why this issue could be filed as
    // "add overflow-x: auto" — it was already there.
    const blocks = designBlocks()
    for (const width of [DESKTOP, NARROW, PHONE]) {
      expect(valueAt(blocks, '.reg-wrap', 'overflow-x', width), `at ${width}px`).toBe('auto')
    }
    // What was missing is an affordance and a break opportunity, both phone-only.
    expect(valueAt(blocks, '.reg-wrap', 'scrollbar-width', PHONE)).toBe('thin')
    expect(valueAt(blocks, '.reg-wrap', 'scrollbar-color', PHONE)).toContain('ink-4')
    expect(valueAt(blocks, '.reg-wrap', 'scrollbar-width', DESKTOP)).toBe('')
    expect(valueAt(blocks, 'table.reg .num', 'overflow-wrap', PHONE)).toBe('anywhere')
    expect(valueAt(blocks, 'table.reg td.num', 'overflow-wrap', PHONE)).toBe('anywhere')
    expect(valueAt(blocks, 'table.reg .num', 'overflow-wrap', DESKTOP)).toBe('')
  })

  it('lays the agent workspace section nav out as a strip on a phone', () => {
    const blocks = [...designBlocks(), ...workspaceBlocks()]
    // Desktop: the 170px column, unchanged.
    expect(valueAt(blocks, '.section-nav', 'flex-direction', DESKTOP)).toBe('column')
    expect(valueAt(blocks, '.section-nav', 'width', DESKTOP)).toBe('170px')
    expect(valueAt(blocks, '.section-nav-heading', 'display', DESKTOP)).toBe('')
    // Phone: full width, wrapping row, no right-hand rule. Below 900px the
    // workspace stacks, so a column nav here is the whole first screen.
    expect(valueAt(blocks, 'nav.section-nav', 'flex-direction', PHONE)).toBe('row')
    expect(valueAt(blocks, 'nav.section-nav', 'flex-wrap', PHONE)).toBe('wrap')
    expect(valueAt(blocks, 'nav.section-nav', 'width', PHONE)).toBe('auto')
    expect(valueAt(blocks, 'nav.section-nav', 'border-right', PHONE)).toBe('0')
    expect(valueAt(blocks, 'nav.section-nav', 'border-bottom', PHONE)).toContain('paper-deep')
    // The group boxes unwrap into the row and their captions go with them.
    expect(valueAt(blocks, 'nav.section-nav .section-nav-group', 'display', PHONE)).toBe('contents')
    expect(valueAt(blocks, 'nav.section-nav .section-nav-list', 'flex-direction', PHONE)).toBe(
      'row',
    )
    expect(valueAt(blocks, 'nav.section-nav .section-nav-heading', 'display', PHONE)).toBe('none')
    // Nothing at the narrow tier: 600px still has room for the column.
    expect(valueAt(blocks, 'nav.section-nav', 'flex-direction', NARROW)).toBe('')
  })

  it('leaves primitives.css flat, which is what its other readers assume', () => {
    // codeSurfaces, scrollCap and themeContrast read primitives.css through
    // cssThemeKit.parseRules, and that function throws on any conditional
    // at-rule. A media query in primitives.css is not a style preference; it
    // breaks three specs whose contract with this file is "no @media".
    const nested = parseBlocks(read(join('design', 'primitives.css'))).filter(
      (b) => b.media !== null,
    )
    expect(
      nested,
      'primitives.css must stay free of @media — put the rule in responsive.css ' +
        'so cssThemeKit.parseRules can still read the flat primitive.',
    ).toEqual([])
  })
})

describe('page container', () => {
  const blocks = designBlocks()

  /** Sum of the px values in a padding shorthand — enough to order the tiers. */
  const bulk = (value: string): number =>
    (value.match(/(\d+(?:\.\d+)?)px/g) ?? []).reduce((sum, px) => sum + Number(px.slice(0, -2)), 0)

  it('exists with the desktop gutter every page used to inline', () => {
    expect(valueAt(blocks, '.page', 'padding', DESKTOP)).toBe('28px 32px 48px')
  })

  it('closes the gutters at each smaller tier', () => {
    const desktop = bulk(valueAt(blocks, '.page', 'padding', DESKTOP))
    const narrow = bulk(valueAt(blocks, '.page', 'padding', NARROW))
    const phone = bulk(valueAt(blocks, '.page', 'padding', PHONE))
    expect(narrow, '.page padding does not shrink at the narrow tier').toBeLessThan(desktop)
    expect(phone, '.page padding does not shrink at the phone tier').toBeLessThan(narrow)
  })

  it('replaces the inline desktop padding, and keeps it that way (ratchet)', () => {
    // Files still carrying the inline page padding, and how often. Shrink it,
    // never grow it: an inline padding is invisible to every media query above.
    const BASELINE: Record<string, number> = {}
    const introduced: string[] = []
    for (const file of walkTsx(SRC)) {
      const rel = relative(SRC, file)
      const hits = (readFileSync(file, 'utf8').match(/padding:\s*'28px 32px'/g) ?? []).length
      if (hits > (BASELINE[rel] ?? 0)) introduced.push(`${rel}: ${hits}`)
    }
    expect(
      introduced,
      "Inline `padding: '28px 32px'` cannot respond to a media query, so the " +
        'page keeps desktop gutters on a phone. Use className="page".',
    ).toEqual([])

    const stale: string[] = []
    for (const [file, count] of Object.entries(BASELINE)) {
      const now = (readFileSync(join(SRC, file), 'utf8').match(/padding:\s*'28px 32px'/g) ?? [])
        .length
      if (now < count) stale.push(`${file}: ${count} -> ${now}`)
    }
    expect(stale, 'These baseline entries no longer match the source.').toEqual([])
  })
})

/**
 * Inline column counts on a collapsing primitive (issue #284).
 *
 * `style={{ gridTemplateColumns: 'repeat(7, 1fr)' }}` beats every rule in
 * responsive.css — media queries cannot touch an inline declaration — so the
 * grid keeps its desktop shape at 360px and overflows the panel. The two grids
 * on the agent overview were the reported symptom; the ratchet keeps the rest of
 * the console from re-growing the same defect.
 */
describe('inline column counts', () => {
  /** Primitives whose columns responsive.css collapses (see COLLAPSES). */
  const COLLAPSING = /\b(kpi-row|info-grid|form-grid|md-grid|dual-list)\b/

  /** Index of the `>` closing the tag that starts at `from`, skipping quoted
   *  strings and counting brackets so an arrow function in an attribute does not
   *  end the tag early. */
  function endOfTag(src: string, from: number): number {
    let depth = 0
    let quote = ''
    for (let i = from; i < src.length; i++) {
      const c = src[i]
      if (quote) {
        if (c === quote) quote = ''
        continue
      }
      if (c === '"' || c === "'" || c === '`') quote = c
      else if (c === '{' || c === '(' || c === '[') depth++
      else if (c === '}' || c === ')' || c === ']') depth--
      else if (c === '>' && depth === 0) return i
    }
    return src.length
  }

  /** How many elements in `source` name a collapsing primitive *and* set
   *  `gridTemplateColumns` in the same opening tag. */
  function inlineColumnCounts(source: string): number {
    let hits = 0
    for (const m of source.matchAll(/className="([^"]*)"/g)) {
      if (!COLLAPSING.test(m[1])) continue
      const start = (m.index ?? 0) + m[0].length
      if (/gridTemplateColumns/.test(source.slice(start, endOfTag(source, start)))) hits++
    }
    return hits
  }

  it('is not used on a collapsing primitive (ratchet)', () => {
    // Files still carrying an inline column count, and how often. Shrink it,
    // never grow it — and prefer `.info-grid.cols-5` / `.cols-7` over a new
    // baseline entry. The observability screens are the known residue: same
    // defect, filed separately, not fixed here.
    const BASELINE: Record<string, number> = {
      'pages/agents/workspace/OverviewSection.tsx': 0,
      'pages/channels/ChannelEditor.tsx': 1,
      // A bespoke three-track `.md-grid`; it loses the responsive collapse the
      // same way, but it is the images editor, not one of the three screens here.
      'pages/images/ToolFieldArray.tsx': 1,
      'pages/observability/SummaryCards.tsx': 1,
      'pages/observability/events/EventView.tsx': 4,
      'pages/observability/tokens/TokensDetail.tsx': 1,
    }
    const introduced: string[] = []
    for (const file of walkTsx(SRC)) {
      const rel = relative(SRC, file)
      const hits = inlineColumnCounts(readFileSync(file, 'utf8'))
      if (hits > (BASELINE[rel] ?? 0)) introduced.push(`${rel}: ${hits}`)
    }
    expect(
      introduced,
      'An inline gridTemplateColumns cannot match a media query, so the grid keeps ' +
        'its desktop column count on a phone and overflows the panel. Use a column ' +
        'count class from primitives.css (`.info-grid.cols-5`, `.cols-7`).',
    ).toEqual([])

    const stale: string[] = []
    for (const [file, count] of Object.entries(BASELINE)) {
      const now = inlineColumnCounts(read(file))
      if (now < count) stale.push(`${file}: ${count} -> ${now}`)
    }
    expect(stale, 'These baseline entries no longer match the source.').toEqual([])
  })

  it('counts a primitive and not an unrelated tag (sanity check on the ratchet)', () => {
    const fixed = `
      <div className="info-grid cols-5">{x}</div>
      <div className="kpi-row" style={{ gridTemplateColumns: 'repeat(7, 1fr)' }} />
      <div className="toolbar" style={{ gridTemplateColumns: '120px 1fr' }} />
      <div className="kpi-row" style={{ display: 'grid' }} onClick={() => go()} />
    `
    expect(inlineColumnCounts(fixed)).toBe(1)
  })
})

describe('phone chrome', () => {
  const layout = (): Block[] => parseBlocks(read(join('layout', 'Layout.css')))
  const titleblock = (): Block[] => parseBlocks(read(join('layout', 'Titleblock.css')))

  it('sticks the title block below the mobile bar, not under it', () => {
    const blocks = titleblock()
    expect(valueAt(blocks, '.titleblock', 'position', DESKTOP)).toBe('sticky')
    // A literal 0 (or nothing) puts it behind the 60px .mobilebar.
    expect(
      valueAt(blocks, '.titleblock', 'top', DESKTOP),
      '.titleblock must offset itself by var(--chrome-top)',
    ).toBe('var(--chrome-top)')
  })

  it('declares --chrome-top at both ends of the drawer breakpoint', () => {
    const blocks = layout()
    expect(valueAt(blocks, '.shell', '--chrome-top', DESKTOP)).toBe('0px')
    expect(valueAt(blocks, '.shell', '--chrome-top', PHONE)).toBe('60px')
  })

  it('clips the canvas without making it a scroll container', () => {
    // `overflow-x: hidden` with `overflow-y: visible` computes overflow-y to
    // `auto`, which turns .canvas into the nearest scrolling ancestor of the
    // sticky title block. .canvas never scrolls (it grows with its content), so
    // `top: var(--chrome-top)` resolves against a box that does not move and the
    // title block paints over the first card instead of sticking (issue #284).
    const blocks = layout()
    for (const width of [DESKTOP, NARROW, PHONE]) {
      const value = valueAt(blocks, '.canvas', 'overflow-x', width)
      expect(
        value,
        'a bare `hidden` puts the sticky title block back in a box ' +
          `that cannot scroll (${width}px)`,
      ).toBe('clip')
    }
    // `hidden` stays as the fallback for engines without `clip` — declared first.
    expect(valuesAt(blocks, '.canvas', 'overflow-x', DESKTOP)).toEqual(['hidden', 'clip'])
  })

  it('pairs every chrome height with a dvh fallback', () => {
    // `vh` is the largest viewport on mobile browsers, so a `100vh` chrome box
    // is taller than what is visible. Spine.css established the pair; the
    // layouts must not regress it.
    const offenders: string[] = []
    for (const file of [join('layout', 'Layout.css'), join('layout', 'Spine.css')]) {
      for (const block of parseBlocks(read(file))) {
        const decls = block.rule.declarations
        decls.forEach(([prop, value], i) => {
          if (!/-height$/.test(prop)) return
          if (!/\b\d+(?:\.\d+)?vh\b/.test(value) || value.includes('dvh')) return
          const next = decls[i + 1]
          if (!next || next[0] !== prop || !next[1].includes('dvh')) {
            offenders.push(`${file}: ${block.rule.selectors.join(', ')} { ${prop}: ${value} }`)
          }
        })
      }
    }
    expect(
      offenders,
      'A vh height with no dvh follow-up is measured against the largest ' +
        'viewport, so the URL bar covers the bottom of the page.',
    ).toEqual([])
  })
})
