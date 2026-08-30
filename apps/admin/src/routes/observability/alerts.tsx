import { useState, useMemo } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import { FeatureGatedError } from '@/components/ee/feature-gated-error'
import { ui, Trash2 } from '@everstack/ui'
import { Iconify } from '@everstack/ui/icons'
import { Button, Loader, toast } from '@everstack/ui/components'
import { BellRing, CheckCircle, Eye, Globe, Mail, Pencil, Zap, Clock, ArrowRight } from 'lucide-react'
import { z } from 'zod'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { formatTimestamp } from '@everstack/utils/functions/index'
import {
    useAlertRules,
    useAlertEvents,
    useNotificationTargets,
    useAlertsSummary,
    useUpdateAlertRule,
    useDeleteAlertRule,
    useUpdateNotificationTarget,
    useDeleteNotificationTarget,
    useTestNotificationTarget,
    useAcknowledgeAlert,
    useResolveAlert,
    useSeedBuiltinRules,
} from '@/hooks/observability/use-alerts'
import {
    AlertCategory,
    AlertSeverity,
    NotificationTargetType,
    ComparisonOperator,
} from '@/server/alerts'
import type { AlertRule, AlertEvent, NotificationTarget } from '@/server/alerts'
import { AlertRuleSheet, NotificationTargetSheet } from '@/components/layout/topbar/routes/observability/alerts'
import { useChannels } from '@/hooks/deployments/use-channels'

const PLATFORM_ICONS: Record<number, { icon: string; label: string }> = {
    1: { icon: 'simple-icons:discord', label: 'Discord' },
    2: { icon: 'simple-icons:slack', label: 'Slack' },
    3: { icon: 'simple-icons:telegram', label: 'Telegram' },
}

const {
    Tabs,
    TabsContent,
    TabsList,
    TabsTrigger,
    Dialog,
    DialogContent,
    DialogTitle,
    DialogDescription,
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
    Switch,
    Tooltip,
    TooltipProvider,
} = ui

const TAB_TRIGGER_CLASS = 'relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1'

const alertsSearchSchema = z.object({
    tab: z.enum(['rules', 'history', 'targets']).optional().default('rules'),
})

export const Route = createFileRoute('/observability/alerts')({
    component: AlertsPage,
    validateSearch: alertsSearchSchema,
})

// ─── Helpers ─────────────────────────────────────────────────────────

function severityBadge(severity: number) {
    const label = severityLabel(severity)
    const colors: Record<string, string> = {
        critical: 'bg-red-500/15 text-red-300 light:text-red-600',
        warning: 'bg-yellow-500/15 text-yellow-300 light:text-yellow-700',
        info: 'bg-blue-500/15 text-blue-300 light:text-blue-600',
    }
    return colors[label] ?? 'bg-brand-main-700/50 text-brand-main-200'
}

function statusBadge(status: string) {
    const colors: Record<string, string> = {
        firing: 'bg-red-500/15 text-red-300 light:text-red-600',
        acknowledged: 'bg-yellow-500/15 text-yellow-300 light:text-yellow-700',
        resolved: 'bg-green-500/15 text-green-300 light:text-green-600',
    }
    return colors[status] ?? 'bg-brand-main-700/50 text-brand-main-200'
}

function categoryBadge(category: number) {
    const label = categoryLabel(category)
    const colors: Record<string, string> = {
        performance: 'bg-purple-500/15 text-purple-300 light:text-purple-600',
        cost: 'bg-emerald-500/15 text-emerald-300 light:text-emerald-600',
        provider: 'bg-amber-500/15 text-amber-300 light:text-amber-700',
        custom: 'bg-brand-main-700/50 text-brand-main-200',
    }
    return colors[label] ?? 'bg-brand-main-700/50 text-brand-main-200'
}

function categoryLabel(c: number): string {
    switch (c) {
        case AlertCategory.PERFORMANCE: return 'performance'
        case AlertCategory.COST: return 'cost'
        case AlertCategory.PROVIDER: return 'provider'
        case AlertCategory.CUSTOM: return 'custom'
        default: return 'unknown'
    }
}

