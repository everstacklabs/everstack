import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ui } from '@everstack/ui'
import {
  openAIRequest,
  type OpenAIApproval,
  type OpenAICapabilities,
} from '@/server/openai-agents'
import { useOpenAIAgentsScope } from './openai-start-session-dialog'

const { Button, Card, CardContent } = ui

export function OpenAIPendingApprovals() {
  const { authenticated, scope } = useOpenAIAgentsScope()
  const cache = useQueryClient()
  const key = ['openai-agents', scope, 'approvals']
  const approvals = useQuery({
    queryKey: key,
    queryFn: () => openAIRequest<{ data: OpenAIApproval[] }>('/approvals'),
    enabled: authenticated,
    refetchInterval: 3000,
    retry: false,
  })
  const capabilities = useQuery({
    queryKey: ['openai-agents', scope, 'capabilities'],
    queryFn: () => openAIRequest<OpenAICapabilities>('/capabilities'),
    enabled: authenticated,
    retry: false,
  })
  const decisionsEnabled = canResolveOpenAIApproval({
    allowed: capabilities.data?.can_resolve_approvals === true,
    capabilitiesFetching: capabilities.isFetching,
    capabilitiesError: !!capabilities.error,
    approvalsFetching: approvals.isFetching,
    approvalsError: !!approvals.error,
  })
  const resolve = useMutation({
    mutationFn: ({
      id,
      decision,
    }: {
      id: string
      decision: 'approved' | 'denied'
    }) =>
      openAIRequest(`/approvals/${encodeURIComponent(id)}/resolve`, 'POST', {
        decision,
      }),
    onSuccess: () => cache.invalidateQueries({ queryKey: key }),
    retry: false,
  })
  if (
    !authenticated ||
    (!approvals.isPending &&
      !approvals.error &&
      approvals.data?.data.length === 0)
  )
    return null
  return (
    <div className="max-h-[40vh] shrink-0 space-y-2 overflow-y-auto border-b border-brand-main-800/50 px-4 py-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h3 className="text-sm font-semibold text-white light:text-brand-main-50">
            OpenAI runtime approvals
          </h3>
          <p className="text-xs text-white/50 light:text-black/50">
            Pending tool calls from OpenAI-managed sessions. Resolved history is
            not provided by this integration yet.
          </p>
        </div>
        <span className="rounded bg-brand-main-700/50 px-2 py-1 text-[10px] font-medium text-brand-main-200">
          OpenAI Agents API
        </span>
      </div>
      {approvals.isPending && (
        <p role="status" className="text-xs text-white/45 light:text-black/45">
          Checking for pending OpenAI approvals…
        </p>
      )}
      {approvals.error && (
        <p role="alert" className="text-xs text-amber-200 light:text-amber-800">
          OpenAI approvals could not load. Everstack approvals remain available
          below.
        </p>
      )}
      {!decisionsEnabled && approvals.data?.data.length ? (
        <p
          role="status"
          className="text-xs text-amber-200 light:text-amber-800"
        >
          These saved results may be stale. Decisions are disabled until the
          OpenAI runtime is available and refreshed.
        </p>
      ) : null}
      <div className="grid gap-2 lg:grid-cols-2">
        {approvals.data?.data.map((approval) => (
          <ApprovalCard
            key={approval.id}
            approval={approval}
            busy={resolve.isPending || !decisionsEnabled}
            onDecision={(decision) =>
              resolve.mutate({ id: approval.id, decision })
            }
          />
        ))}
      </div>
      {resolve.error && (
        <p role="alert" className="text-xs text-red-400 light:text-red-700">
          {resolve.error instanceof Error
            ? resolve.error.message
            : 'The approval could not be resolved.'}
        </p>
      )}
    </div>
  )
}

export function canResolveOpenAIApproval(state: {
  allowed: boolean
  capabilitiesFetching: boolean
  capabilitiesError: boolean
  approvalsFetching: boolean
  approvalsError: boolean
}) {
  return (
    state.allowed &&
    !state.capabilitiesFetching &&
    !state.capabilitiesError &&
    !state.approvalsFetching &&
    !state.approvalsError
  )
}

function ApprovalCard({
  approval,
  busy,
  onDecision,
}: {
  approval: OpenAIApproval
  busy: boolean
  onDecision: (decision: 'approved' | 'denied') => void
}) {
  return (
    <Card className="border-brand-main-800/50 bg-brand-main-900/40">
      <CardContent className="p-3">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <span className="rounded-full border border-amber-500/20 bg-amber-500/15 px-2 py-0.5 text-[10px] font-medium text-amber-300 light:text-amber-700">
                Pending
              </span>
              <span className="truncate text-sm font-medium text-white light:text-brand-main-50">
                {approval.tool_name}
              </span>
            </div>
            <p className="mt-1 font-mono text-[11px] text-white/40 light:text-black/40">
              Session {approval.session_id.slice(0, 12)}…
            </p>
            <p className="mt-1 text-xs text-white/45 light:text-black/45">
              Expires {new Date(approval.expires_at).toLocaleString()}
            </p>
          </div>
          <div className="flex gap-2">
            <Button
              size="sm"
              disabled={busy}
              onClick={() => onDecision('approved')}
            >
              Approve
            </Button>
            <Button
              size="sm"
              variant="outline"
              disabled={busy}
              onClick={() => onDecision('denied')}
            >
              Deny
            </Button>
          </div>
        </div>
        {approval.arguments && (
          <details className="mt-3">
            <summary className="cursor-pointer text-xs text-white/55 light:text-black/55">
              Tool input
            </summary>
            <pre className="mt-2 max-h-32 overflow-auto whitespace-pre-wrap break-words rounded bg-brand-main-950/50 p-2 text-xs text-white/65 light:text-black/65">
              {JSON.stringify(approval.arguments, null, 2)}
            </pre>
          </details>
        )}
      </CardContent>
    </Card>
  )
}
