import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  listTeamMembers as listTeamMembersApi,
  inviteTeamMember as inviteTeamMemberApi,
  acceptInvitation as acceptInvitationApi,
  removeTeamMember as removeTeamMemberApi,
  revokeInvitation as revokeInvitationApi,
} from '@/server/auth'

export interface TeamMember {
  user: {
    id: string
    email: string
    name?: string
    avatarUrl?: string
    createdAt: string
  }
  role: string
  joinedAt: string
}

export interface Invitation {
  id: string
  email: string
  role: string
  invitedByEmail: string
  expiresAt: string
  createdAt: string
  accepted: boolean
}

export interface ListTeamMembersResponse {
  members: TeamMember[]
  pendingInvitations: Invitation[]
  seatLimit: number
  seatsUsed: number
}

export function useTeamMembers() {
  return useQuery({
    queryKey: ['auth', 'team'],
    queryFn: async (): Promise<ListTeamMembersResponse> => {
      const response = await listTeamMembersApi()
      return {
        members: (response.members ?? []) as unknown as TeamMember[],
        pendingInvitations: (response.pendingInvitations ?? []) as unknown as Invitation[],
        seatLimit: response.seatLimit ?? 0,
        seatsUsed: response.seatsUsed ?? 0,
      }
    },
  })
}

export function useInviteTeamMember() {
  const queryClient = useQueryClient()
  
  return useMutation({
    mutationFn: async ({ email, role }: { email: string; role: number }) => {
      const response = await inviteTeamMemberApi(email, role)
      return response
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['auth', 'team'] })
    },
  })
}

export function useAcceptInvitation() {
  const queryClient = useQueryClient()
  
  return useMutation({
    mutationFn: async ({ token, password, name }: { token: string; password: string; name?: string }) => {
      const response = await acceptInvitationApi(token, password, name)
      return response
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['auth', 'session'] })
    },
  })
}

export function useRemoveTeamMember() {
  const queryClient = useQueryClient()
  
  return useMutation({
    mutationFn: async ({ userId }: { userId: string }) => {
      const response = await removeTeamMemberApi(userId)
      return response
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['auth', 'team'] })
    },
  })
}

export function useRevokeInvitation() {
  const queryClient = useQueryClient()
  
  return useMutation({
    mutationFn: async ({ invitationId }: { invitationId: string }) => {
      const response = await revokeInvitationApi(invitationId)
      return response
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['auth', 'team'] })
    },
  })
}

export function getRoleName(role: string | number): string {
  if (typeof role === 'number') {
    switch (role) {
      case 1: return 'Owner'
      case 2: return 'Admin'
      case 3: return 'Member'
      case 4: return 'Viewer'
      default: return 'Unknown'
    }
  }
  return role.charAt(0).toUpperCase() + role.slice(1)
}

export function getRoleValue(role: string): number {
  switch (role.toLowerCase()) {
    case 'owner': return 1
    case 'admin': return 2
    case 'member': return 3
    case 'viewer': return 4
    default: return 3
  }
}
