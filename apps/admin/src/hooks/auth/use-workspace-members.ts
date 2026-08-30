import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  listWorkspaceMembers as listWorkspaceMembersApi,
  addWorkspaceMember as addWorkspaceMemberApi,
  updateWorkspaceMemberRole as updateWorkspaceMemberRoleApi,
  removeWorkspaceMember as removeWorkspaceMemberApi,
  listAvailableWorkspaceMembers as listAvailableWorkspaceMembersApi,
  listWorkspaces as listWorkspacesApi,
} from '@/server/organizations'
import { useSession } from '@/hooks/auth'

export interface WorkspaceMemberInfo {
  id: string
  workspaceId: string
  userId: string
  role: number
  createdAt: string
  email: string
  name?: string
  avatarUrl?: string
  accessSource: string // "explicit" | "implicit"
  orgRole: number
}

/**
 * Resolves the workspace ID for the current session's organization.
 * In cloud mode, each tenant maps to exactly one workspace.
 */
export function useCurrentWorkspace() {
  const { data: session } = useSession()
  const orgId = session?.user?.organizations?.[0]?.id

  return useQuery({
    queryKey: ['workspace', 'current', orgId],
    queryFn: async () => {
      if (!orgId) throw new Error('No organization found')
      const response = await listWorkspacesApi(orgId)
      const workspaces = response.workspaces ?? []
      if (workspaces.length === 0) return null
      return workspaces[0]
    },
    enabled: !!orgId,
    staleTime: Infinity,
  })
}

export function useWorkspaceMembers(workspaceId: string | undefined) {
  return useQuery({
    queryKey: ['workspace', workspaceId, 'members'],
    queryFn: async () => {
      if (!workspaceId) throw new Error('No workspace ID')
      const response = await listWorkspaceMembersApi(workspaceId)
      return (response.members ?? []) as unknown as WorkspaceMemberInfo[]
    },
    enabled: !!workspaceId,
  })
}

export function useAddWorkspaceMember(workspaceId: string | undefined) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async ({ userId, role }: { userId: string; role: number }) => {
      if (!workspaceId) throw new Error('No workspace ID')
      return addWorkspaceMemberApi(workspaceId, userId, role)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workspace', workspaceId, 'members'] })
      queryClient.invalidateQueries({ queryKey: ['workspace', workspaceId, 'available'] })
    },
  })
}

export function useUpdateWorkspaceMemberRole(workspaceId: string | undefined) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async ({ userId, role }: { userId: string; role: number }) => {
      if (!workspaceId) throw new Error('No workspace ID')
      return updateWorkspaceMemberRoleApi(workspaceId, userId, role)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workspace', workspaceId, 'members'] })
    },
  })
}

export function useRemoveWorkspaceMember(workspaceId: string | undefined) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async ({ userId }: { userId: string }) => {
      if (!workspaceId) throw new Error('No workspace ID')
      return removeWorkspaceMemberApi(workspaceId, userId)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workspace', workspaceId, 'members'] })
      queryClient.invalidateQueries({ queryKey: ['workspace', workspaceId, 'available'] })
    },
  })
}

export function useAvailableWorkspaceMembers(workspaceId: string | undefined) {
  return useQuery({
    queryKey: ['workspace', workspaceId, 'available'],
    queryFn: async () => {
      if (!workspaceId) throw new Error('No workspace ID')
      const response = await listAvailableWorkspaceMembersApi(workspaceId)
      return response.members ?? []
    },
    enabled: !!workspaceId,
  })
}

export function getWsRoleName(role: number): string {
  switch (role) {
    case 1: return 'Admin'
    case 2: return 'Member'
    case 3: return 'Viewer'
    default: return 'Unknown'
  }
}

export function getWsRoleValue(role: string): number {
  switch (role.toLowerCase()) {
    case 'admin': return 1
    case 'member': return 2
    case 'viewer': return 3
    default: return 2
  }
}
