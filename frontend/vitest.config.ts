import { defaultExclude, defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

// Specs that replace `window.location` wholesale.
//
// Under `pool: 'vmThreads'` the environment is jsdom's own VM context, where
// `location` is a non-configurable accessor: both `Object.defineProperty` and
// `vi.stubGlobal` throw "Cannot redefine property: location" before the first
// assertion. Under `forks` (and `threads`) vitest's jsdom environment copies the
// window keys onto the worker global as *configurable* accessors, so the swap
// these specs rely on is allowed.
//
// Those two files stay on `forks`; the rest of the suite shares one jsdom per
// worker. The cost is two fork workers (~0.6 s each) against the ~13 s saved by
// not rebuilding the DOM 134 times. See issue #23.
const LOCATION_SPECS = ['src/api/client.test.ts', 'src/pages/login/Login.test.tsx']

export default defineConfig({
  plugins: [react()],
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    // A spec whose async utils give up must still report the failed assertion.
    // With the 3 s async budget in `src/test/setup.ts`, a spec with several
    // waits can outlast the 5 s default and would be reported as a bare test
    // timeout, which hides the actual element error.
    testTimeout: 10000,
    projects: [
      {
        extends: true,
        test: {
          name: 'location',
          pool: 'forks',
          include: LOCATION_SPECS,
        },
      },
      {
        // One jsdom per worker thread instead of one per test file. Under the
        // default `forks` pool every file gets its own process and rebuilds the
        // whole DOM (~600 ms each), which vitest reports as half the suite's
        // tracked time. Files keep their own module registry, so module-level
        // state (`authToken` in `src/api/client.ts`, `diagramSeq` in
        // ChannelDetailPage) is still per-file — unlike `isolate: false`, which
        // would share it and trade a visible flake for an invisible one.
        extends: true,
        test: {
          name: 'dom',
          pool: 'vmThreads',
          include: ['**/*.{test,spec}.?(c|m)[jt]s?(x)'],
          exclude: [...defaultExclude, ...LOCATION_SPECS],
        },
      },
    ],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'html', 'lcov'],
      thresholds: {
        lines: 70,
        statements: 70,
        functions: 67,
        branches: 60,
      },
    },
  },
})