function severityLabel(s: number): string {
    switch (s) {
        case AlertSeverity.CRITICAL: return 'critical'
        case AlertSeverity.WARNING: return 'warning'
        case AlertSeverity.INFO: return 'info'
        default: return 'unknown'
    }
}

function operatorLabel(o: number): string {
    switch (o) {
        case ComparisonOperator.GT: return '>'
        case ComparisonOperator.LT: return '<'
        case ComparisonOperator.GTE: return '>='
        case ComparisonOperator.LTE: return '<='
        default: return '>'
    }
}

function targetTypeLabel(t: number): string {
    switch (t) {
        case NotificationTargetType.CHANNEL: return 'Channel'
        case NotificationTargetType.WEBHOOK: return 'Webhook'
        case NotificationTargetType.EMAIL: return 'Email'
        default: return 'Unknown'
    }
}

function formatDuration(seconds: number): string {
    if (seconds >= 3600) return `${Math.floor(seconds / 3600)}h`
    if (seconds >= 60) return `${Math.floor(seconds / 60)}m`
    return `${seconds}s`
}

function statusStr(s: number): string {
    switch (s) {
        case 1: return 'firing'
        case 2: return 'acknowledged'
        case 3: return 'resolved'
        default: return 'unknown'
    }
}

// ─── Main Page ───────────────────────────────────────────────────────

function AlertsPage() {
    const gate = useFeatureGate(FeatureKey.ALERTS)

    if (gate.isBlocked) {
        return (
            <FeatureGateBanner
                featureName="Alerts"
                description="Alert evaluation and routing for production monitoring."
                requiredTier="Pro"
                upgradeUrl={gate.upgradeUrl}
                isCE={gate.isCE}
            />
        )
    }

    return <AlertsPageContent />
}

function AlertsPageContent() {
    const { tab } = Route.useSearch()
    const navigate = Route.useNavigate()
    const { data: summary } = useAlertsSummary()

    return (
        <div className="flex flex-col h-full w-full">
            <Tabs
                value={tab}
                onValueChange={(value) => navigate({ search: { tab: value as 'rules' | 'history' | 'targets' } })}
                className="flex-1 flex flex-col overflow-hidden"
            >
                <div className="px-3 pt-2">
                    <TabsList className="w-fit bg-brand-main-800/50 border border-brand-main-600 rounded p-1 h-auto gap-1">
                        <TabsTrigger className={TAB_TRIGGER_CLASS} value="rules">
                            Rules
                            {summary && summary.totalRules > 0 && (
                                <span className="px-1.5 py-0.5 rounded text-[10px] font-medium bg-brand-main-700/50 text-brand-main-200">
                                    {summary.totalRules}
                                </span>
                            )}
                        </TabsTrigger>
                        <TabsTrigger className={TAB_TRIGGER_CLASS} value="history">
                            History
                            {summary && summary.firingCount > 0 && (
                                <span className="px-1.5 py-0.5 rounded text-[10px] font-medium bg-red-500/15 text-red-300 light:text-red-600">
                                    {summary.firingCount}
                                </span>
                            )}
                        </TabsTrigger>
                        <TabsTrigger className={TAB_TRIGGER_CLASS} value="targets">Targets</TabsTrigger>
                    </TabsList>
                </div>

                <div className="flex-1 overflow-hidden">
                    <TabsContent value="rules" className="h-full overflow-hidden flex flex-col">
                        <RulesTab />
                    </TabsContent>
                    <TabsContent value="history" className="h-full overflow-hidden flex flex-col">
                        <HistoryTab />
                    </TabsContent>
                    <TabsContent value="targets" className="h-full overflow-hidden flex flex-col">
                        <TargetsTab />
                    </TabsContent>
                </div>
            </Tabs>
        </div>
    )
}

// ─── Rules Tab ───────────────────────────────────────────────────────

