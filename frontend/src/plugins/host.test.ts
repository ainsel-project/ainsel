import { describe, it, expect, beforeEach } from 'vitest'
import { installPluginHost } from './host'
import { readFileSync } from 'fs'
import { join } from 'path'
import { cwd } from 'process'

const projectRoot = join(cwd(), '..')

describe('installPluginHost', () => {
  beforeEach(() => {
    delete window.__AINSEL_SHARED__
  })

  it('sets up window.__AINSEL_SHARED__ with exactly five keys', () => {
    installPluginHost()
    const shared = window.__AINSEL_SHARED__
    expect(shared).toBeDefined()
    const keys = Object.keys(shared!).sort()
    expect(keys).toEqual([
      '@ainsel/plugin-sdk',
      'react',
      'react-dom',
      'react-dom/client',
      'react/jsx-runtime',
    ])
  })

  it('exposes PLUGIN_API_VERSION as 1.1 in the SDK', () => {
    installPluginHost()
    const shared = window.__AINSEL_SHARED__
    const sdk = shared!['@ainsel/plugin-sdk'] as Record<string, unknown>
    expect((sdk as Record<string, string>).PLUGIN_API_VERSION).toBe('1.1')
  })

  it('is idempotent - calling twice does not throw', () => {
    expect(() => {
      installPluginHost()
      installPluginHost()
    }).not.toThrow()
  })

  it('registers the host primitives with the SDK', () => {
    installPluginHost()
    const shared = window.__AINSEL_SHARED__
    const sdk = shared!['@ainsel/plugin-sdk'] as Record<string, unknown>

    // Verify the Button primitive is exposed
    const Button = sdk.Button
    expect(typeof Button).toBe('function')
  })
})

describe('generator', () => {
  it('generates react.js with useState in the export list', async () => {
    // @ts-expect-error ESM .mjs files do not have declaration files; type checked at runtime
    const { generate } = await import('../../scripts/gen-plugin-shared.mjs')
    const tempDir = join(projectRoot, 'temp-test-dir-react')
    await generate(tempDir)
    const reactJs = readFileSync(join(tempDir, 'react.js'), 'utf-8')
    expect(reactJs).toContain('useState')
    expect(reactJs).toContain("['react']")
    expect(reactJs).not.toContain('__esModule')
    expect(reactJs).not.toContain("'default'")
  })

  it('generates ainsel-plugin-sdk.js with all exports from exports.json', async () => {
    // @ts-expect-error ESM .mjs files do not have declaration files; type checked at runtime
    const { generate } = await import('../../scripts/gen-plugin-shared.mjs')
    const tempDir = join(projectRoot, 'temp-test-dir-sdk')
    await generate(tempDir)

    const exportsPath = join(projectRoot, 'packages', 'plugin-sdk', 'exports.json')
    const exportsJson = JSON.parse(readFileSync(exportsPath, 'utf-8'))

    const sdkJs = readFileSync(join(tempDir, 'ainsel-plugin-sdk.js'), 'utf-8')
    for (const exportName of exportsJson) {
      expect(sdkJs).toContain(`'${exportName}'`)
    }
  })
})
