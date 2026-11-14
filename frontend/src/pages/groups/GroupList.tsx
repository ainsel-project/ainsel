import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useGroups, useDeleteGroup } from '../../api/groups'
import type { Group } from '../../api/groups'
import { Button } from '../../primitives/Button'
import { ConfirmModal } from '../../primitives/ConfirmModal'
import { ListPanel } from '../../primitives/ListPanel'
import { RegisterTable, type Column } from '../../primitives/RegisterTable'
import { EntityLink } from '../../components/EntityLink'
import { groupPath } from '../../components/entityPaths'
import { Titleblock } from '../../layout/Titleblock'

export function GroupList() {
  const navigate = useNavigate()
  const { data, isLoading, error, refetch } = useGroups()
  const remove = useDeleteGroup()
  const [confirmId, setConfirmId] = useState<string | null>(null)

  const groupsById = Object.fromEntries((data ?? []).map((g) => [g.id, g]))
  const confirmGroup = confirmId ? groupsById[confirmId] : null

  const onConfirmDelete = async () => {
    if (!confirmId) return
    await remove.mutateAsync(confirmId)
    setConfirmId(null)
  }

  const columns: readonly Column<Group>[] = [
    {
      key: 'name',
      header: 'Name',
      cell: (g) => <EntityLink id={g.id} label={g.name} path={groupPath} className="rowlink" />,
    },
    {
      key: 'description',
      header: 'Description',
      cell: (g) => g.description || '—',
    },
    {
      key: 'actions',
      header: '',
      width: 80,
      align: 'right',
      cell: (g) => (
        <Button
          variant="danger"
          size="sm"
          onClick={(e) => {
            e.stopPropagation()
            setConfirmId(g.id)
          }}
        >
          Delete
        </Button>
      ),
    },
  ]

  return (
    <>
      <Titleblock
        crumbs={
          <>
            Admin / <b>Groups</b>
          </>
        }
        title={
          <>
            Group <em>Registry</em>
          </>
        }
        actions={
          <Button variant="primary" onClick={() => navigate('/groups/new')}>
            ＋ New Group
          </Button>
        }
      />
      <div className="page">
        <ListPanel
          className="cropped"
          query={{ data, isLoading, error, refetch }}
          errorTitle="Failed to load groups."
          empty="No groups yet."
        >
          {data ? <RegisterTable rows={data} columns={columns} rowKey={(g) => g.id} /> : null}
        </ListPanel>
      </div>

      <ConfirmModal
        open={confirmId !== null}
        title="Delete group?"
        body={
          <>
            <b>{confirmGroup?.name ?? confirmId}</b> will be permanently removed.
          </>
        }
        confirmLabel={remove.isPending ? 'Deleting…' : 'Delete'}
        destructive
        onConfirm={onConfirmDelete}
        onCancel={() => setConfirmId(null)}
      />
    </>
  )
}
