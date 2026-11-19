import { describe, expect, it } from 'vitest'

import { declarationsFor, parseRules, readCss } from './cssThemeKit'

// Every screenshot in `docs/` is a retina capture about 2400px wide, while the
// docs content column (`.docs-content-col .md-body` in DocsPage.css) is capped
// at 760px. With no `img` rule under `.md-body`, images kept their intrinsic
// width and spilled out of the column: `.docs-main` measured clientWidth 1020px
// against scrollWidth 2552px, so every docs page with a screenshot scrolled
// sideways and no screenshot could be seen whole (issue #383).
//
// jsdom has no layout engine, so the overflow cannot be measured from a
// component test — pin the declarations that prevent it instead. The
// full-resolution view is a click-to-zoom overlay in DocsPage.tsx.

const css = readCss('primitives.css')

describe('markdown body images fit the content column', () => {
  const decls = declarationsFor(css, '.md-body img')

  it('caps the image width at the column', () => {
    expect(
      decls['max-width'],
      '.md-body img has no max-width — 2400px screenshots overflow the 760px docs column',
    ).toBe('100%')
  })

  it('keeps the aspect ratio while scaling down', () => {
    expect(decls.height, '.md-body img does not scale height with width').toBe('auto')
  })

  it('lays images out as blocks with a rule border', () => {
    expect(decls.display, '.md-body img is not a block').toBe('block')
    expect(decls.border, '.md-body img lost its border').toContain('var(--rule-soft)')
  })

  it('is not re-widened by any other rule in the stylesheet', () => {
    // `.md-body img` itself plus any descendant/compound selector that still
    // targets images inside the markdown body (e.g. `.docs-content-col .md-body img`).
    const targetsMdBodyImg = (selector: string) =>
      /\.md-body(?![\w-])[^{]*\bimg(?![\w-])/.test(selector)

    for (const rule of parseRules(css)) {
      for (const selector of rule.selectors) {
        if (!targetsMdBodyImg(selector)) continue
        const width = rule.declarations['max-width']
        if (width === undefined) continue
        expect(width, `"${selector}" overrides the .md-body img max-width with "${width}"`).toBe(
          '100%',
        )
      }
    }
  })
})
