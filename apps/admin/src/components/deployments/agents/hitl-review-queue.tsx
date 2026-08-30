import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { Link } from '@tanstack/react-router'
import { useSession } from '@/hooks/auth/use-auth'
import {
  useAgents,
  useReviews,
  useSession_,
  useSessions,
  useSubmitReview,
} from '@/hooks/deployments/use-agents'
import type {
  ApprovalReview,
  AgentSession,
  AgentSessionTurn,
} from '@/server/agents'
import {
  ApprovalAction,
  ApprovalReviewStatus,
  SessionStatus,
} from '@/server/agents'
import { ui } from '@everstack/ui'
import { Loader, toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { formatTimestamp } from '@everstack/utils/functions/index'

const {
  Button,
  Card,
  CardContent,
  CardHeader,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Textarea,
} = ui

const ALL_AGENTS = '__all_agents__'
const ALL_TOOLS = '__all_tools__'
const ALL_RISKS = '__all_risks__'

const REVIEW_STATUS_META: Record<number, { label: string; className: string }> =
  {
    [ApprovalReviewStatus.PENDING]: {
      label: 'Pending',
      className: 'bg-amber-500/15 text-amber-300 border border-amber-500/20',
    },
    [ApprovalReviewStatus.APPROVED]: {
      label: 'Approved',
      className:
        'bg-emerald-500/15 text-emerald-300 border border-emerald-500/20',
    },
    [ApprovalReviewStatus.DENIED]: {
      label: 'Denied',
      className: 'bg-red-500/15 text-red-300 border border-red-500/20',
    },
    [ApprovalReviewStatus.EXPIRED]: {
      label: 'Expired',
      className: 'bg-orange-500/15 text-orange-300 border border-orange-500/20',
    },
    [ApprovalReviewStatus.CANCELLED]: {
      label: 'Cancelled',
      className:
        'bg-brand-main-700/40 text-brand-main-200 border border-brand-main-600/40',
    },
  }

const SESSION_STATUS_LABELS: Record<number, string> = {
  [SessionStatus.CREATED]: 'Created',
  [SessionStatus.RUNNING]: 'Running',
  [SessionStatus.WAITING_FOR_INPUT]: 'Waiting for input',
  [SessionStatus.WAITING_FOR_APPROVAL]: 'Waiting for approval',
  [SessionStatus.COMPLETED]: 'Completed',
  [SessionStatus.FAILED]: 'Failed',
  [SessionStatus.CANCELLED]: 'Cancelled',
}

type ReviewTab = 'pending' | 'history'

export function HitlReviewQueue() {
  const [activeTab, setActiveTab] = useState<ReviewTab>('pending')
  const [selectedReviewId, setSelectedReviewId] = useState<string | null>(null)
  const [agentFilter, setAgentFilter] = useState(ALL_AGENTS)
  const [toolFilter, setToolFilter] = useState(ALL_TOOLS)
  const [riskFilter, setRiskFilter] = useState(ALL_RISKS)
  const { data: reviews = [], isLoading, error } = useReviews()
  const { data: agents = [] } = useAgents({ includeHidden: true, limit: 200 })
  const { data: sessions = [] } = useSessions({ limit: 200 })
  const authSession = useSession()
  const submitMutation = useSubmitReview()

  const reviewerLabel =
    authSession.data?.user?.user?.name ||
    authSession.data?.user?.user?.email ||
    authSession.data?.user?.user?.id ||
    'Admin'

  const agentNameMap = useMemo(() => {
    const map = new Map<string, string>()
    for (const agent of agents) map.set(agent.id, agent.name)
    return map
  }, [agents])

  const sessionMap = useMemo(() => {
    const map = new Map<string, AgentSession>()
    for (const session of sessions) map.set(session.id, session)
    return map
  }, [sessions])

  const filterOptions = useMemo(() => {
    const agentEntries = Array.from(
      new Map(
        reviews.map((review) => [
          review.agentId,
          agentNameMap.get(review.agentId) ?? truncateId(review.agentId),
        ]),
      ).entries(),
    ).sort((a, b) => a[1].localeCompare(b[1]))

    const toolEntries = Array.from(
      new Set(
        reviews.flatMap((review) =>
          review.toolCalls.map((toolCall) => toolCall.toolName),
        ),
      ),
    ).sort((a, b) => a.localeCompare(b))

    const riskEntries = Array.from(
      new Set(reviews.flatMap((review) => deriveRiskBadges(review))),
    ).sort((a, b) => a.localeCompare(b))

    return {
      agents: agentEntries,
      tools: toolEntries,
      risks: riskEntries,
    }
  }, [agentNameMap, reviews])

  const filteredReviews = useMemo(
    () =>
      reviews.filter((review) =>
        matchesFilters(review, agentFilter, toolFilter, riskFilter),
      ),
    [agentFilter, reviews, riskFilter, toolFilter],
  )

  const pendingReviews = useMemo(
    () =>
      sortReviews(
        filteredReviews.filter(
          (review) => review.status === ApprovalReviewStatus.PENDING,
        ),
        'pending',
      ),
    [filteredReviews],
  )

  const historyReviews = useMemo(
    () =>
      sortReviews(
        filteredReviews.filter(
          (review) => review.status !== ApprovalReviewStatus.PENDING,
        ),
        'history',
      ),
    [filteredReviews],
  )

  const visibleReviews =
    activeTab === 'pending' ? pendingReviews : historyReviews

  useEffect(() => {
    if (visibleReviews.length === 0) {
      setSelectedReviewId(null)
      return
    }

    if (
      !selectedReviewId ||
      !visibleReviews.some((review) => review.id === selectedReviewId)
    ) {
      setSelectedReviewId(visibleReviews[0]?.id ?? null)
    }
  }, [selectedReviewId, visibleReviews])

  const selectedReview =
    visibleReviews.find((review) => review.id === selectedReviewId) ?? null

  const handleDecision = async (
    reviewId: string,
    action: ApprovalAction,
    note: string,
  ) => {
    try {
      await submitMutation.mutateAsync({
        reviewId,
        action,
        reason: note.trim(),
        resolvedBy: reviewerLabel,
      })
      toast.success(
        action === ApprovalAction.APPROVE
          ? 'Approval granted'
          : 'Approval denied',
      )
    } catch {
      toast.error(
        action === ApprovalAction.APPROVE
          ? 'Failed to approve review'
          : 'Failed to deny review',
      )
    }
  }

  if (isLoading) {
    return (
      <div className="flex flex-1 items-center justify-center text-white/70 light:text-black/70">
        <Loader loaderText="Loading approvals..." />
      </div>
    )
  }

  if (error) {
    return (
      <div className="flex flex-1 items-center justify-center text-red-400">
        Error loading approvals: {error.message}
      </div>
    )
  }

  if (reviews.length === 0) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center pb-24">
        <div className="relative mb-6">
          <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
          <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
            <Iconify.Icon
              icon="heroicons:clipboard-document-check"
              className="size-8 text-brand-secondary-400"
            />
          </div>
        </div>
        <h3 className="text-base font-medium text-white mb-2 light:text-brand-main-50">
          No approval activity yet
        </h3>
        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
          Approvals appear here when an agent hits a human-in-the-loop rule. Add
          HITL policies to risky tools like deploys, deletes, billing actions,
          or external messages.
        </p>
      </div>
    )
  }

  return (
    <div className="flex h-full min-h-0 flex-col p-4">
      <div className="mb-4 grid gap-3 md:grid-cols-3">
        <StatsCard
          label="Pending"
          value={pendingReviews.length}
          hint="Blocking active agent runs"
        />
        <StatsCard
          label="Resolved"
          value={historyReviews.length}
          hint="Approved, denied, expired, cancelled"
        />
        <StatsCard
          label="Avg payload"
          value={averageToolCalls(reviews)}
          hint="Tool calls per review"
        />
      </div>

      <Tabs
        value={activeTab}
        onValueChange={(value) => setActiveTab(value as ReviewTab)}
        className="flex min-h-0 flex-1 flex-col gap-4"
      >
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 className="text-sm font-semibold text-white light:text-brand-main-50">
              Approvals Center
            </h3>
            <p className="text-xs text-white/50 light:text-black/50">
              Review sensitive agent actions, see why they paused, and keep an
              audit trail.
            </p>
          </div>
          <TabsList className="h-auto w-fit gap-1 rounded border border-brand-main-600 bg-brand-main-800/50 p-1">
            <TabsTrigger className="py-1" value="pending">
              Pending
            </TabsTrigger>
            <TabsTrigger className="py-1" value="history">
              History
            </TabsTrigger>
          </TabsList>
        </div>

        <FilterBar
          agentFilter={agentFilter}
          toolFilter={toolFilter}
          riskFilter={riskFilter}
          onAgentFilterChange={setAgentFilter}
          onToolFilterChange={setToolFilter}
          onRiskFilterChange={setRiskFilter}
          agentOptions={filterOptions.agents}
          toolOptions={filterOptions.tools}
          riskOptions={filterOptions.risks}
        />

        <TabsContent
          value="pending"
          className="min-h-0 flex-1 data-[state=inactive]:hidden"
        >
          <ReviewWorkspace
            reviews={pendingReviews}
            selectedReview={selectedReview}
            selectedReviewId={selectedReviewId}
            onSelectReview={setSelectedReviewId}
            onSubmitDecision={handleDecision}
            isPending={submitMutation.isPending}
            agentNameMap={agentNameMap}
            sessionMap={sessionMap}
            reviewerLabel={reviewerLabel}
            emptyTitle="No approvals waiting"
            emptyDescription="Reviews appear here when an agent hits a human approval rule. Pending items are sorted so urgent actions surface first."
          />
        </TabsContent>

        <TabsContent
          value="history"
          className="min-h-0 flex-1 data-[state=inactive]:hidden"
        >
          <ReviewWorkspace
            reviews={historyReviews}
            selectedReview={selectedReview}
            selectedReviewId={selectedReviewId}
            onSelectReview={setSelectedReviewId}
            onSubmitDecision={handleDecision}
            isPending={submitMutation.isPending}
            agentNameMap={agentNameMap}
            sessionMap={sessionMap}
            reviewerLabel={reviewerLabel}
            emptyTitle="No approval history yet"
            emptyDescription="Once reviews are approved, denied, expired, or cancelled, they will appear here for audit and debugging."
          />
        </TabsContent>
      </Tabs>
    </div>
  )
}

