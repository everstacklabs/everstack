import { useMemo, useRef, useState } from 'react'
import { CornerDownLeft, X } from 'lucide-react'
import { Button } from '@everstack/ui/components'
import { cn } from '@/lib/utils'
import { describeEsql, parseEsql } from '@/utils/esql'

/**
 * Freeform ESQL editor (Surface 2). A textarea with a syntax-highlight overlay,
 * live validation, and a read-only "Matches:" explanation. The escape hatch for
 * queries that outgrow the chip bar — you write ESQL directly.
 */

const KEYWORDS = new Set([
    'and',
    'exists',
    'contains',
    'sequence',
    'failed',
    'slow',
    'expensive',
    'no_output',
    'tool_error',
    'retry',
])

type Tok = { text: string; cls: string }

const LEX = /(\s+)|("(?:\\.|[^"])*"|'(?:\\.|[^'])*')|(->|<=|>=|!=|[:=<>()])|([^\s:=<>()]+)/g

/** Tokenise for highlighting, preserving every character so the overlay aligns. */
function highlight(text: string): Tok[] {
    const toks: Tok[] = []
    const raw: { text: string; kind: 'ws' | 'str' | 'op' | 'word' }[] = []
    let m: RegExpExecArray | null
    LEX.lastIndex = 0
    while ((m = LEX.exec(text))) {
        if (m[1] != null) raw.push({ text: m[1], kind: 'ws' })
        else if (m[2] != null) raw.push({ text: m[2], kind: 'str' })
        else if (m[3] != null) raw.push({ text: m[3], kind: 'op' })
        else raw.push({ text: m[4], kind: 'word' })
    }
    raw.forEach((t, i) => {
        if (t.kind === 'ws') return toks.push({ text: t.text, cls: '' })
        if (t.kind === 'str') return toks.push({ text: t.text, cls: 'text-emerald-400' })
        if (t.kind === 'op') return toks.push({ text: t.text, cls: 'text-white/40 light:text-black/40' })
        // word: keyword, field (followed by an operator), or value
        const lower = t.text.toLowerCase()
        if (KEYWORDS.has(lower)) return toks.push({ text: t.text, cls: 'font-semibold text-brand-secondary-300' })
        let next = raw[i + 1]
        let j = i + 1
        while (next && next.kind === 'ws') next = raw[++j]
        if (next && next.kind === 'op') return toks.push({ text: t.text, cls: 'text-brand-secondary-300' })
        toks.push({ text: t.text, cls: 'text-white/90 light:text-black/90' })
    })
    return toks
}

export function EsqlEditor({
    initialValue,
    onApply,
    onClose,
    className,
}: {
    initialValue: string
    onApply: (esql: string) => boolean
    onClose: () => void
    className?: string
}) {
    const [value, setValue] = useState(initialValue)
    const overlayRef = useRef<HTMLDivElement>(null)
    const taRef = useRef<HTMLTextAreaElement>(null)

    const tokens = useMemo(() => highlight(value), [value])
    const parsed = useMemo(() => parseEsql(value), [value])
    const explanation = parsed.ok ? describeEsql(parsed.query) : null
    const errors = parsed.ok ? [] : parsed.errors

    const run = () => {
        if (onApply(value)) onClose()
    }

    return (
        <div
            className={cn(
                'absolute left-0 top-full z-30 mt-1 w-full overflow-hidden rounded border border-brand-secondary-500/50 bg-brand-main-900 shadow-xl light:bg-white',
                className,
            )}
        >
            <div className="flex items-center gap-2 border-b border-brand-main-700/70 px-3 py-2">
                <span className="font-mono text-[10px] uppercase tracking-[0.16em] text-white/35">ESQL editor</span>
                <div className="ml-auto" />
                <Button
                    variant="ghost"
                    aria-label="Close editor"
                    className="text-white/40"
                    onMouseDown={(e) => e.preventDefault()}
                    onClick={onClose}
                >
                    <X className="size-3.5" />
                </Button>
            </div>

            <div className="relative h-40">
                <div
                    ref={overlayRef}
                    aria-hidden="true"
                    className="pointer-events-none absolute inset-0 overflow-auto whitespace-pre-wrap break-words px-3 py-3 font-mono text-[13px] leading-[1.7]"
                >
                    {tokens.map((t, i) => (
                        <span key={i} className={t.cls}>
                            {t.text}
                        </span>
                    ))}
                    {'​'}
                </div>
                <textarea
                    ref={taRef}
                    value={value}
                    spellCheck={false}
                    autoFocus
                    onScroll={(e) => {
                        if (overlayRef.current) {
                            overlayRef.current.scrollTop = e.currentTarget.scrollTop
                            overlayRef.current.scrollLeft = e.currentTarget.scrollLeft
                        }
                    }}
                    onChange={(e) => setValue(e.target.value)}
                    onKeyDown={(e) => {
                        if (e.key === 'Enter' && !e.shiftKey) {
                            e.preventDefault()
                            run()
                        }
                        if (e.key === 'Escape') onClose()
                    }}
                    className="absolute inset-0 resize-none overflow-auto whitespace-pre-wrap break-words bg-transparent px-3 py-3 font-mono text-[13px] leading-[1.7] text-transparent caret-brand-secondary-500 outline-none"
                />
            </div>

            {errors.length > 0 ? (
                <div className="flex items-start gap-2 border-t border-brand-main-700/70 bg-rose-500/[0.06] px-3 py-2 text-[12px] text-rose-300">
                    <span className="mt-px font-mono text-[10px] uppercase tracking-[0.14em]">Error</span>
                    <span>{errors.join(' · ')}</span>
                </div>
            ) : (
                <div className="flex items-start gap-2 border-t border-brand-main-700/70 px-3 py-2">
                    <span className="mt-px rounded bg-brand-main-800 px-1.5 py-0.5 font-mono text-[9.5px] uppercase tracking-[0.14em] text-white/35">Matches</span>
                    <span className="text-[12.5px] text-white/55 light:text-black/55">{explanation}</span>
                </div>
            )}

            <div className="flex items-center gap-2 border-t border-brand-main-700/70 px-3 py-2">
                <Button
                    disabled={errors.length > 0}
                    onMouseDown={(e) => e.preventDefault()}
                    onClick={run}
                >
                    Run filter
                    <CornerDownLeft className="size-3" />
                </Button>
                <div className="ml-auto font-mono text-[11px] text-white/30">
                    <span className="text-white/50">⏎</span> run · <span className="text-white/50">⇧⏎</span> newline · <span className="text-white/50">esc</span> close
                </div>
            </div>
        </div>
    )
}
