// Slot ids and their props for console extension points.

export type SlotId =
  | 'observability.section'
  | 'invocation.panel'
  | 'agent.panel'
  | 'persona.panel'

export interface SlotProps {
  'observability.section': { range: string }
  // In both agent slots, agentName is the agent's id (its resource
  // metadata.name, e.g. "a-5950e4c3") — not the display name. The display
  // name is not unique and no API resolves it; fetch the agent by id via
  // the hub client to get it.
  'invocation.panel': { invocationId: string; agentName: string }
  'agent.panel': { agentName: string }
  'persona.panel': { personaId: string }
}

export const SLOT_IDS: readonly SlotId[] = [
  'observability.section',
  'invocation.panel',
  'agent.panel',
  'persona.panel',
]
