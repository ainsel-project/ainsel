// Regression guard for issue #285: the `/__BASE_PATH__/` mount placeholder is a
// build-time-only value. If it leaks into dev, `import.meta.env.BASE_URL`
// becomes `/__BASE_PATH__/`, the API base in `api/client.ts` becomes
// `/__BASE_PATH__/api/v1`, and the dev proxy key `/api` never matches — every
// API call then falls through to the SPA fallback and the app dies on a JSON
// parse error. Build output must keep the placeholder, because
// `docker-entrypoint-base-path.sh` sed-replaces it at container startup.
import { describe, expect, it } from 'vitest'
import config from '../vite.config.ts'

type ResolvedConfig = { base?: string }

function resolve(command: 'build' | 'serve'): ResolvedConfig {
  return typeof config === 'function'
    ? (config as (env: { command: string }) => ResolvedConfig)({ command })
    : (config as ResolvedConfig)
}

describe('vite config base path', () => {
  it('keeps the placeholder in build output', () => {
    expect(resolve('build').base).toBe('/__BASE_PATH__/')
  })

  it('serves dev at the root so /api matches the dev proxy', () => {
    expect(resolve('serve').base).toBe('/')
  })
})
