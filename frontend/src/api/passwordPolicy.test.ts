import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  DEFAULT_PASSWORD_POLICY,
  getPasswordPolicy,
  passwordPolicyHint,
} from './passwordPolicy'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('passwordPolicyHint', () => {
  it('renders the historical default text byte-identically', () => {
    expect(passwordPolicyHint(DEFAULT_PASSWORD_POLICY)).toBe('At least 8 characters.')
  })

  it('lists length and every required class', () => {
    expect(
      passwordPolicyHint({
        minLength: 12,
        maxLength: 128,
        requireCharacterClasses: ['upper', 'digit'],
      }),
    ).toBe('At least 12 characters; 1 uppercase letter; 1 digit.')
  })

  it('passes unknown classes through instead of dropping them', () => {
    expect(
      passwordPolicyHint({ minLength: 8, maxLength: 128, requireCharacterClasses: ['emoji'] }),
    ).toBe('At least 8 characters; 1 emoji character.')
  })
})

describe('getPasswordPolicy', () => {
  function stubFetch(body: unknown, ok = true) {
    const fn = vi.fn(() =>
      Promise.resolve(new Response(JSON.stringify(body), { status: ok ? 200 : 500 })),
    )
    vi.stubGlobal('fetch', fn)
    return fn
  }

  it('accepts a well-formed hub response', async () => {
    stubFetch({ minLength: 12, maxLength: 128, requireCharacterClasses: ['upper'] })
    await expect(getPasswordPolicy()).resolves.toEqual({
      minLength: 12,
      maxLength: 128,
      requireCharacterClasses: ['upper'],
    })
  })

  it('normalizes a missing classes list to empty', async () => {
    stubFetch({ minLength: 10, maxLength: 128 })
    await expect(getPasswordPolicy()).resolves.toEqual({
      minLength: 10,
      maxLength: 128,
      requireCharacterClasses: [],
    })
  })

  // Guards the fallback path: anything that is not a valid policy object (old
  // hub, error page JSON, an array) must reject so the caller can fall back
  // to the default hint instead of rendering "At least undefined characters."
  it.each([
    ['array body', []],
    ['empty object', {}],
    ['string minLength', { minLength: '8', maxLength: 128 }],
    ['null body', null],
  ])('rejects invalid response: %s', async (_name, body) => {
    stubFetch(body)
    await expect(getPasswordPolicy()).rejects.toThrow()
  })

  it('rejects when the endpoint errors', async () => {
    stubFetch({ message: 'boom' }, false)
    await expect(getPasswordPolicy()).rejects.toThrow()
  })
})
