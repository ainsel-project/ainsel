import { describe, expect, it } from 'vitest'
import { buildSections, sectionPath } from './sections'

describe('sectionPath', () => {
  it('encodes the agent id in the overview path', () => {
    expect(sectionPath('a b', 'overview')).toBe('/agents/a%20b')
  })

  it('appends a non-overview section segment', () => {
    expect(sectionPath('x', 'runtime')).toBe('/agents/x/runtime')
  })
})

describe('buildSections', () => {
  it('lists the built-in sections when there are no plugins', () => {
    expect(buildSections([]).map((s) => s.id)).toEqual([
      'overview',
      'chat',
      'persona',
      'runtime',
      'tools',
      'skills',
      'triggers',
      'channel',
    ])
  })

  it('appends one Plugins entry per plugin', () => {
    const sections = buildSections([{ id: 'forgejo', label: 'Forgejo' }])
    expect(sections[sections.length - 1]).toEqual({
      group: 'Plugins',
      id: 'plugin:forgejo',
      label: 'Forgejo',
    })
  })
})
