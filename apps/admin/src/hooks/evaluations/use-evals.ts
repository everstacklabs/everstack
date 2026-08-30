import { useQuery, useMutation, useQueryClient, type UseQueryResult, type UseMutationResult } from '@tanstack/react-query'
import {
    listEvalRuns,
    getEvalRun,
    createEvalRun,
    cancelEvalRun,
    deleteEvalRun,
    retryEvalRun,
    getEvalRunItems,
    getEvalRunSummary,
    compareEvalRuns,
    listComparisonRows,
    setBaseline,
    listEvalSchedules,
    createEvalSchedule,
    updateEvalSchedule,
    deleteEvalSchedule,
    listSamplingEvalRules,
    createSamplingEvalRule,
    updateSamplingEvalRule,
    deleteSamplingEvalRule,
    runSamplingEvalRuleNow,
    type CreateEvalRunParams,
    type CreateEvalScheduleParams,
    type UpdateEvalScheduleParams,
    type CreateSamplingEvalRuleParams,
    type UpdateSamplingEvalRuleParams,
    type EvalRun,
    type EvalRunItem,
    type EvalSchedule,
    type SamplingEvalRule,
} from '@/server/evals'
import type { GetEvalRunSummaryResponse, CompareEvalRunsResponse, ListComparisonRowsResponse } from '@everstack/proto/everstack/datasets/v1/datasets_pb'
import { Code, ConnectError } from '@everstack/client'
import { useSession } from '@/hooks/auth'

const EVAL_RUNS_KEY = ['eval-runs']
const EVAL_RUN_ITEMS_KEY = ['eval-run-items']
const EVAL_RUN_SUMMARY_KEY = ['eval-run-summary']
const COMPARISON_ROWS_KEY = ['comparison-rows']
const EVAL_SCHEDULES_KEY = ['eval-schedules']
const SAMPLING_RULES_KEY = ['sampling-eval-rules']

function useOrganizationId(): string {
    const { data: session } = useSession()
    return session?.user?.organizations?.[0]?.id ?? ''
}

export function useEvalRuns(datasetId?: string): UseQueryResult<EvalRun[], Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...EVAL_RUNS_KEY, orgId, datasetId],
        queryFn: async () => {
            const response = await listEvalRuns({ tenantId: orgId, datasetId })
            return response.evalRuns ?? []
        },
        enabled: !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 15_000,
        refetchInterval: 30_000,
    })
}

export function useEvalRun(runId: string): UseQueryResult<EvalRun | undefined, Error> {
    const orgId = useOrganizationId()
    const query = useQuery({
        queryKey: [...EVAL_RUNS_KEY, 'detail', orgId, runId],
        queryFn: async () => {
            const response = await getEvalRun(runId, orgId)
            return response.evalRun
        },
        enabled: !!runId && !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 2_000,
        refetchInterval: (query) => {
            const status = (query.state.data as any)?.status?.toLowerCase()
            if (status === 'pending' || status === 'running') return 3_000
            return false
        },
    })
    return query
}

export function useEvalRunItems(runId: string, isActive = false, limit = 100): UseQueryResult<EvalRunItem[], Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...EVAL_RUN_ITEMS_KEY, orgId, runId, limit],
        queryFn: async () => {
            const response = await getEvalRunItems({ tenantId: orgId, runId, limit })
            return response.items ?? []
        },
        enabled: !!runId && !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 2_000,
        refetchInterval: isActive ? 3_000 : false,
    })
}

export function useEvalRunSummary(runId: string, isActive = false): UseQueryResult<GetEvalRunSummaryResponse | undefined, Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...EVAL_RUN_SUMMARY_KEY, orgId, runId],
        queryFn: async () => {
            return await getEvalRunSummary(runId, orgId)
        },
        enabled: !!runId && !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 2_000,
        refetchInterval: isActive ? 5_000 : false,
    })
}

export function useCreateEvalRun(): UseMutationResult<unknown, Error, Omit<CreateEvalRunParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => createEvalRun({ ...params, tenantId: orgId }),
        onSuccess: async (data, variables) => {
            const created = (data as any)?.evalRun
            if (created && orgId) {
                const baseRun = {
                    ...created,
                    datasetId: created.datasetId || variables.datasetId,
                    name: created.name || variables.name,
                    status: created.status || 'pending',
                    createdAt: created.createdAt || new Date().toISOString(),
                }

                const updateList = (key: unknown[]) => {
                    queryClient.setQueryData(key, (existing: any[] | undefined) => {
                        const list = existing ? [...existing] : []
                        if (!list.some((r) => r.id === baseRun.id)) {
                            list.unshift(baseRun)
                        }
                        return list
                    })
                }

                updateList([...EVAL_RUNS_KEY, orgId, undefined])
                updateList([...EVAL_RUNS_KEY, orgId, variables.datasetId])
            }

            await queryClient.invalidateQueries({ queryKey: EVAL_RUNS_KEY })
            setTimeout(() => {
                queryClient.refetchQueries({ queryKey: EVAL_RUNS_KEY })
            }, 750)
        },
    })
}

export function useCancelEvalRun(): UseMutationResult<unknown, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (id: string) => cancelEvalRun(id, orgId),
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: EVAL_RUNS_KEY })
        },
    })
}

export function useDeleteEvalRun(): UseMutationResult<unknown, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (id: string) => deleteEvalRun(id, orgId),
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: EVAL_RUNS_KEY })
        },
    })
}

export function useRetryEvalRun(): UseMutationResult<
    unknown,
    Error,
    { id: string; retryAll: boolean }
> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: ({ id, retryAll }) => retryEvalRun(id, orgId, retryAll),
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: EVAL_RUNS_KEY })
            await queryClient.invalidateQueries({ queryKey: EVAL_RUN_ITEMS_KEY })
            await queryClient.invalidateQueries({ queryKey: EVAL_RUN_SUMMARY_KEY })
        },
    })
}

export function useCompareEvalRuns(
    runIds: string[],
    opts?: { persist?: boolean },
): UseQueryResult<CompareEvalRunsResponse, Error> {
    const orgId = useOrganizationId()
    const persist = opts?.persist ?? false
    // Sort so the cache key is order-independent (A,B and B,A are one fetch).
    // Persisted verdicts are baseline/candidate-symmetric server-side.
    const key = [...runIds].sort().join(',')
    return useQuery({
        queryKey: [...EVAL_RUNS_KEY, 'compare', orgId, key, persist],
        queryFn: async () => {
            if (!persist) return compareEvalRuns({ tenantId: orgId, runIds })
            try {
                return await compareEvalRuns({ tenantId: orgId, runIds, persist: true })
            } catch (err) {
                // Materialization requires both runs terminal. While one is
                // still in flight, fall back to a verdict-only preview (no
                // comparisonId, so no per-row grid) instead of erroring.
                if (err instanceof ConnectError && err.code === Code.FailedPrecondition) {
                    return compareEvalRuns({ tenantId: orgId, runIds, persist: false })
                }
                throw err
            }
        },
        enabled: !!orgId && runIds.length >= 2,
        refetchOnWindowFocus: false,
        staleTime: 5_000,
    })
}

export function useListComparisonRows(
    comparisonId: string,
    opts?: { onlyRegressions?: boolean; limit?: number; offset?: number },
): UseQueryResult<ListComparisonRowsResponse, Error> {
    const orgId = useOrganizationId()
    const onlyRegressions = opts?.onlyRegressions ?? false
    const limit = opts?.limit ?? 50
    const offset = opts?.offset ?? 0
    return useQuery({
        queryKey: [...COMPARISON_ROWS_KEY, orgId, comparisonId, onlyRegressions, limit, offset],
        queryFn: () =>
            listComparisonRows({ tenantId: orgId, comparisonId, onlyRegressions, limit, offset }),
        enabled: !!orgId && !!comparisonId,
        refetchOnWindowFocus: false,
        staleTime: 30_000,
    })
}

// ─── Baseline ────────────────────────────────────────────────────────

export function useSetBaseline(): UseMutationResult<unknown, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (runId: string) => setBaseline(runId, orgId),
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: EVAL_RUNS_KEY })
        },
    })
}

// ─── Eval Schedules ──────────────────────────────────────────────────

export function useEvalSchedules(datasetId?: string): UseQueryResult<EvalSchedule[], Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...EVAL_SCHEDULES_KEY, orgId, datasetId],
        queryFn: async () => {
            const response = await listEvalSchedules({ tenantId: orgId, datasetId })
            return response.schedules ?? []
        },
        enabled: !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 15_000,
        refetchInterval: 60_000,
    })
}

export function useCreateEvalSchedule(): UseMutationResult<unknown, Error, Omit<CreateEvalScheduleParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => createEvalSchedule({ ...params, tenantId: orgId }),
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: EVAL_SCHEDULES_KEY })
        },
    })
}

export function useUpdateEvalSchedule(): UseMutationResult<unknown, Error, Omit<UpdateEvalScheduleParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => updateEvalSchedule({ ...params, tenantId: orgId }),
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: EVAL_SCHEDULES_KEY })
        },
    })
}

export function useDeleteEvalSchedule(): UseMutationResult<unknown, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (id: string) => deleteEvalSchedule(id, orgId),
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: EVAL_SCHEDULES_KEY })
        },
    })
}

// ─── Sampling / Online Eval Rules ────────────────────────────────────

export function useSamplingEvalRules(enabledOnly = false): UseQueryResult<SamplingEvalRule[], Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...SAMPLING_RULES_KEY, orgId, enabledOnly],
        queryFn: async () => {
            const response = await listSamplingEvalRules({ tenantId: orgId, enabledOnly })
            return response.rules ?? []
        },
        enabled: !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 15_000,
        refetchInterval: 60_000,
    })
}

export function useCreateSamplingEvalRule(): UseMutationResult<unknown, Error, Omit<CreateSamplingEvalRuleParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => createSamplingEvalRule({ ...params, tenantId: orgId }),
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: SAMPLING_RULES_KEY })
        },
    })
}

export function useUpdateSamplingEvalRule(): UseMutationResult<unknown, Error, Omit<UpdateSamplingEvalRuleParams, 'tenantId'>> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => updateSamplingEvalRule({ ...params, tenantId: orgId }),
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: SAMPLING_RULES_KEY })
        },
    })
}

export function useDeleteSamplingEvalRule(): UseMutationResult<unknown, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (id: string) => deleteSamplingEvalRule(id, orgId),
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: SAMPLING_RULES_KEY })
        },
    })
}

export function useRunSamplingEvalRuleNow(): UseMutationResult<
    { tracesMatched: number; tracesSampled: number; scoresRecorded: number; error: string },
    Error,
    string
> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: async (id: string) => {
            const res = await runSamplingEvalRuleNow(id, orgId)
            return {
                tracesMatched: Number(res.tracesMatched ?? 0),
                tracesSampled: Number(res.tracesSampled ?? 0),
                scoresRecorded: Number(res.scoresRecorded ?? 0),
                error: res.error ?? '',
            }
        },
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: SAMPLING_RULES_KEY })
        },
    })
}
