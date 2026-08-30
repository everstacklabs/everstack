import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { toast } from '@everstack/ui/components'
import { FlaskConical, Loader2 } from 'lucide-react'
import { useCreateEvalRun } from '@/hooks/evaluations/use-evals'
import { usePlaygroundStore, type PlaygroundVariant } from '@/stores/playground-store'

/** Column label (A, B, C…) matching the task order. */
const label = (i: number) => String.fromCharCode(65 + i)

/** eval_config that lets the eval runner reproduce this task's prompt exactly. */
function taskEvalConfig(v: PlaygroundVariant): Record<string, unknown> {
    const cfg: Record<string, unknown> = {
        prompt_messages: v.messages
            .filter((m) => m.text.trim())
            .map((m) => ({ role: m.role, content: m.text })),
        templating: v.templating,
        temperature: v.temperature,
    }
    if (v.topP !== undefined) cfg.top_p = v.topP
    if (v.maxTokens !== undefined) cfg.max_tokens = v.maxTokens
    if (v.outputFormat === 'json_object') cfg.response_format = { type: 'json_object' }
    return cfg
}

/**
 * Snapshots the current playground into persisted eval runs — one per task —
 * so a side-by-side interactive comparison becomes a durable, shareable
 * experiment. Reuses EvalService.CreateEvalRun (eval_target_type=model): the
 * runner renders each task's prompt template against the attached dataset and
 * scores it with the same scorers.
 */
export function ExperimentsButton() {
    const navigate = useNavigate()
    const createEvalRun = useCreateEvalRun()
    const [busy, setBusy] = useState(false)

    const run = async () => {
        const { variants, datasetId, scorerConfigIds, playgroundName } = usePlaygroundStore.getState()
        const tasks = variants.filter((v) => v.model.trim())
        if (!datasetId) {
            toast.error('Attach a dataset before running experiments')
            return
        }
        if (tasks.length === 0) {
            toast.error('Add a model to at least one task first')
            return
        }

        setBusy(true)
        try {
            const base = playgroundName?.trim() || 'Playground'
            const results = await Promise.allSettled(
                tasks.map((v) =>
                    createEvalRun.mutateAsync({
                        name: `${base} · ${label(variants.indexOf(v))}`,
                        datasetId,
                        evalTargetType: 'model',
                        evalTargetId: v.model,
                        evalConfig: taskEvalConfig(v) as never,
                        scorerConfigIds,
                    }),
                ),
            )
            const failed = results.filter((r) => r.status === 'rejected').length
            if (failed === tasks.length) {
                toast.error('Failed to create experiments')
                return
            }
            toast.success(
                `Created ${tasks.length - failed} experiment${tasks.length - failed === 1 ? '' : 's'}`,
            )
            void navigate({ to: '/evaluations/runs' })
        } finally {
            setBusy(false)
        }
    }

    return (
        <button
            type="button"
            onClick={() => void run()}
            disabled={busy}
            className="inline-flex items-center gap-1.5 rounded border border-brand-main-700 bg-brand-main-950/20 px-2 py-1 text-xs text-white/65 transition-colors hover:border-brand-secondary-500/60 hover:text-white disabled:opacity-50 light:text-black/65 light:hover:text-brand-main-50"
            title="Snapshot each task into a persisted experiment (eval run) over the attached dataset"
        >
            {busy ? <Loader2 className="h-3 w-3 animate-spin" /> : <FlaskConical className="h-3 w-3" />}
            Experiments
        </button>
    )
}
