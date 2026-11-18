import { describe, expect, it } from 'vitest'
import { componentPages } from '../../frontend/src/pages/docs/componentPages'

// Guards for the real docs/ tree the site publishes. A broken sidebar entry or
// link is silent once deployed, so it fails here instead. The files are read
// through Vite (`?raw` glob) rather than node:fs, which this package's
// tsconfig has no types for.

const docs = import.meta.glob<string>('../../docs/**/*.md', {
  query: '?raw',
  import: 'default',
  eager: true,
})
const read = (rel: string) => docs[`../../docs/${rel}`] ?? ''

function sidebarSlugs(): string[] {
  return Array.from(read('_sidebar.md').matchAll(/^\s*[-*]\s*\[[^\]]+\]\(([^)]+)\)/gm), (m) =>
    m[1].replace(/^\.?\//, '').replace(/\.md$/, ''),
  )
}

function resolves(slug: string): boolean {
  return slug in componentPages || `../../docs/${slug}.md` in docs
}

describe('docs sidebar', () => {
  it('opens with the Overview section', () => {
    expect(sidebarSlugs().slice(0, 2)).toEqual(['at-a-glance', 'why-ainsel'])
    expect(read('_sidebar.md').trimStart().startsWith('## Overview')).toBe(true)
  })

  it('every sidebar slug resolves to a page', () => {
    const slugs = sidebarSlugs()
    expect(slugs.length).toBeGreaterThan(10)
    expect(slugs.filter((slug) => !resolves(slug))).toEqual([])
  })

  it('every relative link in why-ainsel resolves', () => {
    const targets = Array.from(
      read('why-ainsel.md').matchAll(/(?<!!)\[[^\]]*\]\(([^)]+)\)/g),
      (m) => m[1],
    )
      .filter((href) => !/^(https?:|mailto:|#|\.\.\/)/.test(href))
      .map((href) => href.split('#')[0].replace(/^\.\//, '').replace(/\.md$/, ''))
    expect(targets.length).toBeGreaterThan(0)
    expect(targets.filter((slug) => !resolves(slug))).toEqual([])
  })
})
