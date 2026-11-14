import { useNavigate } from 'react-router-dom'
import { useAgentImages } from '../../api/agentImages'
import type { AgentImageSummary } from '../../api/agentImages'
import { useListParams } from '../../hooks/useListParams'
import { Button } from '../../primitives/Button'
import { ListPanel } from '../../primitives/ListPanel'
import { Pager } from '../../primitives/Pager'
import { RegisterTable, type Column } from '../../primitives/RegisterTable'
import { EntityLink } from '../../components/EntityLink'
import { agentImagePath } from '../../components/entityPaths'
import { ManagedBadge } from '../../components/ManagedBadge'
import { Titleblock } from '../../layout/Titleblock'

export function ImageList() {
  const navigate = useNavigate()
  const { page, pageSize, pageSizes, setPage, setPageSize } = useListParams()

  const { data, isLoading, error, refetch } = useAgentImages({ page, pageSize })

  const columns: readonly Column<AgentImageSummary>[] = [
    {
      key: 'name',
      header: 'Name',
      cell: (i) => (
        <>
          <EntityLink
            id={i.id}
            label={i.displayName ?? i.id}
            path={agentImagePath}
            className="rowlink"
          />{' '}
          <ManagedBadge managedBy={i.managedBy} />
        </>
      ),
    },
    {
      key: 'image',
      header: 'Image URL',
      cell: (i) => <span className="num">{i.imageURL}</span>,
    },
    {
      key: 'tools',
      header: 'Tools',
      width: 80,
      align: 'right',
      cell: (i) => <span className="num">{i.toolCount ?? 0}</span>,
    },
  ]

  return (
    <>
      <Titleblock
        crumbs={
          <>
            Library / <b>Agent Images</b>
          </>
        }
        title={
          <>
            Agent <em>Images</em>
          </>
        }
        actions={
          <Button variant="primary" onClick={() => navigate('/agent-images/new')}>
            ＋ New Agent Image
          </Button>
        }
      />
      <div className="page">
        <ListPanel
          className="cropped"
          query={{ data, isLoading, error, refetch }}
          errorTitle="Failed to load agent images."
          empty="No agent images yet."
        >
          {data ? (
            <>
              <RegisterTable rows={data.items} columns={columns} rowKey={(i) => i.id} />
              <Pager
                page={page}
                pageSize={pageSize}
                total={data.total}
                pageSizeOptions={pageSizes}
                onPageChange={setPage}
                onPageSizeChange={setPageSize}
              />
            </>
          ) : null}
        </ListPanel>
      </div>
    </>
  )
}
