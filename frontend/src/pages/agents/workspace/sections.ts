export type SectionId =
  | 'overview'
  | 'chat'
  | 'persona'
  | 'runtime'
  | 'tools'
  | 'skills'
  | 'triggers'
  | 'channel'
  | `plugin:${string}`

export type SectionGroup = 'top' | 'Configure' | 'Wiring' | 'Plugins'

export type SectionItem = { group: SectionGroup; id: SectionId; label: string }

/** Path to a section of an agent; Overview is the agent's index route. */
export function sectionPath(agentId: string, section: SectionId): string {
  const base = `/agents/${encodeURIComponent(agentId)}`
  return section === 'overview' ? base : `${base}/${section}`
}

const BUILT_IN: SectionItem[] = [
  { group: 'top', id: 'overview', label: 'Overview' },
  { group: 'top', id: 'chat', label: 'Chat' },
  { group: 'Configure', id: 'persona', label: 'Persona' },
  { group: 'Configure', id: 'runtime', label: 'Runtime & env' },
  { group: 'Configure', id: 'tools', label: 'Tools & MCP' },
  { group: 'Configure', id: 'skills', label: 'Skills' },
  { group: 'Wiring', id: 'triggers', label: 'Triggers' },
  { group: 'Wiring', id: 'channel', label: 'Channel' },
]

/** Built-in sections in nav order, followed by one Plugins entry per plugin. */
export function buildSections(plugins: { id: string; label: string }[]): SectionItem[] {
  return [
    ...BUILT_IN,
    ...plugins.map((p) => ({
      group: 'Plugins' as const,
      id: `plugin:${p.id}` as const,
      label: p.label,
    })),
  ]
}

const BUILT_IN_IDS: readonly string[] = BUILT_IN.map((s) => s.id)

/** Parses a URL segment into a section id; null for anything unknown. */
export function parseSection(segment: string): SectionId | null {
  if (BUILT_IN_IDS.includes(segment)) return segment as SectionId
  if (/^plugin:.+/.test(segment)) return segment as SectionId
  return null
}
