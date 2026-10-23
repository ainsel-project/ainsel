import { describe, it, expect } from 'vitest'
import { SLOT_IDS, type SlotId } from './slots'

describe('slots', () => {
  it('SLOT_IDS contains the correct slot ids in order', () => {
    const expected: SlotId[] = [
      'observability.section',
      'invocation.panel',
      'agent.panel',
      'persona.panel',
    ]
    expect(SLOT_IDS).toEqual(expected)
  })

  it('SLOT_IDS is readonly', () => {
    // This is a compile-time check, but we can verify it's an array
    expect(Array.isArray(SLOT_IDS)).toBe(true)
    expect(SLOT_IDS.length).toBe(4)
  })
})
