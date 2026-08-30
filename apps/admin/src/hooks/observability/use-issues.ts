import {
  useQuery,
  useMutation,
  useQueryClient,
  type UseQueryResult,
} from '@tanstack/react-query'
import { listIssues, getIssue, updateIssueStatus, type ListIssuesParams } from '@/server/issues'
import type {
  ListIssuesResponse,
  GetIssueResponse,
  IssueStatus,
} from '@everstack/proto/everstack/issues/v1/issues_pb'

const ISSUES_KEY = ['issues']

export function useIssues(params: ListIssuesParams): UseQueryResult<ListIssuesResponse, Error> {
  return useQuery({
    queryKey: [
      ...ISSUES_KEY,
      params.from?.toISOString(),
      params.to?.toISOString(),
      params.statusFilter ?? 0,
      params.query ?? '',
      params.limit ?? 100,
      params.offset ?? 0,
    ],
    queryFn: () => listIssues(params),
    refetchOnWindowFocus: false,
    staleTime: 30_000,
  })
}

export function useIssue(
  fingerprint: string,
  params: { from?: Date; to?: Date; interval?: string } = {},
): UseQueryResult<GetIssueResponse, Error> {
  return useQuery({
    queryKey: [
      ...ISSUES_KEY,
      'detail',
      fingerprint,
      params.from?.toISOString(),
      params.to?.toISOString(),
      params.interval ?? '',
    ],
    queryFn: () => getIssue(fingerprint, params),
    enabled: !!fingerprint,
    refetchOnWindowFocus: false,
    staleTime: 30_000,
  })
}

export function useUpdateIssueStatus() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (vars: {
      fingerprint: string
      status: IssueStatus
      signature?: string
      title?: string
      assignee?: string
    }) => updateIssueStatus(vars),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ISSUES_KEY })
    },
  })
}
