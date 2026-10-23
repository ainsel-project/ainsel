import { useQuery } from '@tanstack/react-query'
import type { PluginManifest } from '@ainsel/plugin-sdk'
import { request } from './client'

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

export function getPlugins() {
  return request<PluginListItem[]>('/plugins')
}

export function getPluginContext() {
  return request<PluginCallerContext>('/plugin-context')
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
