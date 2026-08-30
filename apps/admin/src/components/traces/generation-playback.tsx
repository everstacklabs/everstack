import { useEffect, useMemo, useRef, useState } from 'react'
import { ui } from '@everstack/ui'
import { Pause, Play, RotateCcw, Zap } from 'lucide-react'
import type { Span } from '@everstack/proto/everstack/traces/v1/traces_pb'
import { cn } from '@everstack/utils/functions/cn'
import { getAttr } from '@/utils/traces-common'
import { formatDuration } from '@/utils/trace-formatters'
import { statusTint } from './trace-viz'

const { Card, CardContent, CardHeader, CardTitle, Button } = ui

/**
 * Streaming-generation playback for a single GENERATION span.
 *
 * Replays the output at the original cadence using TTFT and
 * per-token timings captured on the span. The replay is a visual
 * approximation — we don't have actual token boundary timestamps,
 * only the per-token average — so we re-emit by chunked groups of
 * characters sized to roughly match average-token-length (4 chars).
 *
 * The point is to make TTFT spikes and per-token stalls visible
 * at a glance: a long flat lead-in before the first character means
 * a slow TTFT; choppy or extra-slow streaming becomes visceral
 * rather than a number in a table.
 */

interface GenerationPlaybackProps {
    span: Span
}

const AVG_CHARS_PER_TOKEN = 4

function extractOutputText(span: Span): string {
    const choices = getAttr(span, 'llm.response.choices')
    if (choices) {
        // Choices is JSON like [{message: {content: "..."}}] or similar
        try {
            const parsed = JSON.parse(choices)
            if (Array.isArray(parsed)) {
                const first = parsed[0]
                if (typeof first === 'string') return first
                if (first?.message?.content) return String(first.message.content)
                if (first?.delta?.content) return String(first.delta.content)
                if (first?.text) return String(first.text)
            }
            if (typeof parsed === 'string') return parsed
        } catch {
            return choices
        }
        return choices
    }
    return (
        getAttr(span, 'trace.output') ||
        getAttr(span, 'output') ||
        getAttr(span, 'gen_ai.completion') ||
        ''
    )
}

