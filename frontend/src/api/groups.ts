import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { expectArray, expectItems, request } from './client'
import type { HubUser } from './users'

export type GroupRole = 'reader' | 'writer' | 'owner'

export type Group = {
  id: string
  name: string
  description: string
  createdAt: string
  updatedAt: string
}

export type GroupMember = {
  user: HubUser
  role: GroupRole
}

export type GroupDetail = {
  group: Group
  members: GroupMember[]
}

export type GroupResourceType =
  | 'agent'
  | 'agent-image'
  | 'connector'
  | 'persona'
  | 'skill'
  | 'channel'
  | 'mcp-server'
  | 'trigger'
  | 'cron-trigger'

export type GroupResourceItem = {
  type: GroupResourceType
  id: string
  name: string
  public: boolean
  agentRef?: string
}

export type GroupResources = {
  items: GroupResourceItem[]
}

// The hub has answered these list endpoints with a paginated envelope before
// (issue #283); validate the bare-array shape here so a wrong shape surfaces
// as an ApiError instead of a render-time TypeError in the route.
export async function listGroups(): Promise<Group[]> {
  return expectArray<Group>(await request<unknown>('/groups'), 'GET /groups')
}

export function getGroup(id: string) {
  return request<GroupDetail>(`/groups/${encodeURIComponent(id)}`)
}

export async function getGroupResources(id: string): Promise<GroupResources> {
  const env = expectItems<GroupResourceItem>(
    await request<unknown>(`/groups/${encodeURIComponent(id)}/resources`),
    'GET /groups/:id/resources',
  )
  return { items: env.items }
}

export function createGroup(body: { name: string; description?: string }) {
  return request<Group>('/groups', { method: 'POST', body })
}

export function updateGroup(id: string, body: { name: string; description: string }) {
  return request<Group>(`/groups/${encodeURIComponent(id)}`, { method: 'PATCH', body })
}

export function deleteGroup(id: string) {
  return request<void>(`/groups/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export function addGroupMembers(groupId: string, userIds: string[], role: GroupRole) {
  return request<void>(`/groups/${encodeURIComponent(groupId)}/members`, {
    method: 'POST',
    body: { userIds, role },
  })
}

export function removeGroupMember(groupId: string, userId: string) {
  return request<void>(
    `/groups/${encodeURIComponent(groupId)}/members/${encodeURIComponent(userId)}`,
    { method: 'DELETE' },
  )
}

export function useGroups() {
  return useQuery({ queryKey: ['groups'], queryFn: listGroups })
}

export function useGroup(id: string | undefined) {
  return useQuery({
    queryKey: ['groups', id],
    queryFn: () => getGroup(id!),
    enabled: id !== undefined,
  })
}

// Lazy by design: only GroupDetail mounts this, so the fan-out across the
// per-type stores never runs for the group list page.
export function useGroupResources(id: string | undefined) {
  return useQuery({
    queryKey: ['groups', id, 'resources'],
    queryFn: () => getGroupResources(id!),
    enabled: id !== undefined,
  })
}

export function useCreateGroup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: createGroup,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['groups'] }),
  })
}

export function useUpdateGroup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: { name: string; description: string } }) =>
      updateGroup(id, body),
    onSuccess: (_data, { id }) => {
      qc.invalidateQueries({ queryKey: ['groups'] })
      qc.invalidateQueries({ queryKey: ['groups', id] })
    },
  })
}

export function useDeleteGroup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: deleteGroup,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['groups'] }),
  })
}

export function useAddGroupMembers() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      groupId,
      userIds,
      role,
    }: {
      groupId: string
      userIds: string[]
      role: GroupRole
    }) => addGroupMembers(groupId, userIds, role),
    onSuccess: (_data, { groupId }) => {
      qc.invalidateQueries({ queryKey: ['groups', groupId] })
    },
  })
}

export function useRemoveGroupMember() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ groupId, userId }: { groupId: string; userId: string }) =>
      removeGroupMember(groupId, userId),
    onSuccess: (_data, { groupId }) => {
      qc.invalidateQueries({ queryKey: ['groups', groupId] })
    },
  })
}
