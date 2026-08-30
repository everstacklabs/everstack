import { useState, useCallback, useEffect } from 'react'
import { useSearch } from '@tanstack/react-router'
import { Button, toast } from '@everstack/ui/components'
import { ui } from '@everstack/ui'
import { type ActionGroup } from '@/components/layout/topbar/types'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import {
    useCreateAlertRule,
    useUpdateAlertRule,
    useCreateNotificationTarget,
    useUpdateNotificationTarget,
    useNotificationTargets,
} from '@/hooks/observability/use-alerts'
import { Iconify } from '@everstack/ui/icons'
import { useChannels, usePlatformChannels } from '@/hooks/deployments/use-channels'
import {
    AlertCategory,
    AlertSeverity,
    ComparisonOperator,
    NotificationTargetType,
} from '@/server/alerts'
import type { AlertRule, NotificationTarget } from '@/server/alerts'

const {
    Input,
    Label,
    Select,
    SelectContent,
    SelectGroup,
    SelectItem,
    SelectLabel,
    SelectTrigger,
    SelectValue,
    Sheet,
    SheetContent,
    SheetFooter,
    SheetHeader,
    SheetTitle,
    Switch,
    Tabs,
    TabsContent,
    TabsList,
    TabsTrigger,
    Textarea,
    Popover,
    PopoverContent,
    PopoverTrigger,
} = ui

// ─── Constants ───────────────────────────────────────────────────────

const METRIC_GROUPS = [
    {
        label: 'Errors',
        metrics: [
            { value: 'error_rate', label: 'Error Rate', unit: '0–1 ratio' },
            { value: 'error_count', label: 'Error Count', unit: 'count' },
        ],
    },
    {
        label: 'Latency',
        metrics: [
            { value: 'avg_latency_ms', label: 'Average Latency', unit: 'ms' },
            { value: 'p50_latency_ms', label: 'P50 Latency', unit: 'ms' },
            { value: 'p95_latency_ms', label: 'P95 Latency', unit: 'ms' },
            { value: 'p99_latency_ms', label: 'P99 Latency', unit: 'ms' },
            { value: 'max_latency_ms', label: 'Max Latency', unit: 'ms' },
        ],
    },
    {
        label: 'Throughput',
        metrics: [
            { value: 'request_count', label: 'Request Count', unit: 'count' },
            { value: 'requests_per_minute', label: 'Requests / min', unit: 'rpm' },
        ],
    },
    {
        label: 'Cost',
        metrics: [
            { value: 'total_cost', label: 'Total Cost', unit: 'USD' },
            { value: 'avg_cost_per_request', label: 'Avg Cost / Request', unit: 'USD' },
            { value: 'cost_savings', label: 'Cost Savings (cache)', unit: 'USD' },
        ],
    },
    {
        label: 'Tokens',
        metrics: [
            { value: 'total_tokens', label: 'Total Tokens', unit: 'count' },
            { value: 'input_tokens', label: 'Input Tokens', unit: 'count' },
            { value: 'output_tokens', label: 'Output Tokens', unit: 'count' },
            { value: 'avg_tokens_per_request', label: 'Avg Tokens / Request', unit: 'count' },
        ],
    },
    {
        label: 'Cache',
        metrics: [
            { value: 'cache_hit_rate', label: 'Cache Hit Rate', unit: '0–1 ratio' },
        ],
    },
]

const PLATFORM_ICONS: Record<number, { icon: string; label: string }> = {
    1: { icon: 'simple-icons:discord', label: 'Discord' },
    2: { icon: 'simple-icons:slack', label: 'Slack' },
    3: { icon: 'simple-icons:telegram', label: 'Telegram' },
}

const ALL_METRICS = METRIC_GROUPS.flatMap(g => g.metrics)

const OPERATORS = [
    { value: ComparisonOperator.GT, label: '>' },
    { value: ComparisonOperator.LT, label: '<' },
    { value: ComparisonOperator.GTE, label: '>=' },
    { value: ComparisonOperator.LTE, label: '<=' },
]

const DURATIONS = [
    { value: 60, label: '1 minute' },
    { value: 300, label: '5 minutes' },
    { value: 600, label: '10 minutes' },
    { value: 900, label: '15 minutes' },
    { value: 1800, label: '30 minutes' },
    { value: 3600, label: '1 hour' },
]

