import { useNavigate } from 'react-router-dom'
import { useConnectors } from '../../api/connectors'
import type { ConnectorResponse } from '../../api/connectors'
import { useListParams } from '../../hooks/useListParams'
import { Button } from '../../primitives/Button'
import { Dot } from '../../primitives/Dot'
import { ListPanel } from '../../primitives/ListPanel'
import { Pager } from '../../primitives/Pager'
import { RegisterTable, type Column } from '../../primitives/RegisterTable'
import { ManagedBadge } from '../../components/ManagedBadge'
import { Titleblock } from '../../layout/Titleblock'

export function ConnectorList() {
  const navigate = useNavigate()
  const { page, pageSize, pageSizes, setPage, setPageSize } = useListParams()

  const { data, isLoading, error, refetch } = useConnectors({ page, pageSize })

  const columns: readonly Column<ConnectorResponse>[] = [
    {
      key: 'name',
      header: 'Name',
      cell: (c) => (
        <>
          <b
            style={{ cursor: 'pointer', color: 'var(--accent)' }}
            onClick={() => navigate(`/connectors/${encodeURIComponent(c.id)}`)}
          >
            {c.name}
          </b>{' '}
          <ManagedBadge managedBy={c.managedBy} />
        </>
      ),
    },
    {
      key: 'signatureHeader',
      header: 'Signature Header',
      cell: (c) => <span className="num">{c.signatureHeader}</span>,
    },
    {
      key: 'endpoint',
      header: 'Endpoint',
      cell: (c) => <span className="num">{c.webhookEndpoint ?? '—'}</span>,
    },
    {
      key: 'status',
      header: 'Status',
      width: 130,
      cell: (c) => {
        if (c.disabled) {
          return (
            <>
              <Dot state="stale" /> Disabled
            </>
          )
        }
        const ok = c.status?.ready === true
        return (
          <>
            <Dot state={ok ? 'ok' : 'err'} /> {ok ? 'Ready' : 'Failing'}
          </>
        )
      },
    },
    {
      key: 'actions',
      header: 'Actions',
      width: 80,
      align: 'right',
      cell: (c) => (
        <Button size="sm" onClick={() => navigate(`/connectors/${encodeURIComponent(c.id)}`)}>
          View
        </Button>
      ),
    },
  ]

  return (
    <>
      <Titleblock
        crumbs={
          <>
            Admin / <b>Connectors</b>
          </>
        }
        title={
          <>
            Connector <em>Catalogue</em>
          </>
        }
        actions={
          <Button variant="primary" onClick={() => navigate('/connectors/new')}>
            ＋ New Connector
          </Button>
        }
      />
      <div className="page">
        <ListPanel
          className="cropped"
          query={{ data, isLoading, error, refetch }}
          errorTitle="Failed to load connectors."
          empty="No connectors yet."
        >
          {data ? (
            <>
              <RegisterTable
                rows={data.items}
                columns={columns}
                rowKey={(c) => c.id}
                rowClassName={(c) =>
                  c.disabled ? 'row-stale' : c.status?.ready === false ? 'row-err' : undefined
                }
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
