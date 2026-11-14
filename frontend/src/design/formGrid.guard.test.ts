import { describe, expect, it } from 'vitest'

import { declarationsFor, parseRules, readCss, type CssRule } from './cssThemeKit'

// `.form-grid` is the two/three-column grid behind every edit form in the app
// (agent runtime, wizard, schedule, trigger, persona, skill, connector, group).
//
// Grid items take `align-items: normal`, which for grid items *is* stretch, so a
// field sharing a row with a taller one is grown to the row track's height — and
// the row track is sized by the tallest content in the row, which is usually the
// neighbouring field's `.field-hint`. `.field` is itself a grid with auto rows,
// and `.input` sets no height, so the surplus lands in the control's row and the
// control stretches with it: on Runtime & env the Temperature input rendered
// about three times as tall as the Context Window input beside it (issue #276).
//
// One declaration on `.form-grid` fixes it, and `.form-section > .form-grid`
// inherits it because that descendant rule never declares `align-items`. jsdom
// has no layout engine, so the stretched control cannot be measured from a
// component test — pin the declarations that prevent it instead.

const rules = parseRules(readCss('primitives.css'))

/** Every top-level rule whose selector list names `selector` as a whole entry. */
function rulesFor(selector: string): CssRule[] {
  return rules.filter((rule) => rule.selectors.includes(selector))
}

/** Values that would put a grid item back on the row's full height. */
const STRETCH_VALUES = new Set(['stretch', 'normal'])

/** Values that keep an item at its content height (`flex-start` is the legacy alias). */
const START_VALUES = new Set(['start', 'flex-start'])

const FORM_GRID_SELECTORS = [
  '.form-grid',
  '.form-grid.cols-2',
  '.form-grid.cols-3',
  '.form-section > .form-grid',
  '.form-section > .form-grid.cols-2',
  '.form-section > .form-grid.cols-3',
]

describe('form grid alignment', () => {
  const decls = declarationsFor(readCss('primitives.css'), '.form-grid')

  it('is still a gapped grid, so the fix is alignment and not a layout change', () => {
    expect(decls.display, '.form-grid is no longer a grid').toBe('grid')
    expect(decls.gap, '.form-grid lost its gap').toBeTruthy()
  })

  it('aligns fields to the start of the row instead of stretching them', () => {
    const align = decls['align-items']
    expect(
      align,
      '.form-grid has no align-items, so items stretch to the tallest cell',
    ).toBeTruthy()
    expect(
      START_VALUES.has(align),
      `.form-grid align-items: "${align}" does not stop fields stretching to their neighbour`,
    ).toBe(true)
  })

  it('leaves the numbered-section grid to inherit it, rather than restating it', () => {
    // A second declaration would be a second source of truth to drift; the
    // descendant rule only ever declared display/gap, so the cascade hands
    // `align-items` through.
    const section = declarationsFor(readCss('primitives.css'), '.form-section > .form-grid')
    expect(section.display, '.form-section > .form-grid is no longer a grid').toBe('grid')
    expect(
      section['align-items'],
      '.form-section > .form-grid restates align-items — remove it and let .form-grid own it',
    ).toBeUndefined()
  })

  it('never resets the alignment on the column variants either', () => {
    for (const selector of FORM_GRID_SELECTORS) {
      expect(rulesFor(selector).length, `no rule for "${selector}"`).toBeGreaterThan(0)
      for (const rule of rulesFor(selector)) {
        const align = rule.declarations['align-items']
        if (align === undefined) continue
        expect(
          START_VALUES.has(align),
          `"${selector}" sets align-items: "${align}" — only .form-grid may set it, and only to a start value`,
        ).toBe(true)
      }
    }
  })

  it('has no rule that stretches the grid back out', () => {
    // A per-field opt-in (`.form-grid > .field.fill { align-self: stretch }`) is
    // allowed; what is not allowed is anything that stretches the whole grid or
    // every field in it, which would put the defect back for all eight forms.
    const everyChild = (selector: string) => /\.form-grid[^>]*>\s*\*/.test(selector)
    // `.field` as a whole compound — `.field.fill` and `.field-hint` are not it.
    const everyField = (selector: string) => /(^|[\s>+~])\.field(?![\w.-])/.test(selector)
    const stretchesEverything = (selector: string) =>
      everyChild(selector) || (selector.includes('.form-grid') && everyField(selector))

    for (const rule of rules) {
      for (const selector of rule.selectors) {
        if (!selector.includes('.form-grid')) continue
        const align = rule.declarations['align-items']
        if (align !== undefined) {
          expect(
            !STRETCH_VALUES.has(align),
            `"${selector}" re-stretches the form grid with align-items: "${align}"`,
          ).toBe(true)
        }
        const alignSelf = rule.declarations['align-self']
        if (alignSelf !== undefined && stretchesEverything(selector)) {
          expect(
            !STRETCH_VALUES.has(alignSelf),
            `"${selector}" re-stretches every field with align-self: "${alignSelf}" — opt in per field instead`,
          ).toBe(true)
        }
      }
    }
  })

  it('leaves .field without an align-self of its own', () => {
    // `.field` is used inside the form grid; a stretch here would reintroduce
    // the defect for every form at once.
    const field = declarationsFor(readCss('primitives.css'), '.field')
    expect(
      field['align-self'],
      '.field sets align-self, which overrides what .form-grid asks for',
    ).toBeUndefined()
  })
})
