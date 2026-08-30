import { useEffect, useMemo, useRef, useState } from 'react'
import { Code2, Search, X } from 'lucide-react'
import { Button } from '@everstack/ui/components'
import { cn } from '@/lib/utils'
import { EsqlEditor } from './esql-editor'
import { useEsqlQueriesStore } from '@/stores/esql-queries-store'
import {
    clearedEsqlParams,
    esqlFromLegacyParams,
    ESQL_FIELDS,
    esqlToSearchParams,
    parseEsql,
    PRESETS,
    serializeEsql,
    type EsqlField,
    type EsqlNode,
    type EsqlQuery,
    type PresetId,
} from '@/utils/esql'

/**
 * ESQL filter bar (Surface 1 — compact). Committed filters render as neutral
 * chips (the literal ESQL token); the trailing input takes freeform ESQL with a
 * grouped Lens/facet autocomplete menu. Everything drives the tested esql engine
 * (parseEsql -> compileToListTracesParams). Chips and text are the same query.
 */

// Facet families for the autocomplete menu, agent-shaped.
const FAMILIES: { label: string; fieldIds: string[] }[] = [
    { label: 'Outcome', fieldIds: ['status', 'output'] },
    { label: 'AI', fieldIds: ['model', 'provider', 'cache.hit'] },
    { label: 'Tooling', fieldIds: ['tool.name', 'tool.error'] },
    { label: 'Runtime', fieldIds: ['has', 'agent'] },
    { label: 'Cost & Latency', fieldIds: ['cost', 'duration', 'ttft', 'tokens.total'] },
    { label: 'Context', fieldIds: ['trace', 'user', 'session', 'thread', 'environment', 'correlation', 'tag'] },
]

/** One chip per committed AST node. Label is the canonical ESQL token. */
type Chip = { token: string; index: number }

function chipToken(node: EsqlNode): string {
    if (node.kind === 'preset') return PRESETS[node.id]?.label ?? node.id
    return serializeEsql({ nodes: [node] })
}

/** Nearest field id/alias for a typo, for the did-you-mean hint. */
function didYouMean(name: string): string | null {
    const n = name.toLowerCase()
    let best: string | null = null
    let bestScore = 0
    for (const field of ESQL_FIELDS) {
        for (const candidate of [field.id, ...(field.aliases ?? [])]) {
            const c = candidate.toLowerCase()
            let score = 0
            if (c.startsWith(n.slice(0, 3))) score = 3
            else if (c.includes(n.slice(0, 2))) score = 1
            if (Math.abs(c.length - n.length) <= 2 && c[0] === n[0]) score += 2
            if (score > bestScore) {
                bestScore = score
                best = field.id
            }
        }
    }
    return bestScore >= 3 ? best : null
}

type Suggestion =
    | { kind: 'preset'; id: PresetId; label: string }
    | { kind: 'field'; field: EsqlField; group: string }

function currentFragment(draft: string): string {
    const m = draft.match(/(\S*)$/)
    return m ? m[1] : ''
}

