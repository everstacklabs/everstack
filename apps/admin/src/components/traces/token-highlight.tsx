import { useMemo, useState } from 'react'
import { ui } from '@everstack/ui'
import { Eye, EyeOff, Info } from 'lucide-react'
import type { Span } from '@everstack/proto/everstack/traces/v1/traces_pb'
import { getAttr } from '@/utils/traces-common'
import { cn } from '@everstack/utils/functions/cn'

const { Tooltip } = ui

/**
 * Per-token highlighting for a generation span.
 *
 * Lights up when the span carries a `llm.response.logprobs` attribute
 * encoded as JSON. Two supported shapes (OpenAI-style):
 *
 *   [{ "token": "...", "logprob": -0.5, "top_logprobs": [...] }, ...]
 *
 *   or its older nested form:
 *
 *   { "content": [ ...same as above... ] }
 *
 * Renders each token with a background tinted by confidence (greener =
 * higher probability, redder = lower). On hover, shows the per-token
 * logprob + top alternatives if present.
 *
 * Falls back to plain rendering if no logprobs are emitted. No schema
 * change required — emitters just need to set the attribute.
 */

interface TokenEntry {
    token: string
    logprob: number
    topLogprobs?: { token: string; logprob: number }[]
}

function parseLogprobs(raw: string): TokenEntry[] | null {
    try {
        const parsed = JSON.parse(raw)
        const arr: any[] | null = Array.isArray(parsed)
            ? parsed
            : Array.isArray(parsed?.content)
              ? parsed.content
              : null
        if (!arr) return null
        const out: TokenEntry[] = []
        for (const item of arr) {
            if (!item || typeof item.token !== 'string' || typeof item.logprob !== 'number') continue
            const entry: TokenEntry = { token: item.token, logprob: item.logprob }
            if (Array.isArray(item.top_logprobs)) {
                entry.topLogprobs = item.top_logprobs
                    .filter((t: any) => typeof t?.token === 'string' && typeof t?.logprob === 'number')
                    .map((t: any) => ({ token: t.token, logprob: t.logprob }))
            }
            out.push(entry)
        }
        return out.length > 0 ? out : null
    } catch {
        return null
    }
}

/**
 * Map logprob (always ≤ 0) to a background colour:
 *   logprob ≥ -0.1   → ~100% confident → emerald
 *   logprob ≈ -0.5   → strong          → emerald/amber blend
 *   logprob ≈ -1.5   → uncertain       → amber
 *   logprob ≤ -3     → very uncertain  → rose
 */
function logprobColor(lp: number): string {
    const p = Math.exp(lp) // probability, in (0,1]
    if (p >= 0.85) return 'bg-emerald-500/15 border-emerald-500/30'
    if (p >= 0.6) return 'bg-green-500/15 border-green-500/30'
    if (p >= 0.4) return 'bg-amber-500/15 border-amber-500/30'
    if (p >= 0.2) return 'bg-orange-500/20 border-orange-500/40'
    return 'bg-rose-500/20 border-rose-500/40'
}

export function isTokenHighlightable(span: Span): boolean {
    const raw =
        getAttr(span, 'llm.response.logprobs') ||
        getAttr(span, 'gen_ai.response.logprobs') ||
        ''
    if (!raw) return false
    return parseLogprobs(raw) !== null
}

interface TokenHighlightProps {
    span: Span
}

export function TokenHighlight({ span }: TokenHighlightProps) {
    const raw =
        getAttr(span, 'llm.response.logprobs') ||
        getAttr(span, 'gen_ai.response.logprobs') ||
        ''
    const tokens = useMemo(() => parseLogprobs(raw), [raw])
    const [highlight, setHighlight] = useState(true)

    if (!tokens) return null

    const stats = useMemo(() => {
        if (!tokens.length) return null
        const probs = tokens.map((t) => Math.exp(t.logprob))
        const avg = probs.reduce((a, b) => a + b, 0) / probs.length
        const low = probs.filter((p) => p < 0.2).length
        return { avg, low, count: tokens.length }
    }, [tokens])

    return (
        <div className="rounded border border-brand-main-500 bg-brand-main-900/40">
            <div className="flex items-center justify-between px-3 py-1.5 border-b border-brand-main-700">
                <div className="flex items-center gap-2 text-xs text-brand-main-50 light:text-black">
                    <Info className="h-3 w-3 text-brand-main-200" />
                    Token confidence
                    {stats && (
                        <span className="text-[10px] text-brand-main-50 light:text-black">
                            ({stats.count} tokens · avg p {stats.avg.toFixed(2)}
                            {stats.low > 0 && `, ${stats.low} below 0.2`})
                        </span>
                    )}
                </div>
                <button
                    type="button"
                    onClick={() => setHighlight((h) => !h)}
                    className="text-[10px] text-brand-main-50 hover:text-brand-main-50 inline-flex items-center gap-1 light:text-black light:hover:text-brand-main-50"
                >
                    {highlight ? <EyeOff className="h-3 w-3" /> : <Eye className="h-3 w-3" />}
                    {highlight ? 'Plain' : 'Highlight'}
                </button>
            </div>
            <div className="px-3 py-2 text-[12px] leading-relaxed whitespace-pre-wrap max-h-72 overflow-auto">
                {tokens.map((t, i) =>
                    highlight ? (
                        <Tooltip key={i} content={<TokenTooltip entry={t} />}>
                            <span
                                className={cn(
                                    'inline rounded border px-px',
                                    logprobColor(t.logprob),
                                )}
                            >
                                {t.token}
                            </span>
                        </Tooltip>
                    ) : (
                        <span key={i}>{t.token}</span>
                    ),
                )}
            </div>
        </div>
    )
}

function TokenTooltip({ entry }: { entry: TokenEntry }) {
    const prob = Math.exp(entry.logprob)
    return (
        <div className="text-[10px] space-y-1">
            <div className="text-brand-main-50 light:text-black">
                "{entry.token}"
            </div>
            <div className="text-brand-main-50 light:text-black">
                logprob {entry.logprob.toFixed(3)} · p {prob.toFixed(3)}
            </div>
            {entry.topLogprobs && entry.topLogprobs.length > 0 && (
                <div className={cn('pt-1 border-t border-white/10 space-y-0.5 light:border-black/10')}>
                    <div className="text-brand-main-50 light:text-black">alternatives</div>
                    {entry.topLogprobs.slice(0, 5).map((alt, i) => (
                        <div key={i} className="flex justify-between gap-3 text-brand-main-50 light:text-black">
                            <span className="truncate max-w-[100px]">"{alt.token}"</span>
                            <span>{Math.exp(alt.logprob).toFixed(3)}</span>
                        </div>
                    ))}
                </div>
            )}
        </div>
    )
}
