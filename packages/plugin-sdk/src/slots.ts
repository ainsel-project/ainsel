// Slot ids and their props for console extension points.

export type SlotId =
  | 'observability.section'
  | 'invocation.panel'
  | 'agent.panel'
  | 'persona.panel'

export interface SlotProps {
  'observability.section': { range: string }
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
