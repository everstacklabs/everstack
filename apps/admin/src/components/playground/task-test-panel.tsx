import { useMemo, useRef, useState } from 'react'
import { ui } from '@everstack/ui'
import { Play, Square, Timer } from 'lucide-react'
import { scoreOutput, type ScoreMap } from '@/server/scoring'
import {
    executePlaygroundTask,
    isTaskRunnable,
    renderTaskMessages,
    taskReadinessError,
    usePlaygroundMetadata,
} from '@/stores/playground-run-store'
import { usePlaygroundStore, type PlaygroundVariant } from '@/stores/playground-store'

const { Badge } = ui

type Status = 'idle' | 'running' | 'scoring' | 'done' | 'error'

/**
 * Live test for a single task: type a sample input, see the templated prompt,
 * run one generation, and get the output + scores immediately — the "play"
 * half of the play area. Reuses the exact generation + scoring path the grid
 * uses (renderTaskMessages -> task executor -> ScoreOutput), so what you test
 * is what a full run produces.
 */
export function TaskTestPanel({ variant }: { variant: PlaygroundVariant }) {
    const firstRowInput = usePlaygroundStore((s) => s.rows[0]?.input ?? '')
    const scorerConfigIds = usePlaygroundStore((s) => s.scorerConfigIds)
    const metadata = usePlaygroundMetadata()

    const [input, setInput] = useState(firstRowInput)
    const [expected, setExpected] = useState('')
    const [status, setStatus] = useState<Status>('idle')
    const [output, setOutput] = useState('')
    const [scores, setScores] = useState<ScoreMap>({})
    const [error, setError] = useState<string>()
    const [ttftMs, setTtftMs] = useState<number>()
    const [durationMs, setDurationMs] = useState<number>()
    const ctrlRef = useRef<AbortController | null>(null)

    // Preview of the first user message with the sample input substituted.
    const rendered = useMemo(() => {
        const msgs = renderTaskMessages(variant, input, expected)
        return msgs.find((m) => m.role === 'user')?.text ?? msgs[0]?.text ?? ''
    }, [variant, input, expected])

    const running = status === 'running' || status === 'scoring'
    const readinessError = taskReadinessError(variant)
    const runnable = isTaskRunnable(variant)

    const run = async () => {
        if (!runnable) {
            setError(readinessError ?? 'This task is not runnable yet')
            setStatus('error')
            return
        }
        const messages = renderTaskMessages(variant, input, expected)
        if (!messages.length) return

        ctrlRef.current?.abort()
        const ctrl = new AbortController()
        ctrlRef.current = ctrl
        const startedAt = performance.now()
        let ttft: number | undefined
        setOutput('')
        setScores({})
        setError(undefined)
        setTtftMs(undefined)
        setDurationMs(undefined)
        setStatus('running')

        try {
            let acc = ''
            await executePlaygroundTask({
                variant,
                messages,
                metadata,
                signal: ctrl.signal,
                onChunk: (piece) => {
                    if (piece && ttft === undefined) {
                        ttft = Math.round(performance.now() - startedAt)
                        setTtftMs(ttft)
                    }
                    acc += piece
                    setOutput(acc)
                },
            })
            setDurationMs(Math.round(performance.now() - startedAt))

            if (scorerConfigIds.length) {
                setStatus('scoring')
                try {
                    const s = await scoreOutput({
                        input,
                        output: acc,
                        expectedOutput: expected || undefined,
                        scorerConfigIds,
                        signal: ctrl.signal,
                    })
                    setScores(s)
                } catch {
                    /* scoring failure must not mask a good generation */
                }
            }
            setStatus('done')
        } catch (e) {
            const aborted = (e as { name?: string })?.name === 'AbortError'
            if (!aborted) {
                setError((e as Error)?.message ?? String(e))
                setStatus('error')
            } else {
                setStatus('idle')
            }
        } finally {
            if (ctrlRef.current === ctrl) ctrlRef.current = null
        }
    }

    const scoreChips = Object.entries(scores).filter(
        ([k]) => !k.endsWith('_reason') && !k.endsWith('_error'),
    )

    return (
        <div className="flex flex-col gap-2.5 border-t border-brand-main-700 pt-2.5 mt-1">
            <div className="flex items-center justify-between">
                <span className="text-[10px] uppercase tracking-wide text-white/40 light:text-black/40">
                    Test run
                </span>
                {running ? (
                    <button
                        type="button"
                        onClick={() => ctrlRef.current?.abort()}
                        className="inline-flex items-center gap-1 rounded border border-brand-main-600 px-2 py-0.5 text-[11px] text-rose-300 hover:border-rose-400/50"
                    >
                        <Square className="h-2.5 w-2.5" /> Stop
                    </button>
                ) : (
                    <button
                        type="button"
                        onClick={() => void run()}
                        disabled={!runnable}
                        className="inline-flex items-center gap-1 rounded border border-brand-secondary-500/40 bg-brand-secondary-500/10 px-2 py-0.5 text-[11px] text-brand-secondary-200 hover:bg-brand-secondary-500/20 disabled:opacity-40"
                        title={readinessError ?? undefined}
                    >
                        <Play className="h-2.5 w-2.5" /> Run row
                    </button>
                )}
            </div>

            <label className="flex flex-col gap-1">
                <span className="text-[10px] text-white/35 light:text-black/35 inline-flex items-center gap-1">
                    Sample input
                    <span className="font-mono text-brand-secondary-300">{'{{input}}'}</span>
                </span>
                <input
                    value={input}
                    onChange={(e) => setInput(e.target.value)}
                    placeholder="green"
                    className="rounded border border-brand-main-600 bg-brand-main-800/40 px-2 py-1.5 text-xs font-mono text-white/85 light:text-black/85 outline-none focus:border-brand-secondary-500/50"
                />
            </label>

            <label className="flex flex-col gap-1">
                <span className="text-[10px] text-white/35 light:text-black/35">Expected (optional)</span>
                <input
                    value={expected}
                    onChange={(e) => setExpected(e.target.value)}
                    placeholder="#008000"
                    className="rounded border border-brand-main-600 bg-brand-main-800/40 px-2 py-1.5 text-xs font-mono text-white/85 light:text-black/85 outline-none focus:border-brand-secondary-500/50"
                />
            </label>

            {rendered && (
                <div className="rounded border border-dashed border-brand-main-600 px-2.5 py-2 text-[11px] leading-relaxed text-white/55 light:text-black/55">
                    {rendered}
                </div>
            )}

            {(output || running || status === 'error') && (
                <div className="flex flex-col gap-1.5">
                    <span className="text-[10px] text-white/35 light:text-black/35">Output</span>
                    {status === 'error' ? (
                        <div className="rounded border border-rose-500/40 bg-rose-500/10 px-2.5 py-2 text-[11px] text-rose-300">
                            {error}
                        </div>
                    ) : (
                        <div className="rounded border border-brand-main-600 bg-brand-main-800/40 px-2.5 py-2 text-xs font-mono text-white/90 light:text-black/90 whitespace-pre-wrap break-words">
                            {output}
                            {status === 'running' && (
                                <span className="inline-block w-1.5 h-3.5 bg-brand-secondary-400 ml-0.5 animate-pulse align-text-bottom" />
                            )}
                            {status === 'scoring' && (
                                <span className="block text-[10px] text-white/30 mt-1">scoring…</span>
                            )}
                        </div>
                    )}
                    {(ttftMs !== undefined || durationMs !== undefined) && status !== 'error' && (
                        <div className="flex items-center gap-3 text-[10px] font-mono text-white/30 light:text-black/30">
                            {ttftMs !== undefined && (
                                <span className="inline-flex items-center gap-1">
                                    <Timer className="h-2.5 w-2.5" />
                                    {ttftMs}ms
                                </span>
                            )}
                            {durationMs !== undefined && <span>{(durationMs / 1000).toFixed(2)}s</span>}
                        </div>
                    )}
                    {scoreChips.length > 0 && (
                        <div className="flex flex-wrap gap-1">
                            {scoreChips.map(([name, value]) => {
                                const pass = value === true || value === 1 || value === '1'
                                const fail = value === false || value === 0 || value === '0'
                                const label = typeof value === 'number' ? value.toFixed(2) : String(value)
                                return (
                                    <Badge
                                        key={name}
                                        variant="outline"
                                        className={
                                            pass
                                                ? 'border-emerald-500/40 bg-emerald-500/10 text-emerald-300 text-[10px]'
                                                : fail
                                                  ? 'border-red-500/40 bg-red-500/10 text-red-300 text-[10px]'
                                                  : 'border-brand-secondary-500/40 bg-brand-secondary-500/10 text-brand-secondary-200 text-[10px]'
                                        }
                                    >
                                        {name}: {label}
                                    </Badge>
                                )
                            })}
                        </div>
                    )}
                </div>
            )}
        </div>
    )
}
