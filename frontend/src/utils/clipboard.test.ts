import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { copy } from './clipboard'

describe('clipboard.copy', () => {
  let writeText: ReturnType<typeof vi.fn>

  beforeEach(() => {
    writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('writes the given text', async () => {
    const ok = await copy('hello')
    expect(writeText).toHaveBeenCalledWith('hello')
    expect(ok).toBe(true)
  })

  it('returns false when the API throws', async () => {
    writeText.mockRejectedValueOnce(new Error('denied'))
    const ok = await copy('x')
    expect(ok).toBe(false)
  })
})