export function EsqlFilterBar({
    search,
    navigate,
    placeholder = 'Filter or search…',
    className,
}: {
    search: Record<string, any>
    navigate: (options: any) => void
    placeholder?: string
    className?: string
}) {
    const [draft, setDraft] = useState('')
    const [open, setOpen] = useState(false)
    const [editorOpen, setEditorOpen] = useState(false)
    // -1 = nothing highlighted; the user must arrow into the list to select.
    const [active, setActive] = useState(-1)
    const [error, setError] = useState<string | null>(null)
    const [suggestion, setSuggestion] = useState<string | null>(null)
    const inputRef = useRef<HTMLInputElement>(null)
    // Set while Escape is backing out, so the blur handler doesn't re-commit.
    const escapingRef = useRef(false)
    const pushHistory = useEsqlQueriesStore((s) => s.pushHistory)

    // Committed query comes from the canonical `?q=` ESQL string when present
    // (so span-scoped Tier-2 chips survive), else derived from legacy flat params.
    const committed: EsqlQuery = useMemo(() => {
        const source =
            typeof search.q === 'string' && search.q.trim() ? search.q : esqlFromLegacyParams(search)
        const parsed = parseEsql(source)
        return parsed.ok ? parsed.query : { nodes: [] }
    }, [search])

    const chips: Chip[] = useMemo(
        () => committed.nodes.map((node, index) => ({ token: chipToken(node), index })),
        [committed],
    )

    const suggestions: Suggestion[] = useMemo(() => {
        if (!open) return []
        const frag = currentFragment(draft).toLowerCase()
        const presets: Suggestion[] = (Object.keys(PRESETS) as PresetId[])
            .filter((id) => !frag || id.startsWith(frag) || PRESETS[id].label.toLowerCase().includes(frag))
            .map((id) => ({ kind: 'preset', id, label: PRESETS[id].label }))
        const fields: Suggestion[] = FAMILIES.flatMap((fam) =>
            fam.fieldIds
                .map((fid) => ESQL_FIELDS.find((f) => f.id === fid))
                .filter((f): f is EsqlField => Boolean(f))
                .filter((f) => {
                    if (!frag) return true
                    return f.id.toLowerCase().startsWith(frag) || f.label.toLowerCase().includes(frag) || (f.aliases ?? []).some((a) => a.toLowerCase().startsWith(frag))
                })
                .map((f) => ({ kind: 'field' as const, field: f, group: fam.label })),
        )
        return [...presets, ...fields]
    }, [open, draft])

    // Reset highlight to "none" as the draft/menu changes — no preselection.
    useEffect(() => setActive(-1), [draft, open])

    // Global shortcuts: "/" focuses the search; Cmd/Ctrl+"/" opens the editor.
    useEffect(() => {
        const onKey = (e: KeyboardEvent) => {
            const el = e.target as HTMLElement | null
            const typing =
                !!el &&
                (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.isContentEditable)
            if (e.key === '/' && (e.metaKey || e.ctrlKey)) {
                e.preventDefault()
                setOpen(false)
                setEditorOpen(true)
                return
            }
            if (e.key === '/' && !e.altKey && !typing) {
                e.preventDefault()
                inputRef.current?.focus()
                setOpen(true)
            }
        }
        window.addEventListener('keydown', onKey)
        return () => window.removeEventListener('keydown', onKey)
    }, [])

    /** Rebuild the full ESQL string, compile, and replace the managed params. */
    const apply = (esql: string) => {
        const value = esql.trim()
        const result = esqlToSearchParams(value)
        if (!result.ok) {
            const first = result.error ?? 'Invalid filter'
            setError(first)
            const m = first.match(/Unknown field: (\S+)/)
            setSuggestion(m ? didYouMean(m[1]) : null)
            return false
        }
        navigate({
            search: {
                ...search,
                ...clearedEsqlParams(),
                ...result.params,
                q: value || undefined,
                // Applying a search pauses live tailing so results stay put while
                // you read them; clearing leaves the live state untouched.
                ...(value ? { live: 'false' } : { span: undefined }),
            },
            replace: true,
        })
        setError(null)
        setSuggestion(null)
        if (value)
            pushHistory(value, {
                range: typeof search.range === 'string' ? search.range : undefined,
                from: typeof search.from === 'string' ? search.from : undefined,
                to: typeof search.to === 'string' ? search.to : undefined,
            })
        return true
    }

    const commitDraft = () => {
        const merged = [serializeEsql(committed), draft].filter((s) => s.trim()).join(' ')
        if (apply(merged)) setDraft('')
    }

    const removeChip = (index: number) => {
        apply(serializeEsql({ nodes: committed.nodes.filter((_, i) => i !== index) }))
        inputRef.current?.focus()
    }

    const editChip = (index: number) => {
        const token = chipToken(committed.nodes[index])
        apply(serializeEsql({ nodes: committed.nodes.filter((_, i) => i !== index) }))
        setDraft((prev) => `${prev ? `${prev} ` : ''}${token} `)
        setOpen(true)
        requestAnimationFrame(() => inputRef.current?.focus())
    }

    const clearAll = () => {
        apply('')
        setDraft('')
        inputRef.current?.focus()
    }

    const chooseSuggestion = (s: Suggestion) => {
        if (s.kind === 'preset') {
            const merged = [serializeEsql(committed), s.id].filter((x) => x.trim()).join(' ')
            if (apply(merged)) setDraft('')
        } else {
            const token = `${s.field.id}:`
            setDraft((prev) => prev.replace(/(\S*)$/, token))
            setOpen(true)
        }
        requestAnimationFrame(() => inputRef.current?.focus())
    }

    const hasContent = chips.length > 0 || draft.length > 0

    return (
        <div className={cn('relative', className)}>
            <div
                className={cn(
                    'flex min-h-8 min-w-0 flex-wrap items-center gap-1 rounded border bg-brand-main-950 px-1 py-0.5 text-sm transition-colors focus-within:border-brand-secondary-500/70 focus-within:shadow-[0_0_0_3px_rgba(118,126,255,0.13)] light:bg-white/90',
                    error ? 'border-rose-500/60' : 'border-brand-main-500',
                )}
                onClick={() => inputRef.current?.focus()}
            >
                <Search className="ml-0.5 size-4 shrink-0 text-white/35 light:text-black/35" />

                {chips.map((chip) => (
                    <span
                        key={`${chip.token}-${chip.index}`}
                        className="inline-flex items-center gap-1.5 rounded border border-brand-main-500 bg-brand-main-700/60 py-0.5 pl-2 pr-1 font-mono text-[12px] text-white/90 light:bg-black/5 light:text-black/80"
                    >
                        <button
                            type="button"
                            className="outline-none"
                            onMouseDown={(e) => e.preventDefault()}
                            onClick={(e) => {
                                e.stopPropagation()
                                editChip(chip.index)
                            }}
                            title="Edit filter"
                        >
                            {chip.token}
                        </button>
                        <button
                            type="button"
                            aria-label={`Remove ${chip.token}`}
                            className="text-white/35 hover:text-white/80 light:text-black/35 light:hover:text-black/70"
                            onMouseDown={(e) => e.preventDefault()}
                            onClick={(e) => {
                                e.stopPropagation()
                                removeChip(chip.index)
                            }}
                        >
                            <X className="size-3" />
                        </button>
                    </span>
                ))}

                <input
                    ref={inputRef}
                    value={draft}
                    onChange={(e) => {
                        setDraft(e.target.value)
                        setOpen(true)
                        if (error) setError(null)
                    }}
                    onFocus={() => setOpen(true)}
                    onKeyDown={(e) => {
                        if (open && suggestions.length > 0) {
                            if (e.key === 'ArrowDown') {
                                e.preventDefault()
                                // From -1 (none) step into the list; stop at the end.
                                setActive((a) => Math.min(a + 1, suggestions.length - 1))
                                return
                            }
                            if (e.key === 'ArrowUp') {
                                e.preventDefault()
                                // Step back up; -1 returns to plain typing (no highlight).
                                setActive((a) => Math.max(a - 1, -1))
                                return
                            }
                            if (e.key === 'Tab') {
                                // Tab completes the highlighted item, or the top match.
                                e.preventDefault()
                                chooseSuggestion(suggestions[active >= 0 ? active : 0])
                                return
                            }
                        }
                        if (e.key === 'Enter') {
                            e.preventDefault()
                            // Only take a suggestion when one is highlighted; otherwise
                            // Enter runs exactly what was typed.
                            if (open && active >= 0 && suggestions[active]) {
                                chooseSuggestion(suggestions[active])
                            } else {
                                commitDraft()
                                setOpen(false)
                            }
                            return
                        }
                        if (e.key === 'Escape') {
                            e.preventDefault()
                            setError(null)
                            escapingRef.current = true
                            // Escape backs all the way out: discard in-progress
                            // typing, else clear the active search, then close the
                            // menu and blur so the bar is idle (and "/" reopens it).
                            if (draft) {
                                setDraft('')
                            } else if (chips.length > 0) {
                                apply('')
                                setDraft('')
                            }
                            setOpen(false)
                            inputRef.current?.blur()
                            return
                        }
                        if (e.key === 'Backspace' && !draft && chips.length > 0) {
                            e.preventDefault()
                            removeChip(chips[chips.length - 1].index)
                        }
                    }}
                    onBlur={() => {
                        window.setTimeout(() => {
                            setOpen(false)
                            // Escape already handled this blur — don't re-commit.
                            if (escapingRef.current) {
                                escapingRef.current = false
                                return
                            }
                            if (draft.trim()) commitDraft()
                        }, 140)
                    }}
                    placeholder={chips.length === 0 ? placeholder : ''}
                    spellCheck={false}
                    className="min-w-[110px] flex-1 bg-transparent px-1 pt-0.5 font-mono text-[13px] text-white/90 outline-none placeholder:font-sans placeholder:text-white/35 light:text-black/90 light:placeholder:text-black/35"
                />

                {!hasContent && !open && (
                    <kbd className="pointer-events-none mr-0.5 hidden rounded border border-brand-main-600 bg-brand-main-800/60 px-1.5 py-0.5 font-mono text-[10px] text-white/35 sm:inline light:border-black/10 light:bg-black/5 light:text-black/35">
                        /
                    </kbd>
                )}
                {hasContent && (
                    <Button
                        variant="ghost"
                        aria-label="Clear all filters"
                        className="text-white/35 hover:text-white/75"
                        onMouseDown={(e) => e.preventDefault()}
                        onClick={clearAll}
                    >
                        <X className="size-3.5" />
                    </Button>
                )}
                <Button
                    variant={editorOpen ? 'secondary' : 'outline'}
                    aria-label="Open ESQL editor"
                    title="ESQL editor (⌘/)"
                    onMouseDown={(e) => e.preventDefault()}
                    onClick={() => {
                        setOpen(false)
                        setEditorOpen((v) => !v)
                    }}
                >
                    <Code2 className="size-3.5" />
                    Editor
                </Button>
            </div>

            {/* Editor pane: freeform ESQL. */}
            {editorOpen && (
                <EsqlEditor
                    initialValue={serializeEsql(committed)}
                    onApply={apply}
                    onClose={() => {
                        setEditorOpen(false)
                        inputRef.current?.focus()
                    }}
                />
            )}

            {/* Autocomplete menu: Lenses + grouped facet families. */}
            {!editorOpen && open && suggestions.length > 0 && (
                <div className="absolute left-0 top-full z-30 mt-1 max-h-[380px] w-full overflow-auto rounded border border-brand-main-600 bg-brand-main-900 shadow-xl">
                    {suggestions.some((s) => s.kind === 'preset') && (
                        <div className="border-b border-brand-main-700/70 px-2 py-2">
                            <div className="px-2 pb-1.5 font-mono text-[10px] uppercase tracking-[0.16em] text-white/35">Lenses</div>
                            <div className="flex flex-wrap gap-1.5 px-1">
                                {suggestions.map((s, i) =>
                                    s.kind === 'preset' ? (
                                        <button
                                            key={s.id}
                                            type="button"
                                            className={cn(
                                                'rounded border px-2.5 py-1 text-xs transition-colors',
                                                i === active
                                                    ? 'border-brand-secondary-500/40 bg-brand-secondary-500/15 text-brand-secondary-200'
                                                    : 'border-brand-main-600 bg-brand-main-800/60 text-white/80 hover:border-brand-secondary-500/30',
                                            )}
                                            onMouseDown={(e) => e.preventDefault()}
                                            onMouseEnter={() => setActive(i)}
                                            onClick={() => chooseSuggestion(s)}
                                        >
                                            {s.label}
                                        </button>
                                    ) : null,
                                )}
                            </div>
                        </div>
                    )}
                    {FAMILIES.map((fam) => {
                        const rows = suggestions
                            .map((s, i) => ({ s, i }))
                            .filter(({ s }) => s.kind === 'field' && s.group === fam.label)
                        if (rows.length === 0) return null
                        return (
                            <div key={fam.label} className="py-1">
                                <div className="px-4 pb-1 pt-1.5 font-mono text-[10px] uppercase tracking-[0.16em] text-white/35">{fam.label}</div>
                                {rows.map(({ s, i }) =>
                                    s.kind === 'field' ? (
                                        <button
                                            key={s.field.id}
                                            type="button"
                                            className={cn(
                                                'flex w-full items-center gap-3 px-4 py-1.5 text-left transition-colors',
                                                i === active ? 'bg-brand-secondary-500/15' : 'hover:bg-brand-main-800',
                                            )}
                                            onMouseDown={(e) => e.preventDefault()}
                                            onMouseEnter={() => setActive(i)}
                                            onClick={() => chooseSuggestion(s)}
                                        >
                                            <span className="text-[13px] text-white/90 light:text-black/90">{s.field.label}</span>
                                            <span className="ml-auto rounded border border-brand-main-600 bg-brand-main-800 px-1.5 py-0.5 font-mono text-[11px] text-white/45">
                                                {s.field.id}
                                                {s.field.ops.includes('exists') ? ' exists' : s.field.type === 'number' || s.field.type === 'duration' ? ' >' : ':'}
                                            </span>
                                        </button>
                                    ) : null,
                                )}
                            </div>
                        )
                    })}
                </div>
            )}

            {error && (
                <div className="absolute left-0 top-full z-20 mt-1 rounded border border-rose-500/30 bg-rose-950/95 px-2.5 py-1.5 text-[12px] text-rose-100 shadow-xl light:bg-rose-50 light:text-rose-700">
                    {error}
                    {suggestion && (
                        <Button
                            variant="link"
                            className="ml-2 h-auto p-0 text-rose-100 underline decoration-dotted underline-offset-2 hover:text-white"
                            onMouseDown={(e) => e.preventDefault()}
                            onClick={() => {
                                setDraft((prev) => prev.replace(/(\S+)(\s*)$/, `${suggestion}:$2`))
                                setError(null)
                                setSuggestion(null)
                                inputRef.current?.focus()
                            }}
                        >
                            use {suggestion}
                        </Button>
                    )}
                </div>
            )}
        </div>
    )
}
