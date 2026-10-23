import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import React from 'react'
import { installPluginHost } from './host'
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync } from 'fs'
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
  let outDir: string

  beforeEach(() => {
    // Inside the project: vitest only loads modules from under its root.
    const base = join(cwd(), 'node_modules', '.cache')
    mkdirSync(base, { recursive: true })
    outDir = mkdtempSync(join(base, 'plugin-shared-test-'))
  })

  afterEach(() => {
    rmSync(outDir, { recursive: true, force: true })
    delete window.__AINSEL_SHARED__
  })

  // Imports a generated shim as a real ES module, so a syntax error in the
  // generated code fails here rather than in the browser.
  async function importShim(file: string): Promise<Record<string, unknown>> {
    const target = join(outDir, file.replace(/\.js$/, '.mjs'))
    copyFileSync(join(outDir, file), target)
    return import(/* @vite-ignore */ target)
  }

  it('generates a react shim that re-exports the console React', async () => {
    const { generate } = await import('../../scripts/gen-plugin-shared.mjs')
    await generate(outDir)
    installPluginHost()

    const shim = await importShim('react.js')
    expect(shim.useState).toBe(React.useState)
    expect(shim.default).toBe(React)
    expect(readFileSync(join(outDir, 'react.js'), 'utf-8')).not.toContain('__esModule')
  })

  it('generates an sdk shim exporting every name in exports.json', async () => {
    const { generate } = await import('../../scripts/gen-plugin-shared.mjs')
    await generate(outDir)
    installPluginHost()

    const exportsJson: string[] = JSON.parse(
      readFileSync(join(projectRoot, 'packages', 'plugin-sdk', 'exports.json'), 'utf-8'),
    )
    const shim = await importShim('ainsel-plugin-sdk.js')
    for (const name of exportsJson) {
      expect(shim[name], name).toBeDefined()
    }
    expect(shim.PLUGIN_API_VERSION).toBe('1.1')
  })
})