function RulesTab() {
    const [categoryFilter, setCategoryFilter] = useState<string>('all')
    const [editRule, setEditRule] = useState<AlertRule | null>(null)
    const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null)
    const [deleteConfirmName, setDeleteConfirmName] = useState('')
    const { data: rules, isLoading, error, refetch } = useAlertRules()
    const updateRule = useUpdateAlertRule()
    const deleteRuleMutation = useDeleteAlertRule()
    const seedBuiltin = useSeedBuiltinRules()
    const { data: events } = useAlertEvents()

    const firingRuleIds = useMemo(() => new Set(
        events?.filter(e => e.status === 1 || e.status === 2).map(e => e.alertRuleId) ?? []
    ), [events])

    const filteredRules = useMemo(() => {
        if (!rules) return []
        if (categoryFilter === 'all') return rules
        return rules.filter(r => categoryLabel(r.category) === categoryFilter)
    }, [rules, categoryFilter])

    if (isLoading) {
        return (
            <div className="flex-1 flex items-center justify-center text-white/70 light:text-black/70">
                <Loader loaderText="Loading alert rules..." />
            </div>
        )
    }

    if (error) {
        return (
            <FeatureGatedError
                error={error}
                featureKey={FeatureKey.ALERTS}
                featureName="Alerts"
                description="Alert evaluation and routing for production monitoring."
            />
        )
    }

    const handleDelete = async (id: string) => {
        try {
            await deleteRuleMutation.mutateAsync(id)
            setDeleteConfirmId(null)
            setDeleteConfirmName('')
            toast.success('Alert rule deleted successfully')
            refetch()
        } catch {
            toast.error('Failed to delete alert rule')
        }
    }

    const handleToggleEnabled = async (rule: AlertRule) => {
        try {
            await updateRule.mutateAsync({ id: rule.id, enabled: !rule.enabled })
            toast.success(`Alert rule ${rule.enabled ? 'disabled' : 'enabled'} successfully`)
            refetch()
        } catch {
            toast.error('Failed to update alert rule')
        }
    }

    const columns: ColumnConfig<AlertRule>[] = [
        {
            id: 'name',
            header: 'Name',
            width: 280,
            minWidth: 160,
            render: (rule) => (
                <div className="flex min-w-0 items-center gap-2">
                    {firingRuleIds.has(rule.id) && (
                        <BellRing className="h-3.5 w-3.5 shrink-0 text-red-400 light:text-red-600 animate-pulse" />
                    )}
                    <span className="truncate font-medium text-brand-secondary-100 text-xs">
                        {rule.name}
                    </span>
                    <span className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${severityBadge(rule.severity)}`}>
                        {severityLabel(rule.severity)}
                    </span>
                    <span className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${categoryBadge(rule.category)}`}>
                        {categoryLabel(rule.category)}
                    </span>
                </div>
            ),
        },
        {
            id: 'condition',
            header: 'Condition',
            width: 220,
            minWidth: 140,
            render: (rule) => (
                <span className="text-xs text-brand-main-100 font-mono">
                    {rule.metric} {operatorLabel(rule.operator)} {rule.threshold}
                </span>
            ),
        },
        {
            id: 'window',
            header: 'Window',
            width: 80,
            minWidth: 60,
            render: (rule) => (
                <span className="px-1.5 py-0.5 rounded text-[10px] font-medium bg-brand-main-700/50 text-brand-main-200">
                    {formatDuration(rule.durationSeconds)}
                </span>
            ),
        },
        {
            id: 'description',
            header: 'Description',
            width: 200,
            minWidth: 100,
            render: (rule) => (
                <span className="truncate text-xs text-brand-main-100">{rule.description || '-'}</span>
            ),
        },
        {
            id: 'enabled',
            header: 'Enabled',
            width: 80,
            minWidth: 80,
            render: (rule) => (
                <div data-row-actions>
                    <Switch
                        checked={rule.enabled}
                        onCheckedChange={() => handleToggleEnabled(rule)}
                        disabled={updateRule.isPending}
                    />
                </div>
            ),
        },
        {
            id: 'actions',
            header: '',
            width: 100,
            minWidth: 100,
            maxWidth: 100,
            resizable: false,
            render: (rule) => (
                <div className="flex items-center gap-2 justify-center" data-row-actions>
                    <button
                        type="button"
                        className="p-1 rounded hover:bg-blue-500/20 hover:text-blue-400 light:hover:text-blue-600 transition-colors text-brand-main-200"
                        onClick={() => setEditRule(rule)}
                        title="Edit rule"
                    >
                        <Pencil size={14} />
                    </button>
                    <button
                        type="button"
                        className="p-1 rounded hover:bg-red-500/20 hover:text-red-400 light:hover:text-red-600 transition-colors"
                        onClick={() => {
                            setDeleteConfirmId(rule.id)
                            setDeleteConfirmName(rule.name)
                        }}
                        title="Delete rule"
                    >
                        <Trash2 size={14} />
                    </button>
                </div>
            ),
        },
    ]

    return (
        <div className="flex-1 min-h-0 w-full h-full overflow-hidden flex flex-col">
            {/* Filter bar */}
            <div className="shrink-0 flex items-center justify-between gap-3 px-3 py-2 border-b border-brand-main-800/40 bg-brand-main-900/20">
                <div className="flex items-center gap-2">
                    <span className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">Category</span>
                    <Select value={categoryFilter} onValueChange={setCategoryFilter}>
                        <SelectTrigger className="h-8 w-[150px] bg-brand-main-900/60 border-brand-main-700 text-xs text-zinc-200 light:text-zinc-800">
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent className="bg-brand-main-900 border-brand-main-600 text-zinc-200 light:text-zinc-800">
                            <SelectItem value="all">All categories</SelectItem>
                            <SelectItem value="performance">Performance</SelectItem>
                            <SelectItem value="cost">Cost</SelectItem>
                            <SelectItem value="provider">Provider</SelectItem>
                            <SelectItem value="custom">Custom</SelectItem>
                        </SelectContent>
                    </Select>
                </div>
                <div className="flex items-center gap-2">
                    <Button
                        size="sm"
                        variant="outline"
                        className="h-8 text-xs border-brand-main-700 text-zinc-200 light:text-zinc-800 hover:text-white light:hover:text-brand-main-50 hover:bg-brand-main-800"
                        onClick={() => seedBuiltin.mutate()}
                        disabled={seedBuiltin.isPending}
                    >
                        <Zap className="w-3.5 h-3.5 mr-1" />
                        {seedBuiltin.isPending ? 'Seeding...' : 'Seed Built-in Rules'}
                    </Button>
                </div>
            </div>

            <ResponsiveTable
                columns={columns}
                data={filteredRules}
                enableResizing={true}
                minTableWidth="100%"
                emptyMessage={!rules?.length ? (
                    <div className="flex flex-col items-center justify-center">
                        <div className="relative mb-6">
                            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                                <Iconify.Icon icon="solar:bell-linear" className="size-8 text-brand-secondary-400" />
                            </div>
                        </div>
                        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No alert rules found</h3>
                        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                            Click "Seed Built-in Rules" to get started with pre-configured alert rules.
                        </p>
                    </div>
                ) : 'No rules match this category.'}
                rowKey={(rule) => rule.id}
            />

            <AlertRuleSheet
                mode="edit"
                rule={editRule ?? undefined}
                open={editRule !== null}
                onOpenChange={(open) => { if (!open) setEditRule(null) }}
            />

            {/* Delete confirmation dialog */}
            <Dialog open={deleteConfirmId !== null} onOpenChange={(open) => !open && setDeleteConfirmId(null)}>
                <DialogContent className="w-[500px]">
                    <DialogTitle>Delete Alert Rule</DialogTitle>
                    <DialogDescription className="text-brand-main-100">
                        Are you sure you want to delete <strong className="text-brand-main-100">{deleteConfirmName}</strong>? This will also remove all associated alert events.
                    </DialogDescription>
                    <div className="flex justify-end gap-3 mt-4">
                        <Button variant="outline" onClick={() => setDeleteConfirmId(null)} disabled={deleteRuleMutation.isPending}>
                            Cancel
                        </Button>
                        <Button
                            variant="destructive"
                            className="bg-destructive/60 text-brand-main-100 hover:bg-destructive/90"
                            onClick={() => deleteConfirmId && handleDelete(deleteConfirmId)}
                            disabled={deleteRuleMutation.isPending}
                        >
                            {deleteRuleMutation.isPending ? 'Deleting...' : 'Delete'}
                        </Button>
                    </div>
                </DialogContent>
            </Dialog>
        </div>
    )
}

