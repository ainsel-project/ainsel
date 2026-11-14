import { useNavigate } from 'react-router-dom'
import { useAgents } from '../../api/agents'
import { usePersonas, type PersonaSummary } from '../../api/personas'
import { useListParams } from '../../hooks/useListParams'
import { Button } from '../../primitives/Button'
import { ListPanel } from '../../primitives/ListPanel'
import { Pager } from '../../primitives/Pager'
import { RegisterTable, type Column } from '../../primitives/RegisterTable'
import { Tag } from '../../primitives/Tag'
import { ManagedBadge } from '../../components/ManagedBadge'
import { Titleblock } from '../../layout/Titleblock'
import { formatRelative } from '../../utils/time'

export function PersonaList() {
  const navigate = useNavigate()
  const { page, pageSize, pageSizes, setPage, setPageSize } = useListParams()

  const { data, isLoading, error, refetch } = usePersonas({ page, pageSize })
  // Used-by computed from agents — N+M scan; acceptable at scale (<= 100 agents).
  const { data: agents } = useAgents({ pageSize: 100 })
  const usedByCount = (personaId: string) =>
    (agents?.items ?? []).filter((a) => a.persona?.id === personaId).length

  const columns: readonly Column<PersonaSummary>[] = [
    {
      key: 'name',
      header: 'Name',
      cell: (p) => (
        <>
          <b
            style={{ cursor: 'pointer', color: 'var(--accent)' }}
            onClick={() => navigate(`/personas/${encodeURIComponent(p.id)}`)}
          >
            {p.name}
          </b>{' '}
          <ManagedBadge managedBy={p.managedBy} />
        </>
      ),
    },
    {
      key: 'description',
      header: 'Description',
      cell: (p) => <span style={{ color: 'var(--ink-3)' }}>{p.description || '—'}</span>,
    },
    {
      key: 'version',
      header: 'Version',
      width: 90,
      cell: (p) => <Tag>{`v${p.currentVersion}`}</Tag>,
    },
    {
      key: 'updated',
      header: 'Updated',
      width: 130,
      align: 'right',
      cell: (p) => <span className="num">{formatRelative(p.updatedAt)}</span>,
    },
    {
      key: 'usedBy',
      header: 'Used by',
      width: 110,
      align: 'right',
      cell: (p) => <span className="num">{usedByCount(p.id)} agents</span>,
    },
  ]

  return (
    <>
      <Titleblock
        crumbs={
          <>
            Library / <b>Personas</b>
          </>
        }
        title={
          <>
            Persona <em>Library</em>
          </>
        }
        actions={
          <Button variant="primary" onClick={() => navigate('/personas/new')}>
            ＋ New Persona
          </Button>
        }
      />
      <div className="page">
        <p className="label" style={{ margin: '0 0 14px', color: 'var(--ink-3)' }}>
          Shared templates. A persona an agent owns privately is edited in that agent’s Persona
          section and does not appear here.
        </p>
        <ListPanel
          className="cropped"
          query={{ data, isLoading, error, refetch }}
          errorTitle="Failed to load personas."
          empty="No personas yet. Create one to define agent behavior."
        >
          {data ? (
            <>
              <RegisterTable
                rows={data.items}
                columns={columns}
                rowKey={(p) => p.id}
                rowClassName={() => 'clickable'}
              />
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
