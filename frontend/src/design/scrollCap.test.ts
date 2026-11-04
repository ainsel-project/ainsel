import { describe, expect, it } from 'vitest'

import { declarationsFor, readCss } from './cssThemeKit'

// `.scroll-cap` is the design system's "paint this region in a capped window and
// let the reader scroll it" primitive — the payload well and the invocation
// stack on the event detail page both sit inside one.
//
// The cap only does its job while the region can *overflow* it, and a cap that
// is also a layout grid cannot: `auto` rows are free to shrink down to the
// cap's own height, and `.panel` declares `overflow: hidden`, which makes a grid
// item's automatic minimum size zero. The panels are therefore squashed to
// 480px and clip the rest of their own content, while the container reports no
// overflow at all — the transcript becomes unreachable, with no scrollbar to
// pull it back into view.
//
// Vitest runs on jsdom, which has no layout engine, so the collapse cannot be
// measured from a component test. Pin the declaration that prevents it instead.

describe('scroll cap', () => {
  const decls = declarationsFor(readCss('primitives.css'), '.scroll-cap')

  it('caps the region at a fixed height and scrolls it', () => {
    expect(decls['max-height'], '.scroll-cap has no max-height to cap at').toBeTruthy()
    expect(decls['overflow-y'], '.scroll-cap does not scroll vertically').toBe('auto')
  })

  it('sizes grid rows to their content, so a grid-laid-out cap still overflows', () => {
    expect(decls['grid-auto-rows'], '.scroll-cap rows may collapse into the cap').toBe(
      'max-content',
    )
  })
})

// The agent Skills picker opts into `.dual-list-rows--tall` (issue #216) so the
// card grows into the available page height instead of the flat 320px cap. It is
// the same capped-scroll-window idea as `.scroll-cap`, so it must keep a
// viewport-relative cap and a floor that never shrinks below the default.
describe('dual-list tall variant', () => {
  const decls = declarationsFor(readCss('primitives.css'), '.dual-list-rows--tall')

  it('caps the rows at a viewport-relative height', () => {
    const maxHeight = decls['max-height']
    expect(maxHeight, '.dual-list-rows--tall has no max-height').toBeTruthy()
    expect(maxHeight, 'tall rows are not viewport-capped').toContain('100vh')
  })

  it('floors the cap at the default 320px so short viewports are unchanged', () => {
    expect(decls['max-height'], 'tall rows may shrink below the default cap').toContain('320px')
  })
})
