import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
import { componentPages } from '../../frontend/src/pages/docs/componentPages'

// Guards for the real docs/ tree the site publishes. A broken sidebar entry or
// link is silent once deployed, so it fails here instead.

// Same resolution as repoDocsPlugin in vite.config.ts (tests run from docs-site/).
const docsDir = path.resolve(process.cwd(), '../docs')
const read = (rel: string) => readFileSync(path.join(docsDir, rel), 'utf8')

function sidebarSlugs(): string[] {
  return Array.from(read('_sidebar.md').matchAll(/^\s*[-*]\s*\[[^\]]+\]\(([^)]+)\)/gm)).map((m) =>
    m[1].replace(/^\.?\//, '').replace(/\.md$/, ''),
  )
}

function resolves(slug: string): boolean {
  return slug in componentPages || existsSync(path.join(docsDir, `${slug}.md`))
}

describe('docs sidebar', () => {
  it('opens with the Overview section', () => {
    expect(sidebarSlugs().slice(0, 2)).toEqual(['at-a-glance', 'why-ainsel'])
    expect(read('_sidebar.md').trimStart().startsWith('## Overview')).toBe(true)
  })

  it('every sidebar slug resolves to a page', () => {
    const missing = sidebarSlugs().filter((slug) => !resolves(slug))
    expect(missing).toEqual([])
  })

  it('every relative link in why-ainsel resolves', () => {
    const md = read('why-ainsel.md')
    const targets = Array.from(md.matchAll(/(?<!!)\[[^\]]*\]\(([^)]+)\)/g))
      .map((m) => m[1])
      .filter((href) => !/^(https?:|mailto:|#|\.\.\/)/.test(href))
      .map((href) => href.split('#')[0].replace(/^\.\//, '').replace(/\.md$/, ''))
    expect(targets.length).toBeGreaterThan(0)
    expect(targets.filter((slug) => !resolves(slug))).toEqual([])
  })
})
