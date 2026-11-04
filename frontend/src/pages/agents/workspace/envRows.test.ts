import { describe, expect, it } from 'vitest'
import { envNameErrors, mergeEnv } from './envRows'

describe('mergeEnv', () => {
  it('marks an agent entry that shadows an image entry as an override', () => {
    const rows = mergeEnv(
      [{ name: 'A', value: '1' }],
      [
        { name: 'A', value: '2' },
        { name: 'B', value: '3' },
      ],
    )

    expect(rows).toEqual([
      { name: 'A', kind: 'override', agentValue: '1', imageValue: '2', secret: false },
      { name: 'B', kind: 'image', imageValue: '3', secret: false },
    ])
  })

  it('marks agent-only entries as agent rows, after overrides', () => {
    const rows = mergeEnv(
      [
        { name: 'C', value: 'x' },
        { name: 'A', value: '1' },
      ],
      [{ name: 'A', value: '2' }],
    )

    expect(rows.map((r) => [r.name, r.kind])).toEqual([
      ['A', 'override'],
      ['C', 'agent'],
    ])
  })

  it('returns only agent rows when there is no image env', () => {
    const rows = mergeEnv(
      [
        { name: 'B', value: '1' },
        { name: 'A', value: '2' },
      ],
      [],
    )

    expect(rows.map((r) => [r.name, r.kind])).toEqual([
      ['A', 'agent'],
      ['B', 'agent'],
    ])
  })

  it('masks a secret image entry shadowed by a plain agent entry', () => {
    const [row] = mergeEnv(
      [{ name: 'S', value: 'x' }],
      [{ name: 'S', value: 'hunter2', secret: true }],
    )

    expect(row).toMatchObject({ kind: 'override', secret: true })
  })
})

describe('envNameErrors', () => {
  it('flags empty names and every member of a duplicate pair', () => {
    expect(envNameErrors([{ name: 'A' }, { name: ' ' }, { name: 'A' }])).toEqual([
      'Duplicate name.',
      'Name is required.',
      'Duplicate name.',
    ])
  })

  it('returns null for valid unique names', () => {
    expect(envNameErrors([{ name: 'A' }, { name: 'B' }])).toEqual([null, null])
  })
})
