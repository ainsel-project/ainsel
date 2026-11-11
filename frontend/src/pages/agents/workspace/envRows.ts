import type { AgentEnvVar } from '../../../api/agents'
import type { EnvVar } from '../../../api/agentImages'

/**
 * image: a runtime-image entry the agent does not touch.
 * override: an agent entry shadowing an image entry of the same name.
 * agent: an agent-only entry.
 */
type EnvRowKind = 'image' | 'override' | 'agent'

export type EnvRow = {
  name: string
  kind: EnvRowKind
  agentValue?: string
  imageValue?: string
  secret: boolean
}

/** A table row; agent-entry rows carry their index into the agent's env. */
export type TableRow = EnvRow & { agentIndex?: number }

/** An agent entry in the editor. The id survives renames, so the table can keep a row's place. */
export type DraftEnvVar = AgentEnvVar & { id: number }

let lastDraftId = 0
export const newDraftId = () => ++lastDraftId

export function withDraftIds(env: AgentEnvVar[]): DraftEnvVar[] {
  return env.map((e) => ({ ...e, id: newDraftId() }))
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
 * The agent-entry rows of mergeEnv, each paired with its index into `entries`.
 * mergeEnv returns rows, not entries, so this maps them back by name; equal
 * names take entries in their stored order.
 */
function agentRowsInOrder(
  entries: AgentEnvVar[],
  imageEnv: EnvVar[],
): { row: EnvRow; index: number }[] {
  const unused = entries.map((_, i) => i)
  return mergeEnv(entries, imageEnv)
    .filter((row) => row.kind !== 'image')
    .map((row) => {
      const [index] = unused.splice(
        unused.findIndex((i) => entries[i].name === row.name),
        1,
      )
      return { row, index }
    })
}

/**
 * Table rows for the editor. Agent entries follow mergeEnv's order for the
 * saved env, so renames and new rows never move a row while editing; entries
 * added since the save sort after the saved ones, in entry order. Image-only
 * rows come last, in mergeEnv order.
 */
export function tableRows(
  agentEnv: DraftEnvVar[],
  savedEnv: DraftEnvVar[],
  imageEnv: EnvVar[],
): TableRow[] {
  const savedRank = new Map<number, number>()
  agentRowsInOrder(savedEnv, imageEnv).forEach(({ index }, pos) =>
    savedRank.set(savedEnv[index].id, pos),
  )
  const rankOf = (index: number) => savedRank.get(agentEnv[index].id) ?? Infinity

  const agentRows: TableRow[] = agentRowsInOrder(agentEnv, imageEnv)
    .map(({ row, index }) => ({ ...row, agentIndex: index }))
    .sort((a, b) => {
      const ra = rankOf(a.agentIndex ?? -1)
      const rb = rankOf(b.agentIndex ?? -1)
      if (ra !== rb) return ra < rb ? -1 : 1
      return (a.agentIndex ?? -1) - (b.agentIndex ?? -1)
    })
  const imageRows = mergeEnv(agentEnv, imageEnv).filter((r) => r.kind === 'image')
  return [...agentRows, ...imageRows]
}
