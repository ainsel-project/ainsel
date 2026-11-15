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

  // Issue #190: a plugin that stamps its own resources at create needs no
  // console change — the raw stamp is the label.
  it('treats a plugin create stamp as externally managed', () => {
    expect(isManaged('plugin-forgejo')).toBe(true)
  })
})

describe('managedByLabel', () => {
  it('names the manager', () => {
    expect(managedByLabel('gitops')).toBe('managed by gitops')
  })

  it('names a stamping plugin', () => {
    expect(managedByLabel('plugin-forgejo')).toBe('managed by plugin-forgejo')
  })
})