interface FilterBarProps {
  agentFilter: string
  toolFilter: string
  riskFilter: string
  onAgentFilterChange: (value: string) => void
  onToolFilterChange: (value: string) => void
  onRiskFilterChange: (value: string) => void
  agentOptions: Array<[string, string]>
  toolOptions: string[]
  riskOptions: string[]
}

function FilterBar({
  agentFilter,
  toolFilter,
  riskFilter,
  onAgentFilterChange,
  onToolFilterChange,
  onRiskFilterChange,
  agentOptions,
  toolOptions,
  riskOptions,
}: FilterBarProps) {
  const triggerClass =
    'h-9 border-brand-main-700 bg-brand-main-950/70 text-white light:text-brand-main-50'

  return (
    <Card className="border-brand-main-800/50 bg-brand-main-900/40">
      <CardContent className="grid gap-3 p-4 md:grid-cols-3">
        <div className="space-y-2">
          <Label className="text-xs text-white/55 light:text-black/55">Agent</Label>
          <Select value={agentFilter} onValueChange={onAgentFilterChange}>
            <SelectTrigger className={triggerClass}>
              <SelectValue placeholder="All agents" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL_AGENTS}>All agents</SelectItem>
              {agentOptions.map(([id, name]) => (
                <SelectItem key={id} value={id}>
                  {name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div className="space-y-2">
          <Label className="text-xs text-white/55 light:text-black/55">Tool</Label>
          <Select value={toolFilter} onValueChange={onToolFilterChange}>
            <SelectTrigger className={triggerClass}>
              <SelectValue placeholder="All tools" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL_TOOLS}>All tools</SelectItem>
              {toolOptions.map((tool) => (
                <SelectItem key={tool} value={tool}>
                  {tool}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div className="space-y-2">
          <Label className="text-xs text-white/55 light:text-black/55">Risk</Label>
          <Select value={riskFilter} onValueChange={onRiskFilterChange}>
            <SelectTrigger className={triggerClass}>
              <SelectValue placeholder="All risks" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL_RISKS}>All risks</SelectItem>
              {riskOptions.map((risk) => (
                <SelectItem key={risk} value={risk}>
                  {risk}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </CardContent>
    </Card>
  )
}

interface ReviewWorkspaceProps {
  reviews: ApprovalReview[]
  selectedReview: ApprovalReview | null
  selectedReviewId: string | null
  onSelectReview: (reviewId: string) => void
  onSubmitDecision: (
    reviewId: string,
    action: ApprovalAction,
    note: string,
  ) => Promise<void>
  isPending: boolean
  agentNameMap: Map<string, string>
  sessionMap: Map<string, AgentSession>
  reviewerLabel: string
  emptyTitle: string
  emptyDescription: string
}

function ReviewWorkspace({
  reviews,
  selectedReview,
  selectedReviewId,
  onSelectReview,
  onSubmitDecision,
  isPending,
  agentNameMap,
  sessionMap,
  reviewerLabel,
  emptyTitle,
  emptyDescription,
}: ReviewWorkspaceProps) {
  if (reviews.length === 0) {
    return (
      <Card className="flex h-full items-center justify-center border-brand-main-800/50 bg-brand-main-900/40">
        <CardContent className="flex flex-col items-center px-6 py-10 text-center">
          <div className="relative mb-5">
            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-3">
              <Iconify.Icon icon="heroicons:clipboard-document-check" className="size-6 text-brand-secondary-400" />
            </div>
          </div>
          <div className="text-sm font-medium text-white light:text-brand-main-50">{emptyTitle}</div>
          <div className="mt-2 max-w-md text-sm leading-relaxed text-white/50 light:text-black/50">
            {emptyDescription}
          </div>
        </CardContent>
      </Card>
    )
  }

  return (
    <div className="grid h-full min-h-0 gap-4 xl:grid-cols-[minmax(360px,460px)_minmax(0,1fr)]">
      <div className="min-h-0 space-y-3 overflow-y-auto pr-1">
        {reviews.map((review) => (
          <ReviewCard
            key={review.id}
            review={review}
            isSelected={review.id === selectedReviewId}
            onSelect={() => onSelectReview(review.id)}
            agentName={
              agentNameMap.get(review.agentId) ?? truncateId(review.agentId)
            }
          />
        ))}
      </div>

      <ReviewDetailPanel
        review={selectedReview}
        onSubmitDecision={onSubmitDecision}
        isPending={isPending}
        agentNameMap={agentNameMap}
        sessionMap={sessionMap}
        reviewerLabel={reviewerLabel}
      />
    </div>
  )
}

interface ReviewCardProps {
  review: ApprovalReview
  isSelected: boolean
  onSelect: () => void
  agentName: string
}

function ReviewCard({
  review,
  isSelected,
  onSelect,
  agentName,
}: ReviewCardProps) {
  const statusMeta =
    REVIEW_STATUS_META[review.status] ??
    REVIEW_STATUS_META[ApprovalReviewStatus.CANCELLED]
  const riskBadges = deriveRiskBadges(review)

  return (
    <button
      type="button"
      onClick={onSelect}
      className={`w-full rounded-xl border p-4 text-left transition-colors ${
        isSelected
          ? 'border-brand-secondary-500/50 bg-brand-secondary-700/10'
          : 'border-brand-main-800/50 bg-brand-main-900/40 hover:border-brand-main-700 hover:bg-brand-main-900/60'
      }`}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span
              className={`inline-flex rounded-full px-2 py-0.5 text-[10px] font-medium ${statusMeta.className}`}
            >
              {statusMeta.label}
            </span>
            <span className="text-xs font-medium text-white/85 light:text-black/85">
              {agentName}
            </span>
            <span className="text-[11px] text-white/40 light:text-black/40">
              Turn {review.turnNumber}
            </span>
          </div>

          <div className="mt-2 text-sm font-medium text-white light:text-brand-main-50">
            {summarizeReviewIntent(review)}
          </div>

          <div className="mt-1 text-xs text-white/45 light:text-black/45">
            Requested {formatRelativeTime(review.requestedAt)}
            {review.status === ApprovalReviewStatus.PENDING && review.expiresAt
              ? ` • Expires ${formatRelativeTime(review.expiresAt)}`
              : ''}
          </div>

          {review.status !== ApprovalReviewStatus.PENDING && (
            <div className="mt-2 space-y-1 text-xs text-white/55 light:text-black/55">
              <div>
                Reviewed by{' '}
                <span className="text-white/75 light:text-black/75">
                  {review.resolvedBy || 'Unassigned'}
                </span>
              </div>
              {review.resolutionReason && (
                <div className="line-clamp-2 text-white/45 light:text-black/45">
                  Note: {review.resolutionReason}
                </div>
              )}
            </div>
          )}
        </div>

        <div className="text-right text-[11px] text-white/45 light:text-black/45">
          <div>
            {review.toolCalls.length} tool call
            {review.toolCalls.length === 1 ? '' : 's'}
          </div>
          <div className="mt-1 font-mono">{truncateId(review.id, 8)}</div>
        </div>
      </div>

      <div className="mt-3 flex flex-wrap gap-2">
        {riskBadges.map((badge) => (
          <span
            key={badge}
            className="rounded-full bg-brand-main-800/80 px-2 py-0.5 text-[10px] uppercase tracking-wide text-white/60 light:text-black/60"
          >
            {badge}
          </span>
        ))}
      </div>
    </button>
  )
}

interface ReviewDetailPanelProps {
  review: ApprovalReview | null
  onSubmitDecision: (
    reviewId: string,
    action: ApprovalAction,
    note: string,
  ) => Promise<void>
  isPending: boolean
  agentNameMap: Map<string, string>
  sessionMap: Map<string, AgentSession>
  reviewerLabel: string
}

function ReviewDetailPanel({
  review,
  onSubmitDecision,
  isPending,
  agentNameMap,
  sessionMap,
  reviewerLabel,
}: ReviewDetailPanelProps) {
  const [note, setNote] = useState('')
  const selectedSession = useSession_(review?.sessionId ?? '')

  useEffect(() => {
    setNote(review?.resolutionReason ?? '')
  }, [review?.id, review?.resolutionReason])

  if (!review) {
    return (
      <Card className="hidden h-full border-brand-main-800/50 bg-brand-main-900/40 xl:flex xl:items-center xl:justify-center">
        <CardContent className="text-center text-sm text-white/50 light:text-black/50">
          Select an approval to inspect its context and take action.
        </CardContent>
      </Card>
    )
  }

  const statusMeta =
    REVIEW_STATUS_META[review.status] ??
    REVIEW_STATUS_META[ApprovalReviewStatus.CANCELLED]
  const session = sessionMap.get(review.sessionId)
  const detailedSession = selectedSession.data
  const relevantTurns = getRelevantTurns(detailedSession, review.turnNumber)
  const triggeringTurn = relevantTurns[relevantTurns.length - 1] ?? null
  const resolvedLabel = review.resolvedAt
    ? `${statusMeta.label} ${formatRelativeTime(review.resolvedAt)}`
    : 'Awaiting decision'

  const submit = async (action: ApprovalAction) => {
    await onSubmitDecision(review.id, action, note)
  }

  return (
    <Card className="h-full min-h-0 overflow-y-auto border-brand-main-800/50 bg-brand-main-900/50">
      <CardHeader className="space-y-4 border-b border-brand-main-800/60 pb-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <div className="flex flex-wrap items-center gap-2">
              <span
                className={`inline-flex rounded-full px-2 py-0.5 text-[10px] font-medium ${statusMeta.className}`}
              >
                {statusMeta.label}
              </span>
              <span className="font-mono text-xs text-white/45 light:text-black/45">
                {review.id}
              </span>
            </div>
            <h3 className="mt-3 text-lg font-semibold text-white light:text-brand-main-50">
              {summarizeReviewIntent(review)}
            </h3>
            <p className="mt-1 text-sm text-white/55 light:text-black/55">{resolvedLabel}</p>
          </div>

          {review.status === ApprovalReviewStatus.PENDING && (
            <div className="hidden items-center gap-2 xl:flex">
              <Button
                size="sm"
                variant="default"
                onClick={() => void submit(ApprovalAction.APPROVE)}
                disabled={isPending}
                className="text-xs"
              >
                Approve
              </Button>
              <Button
                size="sm"
                variant="destructive"
                className="text-xs bg-destructive/60 hover:bg-destructive/90"
                onClick={() => void submit(ApprovalAction.DENY)}
                disabled={isPending}
              >
                Deny
              </Button>
            </div>
          )}
        </div>

        <div className="flex flex-wrap gap-2">
          {deriveRiskBadges(review).map((badge) => (
            <span
              key={badge}
              className="rounded-full bg-brand-main-800/80 px-2 py-0.5 text-[10px] uppercase tracking-wide text-white/60 light:text-black/60"
            >
              {badge}
            </span>
          ))}
        </div>
      </CardHeader>

      <CardContent className="space-y-6 pt-5">
        <Section title="Review Context">
          <MetaRow
            label="Agent"
            value={agentNameMap.get(review.agentId) ?? review.agentId}
          />
          <MetaRow
            label="Session"
            value={
              <Link
                className="font-mono text-brand-secondary-300 hover:text-brand-secondary-200"
                to="/deployments/agents/sessions/$sessionId"
                params={{ sessionId: review.sessionId }}
              >
                {review.sessionId}
              </Link>
            }
          />
          <MetaRow
            label="Session status"
            value={
              session
                ? (SESSION_STATUS_LABELS[session.status] ??
                  String(session.status))
                : 'Unknown'
            }
          />
          <MetaRow label="Turn" value={String(review.turnNumber)} />
          <MetaRow label="Iteration" value={String(review.iteration)} />
          <MetaRow
            label="Requested"
            value={formatTimestamp(review.requestedAt)}
          />
          <MetaRow
            label="Expires"
            value={
              review.expiresAt ? formatTimestamp(review.expiresAt) : 'No expiry'
            }
          />
          <MetaRow
            label="Default action"
            value={humanizeDefaultAction(review.defaultAction)}
          />
        </Section>

        <Section title="Approval Briefing">
          {selectedSession.isLoading ? (
            <div className="text-sm text-white/55 light:text-black/55">
              Loading transcript context...
            </div>
          ) : selectedSession.error ? (
            <div className="text-sm text-red-300">
              Failed to load session context.
            </div>
          ) : relevantTurns.length === 0 ? (
            <div className="text-sm text-white/55 light:text-black/55">
              No transcript context available for this review yet.
            </div>
          ) : (
            <div className="space-y-3">
              {triggeringTurn?.userInput && (
                <BriefingCard
                  label="Triggering user request"
                  value={triggeringTurn.userInput}
                  accent="text-brand-secondary-200"
                />
              )}
              {triggeringTurn?.assistantOutput && (
                <BriefingCard
                  label="Assistant response before pause"
                  value={triggeringTurn.assistantOutput}
                  accent="text-white/80 light:text-black/80"
                />
              )}
              {relevantTurns.length > 1 && (
                <div className="space-y-2">
                  <div className="text-xs uppercase tracking-[0.16em] text-white/40 light:text-black/40">
                    Nearby context
                  </div>
                  {relevantTurns.slice(0, -1).map((turn) => (
                    <div
                      key={turn.id}
                      className="rounded-xl border border-brand-main-800/60 bg-brand-main-950/60 p-3"
                    >
                      <div className="text-[11px] text-white/40 light:text-black/40">
                        Turn {turn.turnNumber}
                      </div>
                      {turn.userInput && (
                        <div className="mt-1 text-sm text-brand-secondary-200">
                          User: {turn.userInput}
                        </div>
                      )}
                      {turn.assistantOutput && (
                        <div className="mt-2 text-sm text-white/65 light:text-black/65">
                          Assistant: {truncateText(turn.assistantOutput, 280)}
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
        </Section>

        <Section title="Why It Paused">
          <p className="text-sm leading-relaxed text-white/65 light:text-black/65">
            This run hit a human approval gate before the agent could continue.
            Review the target, scope, and payload below to decide whether the
            action should proceed.
          </p>
        </Section>

        <Section title="Tool Calls">
          <div className="space-y-3">
            {review.toolCalls.map((toolCall) => (
              <div
                key={toolCall.toolCallId}
                className="rounded-xl border border-brand-main-800/60 bg-brand-main-950/70 p-3"
              >
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="text-sm font-medium text-white light:text-brand-main-50">
                    {toolCall.toolName}
                  </div>
                  <div className="font-mono text-[11px] text-white/35 light:text-black/35">
                    {toolCall.toolCallId}
                  </div>
                </div>
                <pre className="mt-3 overflow-x-auto whitespace-pre-wrap break-all rounded-lg bg-black/20 p-3 text-xs leading-relaxed text-white/65 light:text-black/65">
                  {prettyToolArgs(toolCall.toolArgs)}
                </pre>
              </div>
            ))}
          </div>
        </Section>

        <Section title="Decision Log">
          <MetaRow
            label="Resolved by"
            value={review.resolvedBy || 'Unassigned'}
          />
          <MetaRow
            label="Resolved at"
            value={
              review.resolvedAt
                ? formatTimestamp(review.resolvedAt)
                : 'Not resolved'
            }
          />
          <MetaRow
            label="Reason"
            value={review.resolutionReason || 'No note provided'}
          />
          <MetaRow
            label="Per-tool decisions"
            value={
              review.decisions.length > 0 ? (
                <div className="space-y-1">
                  {review.decisions.map((decision) => (
                    <div
                      key={decision.toolCallId}
                      className="font-mono text-xs text-white/65 light:text-black/65"
                    >
                      {decision.toolCallId}:{' '}
                      {decision.action === ApprovalAction.APPROVE
                        ? 'approve'
                        : 'deny'}
                      {decision.reason ? ` - ${decision.reason}` : ''}
                    </div>
                  ))}
                </div>
              ) : (
                'No per-tool overrides'
              )
            }
          />
        </Section>

        {review.status === ApprovalReviewStatus.PENDING && (
          <Section title="Reviewer Decision">
            <div className="space-y-3 rounded-xl border border-brand-main-800/60 bg-brand-main-950/60 p-4">
              <MetaRow label="Reviewer" value={reviewerLabel} />
              <div className="space-y-2">
                <Label
                  htmlFor="approval-note"
                  className="text-xs text-white/55 light:text-black/55"
                >
                  Decision note (optional)
                </Label>
                <Textarea
                  id="approval-note"
                  value={note}
                  onChange={(event) => setNote(event.target.value)}
                  placeholder="Add context for why you approved or denied this action."
                  className="min-h-24 border-brand-main-700 bg-brand-main-950/70 text-white placeholder:text-white/30 light:text-brand-main-50 light:placeholder:text-black/30"
                />
              </div>

              <div className="flex items-center gap-2 border-t border-brand-main-800/60 pt-2 xl:hidden">
                <Button
                  size="sm"
                  variant="default"
                  onClick={() => void submit(ApprovalAction.APPROVE)}
                  disabled={isPending}
                  className="text-xs"
                >
                  Approve
                </Button>
                <Button
                  size="sm"
                  variant="destructive"
                  className="text-xs bg-destructive/60 hover:bg-destructive/90"
                  onClick={() => void submit(ApprovalAction.DENY)}
                  disabled={isPending}
                >
                  Deny
                </Button>
              </div>
            </div>
          </Section>
        )}
      </CardContent>
    </Card>
  )
}

function BriefingCard({
  label,
  value,
  accent,
}: {
  label: string
  value: string
  accent: string
}) {
  return (
    <div className="rounded-xl border border-brand-main-800/60 bg-brand-main-950/60 p-3">
      <div className="text-xs uppercase tracking-[0.16em] text-white/40 light:text-black/40">
        {label}
      </div>
      <div
        className={`mt-2 whitespace-pre-wrap text-sm leading-relaxed ${accent}`}
      >
        {value}
      </div>
    </div>
  )
}

function StatsCard({
  label,
  value,
  hint,
}: {
  label: string
  value: string | number
  hint: string
}) {
  return (
    <Card className="border-brand-main-800/50 bg-brand-main-900/40">
      <CardContent className="px-4 py-3">
        <div className="text-[11px] uppercase tracking-[0.14em] text-white/40 light:text-black/40">
          {label}
        </div>
        <div className="mt-1 text-2xl font-semibold text-white light:text-brand-main-50">{value}</div>
        <div className="mt-1 text-xs text-white/45 light:text-black/45">{hint}</div>
      </CardContent>
    </Card>
  )
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section>
      <h4 className="text-xs font-semibold uppercase tracking-[0.16em] text-white/40 light:text-black/40">
        {title}
      </h4>
      <div className="mt-3 space-y-3">{children}</div>
    </section>
  )
}

function MetaRow({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="grid gap-1 sm:grid-cols-[140px_minmax(0,1fr)] sm:gap-4">
      <div className="text-xs text-white/40 light:text-black/40">{label}</div>
      <div className="break-all text-sm text-white/75 light:text-black/75">{value}</div>
    </div>
  )
}

function averageToolCalls(reviews: ApprovalReview[]): string {
  if (reviews.length === 0) return '0.0'
  const total = reviews.reduce(
    (sum, review) => sum + review.toolCalls.length,
    0,
  )
  return (total / reviews.length).toFixed(1)
}

function matchesFilters(
  review: ApprovalReview,
  agentFilter: string,
  toolFilter: string,
  riskFilter: string,
): boolean {
  if (agentFilter !== ALL_AGENTS && review.agentId !== agentFilter) return false
  if (
    toolFilter !== ALL_TOOLS &&
    !review.toolCalls.some((toolCall) => toolCall.toolName === toolFilter)
  ) {
    return false
  }

  if (
    riskFilter !== ALL_RISKS &&
    !deriveRiskBadges(review).includes(riskFilter)
  ) {
    return false
  }

  return true
}

function sortReviews(
  reviews: ApprovalReview[],
  mode: ReviewTab,
): ApprovalReview[] {
  return [...reviews].sort((a, b) => {
    if (mode === 'pending') {
      const aTime = timestampToMs(a.expiresAt)
      const bTime = timestampToMs(b.expiresAt)
      return aTime - bTime
    }

    const aTime = timestampToMs(a.resolvedAt ?? a.requestedAt)
    const bTime = timestampToMs(b.resolvedAt ?? b.requestedAt)
    return bTime - aTime
  })
}

function getRelevantTurns(
  session: AgentSession | undefined,
  turnNumber: number,
): AgentSessionTurn[] {
  if (!session?.turns?.length) return []
  const targetIndex = session.turns.findIndex(
    (turn) => turn.turnNumber === turnNumber,
  )
  if (targetIndex === -1) return []
  return session.turns.slice(Math.max(0, targetIndex - 2), targetIndex + 1)
}

function summarizeReviewIntent(review: ApprovalReview): string {
  const firstTool = review.toolCalls[0]
  if (!firstTool) return 'Agent requested approval'

  const args = parseToolArgs(firstTool.toolArgs)
  const target =
    args.environment ??
    args.project ??
    args.service ??
    args.repository ??
    args.resource ??
    args.user ??
    args.path ??
    args.url

  if (target)
    return `Agent wants to run ${firstTool.toolName} on ${String(target)}`
  return `Agent wants to run ${firstTool.toolName}`
}

function deriveRiskBadges(review: ApprovalReview): string[] {
  const badges = new Set<string>()
  const haystack =
    `${review.toolCalls.map((toolCall) => `${toolCall.toolName} ${toolCall.toolArgs}`).join(' ')} ${review.defaultAction}`.toLowerCase()

  if (/(delete|destroy|remove|drop|terminate|revoke)/.test(haystack))
    badges.add('Destructive')
  if (/(prod|production|deploy|release|restart)/.test(haystack))
    badges.add('Production')
  if (
    /(email|slack|discord|sms|webhook|github|post|publish|notify)/.test(
      haystack,
    )
  )
    badges.add('External')
  if (/(billing|invoice|payment|subscription|customer|license)/.test(haystack))
    badges.add('Billing')
  if (/(ssh|token|secret|credential|key|admin)/.test(haystack))
    badges.add('Sensitive')
  if (badges.size === 0) badges.add('Manual review')

  return Array.from(badges)
}

function parseToolArgs(toolArgs: string): Record<string, unknown> {
  try {
    const parsed = JSON.parse(toolArgs)
    return typeof parsed === 'object' && parsed !== null
      ? (parsed as Record<string, unknown>)
      : {}
  } catch {
    return {}
  }
}

function prettyToolArgs(toolArgs: string): string {
  try {
    return JSON.stringify(JSON.parse(toolArgs), null, 2)
  } catch {
    return toolArgs
  }
}

function humanizeDefaultAction(action: string): string {
  if (!action) return 'Not specified'
  return action.charAt(0).toUpperCase() + action.slice(1)
}

function formatRelativeTime(
  timestamp?: { seconds?: bigint | number | string | null } | null,
): string {
  const target = timestampToMs(timestamp)
  if (!target) return 'just now'

  const diffMs = target - Date.now()
  const diffAbs = Math.abs(diffMs)
  const minutes = Math.round(diffAbs / 60000)
  const hours = Math.round(diffAbs / 3600000)
  const days = Math.round(diffAbs / 86400000)

  let label = 'moments'
  if (diffAbs < 60000) label = 'less than a minute'
  else if (diffAbs < 3600000)
    label = `${minutes} minute${minutes === 1 ? '' : 's'}`
  else if (diffAbs < 86400000) label = `${hours} hour${hours === 1 ? '' : 's'}`
  else label = `${days} day${days === 1 ? '' : 's'}`

  return diffMs >= 0 ? `in ${label}` : `${label} ago`
}

function timestampToMs(
  timestamp?: { seconds?: bigint | number | string | null } | null,
): number {
  const seconds = timestamp?.seconds
  const value =
    typeof seconds === 'bigint'
      ? Number(seconds)
      : typeof seconds === 'string'
        ? Number(seconds)
        : (seconds ?? 0)
  return value > 0 ? value * 1000 : 0
}

function truncateId(value: string, length = 12): string {
  return value.length > length ? `${value.slice(0, length)}...` : value
}

function truncateText(value: string, length: number): string {
  return value.length > length ? `${value.slice(0, length)}...` : value
}
