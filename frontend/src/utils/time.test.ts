import { describe, expect, it, vi } from 'vitest'
import { formatISO, formatRelative, resolveSinceParam, SINCE_SENTINEL, startOfToday } from './time'

describe('startOfToday', () => {
  it('is local midnight of the current day', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-05-20T12:42:18Z'))
    const s = startOfToday()
    const d = new Date(s)
    expect([d.getHours(), d.getMinutes(), d.getSeconds()]).toEqual([0, 0, 0])
    expect(d.toDateString()).toBe(new Date().toDateString())
    vi.useRealTimers()
  })
})

describe('resolveSinceParam', () => {
  it('resolves the today sentinel to local midnight', () => {
    expect(resolveSinceParam(SINCE_SENTINEL)).toBe(startOfToday())
  })

  it('ignores unknown and missing values', () => {
    expect(resolveSinceParam('yesterday')).toBeUndefined()
    expect(resolveSinceParam('2026-05-20T00:00:00Z')).toBeUndefined()
    expect(resolveSinceParam('')).toBeUndefined()
    expect(resolveSinceParam(undefined)).toBeUndefined()
  })
})

describe('formatRelative', () => {
  const now = new Date('2026-05-20T12:42:18Z').getTime()

  it('returns "just now" for <5 seconds', () => {
    expect(formatRelative(new Date(now - 2_000).toISOString(), now)).toBe('just now')
  })

  it('returns "<n>s" for seconds', () => {
    expect(formatRelative(new Date(now - 12_000).toISOString(), now)).toBe('12s ago')
  })

  it('returns "<n>m" for minutes', () => {
    expect(formatRelative(new Date(now - 5 * 60_000).toISOString(), now)).toBe('5m ago')
  })

  it('returns "<n>h <n>m" precision past 1h', () => {
    expect(formatRelative(new Date(now - 67 * 60_000).toISOString(), now)).toBe('1h 7m')
  })

  it('returns "<n>h <n>m" for hours', () => {
    expect(formatRelative(new Date(now - 3 * 3600_000 - 15 * 60_000).toISOString(), now)).toBe(
      '3h 15m',
    )
  })

  it('returns "<n>d <n>h" for days', () => {
    expect(formatRelative(new Date(now - 2 * 86400_000 - 4 * 3600_000).toISOString(), now)).toBe(
      '2d 4h',
    )
  })

  it('returns "—" for empty input', () => {
    expect(formatRelative(undefined, now)).toBe('—')
    expect(formatRelative(null, now)).toBe('—')
    expect(formatRelative('', now)).toBe('—')
  })

  it('returns "—" for invalid input', () => {
    expect(formatRelative('not a date', now)).toBe('—')
  })

  it('defaults to current time when no nowMs given', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-05-20T12:42:18Z'))
    expect(formatRelative(new Date('2026-05-20T12:42:13Z').toISOString())).toBe('5s ago')
    vi.useRealTimers()
  })
})

describe('formatISO', () => {
  it('formats as YYYY-MM-DD HH:mm:ss UTC', () => {
    expect(formatISO('2026-05-20T12:42:18Z')).toBe('2026-05-20 12:42:18Z')
  })

  it('returns "—" for missing input', () => {
    expect(formatISO(undefined)).toBe('—')
  })
})