// ─── History Tab ─────────────────────────────────────────────────────

const STATUS_INDICATOR: Record<string, string> = {
    firing: 'bg-red-500 shadow-red-500/40 shadow-[0_0_6px]',
    acknowledged: 'bg-yellow-500 shadow-yellow-500/40 shadow-[0_0_6px]',
    resolved: 'bg-green-500/60',
}

const SEVERITY_ICON_COLOR: Record<string, string> = {
    critical: 'text-red-400 light:text-red-600',
    warning: 'text-yellow-400 light:text-yellow-700',
    info: 'text-blue-400 light:text-blue-600',
}

function HistoryTab() {
    const { data: events, isLoading, error } = useAlertEvents()
    const acknowledgeAlert = useAcknowledgeAlert()
    const resolveAlert = useResolveAlert()
    const [statusFilter, setStatusFilter] = useState<string>('all')

    const filteredEvents = useMemo(() => {
        if (!events) return []
        if (statusFilter === 'all') return events
        return events.filter(e => statusStr(e.status) === statusFilter)
    }, [events, statusFilter])

    if (isLoading) {
        return (
            <div className="flex-1 flex items-center justify-center text-white/70 light:text-black/70">
                <Loader loaderText="Loading alert events..." />
            </div>
        )
    }

    if (error) {
        return (
            <FeatureGatedError
                error={error}
                featureKey={FeatureKey.ALERTS}
                featureName="Alerts"
                description="Alert evaluation and routing for production monitoring."
            />
        )
    }

    const firingCount = events?.filter(e => statusStr(e.status) === 'firing').length ?? 0
    const ackCount = events?.filter(e => statusStr(e.status) === 'acknowledged').length ?? 0

    return (
        <TooltipProvider>
            <div className="flex-1 min-h-0 w-full h-full overflow-hidden flex flex-col">
                {/* Filter bar */}
                <div className="shrink-0 flex items-center gap-3 px-3 py-2 border-b border-brand-main-800/40 bg-brand-main-900/20">
                    <span className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">Status</span>
                    <Select value={statusFilter} onValueChange={setStatusFilter}>
                        <SelectTrigger className="h-8 w-[160px] bg-brand-main-900/60 border-brand-main-700 text-xs text-zinc-200 light:text-zinc-800">
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent className="bg-brand-main-900 border-brand-main-600 text-zinc-200 light:text-zinc-800">
                            <SelectItem value="all">All events</SelectItem>
                            <SelectItem value="firing">Firing{firingCount > 0 ? ` (${firingCount})` : ''}</SelectItem>
                            <SelectItem value="acknowledged">Acknowledged{ackCount > 0 ? ` (${ackCount})` : ''}</SelectItem>
                            <SelectItem value="resolved">Resolved</SelectItem>
                        </SelectContent>
                    </Select>
                    <span className="text-[11px] text-white/30 light:text-black/30 ml-auto">
                        {filteredEvents.length} event{filteredEvents.length !== 1 ? 's' : ''}
                    </span>
                </div>

                {/* Log feed */}
                <div className="flex-1 overflow-y-auto scrollbar-macos">
                    {!filteredEvents.length ? (
                        <div className="flex-1 flex items-center justify-center text-white/50 light:text-black/50 py-20">
                            {events?.length ? 'No events match this filter.' : 'No alert events yet. When alerts fire, they will appear here.'}
                        </div>
                    ) : (
                        <div className="divide-y divide-brand-main-800/30">
                            {filteredEvents.map(event => (
                                <HistoryLogRow
                                    key={event.id}
                                    event={event}
                                    onAcknowledge={() => acknowledgeAlert.mutate(event.id)}
                                    onResolve={() => resolveAlert.mutate(event.id)}
                                />
                            ))}
                        </div>
                    )}
                </div>
            </div>
        </TooltipProvider>
    )
}