function targetTypeLabel(t: number): string {
    switch (t) {
        case NotificationTargetType.CHANNEL: return 'Channel'
        case NotificationTargetType.WEBHOOK: return 'Webhook'
        case NotificationTargetType.EMAIL: return 'Email'
        default: return 'Unknown'
    }
}

// ─── Target Multi-Select ────────────────────────────────────────────

function TargetMultiSelect({
    targets,
    selected,
    onChange,
}: {
    targets: NotificationTarget[]
    selected: string[]
    onChange: (ids: string[]) => void
}) {
    const [open, setOpen] = useState(false)

    const toggle = (id: string) => {
        onChange(
            selected.includes(id)
                ? selected.filter(s => s !== id)
                : [...selected, id],
        )
    }

    const selectedNames = targets
        .filter(t => selected.includes(t.id))
        .map(t => t.name)

    return (
        <Popover open={open} onOpenChange={setOpen}>
            <PopoverTrigger asChild>
                <button
                    type="button"
                    className="w-full flex items-center justify-between bg-brand-main-900/60 border border-brand-main-600 rounded-md px-3 h-8 text-sm text-zinc-200 light:text-zinc-800 hover:border-brand-main-400 transition-colors"
                >
                    <span className="truncate">
                        {selectedNames.length === 0
                            ? 'Select targets...'
                            : selectedNames.length <= 2
                              ? selectedNames.join(', ')
                              : `${selectedNames.length} targets selected`}
                    </span>
                    <Iconify.Icon icon="lucide:chevrons-up-down" className="h-3.5 w-3.5 shrink-0 text-white/40 light:text-black/40 ml-2" />
                </button>
            </PopoverTrigger>
            <PopoverContent
                className="w-[var(--radix-popover-trigger-width)] p-1 bg-brand-main-900 border-brand-main-600"
                align="start"
            >
                {targets.map(t => (
                    <label
                        key={t.id}
                        className="flex items-center gap-2 px-2 py-1.5 rounded hover:bg-brand-main-800 cursor-pointer text-sm text-zinc-200 light:text-zinc-800"
                    >
                        <input
                            type="checkbox"
                            checked={selected.includes(t.id)}
                            onChange={() => toggle(t.id)}
                            className="accent-brand-secondary-500 h-3.5 w-3.5"
                        />
                        <span className="truncate flex-1">{t.name}</span>
                        <span className="text-[10px] text-white/30 light:text-black/30 shrink-0">
                            {targetTypeLabel(t.targetType)}
                        </span>
                    </label>
                ))}
            </PopoverContent>
        </Popover>
    )
}

const TAB_TRIGGER = 'relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1 px-3'
const SELECT_TRIGGER = 'w-full bg-brand-main-900/60 border-brand-main-600 text-zinc-200 light:text-zinc-800 h-8 text-sm'
const SELECT_CONTENT = 'bg-brand-main-900 border-brand-main-600 text-zinc-200 light:text-zinc-800'
const INPUT_CLASS = 'w-full bg-brand-main-700 border-brand-main-500 text-white light:text-brand-main-50 h-8 text-sm'

// ─── Alert Rule Sheet (Create / Edit) ────────────────────────────────

type AlertRuleSheetProps = {
    open: boolean
    onOpenChange: (open: boolean) => void
    mode: 'create' | 'edit'
    rule?: AlertRule
}

