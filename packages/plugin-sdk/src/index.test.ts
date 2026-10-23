import { describe, it, expect } from 'vitest'
import * as sdkModule from './index'
import exportsList from '../exports.json'

describe('index exports', () => {
  it('exports match the exports.json list', async () => {
    const actualExports = Object.keys(sdkModule).sort()
    expect(actualExports).toEqual(exportsList)
  })
})