function HistoryLogRow({ event, onAcknowledge, onResolve }: {
    event: AlertEvent
    onAcknowledge: () => void
    onResolve: () => void
}) {
    const status = statusStr(event.status)
    const severity = severityLabel(event.severity)

    return (
        <div className="group flex items-start gap-3 px-4 py-2.5 hover:bg-brand-main-800/20 transition-colors">
            {/* Status dot + timestamp gutter */}
            <div className="shrink-0 flex flex-col items-center gap-1 pt-0.5 w-[130px]">
                <div className="flex items-center gap-2">
                    <span className={`w-2 h-2 rounded-full shrink-0 ${STATUS_INDICATOR[status] ?? STATUS_INDICATOR.resolved}`} />
                    <span className="text-[11px] text-white/40 light:text-black/40 font-mono tabular-nums">
                        {formatTimestamp(event.firedAt)}
                    </span>
                </div>
            </div>

            {/* Main content */}
            <div className="flex-1 min-w-0 flex flex-col gap-1">
                {/* Rule name + badges */}
                <div className="flex items-center gap-2 flex-wrap">
                    <span className={`${SEVERITY_ICON_COLOR[severity] ?? 'text-white/50 light:text-black/50'}`}>
                        {severity === 'critical' ? <BellRing size={13} /> : severity === 'warning' ? <BellRing size={13} /> : <Clock size={13} />}
                    </span>
                    <span className="text-sm font-medium text-white light:text-brand-main-50 truncate">
                        {event.alertRuleName || 'Unknown Rule'}
                    </span>
                    <span className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${statusBadge(status)}`}>
                        {status}
                    </span>
                    <span className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${severityBadge(event.severity)}`}>
                        {severity}
                    </span>
                </div>

                {/* Metric line */}
                <div className="flex items-center gap-1.5 text-[11px] text-white/40 light:text-black/40 font-mono">
                    <span className="text-white/60 light:text-black/60">{event.metricValue.toFixed(2)}</span>
                    <ArrowRight size={10} className="text-white/25 light:text-black/25" />
                    <span>threshold {event.threshold.toFixed(2)}</span>
                    {event.notificationCount > 1 && (
                        <span className="ml-2 px-1.5 py-0.5 rounded bg-brand-main-700/50 text-brand-main-200 text-[10px] font-medium">
                            {event.notificationCount}x notified
                        </span>
                    )}
                </div>
            </div>

            {/* Actions */}
            <div className="shrink-0 flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
                {status === 'firing' && (
                    <>
                        <Tooltip
                            side="bottom"
                            delayDuration={200}
                            contentClassName="rounded bg-brand-main-600 border-brand-main-500 px-2 py-1 text-xs text-white light:text-brand-main-50"
                            content="Acknowledge — silence re-notifications"
                        >
                            <button
                                type="button"
                                className="p-1.5 rounded hover:bg-yellow-500/20 hover:text-yellow-400 light:hover:text-yellow-700 transition-colors text-brand-main-300"
                                onClick={onAcknowledge}
                            >
                                <Eye size={14} />
                            </button>
                        </Tooltip>
                        <Tooltip
                            side="bottom"
                            delayDuration={200}
                            contentClassName="rounded bg-brand-main-600 border-brand-main-500 px-2 py-1 text-xs text-white light:text-brand-main-50"
                            content="Resolve — mark as no longer firing"
                        >
                            <button
                                type="button"
                                className="p-1.5 rounded hover:bg-green-500/20 hover:text-green-400 light:hover:text-green-600 transition-colors text-brand-main-300"
                                onClick={onResolve}
                            >
                                <CheckCircle size={14} />
                            </button>
                        </Tooltip>
                    </>
                )}
                {status === 'acknowledged' && (
                    <Tooltip
                        side="bottom"
                        delayDuration={200}
                        contentClassName="rounded bg-brand-main-600 border-brand-main-500 px-2 py-1 text-xs text-white light:text-brand-main-50"
                        content="Resolve — mark as no longer firing"
                    >
                        <button
                            type="button"
                            className="p-1.5 rounded hover:bg-green-500/20 hover:text-green-400 light:hover:text-green-600 transition-colors text-brand-main-300"
                            onClick={onResolve}
                        >
                            <CheckCircle size={14} />
                        </button>
                    </Tooltip>
                )}
                {status === 'resolved' && (
                    <span className="text-[10px] text-white/25 light:text-black/25 pr-1">resolved</span>
                )}
            </div>
        </div>
    )
}