export function GenerationPlayback({ span }: GenerationPlaybackProps) {
    const output = useMemo(() => extractOutputText(span), [span])
    const ttftNs = Number(getAttr(span, 'llm.time_to_first_token_ns') || getAttr(span, 'llm.ttft_ns') || 0)
    const perTokenNs = Number(
        getAttr(span, 'llm.time_per_token_ns') || getAttr(span, 'llm.per_token_ns') || 0,
    )

    const totalLen = output.length
    const stepChars = AVG_CHARS_PER_TOKEN
    const totalSteps = Math.max(1, Math.ceil(totalLen / stepChars))
    const ttftMs = ttftNs / 1_000_000
    const perStepMs = (perTokenNs * stepChars) / 1_000_000 || 30 // fallback ~30ms/token if unknown
    const totalDurationMs = ttftMs + perStepMs * totalSteps

    const [playing, setPlaying] = useState(false)
    const [progressMs, setProgressMs] = useState(0)
    const tickRef = useRef<number | null>(null)
    const lastRafTimeRef = useRef<number>(0)

    useEffect(() => {
        if (!playing) return
        lastRafTimeRef.current = performance.now()
        const tick = () => {
            const now = performance.now()
            const delta = now - lastRafTimeRef.current
            lastRafTimeRef.current = now
            setProgressMs((p) => {
                const next = p + delta
                if (next >= totalDurationMs) {
                    setPlaying(false)
                    return totalDurationMs
                }
                return next
            })
            tickRef.current = requestAnimationFrame(tick)
        }
        tickRef.current = requestAnimationFrame(tick)
        return () => {
            if (tickRef.current !== null) cancelAnimationFrame(tickRef.current)
        }
    }, [playing, totalDurationMs])

    const charsRevealed = useMemo(() => {
        if (progressMs <= ttftMs) return 0
        const afterTtft = progressMs - ttftMs
        return Math.min(totalLen, Math.ceil((afterTtft / perStepMs) * stepChars))
    }, [progressMs, ttftMs, perStepMs, stepChars, totalLen])

    const inTtft = progressMs < ttftMs && progressMs > 0

    if (totalLen === 0) {
        return null
    }
    if (ttftNs <= 0 && perTokenNs <= 0) {
        return null // no timing data — playback would be fictional
    }

    return (
        <Card className="border-brand-main-500 bg-brand-main-900/40">
            <CardHeader className="!pb-1.5 flex flex-row items-center justify-between">
                <CardTitle className="text-brand-main-50 text-sm flex items-center gap-1.5 light:text-brand-main-50">
                    <Zap className={cn('h-3.5 w-3.5', statusTint.info.text)} />
                    Stream playback
                </CardTitle>
                <div className="flex items-center gap-2 text-[10px] text-brand-main-50 light:text-black">
                    <span>TTFT {formatDuration(BigInt(Math.round(ttftNs)))}</span>
                    <span>·</span>
                    <span>{(perTokenNs / 1_000_000).toFixed(1)}ms/token</span>
                </div>
            </CardHeader>
            <CardContent className="!pt-0 space-y-2">
                <div className="flex items-center gap-2">
                    <Button
                        size="sm"
                        variant="ghost"
                        className="h-6 px-2"
                        onClick={() => {
                            if (progressMs >= totalDurationMs) setProgressMs(0)
                            setPlaying((p) => !p)
                        }}
                    >
                        {playing ? <Pause className="h-3 w-3" /> : <Play className="h-3 w-3" />}
                        <span className="ml-1 text-[11px]">{playing ?'Pause' : 'Play'}</span>
                    </Button>
                    <Button
                        size="sm"
                        variant="ghost"
                        className="h-6 px-2"
                        onClick={() => {
                            setPlaying(false)
                            setProgressMs(0)
                        }}
                    >
                        <RotateCcw className="h-3 w-3" />
                    </Button>
                    <input
                        type="range"
                        min={0}
                        max={Math.max(1, Math.round(totalDurationMs))}
                        value={Math.round(progressMs)}
                        onChange={(e) => {
                            setPlaying(false)
                            setProgressMs(Number(e.target.value))
                        }}
                        className="flex-1 accent-brand-secondary-500"
                    />
                    <span className="text-[10px] text-brand-main-50 tabular-nums w-20 text-right light:text-black">
                        {(progressMs / 1000).toFixed(2)}s / {(totalDurationMs / 1000).toFixed(2)}s
                    </span>
                </div>

                <div className="text-[12px] leading-relaxed rounded border border-brand-main-700/60 bg-brand-main-950/60 p-3 min-h-[160px] max-h-[320px] overflow-auto">
                    {inTtft ? (
                        <span className={cn('inline-flex items-center gap-1.5 animate-pulse text-xs', statusTint.info.text)}>
                            <span className={cn('inline-block w-1.5 h-1.5 rounded-full', statusTint.info.dot)} />
                            waiting for first token…
                        </span>
                    ) : (
                        <>
                            <span className="text-zinc-200 whitespace-pre-wrap">
                                {output.slice(0, charsRevealed)}
                            </span>
                            {charsRevealed < totalLen && (
                                <span className="inline-block w-1.5 h-3 bg-brand-secondary-400/70 ml-px align-middle animate-pulse" />
                            )}
                            {charsRevealed === totalLen && progressMs >= totalDurationMs && (
                                <span className={cn('text-[10px] ml-1', statusTint.success.text)}>▶ done</span>
                            )}
                        </>
                    )}
                </div>
            </CardContent>
        </Card>
    )
}

/** Probe whether a span has enough data to be playback-renderable. */
export function isPlaybackable(span: Span): boolean {
    if (!span) return false
    const ttft = Number(getAttr(span, 'llm.time_to_first_token_ns') || getAttr(span, 'llm.ttft_ns') || 0)
    const perTok = Number(
        getAttr(span, 'llm.time_per_token_ns') || getAttr(span, 'llm.per_token_ns') || 0,
    )
    if (ttft <= 0 && perTok <= 0) return false
    return extractOutputText(span).length > 0
}
