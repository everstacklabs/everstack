import { ui } from '@everstack/ui'
import { Globe, Workflow as WorkflowIcon } from 'lucide-react'
import { useScoreConfigs } from '@/hooks/evaluations/use-score-configs'
import { useWorkflows } from '@/hooks/deployments/use-workflows'
import { usePlaygroundStore, type PlaygroundVariant } from '@/stores/playground-store'

const { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } = ui

const fieldCls =
    'w-full rounded border border-brand-main-600 bg-brand-main-800/40 px-2.5 py-1.5 text-xs text-white/85 light:text-black/85 outline-none focus:border-brand-secondary-500/50'
const labelCls = 'text-[10px] uppercase tracking-wide text-white/40 light:text-black/40'

/**
 * Config editor for a non-prompt task. Workflow tasks are executable through
 * the backend workflow runner; remote/scorer-column tasks remain config-only
 * until their backend runners exist.
 */
export function TaskTypeEditor({ variant }: { variant: PlaygroundVariant }) {
    const updateTaskConfig = usePlaygroundStore((s) => s.updateTaskConfig)

    if (variant.type === 'workflow') {
        return <WorkflowTaskEditor variant={variant} />
    }

    if (variant.type === 'remote') {
        const r = variant.remote ?? {}
        return (
            <div className="flex flex-col gap-3">
                <div className="flex flex-col gap-1.5">
                    <span className={labelCls}>Endpoint</span>
                    <input
                        value={r.url ?? ''}
                        onChange={(e) => updateTaskConfig(variant.id, { remote: { url: e.target.value } })}
                        placeholder="https://api.your-app.com/v1/run"
                        className={`${fieldCls} font-mono`}
                    />
                </div>
                <div className="flex flex-col gap-1.5">
                    <span className={labelCls}>Auth header</span>
                    <input
                        value={r.auth ?? ''}
                        onChange={(e) => updateTaskConfig(variant.id, { remote: { auth: e.target.value } })}
                        placeholder="Bearer …"
                        className={`${fieldCls} font-mono`}
                    />
                </div>
                <div className="grid grid-cols-2 gap-2">
                    <div className="flex flex-col gap-1.5">
                        <span className={labelCls}>Input → body</span>
                        <input
                            value={r.inputPath ?? ''}
                            onChange={(e) => updateTaskConfig(variant.id, { remote: { inputPath: e.target.value } })}
                            placeholder="body.text"
                            className={`${fieldCls} font-mono`}
                        />
                    </div>
                    <div className="flex flex-col gap-1.5">
                        <span className={labelCls}>Output ← path</span>
                        <input
                            value={r.outputPath ?? ''}
                            onChange={(e) => updateTaskConfig(variant.id, { remote: { outputPath: e.target.value } })}
                            placeholder="choices[0].text"
                            className={`${fieldCls} font-mono`}
                        />
                    </div>
                </div>
                <div className="flex items-center gap-2 rounded-lg border border-dashed border-sky-400/30 px-3 py-2 text-[11px] text-white/40 light:text-black/40">
                    <Globe className="h-4 w-4 shrink-0 text-sky-300/70" />
                    Remote task execution needs a backend proxy before it can safely run auth headers and avoid CORS.
                </div>
            </div>
        )
    }

    // scorer task
    return <ScorerTaskEditor variant={variant} />
}

function WorkflowTaskEditor({ variant }: { variant: PlaygroundVariant }) {
    const updateTaskConfig = usePlaygroundStore((s) => s.updateTaskConfig)
    const { data: workflows, isLoading } = useWorkflows({ enabled: true })
    const all = workflows ?? []

    return (
        <div className="flex flex-col gap-3">
            <div className="flex flex-col gap-1.5">
                <span className={labelCls}>Workflow</span>
                <Select
                    value={variant.workflow?.id ?? ''}
                    onValueChange={(workflowId) => {
                        const workflow = all.find((candidate) => candidate.id === workflowId)
                        updateTaskConfig(variant.id, {
                            workflow: { id: workflowId, name: workflow?.name ?? workflowId },
                        })
                    }}
                >
                    <SelectTrigger className="h-8 bg-brand-main-800/40 border-brand-main-600 text-xs">
                        <SelectValue placeholder={isLoading ? 'Loading workflows...' : 'Pick a workflow'} />
                    </SelectTrigger>
                    <SelectContent className="bg-brand-main-900 border-brand-main-500">
                        {all.length === 0 ? (
                            <SelectItem value="__none" disabled className="text-xs text-white/40 light:text-black/40">
                                No enabled workflows
                            </SelectItem>
                        ) : (
                            all.map((workflow) => (
                                <SelectItem key={workflow.id} value={workflow.id} className="text-xs text-white/80 light:text-black/80">
                                    {workflow.name}
                                </SelectItem>
                            ))
                        )}
                    </SelectContent>
                </Select>
            </div>
            <div className="flex items-center gap-2 rounded-lg border border-dashed border-amber-300/35 px-3 py-2.5 text-[11px] text-white/40 light:text-black/40">
                <WorkflowIcon className="h-4 w-4 shrink-0 text-amber-300/70" />
                Runs the selected backend workflow once per sample input or dataset row.
            </div>
        </div>
    )
}

function ScorerTaskEditor({ variant }: { variant: PlaygroundVariant }) {
    const updateTaskConfig = usePlaygroundStore((s) => s.updateTaskConfig)
    const targets = usePlaygroundStore((s) => s.variants.filter((v) => v.id !== variant.id && v.type !== 'scorer'))
    const { data: configs } = useScoreConfigs()
    const ref = variant.scorerRef ?? {}

    return (
        <div className="flex flex-col gap-3">
            <div className="flex flex-col gap-1.5">
                <span className={labelCls}>Scorer</span>
                <Select
                    value={ref.scorerConfigId ?? ''}
                    onValueChange={(v) => updateTaskConfig(variant.id, { scorerRef: { scorerConfigId: v } })}
                >
                    <SelectTrigger className="h-8 bg-brand-main-800/40 border-brand-main-600 text-xs">
                        <SelectValue placeholder="Pick a scorer" />
                    </SelectTrigger>
                    <SelectContent className="bg-brand-main-900 border-brand-main-500">
                        {(configs ?? []).map((c) => (
                            <SelectItem key={c.id} value={c.id} className="text-xs text-white/80 light:text-black/80">
                                {c.name}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
            </div>
            <div className="flex flex-col gap-1.5">
                <span className={labelCls}>Grades output of</span>
                <Select
                    value={ref.targetTaskId ?? ''}
                    onValueChange={(v) => updateTaskConfig(variant.id, { scorerRef: { targetTaskId: v } })}
                >
                    <SelectTrigger className="h-8 bg-brand-main-800/40 border-brand-main-600 text-xs">
                        <SelectValue placeholder="Which task's output" />
                    </SelectTrigger>
                    <SelectContent className="bg-brand-main-900 border-brand-main-500">
                        {targets.map((t, i) => (
                            <SelectItem key={t.id} value={t.id} className="text-xs text-white/80 light:text-black/80">
                                {String.fromCharCode(65 + i)} · {t.model || t.type}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
            </div>
            <div className="rounded-lg border border-dashed border-brand-main-600 px-3 py-2 text-[11px] text-white/40 light:text-black/40">
                Grade another task's output as its own column — compare graders or A/B a judge. Execution wiring ships next.
            </div>
        </div>
    )
}
