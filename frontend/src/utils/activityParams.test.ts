import { describe, expect, it } from 'vitest'
import {
  hasLegacyActivityParams,
  readActivityParam,
  renameLegacyActivityParams,
} from './activityParams'

// The activity axes are `match` and `runState`; `status` and `outcome` are the
// retired spellings, kept readable for one release (#328).

describe('readActivityParam', () => {
  it('reads the current name', () => {
    expect(readActivityParam(new URLSearchParams('match=matched'), 'match')).toBe('matched')
  })

  it('falls back to the retired name so an old link keeps its filter', () => {
    expect(readActivityParam(new URLSearchParams('status=matched'), 'match')).toBe('matched')
    expect(readActivityParam(new URLSearchParams('outcome=running'), 'runState')).toBe('running')
  })

  it('prefers the current name when both spellings are present', () => {
    expect(readActivityParam(new URLSearchParams('match=unmatched&status=matched'), 'match')).toBe(
      'unmatched',
    )
  })

  it('returns an empty string when neither name is set', () => {
    expect(readActivityParam(new URLSearchParams('connector=c-1'), 'match')).toBe('')
  })
})

describe('renameLegacyActivityParams', () => {
  it('rewrites a retired param onto the current name and drops it', () => {
    const next = new URLSearchParams('status=error&connector=c-1')
    expect(renameLegacyActivityParams(next)).toBe(true)
    expect(next.toString()).toBe('connector=c-1&match=error')
  })

  it('keeps the current value when both spellings are sent', () => {
    const next = new URLSearchParams('match=unmatched&status=matched')
    renameLegacyActivityParams(next)
    expect(next.get('match')).toBe('unmatched')
    expect(next.has('status')).toBe(false)
  })

  it('drops an empty retired param instead of setting an empty filter', () => {
    const next = new URLSearchParams('outcome=')
    renameLegacyActivityParams(next)
    expect(next.has('runState')).toBe(false)
    expect(next.has('outcome')).toBe(false)
  })

  it('reports no change for a URL that already uses the current names', () => {
    const next = new URLSearchParams('match=matched&runState=queued')
    expect(renameLegacyActivityParams(next)).toBe(false)
    expect(next.toString()).toBe('match=matched&runState=queued')
  })

  it('hasLegacyActivityParams detects a retired name including an empty one', () => {
    expect(hasLegacyActivityParams(new URLSearchParams('status=matched'))).toBe(true)
    expect(hasLegacyActivityParams(new URLSearchParams('status='))).toBe(true)
    expect(hasLegacyActivityParams(new URLSearchParams('match=matched'))).toBe(false)
  })
})
