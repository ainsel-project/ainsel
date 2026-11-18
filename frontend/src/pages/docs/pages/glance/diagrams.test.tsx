import { readFileSync } from 'node:fs'
import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import {
  ChooseDiagram,
  GrowDiagram,
  LoopDiagram,
  ManageDiagram,
  ProblemDiagram,
  SolvedDiagram,
  ThreeJobsDiagram,
} from './diagrams'

const DIAGRAMS = {
  ProblemDiagram,
  LoopDiagram,
  ThreeJobsDiagram,
  ChooseDiagram,
  SolvedDiagram,
  ManageDiagram,
  GrowDiagram,
}

function svgsOf(Diagram: () => React.JSX.Element) {
  const { container } = render(<Diagram />)
  return Array.from(container.querySelectorAll('svg'))
}

describe('glance diagrams', () => {
  it.each(Object.entries(DIAGRAMS))('%s is an accessible image', (_, Diagram) => {
    const svgs = svgsOf(Diagram)
    expect(svgs.length).toBeGreaterThan(0)
    for (const svg of svgs) {
      expect(svg).toHaveAttribute('role', 'img')
      const labelId = svg.getAttribute('aria-labelledby') ?? ''
      const title = labelId ? svg.querySelector(`[id="${labelId}"]`) : null
      expect(title?.tagName.toLowerCase()).toBe('title')
      expect(title?.textContent?.trim()).not.toBe('')
    }
  })

  it.each(Object.entries(DIAGRAMS))('%s scales with its container', (_, Diagram) => {
    for (const svg of svgsOf(Diagram)) {
      expect(svg.getAttribute('viewBox')).toMatch(/^0 0 \d+ \d+$/)
      expect(svg.hasAttribute('width')).toBe(false)
      expect(svg.hasAttribute('height')).toBe(false)
    }
  })

  it.each(Object.entries(DIAGRAMS))('%s takes colours from CSS, not attributes', (_, Diagram) => {
    for (const svg of svgsOf(Diagram)) {
      for (const el of Array.from(svg.querySelectorAll('[fill], [stroke]'))) {
        for (const attr of ['fill', 'stroke']) {
          const value = el.getAttribute(attr)
          if (value !== null) expect(['none', 'currentColor']).toContain(value)
        }
      }
    }
  })

  it('source files use design tokens only', () => {
    const literalColour = /#[0-9a-f]{3,8}\b|rgba?\(|hsla?\(/i
    for (const file of ['./diagrams.tsx', '../AtAGlance.css', '../AtAGlance.tsx']) {
      const source = readFileSync(new URL(file, import.meta.url), 'utf8')
      expect(source, file).not.toMatch(literalColour)
    }
  })
})
