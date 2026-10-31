import { describe, expect, it } from 'vitest'
import { isManaged, managedByLabel } from './managed'

describe('isManaged', () => {
  it('treats empty and "user" as console-managed', () => {
    expect(isManaged(undefined)).toBe(false)
    expect(isManaged('')).toBe(false)
    expect(isManaged('user')).toBe(false)
  })

  it('treats any other value as externally managed (value-agnostic)', () => {
    expect(isManaged('gitops')).toBe(true)
    expect(isManaged('helm')).toBe(true)
    expect(isManaged('plugin')).toBe(true)
    expect(isManaged('some-team-tool')).toBe(true)
  })
})

describe('managedByLabel', () => {
  it('names the manager', () => {
    expect(managedByLabel('gitops')).toBe('managed by gitops')
  })
})