// ─── Targets Tab ─────────────────────────────────────────────────────

function TargetsTab() {
    const [editTarget, setEditTarget] = useState<NotificationTarget | null>(null)
    const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null)
    const [deleteConfirmName, setDeleteConfirmName] = useState('')
    const { data: targets, isLoading, error, refetch } = useNotificationTargets()
    const { data: channels } = useChannels()
    const updateTarget = useUpdateNotificationTarget()
    const deleteTargetMutation = useDeleteNotificationTarget()
    const testTarget = useTestNotificationTarget()

    // Map channel config ID → platform number for icon lookup
    const channelPlatformMap = useMemo(() => {
        const map = new Map<string, number>()
        channels?.forEach(ch => { if (ch.platform) map.set(ch.id, ch.platform) })
        return map
    }, [channels])

    if (isLoading) {
        return (
            <div className="flex-1 flex items-center justify-center text-white/70 light:text-black/70">
                <Loader loaderText="Loading notification targets..." />
            </div>
        )
    }

    if (error) {
        return (
            <FeatureGatedError
                error={error}
                featureKey={FeatureKey.ALERTS}
                featureName="Alerts"
                description="Alert evaluation and routing for production monitoring."
            />
        )
    }

    const handleToggleEnabled = async (target: NotificationTarget) => {
        try {
            await updateTarget.mutateAsync({ id: target.id, enabled: !target.enabled })
            toast.success(`Target ${target.enabled ? 'disabled' : 'enabled'} successfully`)
            refetch()
        } catch {
            toast.error('Failed to update notification target')
        }
    }

    const handleDelete = async (id: string) => {
        try {
            await deleteTargetMutation.mutateAsync(id)
            setDeleteConfirmId(null)
            setDeleteConfirmName('')
            toast.success('Notification target deleted successfully')
            refetch()
        } catch {
            toast.error('Failed to delete notification target')
        }
    }

    const handleTest = async (id: string) => {
        try {
            const result = await testTarget.mutateAsync(id)
            if (result.success) {
                toast.success('Test notification sent successfully')
            } else {
                toast.error(`Test failed: ${result.message}`)
            }
        } catch {
            toast.error('Failed to send test notification')
        }
    }

    const columns: ColumnConfig<NotificationTarget>[] = [
        {
            id: 'name',
            header: 'Name',
            width: 220,
            minWidth: 140,
            render: (target) => {
                const platform = target.channelConfigId ? channelPlatformMap.get(target.channelConfigId) : undefined
                const plat = platform ? PLATFORM_ICONS[platform] : undefined

                return (
                    <div className="flex min-w-0 items-center gap-2">
                        {/* Platform / type icon */}
                        {plat ? (
                            <Iconify.Icon icon={plat.icon} className="h-4 w-4 shrink-0 text-white/60 light:text-black/60" />
                        ) : target.targetType === NotificationTargetType.WEBHOOK ? (
                            <Globe className="h-4 w-4 shrink-0 text-amber-400/70 light:text-amber-700/70" />
                        ) : target.targetType === NotificationTargetType.EMAIL ? (
                            <Mail className="h-4 w-4 shrink-0 text-blue-400/70 light:text-blue-600/70" />
                        ) : null}
                        <span className="truncate font-medium text-brand-secondary-100 text-xs">
                            {target.name}
                        </span>
                        {plat && (
                            <span className="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium bg-emerald-500/15 text-emerald-300 light:text-emerald-600">
                                {plat.label}
                            </span>
                        )}
                        {!plat && (
                            <span className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${target.targetType === NotificationTargetType.CHANNEL
                                ? 'bg-emerald-500/15 text-emerald-300 light:text-emerald-600'
                                : target.targetType === NotificationTargetType.WEBHOOK
                                    ? 'bg-amber-500/15 text-amber-300 light:text-amber-700'
                                    : 'bg-blue-500/15 text-blue-300 light:text-blue-600'
                                }`}>
                                {targetTypeLabel(target.targetType)}
                            </span>
                        )}
                    </div>
                )
            },
        },
        {
            id: 'minSeverity',
            header: 'Min Severity',
            width: 120,
            minWidth: 80,
            render: (target) => (
                <span className={`rounded px-1.5 py-0.5 text-[10px] font-medium ${severityBadge(target.minSeverity)}`}>
                    {severityLabel(target.minSeverity)}
                </span>
            ),
        },
        {
            id: 'details',
            header: 'Details',
            width: 200,
            minWidth: 120,
            render: (target) => {
                if (target.channelConfigId) {
                    const ch = channels?.find(c => c.id === target.channelConfigId)
                    return (
                        <span className="truncate text-xs text-brand-main-100">
                            {ch ? ch.name : target.channelConfigId.slice(0, 12) + '...'}
                        </span>
                    )
                }
                if (target.webhookUrl) {
                    return <span className="truncate text-xs text-brand-main-100">Webhook URL configured</span>
                }
                if (target.emailAddresses?.length) {
                    return <span className="truncate text-xs text-brand-main-100">{target.emailAddresses.length} email(s)</span>
                }
                return <span className="text-xs text-white/30 light:text-black/30">—</span>
            },
        },
        {
            id: 'enabled',
            header: 'Enabled',
            width: 80,
            minWidth: 80,
            render: (target) => (
                <div data-row-actions>
                    <Switch
                        checked={target.enabled}
                        onCheckedChange={() => handleToggleEnabled(target)}
                        disabled={updateTarget.isPending}
                    />
                </div>
            ),
        },
        {
            id: 'actions',
            header: '',
            width: 140,
            minWidth: 140,
            maxWidth: 140,
            resizable: false,
            render: (target) => (
                <div className="flex items-center gap-2 justify-center" data-row-actions>
                    <button
                        type="button"
                        className="p-1 rounded hover:bg-blue-500/20 hover:text-blue-400 light:hover:text-blue-600 transition-colors text-brand-main-200"
                        onClick={() => setEditTarget(target)}
                        title="Edit target"
                    >
                        <Pencil size={14} />
                    </button>
                    <button
                        type="button"
                        className="p-1 rounded hover:bg-blue-500/20 hover:text-blue-400 light:hover:text-blue-600 transition-colors text-brand-main-200"
                        onClick={() => handleTest(target.id)}
                        title="Test target"
                    >
                        <Zap size={14} />
                    </button>
                    <button
                        type="button"
                        className="p-1 rounded hover:bg-red-500/20 hover:text-red-400 light:hover:text-red-600 transition-colors text-brand-main-200"
                        onClick={() => {
                            setDeleteConfirmId(target.id)
                            setDeleteConfirmName(target.name)
                        }}
                        title="Delete target"
                    >
                        <Trash2 size={14} />
                    </button>
                </div>
            ),
        },
    ]

    return (
        <div className="flex-1 min-h-0 w-full h-full overflow-hidden flex flex-col">
            <ResponsiveTable
                columns={columns}
                data={targets ?? []}
                enableResizing={true}
                minTableWidth="100%"
                emptyMessage={
                    <div className="flex flex-col items-center justify-center">
                        <div className="relative mb-6">
                            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                                <Iconify.Icon icon="solar:bell-linear" className="size-8 text-brand-secondary-400" />
                            </div>
                        </div>
                        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No notification targets</h3>
                        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                            Create a target to receive alert notifications via your existing channels.
                        </p>
                    </div>
                }
                rowKey={(target) => target.id}
            />

            <NotificationTargetSheet
                mode="edit"
                target={editTarget ?? undefined}
                open={editTarget !== null}
                onOpenChange={(open) => { if (!open) setEditTarget(null) }}
            />

            <Dialog open={deleteConfirmId !== null} onOpenChange={(open) => !open && setDeleteConfirmId(null)}>
                <DialogContent className="w-[500px]">
                    <DialogTitle>Delete Notification Target</DialogTitle>
                    <DialogDescription className="text-brand-main-100">
                        Are you sure you want to delete <strong className="text-brand-main-100">{deleteConfirmName}</strong>? Alert rules referencing this target will no longer send notifications to it.
                    </DialogDescription>
                    <div className="flex justify-end gap-3 mt-4">
                        <Button variant="outline" onClick={() => setDeleteConfirmId(null)} disabled={deleteTargetMutation.isPending}>
                            Cancel
                        </Button>
                        <Button
                            variant="destructive"
                            className="bg-destructive/60 text-brand-main-100 hover:bg-destructive/90"
                            onClick={() => deleteConfirmId && handleDelete(deleteConfirmId)}
                            disabled={deleteTargetMutation.isPending}
                        >
                            {deleteTargetMutation.isPending ? 'Deleting...' : 'Delete'}
                        </Button>
                    </div>
                </DialogContent>
            </Dialog>
        </div>
    )
}