export function AlertRuleSheet({ open, onOpenChange, mode, rule }: AlertRuleSheetProps) {
    const createRule = useCreateAlertRule()
    const updateRule = useUpdateAlertRule()
    const { data: targets } = useNotificationTargets()

    const [activeTab, setActiveTab] = useState('condition')
    const [name, setName] = useState('')
    const [description, setDescription] = useState('')
    const [severity, setSeverity] = useState<number>(AlertSeverity.WARNING)
    const [metric, setMetric] = useState('error_rate')
    const [operator, setOperator] = useState<number>(ComparisonOperator.GT)
    const [threshold, setThreshold] = useState('5')
    const [durationSeconds, setDurationSeconds] = useState(300)
    const [enabled, setEnabled] = useState(true)
    const [selectedTargets, setSelectedTargets] = useState<string[]>([])
    const [filterModel, setFilterModel] = useState('')
    const [filterProvider, setFilterProvider] = useState('')
    const [filterEnvironment, setFilterEnvironment] = useState('')

    const selectedMetric = ALL_METRICS.find(m => m.value === metric)

    const mutation = mode === 'edit' ? updateRule : createRule
    const isPending = mutation.isPending

    // Hydrate form when editing
    useEffect(() => {
        if (mode === 'edit' && rule && open) {
            setName(rule.name)
            setDescription(rule.description ?? '')
            setSeverity(rule.severity)
            setMetric(rule.metric)
            setOperator(rule.operator)
            setThreshold(String(rule.threshold))
            setDurationSeconds(rule.durationSeconds)
            setEnabled(rule.enabled)
            setSelectedTargets(rule.targetIds ?? [])
            const filters = rule.filters as Record<string, string> | undefined
            setFilterModel(filters?.model ?? '')
            setFilterProvider(filters?.provider ?? '')
            setFilterEnvironment(filters?.environment ?? '')
            setActiveTab('condition')
        }
    }, [mode, rule, open])

    const handleClose = useCallback(() => {
        setActiveTab('condition')
        setName('')
        setDescription('')
        setSeverity(AlertSeverity.WARNING)
        setMetric('error_rate')
        setOperator(ComparisonOperator.GT)
        setThreshold('5')
        setDurationSeconds(300)
        setEnabled(true)
        setSelectedTargets([])
        setFilterModel('')
        setFilterProvider('')
        setFilterEnvironment('')
        onOpenChange(false)
    }, [onOpenChange])

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault()
        const filters: Record<string, string> = {}
        if (filterModel.trim()) filters.model = filterModel.trim()
        if (filterProvider.trim()) filters.provider = filterProvider.trim()
        if (filterEnvironment.trim()) filters.environment = filterEnvironment.trim()

        try {
            if (mode === 'edit' && rule) {
                await updateRule.mutateAsync({
                    id: rule.id,
                    name,
                    description,
                    category: AlertCategory.CUSTOM,
                    severity,
                    metric,
                    operator,
                    threshold: parseFloat(threshold),
                    durationSeconds,
                    enabled,
                    targetIds: selectedTargets,
                    filters: Object.keys(filters).length > 0 ? filters : undefined,
                })
                toast.success('Alert rule updated successfully')
            } else {
                await createRule.mutateAsync({
                    name,
                    description,
                    category: AlertCategory.CUSTOM,
                    severity,
                    metric,
                    operator,
                    threshold: parseFloat(threshold),
                    durationSeconds,
                    enabled,
                    targetIds: selectedTargets,
                    filters: Object.keys(filters).length > 0 ? filters : undefined,
                })
                toast.success('Alert rule created successfully')
            }
            handleClose()
        } catch {
            toast.error(mode === 'edit' ? 'Failed to update alert rule' : 'Failed to create alert rule')
        }
    }

    return (
        <Sheet open={open} onOpenChange={onOpenChange}>
            <SheetContent side="right" className="min-w-[520px]">
                <SheetHeader>
                    <SheetTitle>{mode === 'edit' ? 'Edit Alert Rule' : 'Create Alert Rule'}</SheetTitle>
                </SheetHeader>

                {mutation.error && (
                    <div className="rounded-lg border border-red-500/20 bg-red-500/10 p-3 text-sm text-red-400 light:text-red-600 mx-4">
                        {(mutation.error as Error).message}
                    </div>
                )}

                <form onSubmit={handleSubmit} className="flex-1 flex flex-col min-h-0">
                    <Tabs value={activeTab} onValueChange={setActiveTab} className="flex-1 flex flex-col min-h-0">
                        <TabsList className="mt-2 ml-4 w-fit bg-brand-main-800/50 border border-brand-main-600 rounded p-1 h-auto gap-1">
                            <TabsTrigger value="condition" className={TAB_TRIGGER}>Condition</TabsTrigger>
                            <TabsTrigger value="settings" className={TAB_TRIGGER}>Settings</TabsTrigger>
                            <TabsTrigger value="notify" className={TAB_TRIGGER}>Notify</TabsTrigger>
                        </TabsList>

                        <div className="flex-1 overflow-y-auto scrollbar-macos pb-10 px-4">
                            {/* ─── Tab 1: Condition ─── */}
                            <TabsContent value="condition" className="space-y-6 mt-0">
                                <div className="space-y-4 pt-4">
                                    <div className="space-y-2">
                                        <Label className="text-white light:text-brand-main-50 font-medium">Metric</Label>
                                        <Select value={metric} onValueChange={setMetric}>
                                            <SelectTrigger className={SELECT_TRIGGER}>
                                                <SelectValue />
                                            </SelectTrigger>
                                            <SelectContent className={SELECT_CONTENT}>
                                                {METRIC_GROUPS.map(group => (
                                                    <SelectGroup key={group.label}>
                                                        <SelectLabel className="text-white/40 light:text-black/40 text-[10px] uppercase tracking-wider">{group.label}</SelectLabel>
                                                        {group.metrics.map(m => (
                                                            <SelectItem key={m.value} value={m.value}>
                                                                {m.label}
                                                                <span className="ml-1.5 text-white/30 light:text-black/30 text-[10px]">({m.unit})</span>
                                                            </SelectItem>
                                                        ))}
                                                    </SelectGroup>
                                                ))}
                                            </SelectContent>
                                        </Select>
                                    </div>

                                    <div className="grid grid-cols-2 gap-4">
                                        <div className="space-y-2">
                                            <Label className="text-white light:text-brand-main-50 font-medium">Operator</Label>
                                            <Select value={String(operator)} onValueChange={v => setOperator(Number(v))}>
                                                <SelectTrigger className={SELECT_TRIGGER}>
                                                    <SelectValue />
                                                </SelectTrigger>
                                                <SelectContent className={SELECT_CONTENT}>
                                                    {OPERATORS.map(o => (
                                                        <SelectItem key={o.value} value={String(o.value)}>{o.label}</SelectItem>
                                                    ))}
                                                </SelectContent>
                                            </Select>
                                        </div>
                                        <div className="space-y-2">
                                            <Label className="text-white light:text-brand-main-50 font-medium">
                                                Threshold
                                                {selectedMetric?.unit && (
                                                    <span className="ml-1 text-white/30 light:text-black/30 font-normal text-xs">({selectedMetric.unit})</span>
                                                )}
                                            </Label>
                                            <Input
                                                type="number"
                                                step="any"
                                                value={threshold}
                                                onChange={e => setThreshold(e.target.value)}
                                                className={INPUT_CLASS}
                                                required
                                            />
                                        </div>
                                    </div>

                                    <div className="space-y-2">
                                        <Label className="text-white light:text-brand-main-50 font-medium">Evaluation Window</Label>
                                        <Select value={String(durationSeconds)} onValueChange={v => setDurationSeconds(Number(v))}>
                                            <SelectTrigger className={SELECT_TRIGGER}>
                                                <SelectValue />
                                            </SelectTrigger>
                                            <SelectContent className={SELECT_CONTENT}>
                                                {DURATIONS.map(d => (
                                                    <SelectItem key={d.value} value={String(d.value)}>{d.label}</SelectItem>
                                                ))}
                                            </SelectContent>
                                        </Select>
                                    </div>

                                    {/* Condition preview */}
                                    <div className="rounded-lg border border-brand-main-600/30 bg-brand-main-800/20 px-3 py-2.5">
                                        <p className="text-xs text-white/40 light:text-black/40 mb-1">Preview</p>
                                        <p className="text-sm text-white light:text-brand-main-50 font-mono">
                                            Alert when{' '}
                                            <span className="text-brand-secondary-300">{selectedMetric?.label ?? metric}</span>
                                            {' '}{OPERATORS.find(o => o.value === operator)?.label ?? '>'}{' '}
                                            <span className="text-brand-secondary-300">{threshold || '...'}</span>
                                            {selectedMetric?.unit && <span className="text-white/40 light:text-black/40"> {selectedMetric.unit}</span>}
                                            {' '}over{' '}
                                            <span className="text-brand-secondary-300">{DURATIONS.find(d => d.value === durationSeconds)?.label ?? '...'}</span>
                                        </p>
                                    </div>

                                    {/* Dimension filters */}
                                    <div className="space-y-3">
                                        <div className="flex items-center gap-2">
                                            <Label className="text-white light:text-brand-main-50 font-medium">Scope</Label>
                                            <span className="text-[10px] text-white/30 light:text-black/30 uppercase tracking-wider">optional</span>
                                        </div>
                                        <p className="text-xs text-white/40 light:text-black/40 -mt-1">Narrow this alert to specific models, providers, or environments.</p>
                                        <div className="space-y-2">
                                            <Label className="text-white/60 light:text-black/60 text-xs">Model</Label>
                                            <Input
                                                value={filterModel}
                                                onChange={e => setFilterModel(e.target.value)}
                                                placeholder="e.g. gpt-4o, claude-sonnet-4-20250514"
                                                className={INPUT_CLASS}
                                            />
                                        </div>
                                        <div className="space-y-2">
                                            <Label className="text-white/60 light:text-black/60 text-xs">Provider</Label>
                                            <Input
                                                value={filterProvider}
                                                onChange={e => setFilterProvider(e.target.value)}
                                                placeholder="e.g. openai, anthropic"
                                                className={INPUT_CLASS}
                                            />
                                        </div>
                                        <div className="space-y-2">
                                            <Label className="text-white/60 light:text-black/60 text-xs">Environment</Label>
                                            <Input
                                                value={filterEnvironment}
                                                onChange={e => setFilterEnvironment(e.target.value)}
                                                placeholder="e.g. production, staging"
                                                className={INPUT_CLASS}
                                            />
                                        </div>
                                    </div>
                                </div>
                            </TabsContent>

                            {/* ─── Tab 2: Settings ─── */}
                            <TabsContent value="settings" className="space-y-6 mt-0">
                                <div className="space-y-4 pt-4">
                                    <div className="space-y-2">
                                        <Label className="text-white light:text-brand-main-50 font-medium">Name</Label>
                                        <Input
                                            value={name}
                                            onChange={e => setName(e.target.value)}
                                            placeholder="High Error Rate on GPT-4"
                                            className={INPUT_CLASS}
                                            required
                                        />
                                    </div>
                                    <div className="space-y-2">
                                        <Label className="text-white light:text-brand-main-50 font-medium">Description</Label>
                                        <Textarea
                                            value={description}
                                            onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) => setDescription(e.target.value)}
                                            placeholder="Fires when the error rate exceeds the threshold over the evaluation window"
                                            rows={3}
                                            className="w-full bg-brand-main-700 border-brand-main-500 text-white light:text-brand-main-50 text-sm"
                                        />
                                    </div>
                                    <div className="space-y-2">
                                        <Label className="text-white light:text-brand-main-50 font-medium">Severity</Label>
                                        <Select value={String(severity)} onValueChange={v => setSeverity(Number(v))}>
                                            <SelectTrigger className={SELECT_TRIGGER}>
                                                <SelectValue />
                                            </SelectTrigger>
                                            <SelectContent className={SELECT_CONTENT}>
                                                <SelectItem value={String(AlertSeverity.CRITICAL)}>Critical</SelectItem>
                                                <SelectItem value={String(AlertSeverity.WARNING)}>Warning</SelectItem>
                                                <SelectItem value={String(AlertSeverity.INFO)}>Info</SelectItem>
                                            </SelectContent>
                                        </Select>
                                    </div>
                                    <div className="flex items-center gap-2 pt-2">
                                        <Switch checked={enabled} onCheckedChange={setEnabled} />
                                        <Label className="text-white light:text-brand-main-50 font-medium">Enabled</Label>
                                    </div>
                                </div>
                            </TabsContent>

                            {/* ─── Tab 3: Notify ─── */}
                            <TabsContent value="notify" className="space-y-6 mt-0">
                                <div className="space-y-4 pt-4">
                                    {targets && targets.length > 0 ? (
                                        <div className="space-y-3">
                                            <Label className="text-white light:text-brand-main-50 font-medium">Notification Targets</Label>
                                            <p className="text-xs text-white/40 light:text-black/40">Select which targets should receive notifications when this alert fires.</p>
                                            <TargetMultiSelect
                                                targets={targets}
                                                selected={selectedTargets}
                                                onChange={setSelectedTargets}
                                            />
                                        </div>
                                    ) : (
                                        <div className="rounded-lg border border-brand-main-700 bg-brand-main-900/40 p-6 text-center">
                                            <p className="text-sm text-white/50 light:text-black/50">No notification targets configured yet.</p>
                                            <p className="text-xs text-white/30 light:text-black/30 mt-1">Create a target in the Targets tab to receive alert notifications.</p>
                                        </div>
                                    )}
                                </div>
                            </TabsContent>
                        </div>
                    </Tabs>

                    <SheetFooter className="flex items-center justify-center px-6 py-2 mt-auto w-full">
                        <div className="flex items-center justify-end gap-2 w-full">
                            <Button
                                type="button"
                                variant="outline"
                                onClick={handleClose}
                                className="text-white light:text-brand-main-50 w-1/2 hover:bg-brand-main-800"
                            >
                                Cancel
                            </Button>
                            <Button
                                type="submit"
                                className="w-1/2"
                                disabled={isPending || !name}
                            >
                                {isPending
                                    ? (mode === 'edit' ? 'Saving...' : 'Creating...')
                                    : (mode === 'edit' ? 'Save Changes' : 'Create Rule')}
                            </Button>
                        </div>
                    </SheetFooter>
                </form>
            </SheetContent>
        </Sheet>
    )
}

