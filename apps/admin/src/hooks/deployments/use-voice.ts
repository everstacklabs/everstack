import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import type { UseQueryResult } from '@tanstack/react-query'
import {
    listVoiceCloneProfiles,
    createVoiceCloneProfile,
    updateVoiceCloneProfile,
    deleteVoiceCloneProfile,
    type VoiceCloneProfile,
    type CreateVoiceCloneProfileParams,
    type UpdateVoiceCloneProfileParams,
} from '@/server/voice'
import { useSession } from '@/hooks/auth'

function useOrganizationId(): string {
    const { data: session } = useSession()
    return session?.user?.organizations?.[0]?.id ?? ''
}

const VOICE_PROFILES_QUERY_KEY = ['voice-profiles'] as const

export function useVoiceProfiles(): UseQueryResult<VoiceCloneProfile[], Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...VOICE_PROFILES_QUERY_KEY, orgId],
        queryFn: async () => {
            if (!orgId) return []
            const res = await listVoiceCloneProfiles({ orgId })
            return res.profiles ?? []
        },
        enabled: !!orgId,
        staleTime: 30_000,
        retry: 1,
    })
}

export function useCreateVoiceProfile() {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: async (params: Omit<CreateVoiceCloneProfileParams, 'orgId'>) => {
            if (!orgId) throw new Error('No organization ID')
            return createVoiceCloneProfile({ ...params, orgId })
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: VOICE_PROFILES_QUERY_KEY })
        },
    })
}

export function useUpdateVoiceProfile() {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: async (params: UpdateVoiceCloneProfileParams) => {
            return updateVoiceCloneProfile(params)
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: VOICE_PROFILES_QUERY_KEY })
        },
    })
}

export function useDeleteVoiceProfile() {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: async (id: string) => {
            return deleteVoiceCloneProfile({ id })
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: VOICE_PROFILES_QUERY_KEY })
        },
    })
}
