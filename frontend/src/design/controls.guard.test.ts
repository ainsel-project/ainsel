import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { describe, expect, it } from 'vitest'

/**
 * Form-control guard (issue #286).
 *
 * The console styles its own controls — `Select`, `Input`, `Autocomplete` in
 * `primitives/` — and a page that drops to a raw `<select>` renders the
 * browser's default widget next to the custom ones. That is a visual
 * inconsistency no component test can see (jsdom does not apply stylesheets),
 * so this spec checks the source text, on the same ratchet pattern as
 * `responsive.guard.test.ts`: the baseline may shrink, never grow.
 *
 * The rule itself lives in docs/conventions.md, "Console UI consistency".
 *
 * Scope: native `<select>` in `pages/`. The other native elements
 * (`<input>`, `<textarea>`) have a large legitimate residue (checkboxes,
 * password fields, form internals) that the primitives do not cover yet;
 * widening this ratchet to them is a separate change with its own baseline.
 * `primitives/` is excluded by construction — that is where the styled
 * `<select>` lives.
 */

const SRC = join(__dirname, '..')

/** Files still rendering a raw `<select>`, and how often. Shrink it, never
 *  grow it: add the page to `primitives/` usage instead. */
const BASELINE: Record<string, number> = {
  'pages/audit/AuditEvents.tsx': 1,
  'pages/channels/ChannelEditor.tsx': 2,
}

function walkTsx(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) {
      if (entry === 'node_modules' || entry === 'dist') continue
      walkTsx(full, out)
    } else if (entry.endsWith('.tsx') && !entry.endsWith('.test.tsx')) {
      // Specs quote control markup as fixtures; counting them would force
      // prose into the baseline and erode the ratchet (same reasoning as in
      // responsive.guard.test.ts).
      out.push(full)
    }
  }
  return out
}

const NATIVE_SELECT = /<select[\s/>]/g

function countNativeSelects(file: string): number {
  return (readFileSync(file, 'utf8').match(NATIVE_SELECT) ?? []).length
}

describe('native form controls in pages', () => {
  it('do not grow beyond the baseline (ratchet)', () => {
    const introduced: string[] = []
    for (const file of walkTsx(join(SRC, 'pages'))) {
      const rel = relative(SRC, file)
      const hits = countNativeSelects(file)
      if (hits > (BASELINE[rel] ?? 0)) introduced.push(`${rel}: ${hits}`)
    }
    expect(
      introduced,
      'A raw <select> renders the browser default widget next to the styled ' +
        'primitives. Use Select from primitives/Select.tsx.',
    ).toEqual([])
  })

  it('keeps the baseline honest about the residue', () => {
    const stale: string[] = []
    for (const [file, count] of Object.entries(BASELINE)) {
      const now = countNativeSelects(join(SRC, file))
      if (now < count) stale.push(`${file}: ${count} -> ${now}`)
    }
    expect(
      stale,
      'These baseline entries no longer match the source — remove them (or ' +
        'lower the count) so the ratchet stays tight.',
    ).toEqual([])
  })

  it('catches a raw select in a fixture (sanity check on the guard)', () => {
    // The regex must match the opening tag but not the word "select" in
    // prose, a className, or a component named `<Select>`.
    expect(countNativeSelects(join(SRC, 'primitives', 'Select.tsx'))).toBe(1)
    const detect = (src: string): number => (src.match(NATIVE_SELECT) ?? []).length
    expect(detect('return <select value={v}>{opts}</select>')).toBe(1)
    expect(detect('return <Select value={v} />')).toBe(0)
    expect(detect('<div className="select" />')).toBe(0)
  })
})