// ─── Notification Target Sheet (Create / Edit) ──────────────────────

type NotificationTargetSheetProps = {
    open: boolean
    onOpenChange: (open: boolean) => void
    mode: 'create' | 'edit'
    target?: NotificationTarget
}

export function NotificationTargetSheet({ open, onOpenChange, mode, target }: NotificationTargetSheetProps) {
    const createTarget = useCreateNotificationTarget()
    const updateTarget = useUpdateNotificationTarget()
    const { data: channels } = useChannels()

    const [name, setName] = useState('')
    const [targetType, setTargetType] = useState<number>(NotificationTargetType.CHANNEL)
    const [channelConfigId, setChannelConfigId] = useState('')

    const { data: platformChannels, isLoading: platformChannelsLoading } = usePlatformChannels(channelConfigId)
    const [platformChannelRef, setPlatformChannelRef] = useState('')
    const [webhookUrl, setWebhookUrl] = useState('')

    // Clear platform channel ref when channel config changes (so stale IDs from another platform don't persist)
    const handleChannelConfigChange = useCallback((id: string) => {
        setChannelConfigId(id)
        setPlatformChannelRef('')
    }, [])
    const [minSeverity, setMinSeverity] = useState<number>(AlertSeverity.WARNING)
    const [enabled, setEnabled] = useState(true)

    const mutation = mode === 'edit' ? updateTarget : createTarget
    const isPending = mutation.isPending

    // Hydrate form when editing
    useEffect(() => {
        if (mode === 'edit' && target && open) {
            setName(target.name)
            setTargetType(target.targetType)
            setChannelConfigId(target.channelConfigId ?? '')
            setPlatformChannelRef(target.platformChannelRef ?? '')
            setWebhookUrl(target.webhookUrl ?? '')
            setMinSeverity(target.minSeverity)
            setEnabled(target.enabled)
        }
    }, [mode, target, open])

    const handleClose = useCallback(() => {
        setName('')
        setTargetType(NotificationTargetType.CHANNEL)
        setChannelConfigId('')
        setPlatformChannelRef('')
        setWebhookUrl('')
        setMinSeverity(AlertSeverity.WARNING)
        setEnabled(true)
        onOpenChange(false)
    }, [onOpenChange])

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault()
        try {
            if (mode === 'edit' && target) {
                await updateTarget.mutateAsync({
                    id: target.id,
                    name,
                    targetType,
                    channelConfigId: targetType === NotificationTargetType.CHANNEL ? channelConfigId : undefined,
                    platformChannelRef: targetType === NotificationTargetType.CHANNEL && platformChannelRef ? platformChannelRef : undefined,
                    webhookUrl: targetType === NotificationTargetType.WEBHOOK ? webhookUrl : undefined,
                    minSeverity,
                    enabled,
                })
                toast.success('Notification target updated successfully')
            } else {
                await createTarget.mutateAsync({
                    name,
                    targetType,
                    channelConfigId: targetType === NotificationTargetType.CHANNEL ? channelConfigId : undefined,
                    platformChannelRef: targetType === NotificationTargetType.CHANNEL && platformChannelRef ? platformChannelRef : undefined,
                    webhookUrl: targetType === NotificationTargetType.WEBHOOK ? webhookUrl : undefined,
                    minSeverity,
                    enabled,
                })
                toast.success('Notification target created successfully')
            }
            handleClose()
        } catch {
            toast.error(mode === 'edit' ? 'Failed to update notification target' : 'Failed to create notification target')
        }
    }

    return (
        <Sheet open={open} onOpenChange={onOpenChange}>
            <SheetContent side="right" className="min-w-[480px]">
                <SheetHeader>
                    <SheetTitle>{mode === 'edit' ? 'Edit Notification Target' : 'Create Notification Target'}</SheetTitle>
                </SheetHeader>

                {mutation.error && (
                    <div className="rounded-lg border border-red-500/20 bg-red-500/10 p-3 text-sm text-red-400 light:text-red-600 mx-4">
                        {(mutation.error as Error).message}
                    </div>
                )}

                <form onSubmit={handleSubmit} className="flex-1 flex flex-col min-h-0">
                    <div className="flex-1 overflow-y-auto scrollbar-macos pb-10 px-4">
                        <div className="space-y-4 pt-4">
                            <div className="space-y-2">
                                <Label className="text-white light:text-brand-main-50 font-medium">Name</Label>
                                <Input
                                    value={name}
                                    onChange={e => setName(e.target.value)}
                                    placeholder="Slack Alerts"
                                    className={INPUT_CLASS}
                                    required
                                />
                            </div>
                            <div className="space-y-2">
                                <Label className="text-white light:text-brand-main-50 font-medium">Type</Label>
                                <Select value={String(targetType)} onValueChange={v => setTargetType(Number(v))}>
                                    <SelectTrigger className={SELECT_TRIGGER}>
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent className={SELECT_CONTENT}>
                                        <SelectItem value={String(NotificationTargetType.CHANNEL)}>Channel (Slack/Discord/Telegram)</SelectItem>
                                        <SelectItem value={String(NotificationTargetType.WEBHOOK)}>Webhook</SelectItem>
                                        <SelectItem value={String(NotificationTargetType.EMAIL)}>Email (coming soon)</SelectItem>
                                    </SelectContent>
                                </Select>
                            </div>

                            {targetType === NotificationTargetType.CHANNEL && (
                                <>
                                    <div className="space-y-2">
                                        <Label className="text-white light:text-brand-main-50 font-medium">Channel Config</Label>
                                        <Select value={channelConfigId} onValueChange={handleChannelConfigChange}>
                                            <SelectTrigger className={SELECT_TRIGGER}>
                                                <SelectValue placeholder="Select a channel..." />
                                            </SelectTrigger>
                                            <SelectContent className={SELECT_CONTENT}>
                                                {channels?.map(ch => {
                                                    const plat = ch.platform ? PLATFORM_ICONS[ch.platform] : undefined
                                                    return (
                                                        <SelectItem key={ch.id} value={ch.id}>
                                                            <span className="inline-flex items-center gap-1.5">
                                                                {plat && <Iconify.Icon icon={plat.icon} className="h-3.5 w-3.5 shrink-0" />}
                                                                {ch.name}
                                                            </span>
                                                        </SelectItem>
                                                    )
                                                })}
                                            </SelectContent>
                                        </Select>
                                    </div>
                                    <div className="space-y-2">
                                        <Label className="text-white light:text-brand-main-50 font-medium">Platform Channel/Room</Label>
                                        {platformChannels && platformChannels.length > 0 ? (
                                            <Select value={platformChannelRef} onValueChange={setPlatformChannelRef}>
                                                <SelectTrigger className={SELECT_TRIGGER}>
                                                    <SelectValue placeholder="Select a channel..." />
                                                </SelectTrigger>
                                                <SelectContent className={SELECT_CONTENT}>
                                                    {platformChannels.map(ch => (
                                                        <SelectItem key={ch.id} value={ch.id}>
                                                            <span className="inline-flex items-center gap-1.5">
                                                                {ch.name}
                                                                <span className="text-white/30 light:text-black/30 text-[10px]">({ch.type})</span>
                                                            </span>
                                                        </SelectItem>
                                                    ))}
                                                </SelectContent>
                                            </Select>
                                        ) : platformChannelsLoading && channelConfigId ? (
                                            <Input
                                                value=""
                                                disabled
                                                placeholder="Loading channels..."
                                                className={INPUT_CLASS}
                                            />
                                        ) : (
                                            <Input
                                                value={platformChannelRef}
                                                onChange={e => setPlatformChannelRef(e.target.value)}
                                                placeholder="e.g. #alerts or channel ID"
                                                className={INPUT_CLASS}
                                                required
                                            />
                                        )}
                                        <p className="text-xs text-white/30 light:text-black/30">
                                            {platformChannels && platformChannels.length > 0
                                                ? 'Select the channel to send alert notifications to.'
                                                : 'The Slack channel name, Discord channel ID, or Telegram chat ID to send alerts to.'}
                                        </p>
                                    </div>
                                </>
                            )}

                            {targetType === NotificationTargetType.WEBHOOK && (
                                <div className="space-y-2">
                                    <Label className="text-white light:text-brand-main-50 font-medium">Webhook URL</Label>
                                    <Input
                                        type="url"
                                        value={webhookUrl}
                                        onChange={e => setWebhookUrl(e.target.value)}
                                        placeholder="https://hooks.example.com/alerts"
                                        className={INPUT_CLASS}
                                        required
                                    />
                                </div>
                            )}

                            <div className="space-y-2">
                                <Label className="text-white light:text-brand-main-50 font-medium">Minimum Severity</Label>
                                <Select value={String(minSeverity)} onValueChange={v => setMinSeverity(Number(v))}>
                                    <SelectTrigger className={SELECT_TRIGGER}>
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent className={SELECT_CONTENT}>
                                        <SelectItem value={String(AlertSeverity.CRITICAL)}>Critical only</SelectItem>
                                        <SelectItem value={String(AlertSeverity.WARNING)}>Warning and above</SelectItem>
                                        <SelectItem value={String(AlertSeverity.INFO)}>All (info and above)</SelectItem>
                                    </SelectContent>
                                </Select>
                                <p className="text-xs text-white/30 light:text-black/30">Only alerts at or above this severity will be sent to this target.</p>
                            </div>

                            <div className="flex items-center gap-2 pt-2">
                                <Switch checked={enabled} onCheckedChange={setEnabled} />
                                <Label className="text-white light:text-brand-main-50 font-medium">Enabled</Label>
                            </div>
                        </div>
                    </div>

                    <SheetFooter className="flex items-center justify-center px-6 py-2 mt-auto w-full">
                        <div className="flex items-center justify-end gap-2 w-full">
                            <Button
                                type="button"
                                variant="outline"
                                onClick={handleClose}
                                className="text-white light:text-brand-main-50 w-1/2 hover:bg-brand-main-800"
                            >
                                Cancel
                            </Button>
                            <Button
                                type="submit"
                                className="w-1/2"
                                disabled={isPending || !name}
                            >
                                {isPending
                                    ? (mode ==='edit' ? 'Saving...' : 'Creating...')
                                    : (mode === 'edit' ? 'Save Changes' : 'Create Target')}
                            </Button>
                        </div>
                    </SheetFooter>
                </form>
            </SheetContent>
        </Sheet>
    )
}

