import { Link } from '@tanstack/react-router'
import { ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import {
    Check,
    Copy,
    Database,
    Loader2,
    Play,
    Save,
    SlidersHorizontal,
    Square,
    Timer,
    X,
} from 'lucide-react'
import { useState } from 'react'
import { cn } from '@everstack/utils/functions/cn'
import type { PlaygroundVariant, TemplatingEngine, OutputFormat } from '@/stores/playground-store'
import {
    isQuickRunRunnable,
    taskReadinessError,
    type VariantRunState,
} from '@/stores/playground-run-store'
import { Globe, MessageSquare, Triangle, Workflow as WorkflowIcon } from 'lucide-react'
import type { TaskType } from '@/stores/playground-store'
import { MessageComposer } from './message-composer'
import { PromptDiff } from './prompt-diff'
import { TaskTestPanel } from './task-test-panel'
import { TaskTypeEditor } from './task-type-editor'

const TYPE_META: Record<TaskType, { label: string; icon: typeof MessageSquare; cls: string }> = {
    prompt: { label: 'Prompt', icon: MessageSquare, cls: 'text-brand-secondary-300 bg-brand-secondary-500/10' },
    workflow: { label: 'Workflow', icon: WorkflowIcon, cls: 'text-amber-300 bg-amber-400/10' },
    remote: { label: 'Remote eval', icon: Globe, cls: 'text-sky-300 bg-sky-400/10' },
    scorer: { label: 'Scorer', icon: Triangle, cls: 'text-emerald-300 bg-emerald-400/10' },
}

function TaskTypeBadge({ type }: { type: TaskType }) {
    const m = TYPE_META[type]
    const Icon = m.icon
    return (
        <span className={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide ${m.cls}`}>
            <Icon className="h-3 w-3" /> {m.label}
        </span>
    )
}

const {
    Card,
    CardContent,
    Popover,
    PopoverContent,
    PopoverTrigger,
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} = ui

const TEMPLATING_LABELS: Record<TemplatingEngine, string> = {
    mustache: 'Mustache',
    jinja: 'Jinja',
    none: 'No templating',
}

type VariantColumnProps = {
    index: number
    variant: PlaygroundVariant
    run?: VariantRunState
    modelOptions: string[]
    canRemove: boolean
    /** When set (diff mode on, this is a comparison task), show a YAML diff vs base. */
    diffBase?: PlaygroundVariant
    onChange: (patch: Partial<Omit<PlaygroundVariant, 'id' | 'messages'>>) => void
    onRemove: () => void
    onRun: () => void
    onStop: () => void
    onAddToDataset?: () => void
    /** Open the Save-prompt dialog for this task's prompt. */
    onSavePrompt?: () => void
}

function ParamsPopover({
    variant,
    onChange,
}: Pick<VariantColumnProps, 'variant' | 'onChange'>) {
    return (
        <Popover>
            <PopoverTrigger asChild>
                <button
                    type="button"
                    className="p-1 rounded text-white/40 hover:text-white hover:bg-brand-main-700/60 transition-colors light:text-black/40 light:hover:text-brand-main-50"
                    title="Sampling parameters"
                >
                    <SlidersHorizontal className="h-3.5 w-3.5" />
                </button>
            </PopoverTrigger>
            <PopoverContent
                align="end"
                className="w-64 bg-brand-main-900 border-brand-main-500 p-3 space-y-3"
            >
                <div className="space-y-1">
                    <label className="text-[10px] text-white/40 uppercase tracking-wide flex justify-between light:text-black/40">
                        <span>Temperature</span>
                        <span className="font-mono text-white/70 light:text-black/70">
                            {variant.temperature.toFixed(2)}
                        </span>
                    </label>
                    <input
                        type="range"
                        min={0}
                        max={2}
                        step={0.05}
                        value={variant.temperature}
                        onChange={(e) => onChange({ temperature: Number(e.target.value) })}
                        className="w-full accent-brand-secondary-500"
                    />
                </div>
                <div className="space-y-1">
                    <label className="text-[10px] text-white/40 uppercase tracking-wide flex justify-between light:text-black/40">
                        <span>Top P</span>
                        <span className="font-mono text-white/70 light:text-black/70">
                            {variant.topP !== undefined ? variant.topP.toFixed(2) : 'default'}
                        </span>
                    </label>
                    <input
                        type="range"
                        min={0}
                        max={1}
                        step={0.01}
                        value={variant.topP ?? 1}
                        onChange={(e) => onChange({ topP: Number(e.target.value) })}
                        className="w-full accent-brand-secondary-500"
                    />
                </div>
                <div className="space-y-1">
                    <label className="text-[10px] text-white/40 uppercase tracking-wide light:text-black/40">
                        Max tokens
                    </label>
                    <input
                        type="number"
                        min={1}
                        value={variant.maxTokens ?? ''}
                        placeholder="default"
                        onChange={(e) =>
                            onChange({
                                maxTokens: e.target.value
                                    ? Math.max(1, Number(e.target.value))
                                    : undefined,
                            })
                        }
                        className="w-full bg-brand-main-700/60 text-xs text-zinc-200 rounded px-2 py-1.5 border border-brand-main-500 focus:border-brand-secondary-500 outline-none font-mono"
                    />
                </div>
            </PopoverContent>
        </Popover>
    )
}

export function VariantColumn({
    index,
    variant,
    run,
    modelOptions,
    canRemove,
    diffBase,
    onChange,
    onRemove,
    onRun,
    onStop,
    onAddToDataset,
    onSavePrompt,
}: VariantColumnProps) {
    const [copied, setCopied] = useState(false)
    const [showTest, setShowTest] = useState(false)
    const status = run?.status ?? 'idle'
    const streaming = status === 'running'
    const quickRunReady = isQuickRunRunnable(variant)
    const quickRunError = taskReadinessError(variant, 'quick')

    const copyOutput = async () => {
        if (!run?.text) return
        try {
            await navigator.clipboard.writeText(run.text)
            setCopied(true)
            setTimeout(() => setCopied(false), 1200)
        } catch {
            toast.error('Could not copy output')
        }
    }

    // Non-prompt tasks get their type-specific config editor. Workflow tasks can
    // be tested with sample input and run across datasets; remote/scorer tasks
    // stay config-only until backend runners exist.
    if (variant.type !== 'prompt') {
        return (
            <Card className="border-brand-main-500 bg-brand-main-900/40 flex-1 min-w-[300px] overflow-hidden flex flex-col">
                <div className="flex items-center gap-2 px-3 py-2.5 border-b border-brand-main-700">
                    <span className="text-[10px] font-mono text-white/30 shrink-0 light:text-black/30">
                        {String.fromCharCode(65 + index)}
                    </span>
                    <TaskTypeBadge type={variant.type} />
                    {variant.type === 'workflow' && (
                        <button
                            type="button"
                            onClick={() => setShowTest((value) => !value)}
                            className={`ml-auto inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] transition-colors ${
                                showTest
                                    ? 'text-brand-secondary-200 bg-brand-secondary-500/10'
                                    : 'text-white/40 hover:text-white light:text-black/40 light:hover:text-black'
                            }`}
                            title="Test this workflow against a sample input"
                        >
                            <Play className="h-3 w-3" /> Test
                        </button>
                    )}
                    {canRemove && (
                        <button
                            type="button"
                            onClick={onRemove}
                            className={`${variant.type === 'workflow' ? '' : 'ml-auto'} p-1 rounded text-white/30 hover:text-rose-400 transition-colors light:text-black/30`}
                            title="Remove task"
                        >
                            <X className="h-3.5 w-3.5" />
                        </button>
                    )}
                </div>
                <div className="p-3.5 flex-1 overflow-auto">
                    <TaskTypeEditor variant={variant} />
                    {variant.type === 'workflow' && showTest && (
                        <div className="mt-4 border-t border-brand-main-700 pt-3">
                            <TaskTestPanel variant={variant} />
                        </div>
                    )}
                </div>
            </Card>
        )
    }

    return (
        <Card className="border-brand-main-500 bg-brand-main-900/40 flex-1 min-w-[300px] overflow-hidden flex flex-col">
            {/* Header: model picker + params + run controls */}
            <div className="flex items-center gap-1.5 px-2.5 py-2 border-b border-brand-main-700">
                <span className="text-[10px] font-mono text-white/30 shrink-0 light:text-black/30">
                    {String.fromCharCode(65 + index)}
                </span>
                <TaskTypeBadge type="prompt" />
                <div className="flex-1 min-w-0">
                    {modelOptions.length > 0 ? (
                        <Select
                            value={variant.model}
                            onValueChange={(model) => onChange({ model })}
                        >
                            <SelectTrigger className="h-7 bg-brand-main-700/60 border-brand-main-500 text-xs">
                                <SelectValue placeholder="Pick a model" />
                            </SelectTrigger>
                            <SelectContent className="bg-brand-main-900 border-brand-main-500">
                                {modelOptions.map((m) => (
                                    <SelectItem key={m} value={m} className="text-xs text-white/80 light:text-black/80">
                                        {m}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    ) : (
                        <input
                            type="text"
                            value={variant.model}
                            onChange={(e) => onChange({ model: e.target.value })}
                            placeholder="@openai/gpt-4o-mini"
                            className="w-full h-7 bg-brand-main-700/60 text-xs text-zinc-200 rounded px-2 border border-brand-main-500 focus:border-brand-secondary-500 outline-none font-mono"
                        />
                    )}
                </div>
                <ParamsPopover variant={variant} onChange={onChange} />
                {streaming ? (
                    <button
                        type="button"
                        onClick={onStop}
                        className="p-1 rounded text-rose-400 hover:bg-rose-500/10 transition-colors"
                        title="Stop"
                    >
                        <Square className="h-3.5 w-3.5" />
                    </button>
                ) : (
                    <button
                        type="button"
                        onClick={onRun}
                        disabled={!quickRunReady}
                        className="p-1 rounded text-brand-secondary-300 hover:bg-brand-secondary-500/10 disabled:text-white/20 disabled:hover:bg-transparent transition-colors light:disabled:text-black/20"
                        title={quickRunError ?? 'Run this variant'}
                    >
                        <Play className="h-3.5 w-3.5" />
                    </button>
                )}
                {canRemove && (
                    <button
                        type="button"
                        onClick={onRemove}
                        className="p-1 rounded text-white/30 hover:text-rose-400 transition-colors light:text-black/30"
                        title="Remove variant"
                    >
                        <X className="h-3.5 w-3.5" />
                    </button>
                )}
            </div>

            {/* Messages: this task's prompt — or a read-only YAML diff vs base */}
            {diffBase ? (
                <div className="border-b border-brand-main-700 h-[45%] min-h-[160px] overflow-hidden">
                    <PromptDiff base={diffBase} variant={variant} />
                    <div className="px-2.5 py-1 text-[10px] text-white/30 light:text-black/30">
                        Diff vs base task. Turn off Diff to edit.
                    </div>
                </div>
            ) : (
                <div className="border-b border-brand-main-700 max-h-[45%] overflow-auto p-2.5">
                    <div className="flex items-center justify-between mb-1.5 gap-2">
                        <span className="text-[10px] uppercase tracking-wide text-white/40 light:text-black/40">
                            Messages
                        </span>
                        <div className="flex items-center gap-1.5">
                            <button
                                type="button"
                                onClick={() => setShowTest((v) => !v)}
                                className={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] transition-colors ${
                                    showTest
                                        ? 'text-brand-secondary-200 bg-brand-secondary-500/10'
                                        : 'text-white/40 hover:text-white light:text-black/40 light:hover:text-black'
                                }`}
                                title="Test this prompt against a sample input"
                            >
                                <Play className="h-3 w-3" /> Test
                            </button>
                            {onSavePrompt && (
                                <button
                                    type="button"
                                    onClick={onSavePrompt}
                                    className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] text-white/40 hover:text-white light:text-black/40 light:hover:text-black transition-colors"
                                    title="Save this task's prompt to the library"
                                >
                                    <Save className="h-3 w-3" /> Save prompt
                                </button>
                            )}
                        <Select
                            value={variant.outputFormat ?? 'text'}
                            onValueChange={(f) => onChange({ outputFormat: f as OutputFormat })}
                        >
                            <SelectTrigger className="h-6 w-auto gap-1 bg-transparent border-brand-main-600 text-[10px] px-1.5 py-0 text-white/50 light:text-black/50">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent className="bg-brand-main-900 border-brand-main-500">
                                <SelectItem value="text" className="text-xs text-white/80 light:text-black/80">
                                    Text output
                                </SelectItem>
                                <SelectItem value="json_object" className="text-xs text-white/80 light:text-black/80">
                                    JSON output
                                </SelectItem>
                            </SelectContent>
                        </Select>
                        <Select
                            value={variant.templating}
                            onValueChange={(t) => onChange({ templating: t as TemplatingEngine })}
                        >
                            <SelectTrigger className="h-6 w-auto gap-1 bg-transparent border-brand-main-600 text-[10px] px-1.5 py-0 text-white/50 light:text-black/50">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent className="bg-brand-main-900 border-brand-main-500">
                                {(['mustache', 'jinja', 'none'] as TemplatingEngine[]).map((t) => (
                                    <SelectItem key={t} value={t} className="text-xs text-white/80 light:text-black/80">
                                        {TEMPLATING_LABELS[t]}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                        </div>
                    </div>
                    <MessageComposer variantId={variant.id} />
                    {showTest && <TaskTestPanel variant={variant} />}
                </div>
            )}

            {/* Output (quick single-run preview; grid runs live below) */}
            <CardContent className="flex-1 overflow-auto font-mono text-xs !p-3">
                {status === 'error' ? (
                    <pre className="whitespace-pre-wrap text-rose-300">{run?.error}</pre>
                ) : run?.text ? (
                    <pre className="whitespace-pre-wrap text-zinc-100 leading-relaxed">
                        {run.text}
                        {streaming && (
                            <span className="inline-block w-1.5 h-3 bg-brand-secondary-400/70 ml-px align-middle animate-pulse" />
                        )}
                    </pre>
                ) : streaming ? (
                    <div className="flex items-center gap-2 text-white/40 light:text-black/40">
                        <Loader2 className="h-3 w-3 animate-spin" /> Waiting for first token…
                    </div>
                ) : (
                    <div className="text-white/25 light:text-black/25">
                        {quickRunError ?? 'Run to see the response.'}
                    </div>
                )}
            </CardContent>

            {/* Footer: timings + trace link + copy */}
            <div
                className={cn(
                    'flex items-center gap-3 px-2.5 py-1.5 border-t border-brand-main-700 text-[10px] text-white/40 light:text-black/40',
                    status === 'idle' && !run?.text && 'opacity-0 pointer-events-none',
                )}
            >
                {status === 'aborted' && <span className="text-amber-300/80">stopped</span>}
                {run?.ttftMs !== undefined && (
                    <span className="inline-flex items-center gap-1 font-mono">
                        <Timer className="h-3 w-3" />
                        {run.ttftMs}ms first token
                    </span>
                )}
                {run?.durationMs !== undefined && (
                    <span className="font-mono">{(run.durationMs / 1000).toFixed(2)}s total</span>
                )}
                <span className="ml-auto inline-flex items-center gap-2">
                    {run?.traceId && (
                        <Link
                            to="/observability/traces"
                            search={(p: Record<string, unknown>) => ({
                                ...p,
                                trace: run.traceId,
                            })}
                            className="font-mono hover:text-white transition-colors light:hover:text-brand-main-50"
                        >
                            trace {run.traceId.slice(0, 8)}…
                        </Link>
                    )}
                    {run?.text && !streaming && onAddToDataset && (
                        <button
                            type="button"
                            onClick={onAddToDataset}
                            className="p-0.5 rounded hover:text-white transition-colors light:hover:text-brand-main-50"
                            title="Add this run to a dataset"
                        >
                            <Database className="h-3 w-3" />
                        </button>
                    )}
                    {run?.text && !streaming && (
                        <button
                            type="button"
                            onClick={copyOutput}
                            className="p-0.5 rounded hover:text-white transition-colors light:hover:text-brand-main-50"
                            title="Copy output"
                        >
                            {copied ? (
                                <Check className="h-3 w-3 text-emerald-400" />
                            ) : (
                                <Copy className="h-3 w-3" />
                            )}
                        </button>
                    )}
                </span>
            </div>
        </Card>
    )
}
