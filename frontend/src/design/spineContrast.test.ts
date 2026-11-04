import { describe, expect, it } from 'vitest'

import { THEMES, composite, contrast, luminance, parseColor, tokenColor } from './cssThemeKit'

// The spine used to be declared only in `:root`, so every theme shipped the
// light theme's cool-navy sidebar next to a warm-brown page (issue #204). It
// is themed now, and these assertions are what keep it honest: the sidebar
// stays a dark surface, stays visibly distinct from the page, and every spine
// text tier clears WCAG AA over it in all four themes.
//
// Token-level rather than rule-level on purpose: Spine.css lives outside
// cssThemeKit's src/design/ read path, and every spine text colour is one of
// the --spine-ink* tokens, so the palette fully describes what is painted.
// The always-dark invariant is additionally enforced *through* the spine by
// codeSurfaces.test.ts, because --code-bg aliases --spine.

const WCAG_AA = 4.5

/** The text tiers Spine.css paints: nav labels, meta/index, recessive labels. */
const SPINE_INK_TOKENS = ['--spine-ink', '--spine-ink-2', '--spine-ink-3'] as const

const ALL_THEMES: (string | null)[] = [null, ...THEMES]

function themeName(theme: string | null): string {
  return theme ?? 'light'
}

describe('spine chrome', () => {
  it.each(ALL_THEMES.map((t) => [themeName(t), t] as const))(
    'paints every text tier at WCAG AA over the spine in the %s theme',
    (label, theme) => {
      const bg = tokenColor('--spine', theme)
      for (const token of SPINE_INK_TOKENS) {
        const ink = tokenColor(token, theme)
        const ratio = contrast(composite(ink, bg), bg)
        expect(
          ratio,
          `${token} (${ink}) on --spine (${bg}) fails WCAG AA in the ${label} theme`,
        ).toBeGreaterThanOrEqual(WCAG_AA)
      }
    },
  )

  it.each(ALL_THEMES.map((t) => [themeName(t), t] as const))(
    'keeps the spine a dark surface in the %s theme',
    (label, theme) => {
      // Same floor codeSurfaces.test.ts holds the code wells to — the wells
      // alias the spine, so one palette lift would break both.
      const bg = tokenColor('--spine', theme)
      expect(
        luminance(bg),
        `--spine (${bg}) renders as a light box in the ${label} theme`,
      ).toBeLessThan(0.2)
    },
  )

  it.each(ALL_THEMES.map((t) => [themeName(t), t] as const))(
    'keeps the spine distinct from the page in the %s theme',
    (label, theme) => {
      // A contrast ratio cannot express this: two near-black surfaces read as
      // clearly different while measuring ~1.1:1 (dark's spine vs dark's
      // --paper is 1.09:1). Distinctness is a max-channel delta instead —
      // measured here at 225 light / 18 dark / 19 peat / 20 tallow. The spine
      // is adjacent to the page, so --paper is the only relevant backdrop.
      const spine = parseColor(tokenColor('--spine', theme))
      const paper = parseColor(tokenColor('--paper', theme))
      const delta = Math.max(
        Math.abs(spine.r - paper.r),
        Math.abs(spine.g - paper.g),
        Math.abs(spine.b - paper.b),
      )
      expect(
        delta,
        `--spine and --paper differ by at most ${delta}/255 in the ${label} theme — ` +
          'the sidebar stops reading as chrome',
      ).toBeGreaterThanOrEqual(16)
    },
  )
})
