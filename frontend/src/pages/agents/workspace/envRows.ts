import type { AgentEnvVar } from '../../../api/agents'
import type { EnvVar } from '../../../api/agentImages'

/**
 * image: a runtime-image entry the agent does not touch.
 * override: an agent entry shadowing an image entry of the same name.
 * agent: an agent-only entry.
 */
export type EnvRowKind = 'image' | 'override' | 'agent'

export type EnvRow = {
  name: string
  kind: EnvRowKind
  agentValue?: string
  imageValue?: string
  secret: boolean
}

function classifyAgentEntry(entry: AgentEnvVar, imageEnv: EnvVar[]): EnvRow {
  const image = imageEnv.find((e) => e.name === entry.name)
  if (!image) {
    return { name: entry.name, kind: 'agent', agentValue: entry.value, secret: !!entry.secret }
  }
  return {
    name: entry.name,
    kind: 'override',
    agentValue: entry.value,
    imageValue: image.value,
    secret: !!(entry.secret || image.secret),
  }
}

/** A table row; agent-entry rows carry their index into the agent's env. */
export type TableRow = EnvRow & { agentIndex?: number }

const byName = (a: EnvRow, b: EnvRow) => a.name.localeCompare(b.name)

/**
 * Merges an agent's own env with its image's env into one list: overrides,
 * then agent rows, then image rows, each alphabetically. Image entries the
 * agent overrides appear once, as overrides.
 */
export function mergeEnv(agentEnv: AgentEnvVar[], imageEnv: EnvVar[]): EnvRow[] {
  const agentNames = new Set(agentEnv.map((e) => e.name))
  const agentRows = agentEnv.map((e) => classifyAgentEntry(e, imageEnv))
  const imageRows: EnvRow[] = imageEnv
    .filter((e) => !agentNames.has(e.name))
    .map((e) => ({ name: e.name, kind: 'image', imageValue: e.value, secret: !!e.secret }))

  return [
    ...agentRows.filter((r) => r.kind === 'override').sort(byName),
    ...agentRows.filter((r) => r.kind === 'agent').sort(byName),
    ...imageRows.sort(byName),
  ]
}

/** Per-entry validation message for the agent's env list (null when valid). */
export function envNameErrors(env: { name: string }[]): (string | null)[] {
  return env.map((entry, idx) => {
    const name = entry.name.trim()
    if (name === '') return 'Name is required.'
    if (env.some((other, i) => i !== idx && other.name.trim() === name)) {
      return 'Duplicate name.'
    }
    return null
  })
}

/**
 * Rows for the env table in entry order for agent entries (so typing into a
 * row never moves it), followed by image-only rows. Agent rows carry their
 * index into `agentEnv`.
 */
export function tableRows(agentEnv: AgentEnvVar[], imageEnv: EnvVar[]): TableRow[] {
  const agentRows = agentEnv.map((e, agentIndex) => ({
    ...classifyAgentEntry(e, imageEnv),
    agentIndex,
  }))
  const imageRows = mergeEnv(agentEnv, imageEnv).filter((r) => r.kind === 'image')
  return [...agentRows, ...imageRows]
}
