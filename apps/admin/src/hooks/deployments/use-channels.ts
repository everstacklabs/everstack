import { useQuery, useMutation, useQueryClient, type UseQueryResult } from '@tanstack/react-query'
import {
    listChannels,
    createChannel,
    updateChannel,
    deleteChannel,
    testChannel,
    listChannelStatuses,
    listChannelSessions,
    listPlatformChannels,
    type CreateChannelParams,
    type UpdateChannelParams,
    type ChannelConfig,
} from '@/server/channels'
import { useSession } from '@/hooks/auth/use-auth'

function useOrganizationId(): string {
    const { data: session } = useSession()
    return session?.user?.organizations?.[0]?.id ?? ''
}

const CHANNELS_QUERY_KEY = ['channels']
const CHANNEL_STATUSES_KEY = ['channel-statuses']

export function useChannels(params: { platform?: number; agentId?: string; enabled?: boolean } = {}): UseQueryResult<ChannelConfig[]> {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: [...CHANNELS_QUERY_KEY, orgId, params.platform, params.agentId, params.enabled],
        queryFn: async () => {
            const response = await listChannels({
                tenantId: orgId,
                platform: params.platform,
                agentId: params.agentId,
                enabled: params.enabled,
            })
            return response.channels ?? []
        },
        enabled: true,
        refetchOnWindowFocus: false,
        staleTime: 30_000,
    })
}

export function useChannel(id: string) {
    const { data: channels } = useChannels()
    return channels?.find((c) => c.id === id) ?? null
}

export function useCreateChannel() {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params: Omit<CreateChannelParams, 'tenantId'>) =>
            createChannel({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: CHANNELS_QUERY_KEY })
        },
    })
}

export function useUpdateChannel() {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (params: Omit<UpdateChannelParams, 'tenantId'>) =>
            updateChannel({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: CHANNELS_QUERY_KEY })
            queryClient.invalidateQueries({ queryKey: CHANNEL_STATUSES_KEY })
        },
    })
}

export function useDeleteChannel() {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (id: string) => deleteChannel({ id, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: CHANNELS_QUERY_KEY })
            queryClient.invalidateQueries({ queryKey: CHANNEL_STATUSES_KEY })
        },
    })
}

export function useTestChannel() {
    const orgId = useOrganizationId()

    return useMutation({
        mutationFn: (id: string) => testChannel({ id, tenantId: orgId }),
    })
}

export function useChannelStatuses() {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: [...CHANNEL_STATUSES_KEY, orgId],
        queryFn: async () => {
            const response = await listChannelStatuses({ tenantId: orgId })
            return response.statuses ?? []
        },
        refetchInterval: 10_000,
        refetchOnWindowFocus: false,
    })
}

export function usePlatformChannels(channelConfigId: string) {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: ['platform-channels', channelConfigId, orgId],
        queryFn: async () => {
            const response = await listPlatformChannels({
                channelConfigId,
                tenantId: orgId,
            })
            return response.channels ?? []
        },
        enabled: !!channelConfigId && !!orgId,
        staleTime: 60_000,
        retry: false,
        refetchOnWindowFocus: false,
    })
}

export function useChannelSessions(channelId: string) {
    const orgId = useOrganizationId()

    return useQuery({
        queryKey: ['channel-sessions', channelId, orgId],
        queryFn: async () => {
            const response = await listChannelSessions({
                channelId,
                tenantId: orgId,
            })
            return response.sessions ?? []
        },
        enabled: !!channelId,
        refetchOnWindowFocus: false,
        staleTime: 30_000,
    })
}
