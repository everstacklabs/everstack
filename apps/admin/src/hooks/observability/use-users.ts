import { useQuery, type UseQueryResult } from '@tanstack/react-query'
import { listTraceUsers, getTraceUser } from '@/server/observability'
import type {
    ListTraceUsersResponse,
    GetTraceUserResponse,
} from '@everstack/proto/everstack/traces/v1/observability_pb'

const TRACE_USERS_KEY = ['trace-users']

export function useTraceUsers(limit = 50, offset = 0): UseQueryResult<ListTraceUsersResponse, Error> {
    return useQuery({
        queryKey: [...TRACE_USERS_KEY, limit, offset],
        queryFn: () => listTraceUsers({ limit, offset }),
        refetchOnWindowFocus: false,
        staleTime: 30_000,
    })
}

export function useTraceUser(userId: string): UseQueryResult<GetTraceUserResponse, Error> {
    return useQuery({
        queryKey: [...TRACE_USERS_KEY, 'detail', userId],
        queryFn: () => getTraceUser(userId),
        enabled: !!userId,
        refetchOnWindowFocus: false,
        staleTime: 30_000,
    })
}