// ─── Topbar Action Button (switches based on active tab) ─────────────

function AlertsActionButton() {
    const gate = useFeatureGate(FeatureKey.ALERTS)
    const search = useSearch({ strict: false }) as { tab?: string }
    const activeTab = search.tab ?? 'rules'

    const [ruleSheetOpen, setRuleSheetOpen] = useState(false)
    const [targetSheetOpen, setTargetSheetOpen] = useState(false)

    if (gate.isBlocked) return null

    if (activeTab === 'targets') {
        return (
            <>
                <Button variant="default" onClick={() => setTargetSheetOpen(true)}>
                    Create Target
                </Button>
                <NotificationTargetSheet mode="create" open={targetSheetOpen} onOpenChange={setTargetSheetOpen} />
            </>
        )
    }

    return (
        <>
            <Button variant="default" onClick={() => setRuleSheetOpen(true)}>
                Create Alert Rule
            </Button>
            <AlertRuleSheet mode="create" open={ruleSheetOpen} onOpenChange={setRuleSheetOpen} />
        </>
    )
}

export const ObservabilityAlertsActions: ActionGroup[] = [
    {
        title: 'Alerts',
        actions: [
            {
                type: 'custom',
                key: 'alerts-action',
                label: 'Alerts Action',
                component: AlertsActionButton,
            },
        ],
    },
]
