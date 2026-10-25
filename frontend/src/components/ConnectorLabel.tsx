import { useConnectorNames } from '../api/connectors'

/**
 * Renders a connector's display name. When the name cannot be resolved (e.g.
 * the connector was deleted), the raw id is shown instead — styled secondary
 * with the full id in `title`, so it reads as a fallback, not the primary
 * reading. Use this everywhere a connector is displayed; keep passing the raw
 * id to logic (routing, filtering) that needs identity, not a label.
 */
export function ConnectorLabel({ id, className }: { id: string; className?: string }) {
  const { connectorNameById } = useConnectorNames()
  const name = connectorNameById.get(id)
  if (name !== undefined) return <span className={className}>{name}</span>
  return (
    <span className={className} title={id} style={{ color: 'var(--ink-4)' }}>
      {id}
    </span>
  )
}
