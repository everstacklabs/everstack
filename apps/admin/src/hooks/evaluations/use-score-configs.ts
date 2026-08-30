import { useQuery, useMutation, useQueryClient, type UseQueryResult, type UseMutationResult } from '@tanstack/react-query'
import {
    listScoreConfigs,
    createScoreConfig,
    updateScoreConfig,
    deleteScoreConfig,
    listBuiltinMetrics,
    type CreateScoreConfigParams,
    type UpdateScoreConfigParams,
    type ScoreConfig,
    type BuiltinMetric,
} from '@/server/datasets'
import { useSession } from '@/hooks/auth'

const SCORE_CONFIGS_KEY = ['score-configs']

function useOrganizationId(): string {
    const { data: session } = useSession()
    return session?.user?.organizations?.[0]?.id ?? ''
}

export function useScoreConfigs(): UseQueryResult<ScoreConfig[], Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...SCORE_CONFIGS_KEY, orgId],
        queryFn: async () => {
            const response = await listScoreConfigs({ tenantId: orgId })
            return response.scoreConfigs ?? []
        },
        enabled: !!orgId,
        refetchOnWindowFocus: false,
    })
}

export function useCreateScoreConfig(): UseMutationResult<unknown, Error, Omit<CreateScoreConfigParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => createScoreConfig({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: SCORE_CONFIGS_KEY, refetchType: 'all' })
        },
    })
}

export function useUpdateScoreConfig(): UseMutationResult<unknown, Error, Omit<UpdateScoreConfigParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => updateScoreConfig({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: SCORE_CONFIGS_KEY, refetchType: 'all' })
        },
    })
}

const EMPTY_METRICS: BuiltinMetric[] = []

export function useBuiltinMetrics(): UseQueryResult<BuiltinMetric[], Error> {
    return useQuery({
        queryKey: ['builtin-metrics'],
        queryFn: async () => {
            const response = await listBuiltinMetrics()
            return response.metrics ?? EMPTY_METRICS
        },
        staleTime: Infinity,
    })
}

export function useDeleteScoreConfig(): UseMutationResult<unknown, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (id: string) => deleteScoreConfig(id, orgId),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: SCORE_CONFIGS_KEY, refetchType: 'all' })
        },
    })
}
