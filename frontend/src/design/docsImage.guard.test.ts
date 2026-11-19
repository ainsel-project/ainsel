import { describe, expect, it } from 'vitest'

import { declarationsFor, parseRules, readCss, stripComments, type CssRule } from './cssThemeKit'

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
//
// The fix is split across two stylesheets on purpose. `.md-body` is the shared
// markdown class (`Markdown.tsx` renders it in chat, skills and persona pages
// too), so primitives.css carries only the fit every markdown surface wants.
// The block layout, the vertical rhythm and the rule border are a property of a
// docs figure, so they live in DocsPage.css under `.docs-content-col .md-body
// img` — applied globally they would lift a small inline image (badge, status
// glyph, avatar) out of a chat line and frame it as a block.

const shared = readCss('primitives.css')
const docs = readCss('../pages/docs/DocsPage.css')

/**
 * Top-level rules of a page stylesheet. `parseRules` refuses files that contain
 * a conditional at-rule rather than guessing at them, and DocsPage.css has a
 * `@media` block, so the at-rule bodies are peeled off here and re-parsed with
 * `parseRules` — a rule inside `@media` still lands in the result instead of
 * being skipped quietly.
 */
function pageRules(css: string): CssRule[] {
  const source = stripComments(css)
  const rules: CssRule[] = []
  const atRuleBodies: string[] = []
  let i = 0
  for (;;) {
    const open = source.indexOf('{', i)
    if (open === -1) break
    let depth = 1
    let close = open + 1
    while (close < source.length && depth > 0) {
      if (source[close] === '{') depth += 1
      else if (source[close] === '}') depth -= 1
      close += 1
    }
    const head = source.slice(i, open).trim()
    const body = source.slice(open + 1, close - 1)
    if (head.startsWith('@')) {
      atRuleBodies.push(body)
    } else if (head) {
      const declarations: Record<string, string> = {}
      for (const decl of body.split(';')) {
        const [prop, ...value] = decl.split(':')
        if (!prop || value.length === 0) continue
        declarations[prop.trim()] = value.join(':').trim()
      }
      rules.push({ selectors: head.split(',').map((s) => s.trim()), declarations })
    }
    i = close
  }
  return [...rules, ...atRuleBodies.flatMap((body) => parseRules(body))]
}

/** Selectors that still target an image inside the markdown body. */
const targetsMdBodyImg = (selector: string) =>
  /\.md-body(?![\w-])[^{]*\bimg(?![\w-])/.test(selector)

describe('markdown body images fit the content column', () => {
  const decls = declarationsFor(shared, '.md-body img')

  it('caps the image width at the column', () => {
    expect(
      decls['max-width'],
      '.md-body img has no max-width — 2400px screenshots overflow the 760px docs column',
    ).toBe('100%')
  })

  it('keeps the aspect ratio while scaling down', () => {
    expect(decls.height, '.md-body img does not scale height with width').toBe('auto')
  })

  // The shared rule must stay a fit rule. Everything that changes how an image
  // sits in a line of text belongs to the docs column, or it restyles inline
  // images on every other markdown surface.
  it('leaves the docs-only treatment out of the shared stylesheet', () => {
    for (const prop of ['display', 'margin', 'margin-top', 'margin-bottom', 'border']) {
      expect(decls[prop], `.md-body img sets "${prop}" — that is docs-only styling`).toBeUndefined()
    }
  })
})

describe('docs screenshots are laid out as figures', () => {
  const rule = pageRules(docs).find((r) => r.selectors.includes('.docs-content-col .md-body img'))

  it('blocks the image with its own rhythm and a rule border', () => {
    expect(rule, 'DocsPage.css has no ".docs-content-col .md-body img" rule').toBeDefined()
    const decls = rule!.declarations
    expect(decls.display, 'docs screenshot is not a block').toBe('block')
    expect(decls.margin, 'docs screenshot lost its margin').toBe('12px 0')
    expect(decls.border, 'docs screenshot lost its border').toContain('var(--rule-soft)')
  })
})

describe('no rule re-widens a markdown body image', () => {
  const sheets: [string, CssRule[]][] = [
    ['primitives.css', parseRules(shared)],
    ['DocsPage.css', pageRules(docs)],
  ]

  for (const [name, rules] of sheets) {
    it(`in ${name}`, () => {
      for (const rule of rules) {
        for (const selector of rule.selectors) {
          if (!targetsMdBodyImg(selector)) continue
          for (const prop of ['max-width', 'width', 'min-width']) {
            const value = rule.declarations[prop]
            if (value === undefined) continue
            expect(
              value,
              `"${selector}" in ${name} sets ${prop}: "${value}" — it undoes the .md-body img fit`,
            ).toBe('100%')
          }
        }
      }
    })
  }
})
