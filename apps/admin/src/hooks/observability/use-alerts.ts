import {
    useQuery,
    useMutation,
    useQueryClient,
    type UseQueryResult,
    type UseMutationResult,
} from '@tanstack/react-query'
import {
    listAlertRules,
    listAlertEvents,
    listNotificationTargets,
    getAlertsSummary,
    createAlertRule,
    updateAlertRule,
    deleteAlertRule,
    createNotificationTarget,
    updateNotificationTarget,
    deleteNotificationTarget,
    testNotificationTarget,
    acknowledgeAlert,
    resolveAlert,
    seedBuiltinRules,
    type CreateAlertRuleParams,
    type UpdateAlertRuleParams,
    type CreateNotificationTargetParams,
    type UpdateNotificationTargetParams,
    type AlertRule,
    type AlertEvent,
    type NotificationTarget,
    type AlertsSummary,
    AlertCategory,
    AlertEventStatus,
    NotificationTargetType,
} from '@/server/alerts'
import { useSession } from '@/hooks/auth/use-auth'

function useOrganizationId(): string {
    const { data: session } = useSession()
    return session?.user?.organizations?.[0]?.id ?? ''
}

const ALERT_RULES_KEY = ['alert-rules']
const ALERT_EVENTS_KEY = ['alert-events']
const NOTIFICATION_TARGETS_KEY = ['notification-targets']
const ALERTS_SUMMARY_KEY = ['alerts-summary']

// ─── Queries ─────────────────────────────────────────────────────────

export function useAlertRules(params: {
    category?: AlertCategory
    enabled?: boolean
} = {}): UseQueryResult<AlertRule[], Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...ALERT_RULES_KEY, orgId, params.category, params.enabled],
        queryFn: async () => {
            const response = await listAlertRules({
                tenantId: orgId,
                category: params.category,
                enabled: params.enabled,
                limit: 250,
            })
            return response.rules ?? []
        },
        enabled: !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 30_000,
    })
}

export function useAlertEvents(params: {
    alertRuleId?: string
    status?: AlertEventStatus
} = {}): UseQueryResult<AlertEvent[], Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...ALERT_EVENTS_KEY, orgId, params.alertRuleId, params.status],
        queryFn: async () => {
            const response = await listAlertEvents({
                tenantId: orgId,
                alertRuleId: params.alertRuleId,
                status: params.status,
                limit: 100,
            })
            return response.events ?? []
        },
        enabled: !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 15_000,
        refetchInterval: 15_000,
    })
}

export function useNotificationTargets(params: {
    targetType?: NotificationTargetType
} = {}): UseQueryResult<NotificationTarget[], Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...NOTIFICATION_TARGETS_KEY, orgId, params.targetType],
        queryFn: async () => {
            const response = await listNotificationTargets({
                tenantId: orgId,
                targetType: params.targetType,
                limit: 250,
            })
            return response.targets ?? []
        },
        enabled: !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 30_000,
    })
}

export function useAlertsSummary(): UseQueryResult<AlertsSummary | undefined, Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...ALERTS_SUMMARY_KEY, orgId],
        queryFn: async () => {
            const response = await getAlertsSummary({ tenantId: orgId })
            return response.summary
        },
        enabled: !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 30_000,
        refetchInterval: 30_000,
    })
}

// ─── Mutations ───────────────────────────────────────────────────────

export function useCreateAlertRule(): UseMutationResult<unknown, Error, Omit<CreateAlertRuleParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => createAlertRule({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ALERT_RULES_KEY })
            queryClient.invalidateQueries({ queryKey: ALERTS_SUMMARY_KEY })
        },
    })
}

export function useUpdateAlertRule(): UseMutationResult<unknown, Error, Omit<UpdateAlertRuleParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => updateAlertRule({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ALERT_RULES_KEY })
            queryClient.invalidateQueries({ queryKey: ALERTS_SUMMARY_KEY })
        },
    })
}

export function useDeleteAlertRule(): UseMutationResult<unknown, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (id) => deleteAlertRule({ id, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ALERT_RULES_KEY })
            queryClient.invalidateQueries({ queryKey: ALERTS_SUMMARY_KEY })
        },
    })
}

export function useCreateNotificationTarget(): UseMutationResult<unknown, Error, Omit<CreateNotificationTargetParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => createNotificationTarget({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: NOTIFICATION_TARGETS_KEY })
        },
    })
}

export function useUpdateNotificationTarget(): UseMutationResult<unknown, Error, Omit<UpdateNotificationTargetParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => updateNotificationTarget({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: NOTIFICATION_TARGETS_KEY })
        },
    })
}

export function useDeleteNotificationTarget(): UseMutationResult<unknown, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (id) => deleteNotificationTarget({ id, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: NOTIFICATION_TARGETS_KEY })
        },
    })
}

export function useTestNotificationTarget(): UseMutationResult<{ success: boolean; message: string }, Error, string> {
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: async (id) => {
            const resp = await testNotificationTarget({ id, tenantId: orgId })
            return { success: resp.success, message: resp.message }
        },
    })
}

export function useAcknowledgeAlert(): UseMutationResult<unknown, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (id) => acknowledgeAlert({ id, tenantId: orgId, acknowledgedBy: 'user' }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ALERT_EVENTS_KEY })
            queryClient.invalidateQueries({ queryKey: ALERTS_SUMMARY_KEY })
        },
    })
}

export function useResolveAlert(): UseMutationResult<unknown, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (id) => resolveAlert({ id, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ALERT_EVENTS_KEY })
            queryClient.invalidateQueries({ queryKey: ALERTS_SUMMARY_KEY })
        },
    })
}

export function useSeedBuiltinRules(): UseMutationResult<{ seededCount: number }, Error, void> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: async () => {
            const resp = await seedBuiltinRules({ tenantId: orgId })
            return { seededCount: resp.seededCount }
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ALERT_RULES_KEY })
            queryClient.invalidateQueries({ queryKey: ALERTS_SUMMARY_KEY })
        },
    })
}
