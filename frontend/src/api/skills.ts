import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { request } from './client'
import type { Paginated } from './types'

export type SkillSummary = {
  id: string
  name: string
  description: string
  tags: string[]
  usedBy: number
  createdAt: string
  updatedAt: string
  /** External author (e.g. 'gitops', 'plugin'); anything but empty/'user'
   *  makes the skill read-only in the console (issue #146). */
  managedBy?: string
}

export type SkillResponse = SkillSummary & {
  body: string
}

export type SkillRequest = {
  id?: string
  name?: string
  groupId?: string
  description?: string
  body?: string
  tags?: string[]
}

export type ListSkillsParams = {
  page?: number
  pageSize?: number
  search?: string
  tags?: string
  orderBy?: string
  orderDir?: string
}

export function listSkills(params: ListSkillsParams = {}) {
  return request<Paginated<SkillSummary>>('/skills', { query: params })
}

export function getSkill(id: string) {
  return request<SkillResponse>(`/skills/${encodeURIComponent(id)}`)
}

export function createSkill(body: SkillRequest) {
  return request<SkillResponse>('/skills', { method: 'POST', body })
}

export function updateSkill(id: string, body: SkillRequest) {
  return request<SkillResponse>(`/skills/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body,
  })
}

export function deleteSkill(id: string) {
  return request<void>(`/skills/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export function useSkills(params: ListSkillsParams = {}) {
  return useQuery({
    queryKey: ['skills', params],
    queryFn: () => listSkills(params),
    placeholderData: keepPreviousData,
  })
}

export function useSkill(id: string | undefined) {
  return useQuery({
    queryKey: ['skills', 'detail', id],
    queryFn: () => getSkill(id!),
    enabled: id !== undefined && id !== '',
  })
}

export function useCreateSkill() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: createSkill,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['skills'] }),
  })
}

export function useUpdateSkill() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: SkillRequest }) => updateSkill(id, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['skills'] }),
  })
}

export function useDeleteSkill() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: deleteSkill,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['skills'] }),
  })
}

// --- Assignment endpoints ---

/** One agent that selects this skill (`agentName` is the Agent CR name — the
 *  same value as the agent's API id). */
export type SkillAssignment = {
  agentName: string
  agentId?: string
}

export function getSkillAssignments(skillId: string) {
  return request<{ items: SkillAssignment[] }>(`/skills/${encodeURIComponent(skillId)}/assignments`)
}

export function assignSkill(skillId: string, agentName: string) {
  return request<void>(
    `/skills/${encodeURIComponent(skillId)}/assignments/${encodeURIComponent(agentName)}`,
    { method: 'PUT' },
  )
}

export function unassignSkill(skillId: string, agentName: string) {
  return request<void>(
    `/skills/${encodeURIComponent(skillId)}/assignments/${encodeURIComponent(agentName)}`,
    { method: 'DELETE' },
  )
}

export function useSkillAssignments(skillId: string | undefined) {
  return useQuery({
    queryKey: ['skills', skillId, 'assignments'],
    queryFn: () => getSkillAssignments(skillId!),
    enabled: skillId !== undefined && skillId !== '',
  })
}

export function useAssignSkill() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ skillId, agentName }: { skillId: string; agentName: string }) =>
      assignSkill(skillId, agentName),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['skills'] }),
  })
}

export function useUnassignSkill() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ skillId, agentName }: { skillId: string; agentName: string }) =>
      unassignSkill(skillId, agentName),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['skills'] }),
  })
}
