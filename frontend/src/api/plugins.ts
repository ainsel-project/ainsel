import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { PluginManifest } from '@ainsel/plugin-sdk'
import { request } from './client'
import type { Paginated } from './types'

export interface PluginListItem {
  id: string
  displayName?: string
  version?: string
  ready?: boolean
  disabled?: boolean
  reason?: string
  message?: string
  declaredPermissions?: string[]
  approvedPermissions?: string[]
  groupAccess?: string[]
  signature?: {
    verified: boolean
    signer?: string
    checkedAt?: string
    message?: string
  }
  manifest?: PluginManifest
  imageRepository?: string
  imageDigest?: string
}

export interface PluginCallerContext {
  isAdmin: boolean
  sub: string
  username: string
  groups: { id: string; name: string; role: string }[]
}

// The hub pages the list; 200 is its maximum page size and far above the
// number of plugins a deployment installs.
export async function getPlugins(): Promise<PluginListItem[]> {
  const page = await request<Paginated<PluginListItem>>('/plugins', {
    query: { pageSize: 200 },
  })
  return page.items ?? []
}

export function getPluginContext() {
  return request<PluginCallerContext>('/plugin-context')
}

export function enablePlugin(id: string) {
  return request<void>(`/plugins/${encodeURIComponent(id)}/enable`, { method: 'POST' })
}

export function disablePlugin(id: string) {
  return request<void>(`/plugins/${encodeURIComponent(id)}/disable`, { method: 'POST' })
}

export function approvePluginPermissions(id: string, permissions: string[]) {
  return request<void>(`/plugins/${encodeURIComponent(id)}/approve-permissions`, {
    method: 'POST',
    body: { permissions },
  })
}

export function setPluginGroupAccess(id: string, groups: string[]) {
  return request<void>(`/plugins/${encodeURIComponent(id)}/group-access`, {
    method: 'PUT',
    body: { groups },
  })
}

export function uninstallPlugin(id: string) {
  return request<void>(`/plugins/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export function usePlugins() {
  return useQuery({
    queryKey: ['plugins'],
    queryFn: getPlugins,
  })
}

export function usePluginCallerContext() {
  return useQuery({
    queryKey: ['plugin-context'],
    queryFn: getPluginContext,
  })
}

export function useEnablePlugin() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: enablePlugin,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['plugins'] }),
  })
}

export function useDisablePlugin() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: disablePlugin,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['plugins'] }),
  })
}

export function useApprovePluginPermissions() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, permissions }: { id: string; permissions: string[] }) =>
      approvePluginPermissions(id, permissions),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['plugins'] }),
  })
}

export function useSetPluginGroupAccess() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, groups }: { id: string; groups: string[] }) =>
      setPluginGroupAccess(id, groups),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['plugins'] }),
  })
}

export function useUninstallPlugin() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: uninstallPlugin,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['plugins'] }),
  })
}
