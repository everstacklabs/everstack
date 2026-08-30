import { useQuery, type UseQueryResult } from '@tanstack/react-query'
import { listTraceSessions, getTraceSession } from '@/server/observability'
import type {
    ListTraceSessionsResponse,
    GetTraceSessionResponse,
} from '@everstack/proto/everstack/traces/v1/observability_pb'

const TRACE_SESSIONS_KEY = ['trace-sessions']

export function useTraceSessions(limit = 50, offset = 0): UseQueryResult<ListTraceSessionsResponse, Error> {
    return useQuery({
        queryKey: [...TRACE_SESSIONS_KEY, limit, offset],
        queryFn: () => listTraceSessions({ limit, offset }),
        refetchOnWindowFocus: false,
        staleTime: 30_000,
    })
}

export function useTraceSession(sessionId: string): UseQueryResult<GetTraceSessionResponse, Error> {
    return useQuery({
        queryKey: [...TRACE_SESSIONS_KEY, 'detail', sessionId],
        queryFn: () => getTraceSession(sessionId),
        enabled: !!sessionId,
        refetchOnWindowFocus: false,
        staleTime: 30_000,
    })
}
