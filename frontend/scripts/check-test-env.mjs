#!/usr/bin/env node
// Determinism guard for the test pool (issue #23).
//
// The suite's speed depends on the jsdom environment being created once per
// worker rather than once per file — building it per file costs roughly half the
// suite's wall time. Nothing in vitest fails when that regresses: the pool is a
// config value, and the summary hint vitest prints for it is reporter output, not
// an exit code (and vitest suppresses it as soon as a pool is named in the
// config, which ours are). So a change that reverts the pool passes silently.
//
// This runs the suite and asserts on what the regression actually looks like in
// the reported breakdown: `environment` eating more than MAX_ENVIRONMENT_SHARE of
// tracked time means the environment is being rebuilt per file.
//
// Usage:
//   node scripts/check-test-env.mjs           # run vitest and check its output
//   node scripts/check-test-env.mjs --stdin   # check vitest output piped in
import { spawnSync } from 'node:child_process'
import { availableParallelism } from 'node:os'
import { existsSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const frontendRoot = join(dirname(fileURLToPath(import.meta.url)), '..')

// Clean runs measure 10–20%; per-file runs measure 49–62%. Anything past this is
// the regression signature, not noise.
const MAX_ENVIRONMENT_SHARE = 35

// Vitest colourises the summary; the figures are only matchable without the codes.
const stripAnsi = (text) => text.replace(/\u001b\[[0-9;]*m/g, '')

// "   Duration  5.78s (tests 58%, environment 18%, setup 12%, …)"
const DURATION = /Duration\s+[\d.]+(?:ms|s)\s*\(([^)]*)\)/g
// "Environment  jsdom was created 134 times · 76.52s total, 50% of tracked time"
// Only emitted when no pool is named in the config — checked as a bonus, not the
// primary signal.
const HINT = /Environment\s+(\S+)\s+was created\s+(\d+) times/g

function vitestOutput() {
  if (process.argv.includes('--stdin')) {
    return { output: stripAnsi(readFileSync(0, 'utf8')), status: 0 }
  }
  // Resolved explicitly so the guard also works when called as
  // `node scripts/check-test-env.mjs`, where node_modules/.bin is not on PATH.
  const localBin = join(
    frontendRoot,
    'node_modules',
    '.bin',
    process.platform === 'win32' ? 'vitest.cmd' : 'vitest',
  )
  const command = existsSync(localBin) ? localBin : 'vitest'
  const run = spawnSync(command, ['run', '--reporter=default'], {
    cwd: frontendRoot,
    encoding: 'utf8',
    shell: process.platform === 'win32',
  })
  if (run.error) {
    console.error(`check-test-env: could not run vitest: ${run.error.message}`)
    process.exit(1)
  }
  return { output: stripAnsi(`${run.stdout ?? ''}${run.stderr ?? ''}`), status: run.status }
}

const { output, status } = vitestOutput()

if (status !== 0) {
  console.error(output)
  console.error(`\ncheck-test-env: vitest exited with code ${status ?? 'null'}.`)
  process.exit(status ?? 1)
}

const failures = []

// Primary signal: environment share of tracked time.
const shares = [...output.matchAll(DURATION)]
  .map(([, phases]) => {
    const match = /environment\s+(\d+)%/.exec(phases)
    return match ? Number(match[1]) : null
  })
  .filter((share) => share !== null)

if (shares.length === 0) {
  failures.push('no Duration breakdown was reported, so environment cost is unmeasured.')
} else {
  const worst = Math.max(...shares)
  if (worst > MAX_ENVIRONMENT_SHARE) {
    failures.push(
      `the test environment consumed ${worst}% of tracked time (limit ${MAX_ENVIRONMENT_SHARE}%). ` +
        'It is being rebuilt per file, which is roughly half the suite wall time.',
    )
  }
}

// Secondary signal: vitest's own hint, whenever it does print.
const hintViolations = [...output.matchAll(HINT)]
  .map(([, environment, count]) => ({ environment, count: Number(count) }))
  .filter(({ count }) => count > availableParallelism())
for (const { environment, count } of hintViolations) {
  failures.push(
    `${environment} was created ${count}× on a machine with ${availableParallelism()} cores.`,
  )
}

if (failures.length > 0) {
  console.error(`check-test-env: ${failures.join('\ncheck-test-env: ')}`)
  console.error('See frontend/vitest.config.ts > projects (issue #23).')
  process.exit(1)
}

console.log(
  `check-test-env: ok (environment ${shares.join('%/')}% of tracked time, ` +
    `limit ${MAX_ENVIRONMENT_SHARE}%)`,
)
