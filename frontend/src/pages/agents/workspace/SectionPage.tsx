import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { Titleblock } from '../../../layout/Titleblock'
import { useWorkspaceAgent } from './workspaceAgent'

type SectionPageProps = {
  title: ReactNode
  actions?: ReactNode
  children?: ReactNode
}

/** Full-page frame every agent section renders: titleblock, then content. */
export function SectionPage({ title, actions, children }: SectionPageProps) {
  const agent = useWorkspaceAgent()
  return (
    <>
      <Titleblock
        crumbs={
          <>
            Fleet / <Link to="/agents">Agents</Link> / <b>{agent.name}</b>
          </>
        }
        title={title}
        actions={actions}
      />
      <div className="page">{children}</div>
    </>
  )
}
