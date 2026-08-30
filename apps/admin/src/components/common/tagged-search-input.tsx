import { useEffect, useMemo, useRef, useState } from 'react'
import { Popover, PopoverContent, PopoverTrigger } from '@everstack/ui/components'
import { HelpCircle, Search, X } from 'lucide-react'
import { cn } from '@/lib/utils'
import {
    evsManagedSearchKeys,
    parseEvsQuery,
    type EvsQueryField,
} from '@/utils/evs-query'

/**
 * Datadog-style faceted search bar. Committed filters render as removable
 * "bubbles" (pills) derived from the URL search params; the trailing input
 * holds the in-progress fragment and free text. Typing surfaces facet-key and
 * known-value suggestions. Parsing/serialization is owned by evs-query.ts.
 */

type Pill = {
    /** stable key for React + removal */
    id: string
    /** dimmed prefix shown before the value, e.g. "status:", "cost ≥" */
    prefix: string
    /** the value portion, e.g. "ERROR", "gpt-4o" */
    value: string
    /** visual accent bucket */
    tone: 'default' | 'error' | 'ok' | 'meta'
    /** the canonical token to drop back into the input when editing */
    editToken: string
    /** produce the next search state that removes just this pill */
    remove: (search: Record<string, any>) => Record<string, any>
    /** search keys this pill also clears (e.g. trace clears span) */
    clearKeys?: string[]
}

/** Static value suggestions for enum-like fields, keyed by searchKey. */
const VALUE_SUGGESTIONS: Record<string, string[]> = {
    statusCode: ['OK', 'ERROR'],
}

const QUERY_EXAMPLES = [
    'status:error provider:anthropic',
    'text:"missing api key" @plan:pro',
    'tag:prod duration>500ms cost<=0.25',
] as const

function splitCsv(value: string | undefined): string[] {
    if (!value) return []
    return value
        .split(',')
        .map((item) => item.trim())
        .filter(Boolean)
}

function unionCsv(existing: string | undefined, incoming: string): string {
    const merged = [...splitCsv(existing), ...splitCsv(incoming)]
    return Array.from(new Set(merged)).join(',')
}

function formatMs(raw: string): string {
    const ms = Number(raw)
    if (!Number.isFinite(ms)) return `${raw}ms`
    if (ms >= 60_000) return `${+(ms / 60_000).toFixed(2)}m`
    if (ms >= 1_000) return `${+(ms / 1_000).toFixed(2)}s`
    return `${ms}ms`
}

function stripToken(token: string): string {
    return token.replace(/:$/, '')
}

/** Derive the pill set from URL search params (free-text `query` excluded). */
export function derivePills(
    search: Record<string, any>,
    fields: EvsQueryField[],
): Pill[] {
    const pills: Pill[] = []
    const fieldByKey = new Map(fields.map((f) => [f.searchKey, f]))

    // Simple scalar facets, rendered as `<token>:<value>`.
    const scalarKeys = [
        'statusCode',
        'trace',
        'correlationId',
        'userId',
        'sessionId',
        'threadId',
        'model',
        'provider',
        'environment',
    ]
    for (const key of scalarKeys) {
        const value = search[key]
        if (!value) continue
        const field = fieldByKey.get(key)
        const prefix = field ? `${stripToken(field.token)}:` : `${key}:`
        const tone: Pill['tone'] =
            key === 'statusCode'
                ? String(value).toUpperCase() === 'ERROR'
                    ? 'error'
                    : 'ok'
                : 'default'
        pills.push({
            id: `${key}:${value}`,
            prefix,
            value: String(value),
            tone,
            editToken: `${stripToken(field?.token ?? key)}:${quoteIfNeeded(String(value))}`,
            remove: (s) => ({ ...s, [key]: undefined }),
            clearKeys: field?.clearKeys,
        })
    }

    // Tags: one pill per comma-separated entry.
    for (const tag of splitCsv(search.tags)) {
        pills.push({
            id: `tag:${tag}`,
            prefix: 'tag:',
            value: tag,
            tone: 'default',
            editToken: `tag:${quoteIfNeeded(tag)}`,
            remove: (s) => {
                const next = splitCsv(s.tags).filter((t) => t !== tag)
                return { ...s, tags: next.length ? next.join(',') : undefined }
            },
        })
    }

    // Metadata: one pill per `key=value` entry, shown as `@key:value`.
    for (const entry of splitCsv(search.metadata)) {
        const eq = entry.indexOf('=')
        const mKey = eq >= 0 ? entry.slice(0, eq) : entry
        const mVal = eq >= 0 ? entry.slice(eq + 1) : ''
        pills.push({
            id: `meta:${entry}`,
            prefix: `@${mKey}:`,
            value: mVal,
            tone: 'meta',
            editToken: `@${mKey}:${quoteIfNeeded(mVal)}`,
            remove: (s) => {
                const next = splitCsv(s.metadata).filter((m) => m !== entry)
                return { ...s, metadata: next.length ? next.join(',') : undefined }
            },
        })
    }

    // Cost / duration range facets.
    const ranges: Array<[string, string, (v: string) => string]> = [
        ['minCost', 'cost ≥', (v) => `$${v}`],
        ['maxCost', 'cost ≤', (v) => `$${v}`],
        ['minDuration', 'duration ≥', formatMs],
        ['maxDuration', 'duration ≤', formatMs],
    ]
    for (const [key, prefix, fmt] of ranges) {
        const value = search[key]
        if (!value) continue
        const tokenBase =
            key === 'minCost'
                ? 'cost>='
                : key === 'maxCost'
                  ? 'cost<='
                  : key === 'minDuration'
                    ? 'duration>='
                    : 'duration<='
        const editSuffix = key.includes('Duration') ? `${value}ms` : String(value)
        pills.push({
            id: `${key}:${value}`,
            prefix,
            value: fmt(String(value)),
            tone: 'default',
            editToken: `${tokenBase}${editSuffix}`,
            remove: (s) => ({ ...s, [key]: undefined }),
        })
    }

    return pills
}

function quoteIfNeeded(value: string): string {
    if (!value) return value
    return /\s|"/.test(value) ? `"${value.replace(/"/g, '\\"')}"` : value
}

type Suggestion = {
    /** text inserted into the draft when chosen */
    insert: string
    /** primary label */
    label: string
    /** secondary hint */
    hint?: string
    /** whether choosing this should immediately commit (values commit, keys don't) */
    commitOnSelect: boolean
}

/** Current whitespace-delimited token the caret is editing (always the tail). */
function currentFragment(draft: string): string {
    const match = draft.match(/(\S*)$/)
    return match ? match[1] : ''
}

function replaceFragment(draft: string, replacement: string): string {
    return draft.replace(/(\S*)$/, replacement)
}

export function buildSuggestions(
    draft: string,
    fields: EvsQueryField[],
): Suggestion[] {
    const fragment = currentFragment(draft)
    const colon = fragment.indexOf(':')

    // Value stage: fragment already has `key:` — suggest known values.
    if (colon > 0 && !fragment.startsWith('@')) {
        const key = fragment.slice(0, colon).toLowerCase()
        const partial = fragment.slice(colon + 1).toLowerCase()
        const field = fields.find(
            (f) =>
                f.id.toLowerCase() === key ||
                stripToken(f.token).toLowerCase() === key ||
                f.aliases?.some((a) => a.toLowerCase() === key),
        )
        if (!field) return []
        const values = VALUE_SUGGESTIONS[field.searchKey] ?? []
        return values
            .filter((v) => v.toLowerCase().startsWith(partial))
            .map((v) => ({
                insert: `${stripToken(field.token)}:${v} `,
                label: v,
                hint: field.label,
                commitOnSelect: true,
            }))
    }

    // Key stage: suggest matching facet tokens.
    const typed = fragment.toLowerCase()
    return fields
        .filter((f) => {
            if (!typed) return true
            const token = stripToken(f.token).toLowerCase()
            return (
                token.startsWith(typed) ||
                f.id.toLowerCase().startsWith(typed) ||
                f.label.toLowerCase().includes(typed) ||
                f.aliases?.some((a) => a.toLowerCase().startsWith(typed))
            )
        })
        .slice(0, 8)
        .map((f) => ({
            insert: replaceFragment(draft, `${stripToken(f.token)}:`),
            label: `${stripToken(f.token)}:`,
            hint: f.label,
            commitOnSelect: false,
        }))
}

export function TaggedSearchInput({
    fields,
    search,
    navigate,
    placeholder,
    className,
}: {
    fields: EvsQueryField[]
    search: Record<string, any>
    navigate: (options: any) => void
    placeholder: string
    className?: string
}) {
    const [draft, setDraft] = useState<string>(() =>
        search.query ? String(search.query) : '',
    )
    const [error, setError] = useState<string | null>(null)
    const [open, setOpen] = useState(false)
    const [active, setActive] = useState(0)
    const [guideOpen, setGuideOpen] = useState(false)
    const inputRef = useRef<HTMLInputElement>(null)

    const pills = useMemo(() => derivePills(search, fields), [search, fields])

    // Keep the free-text portion in sync when the URL query changes externally.
    useEffect(() => {
        setDraft(search.query ? String(search.query) : '')
    }, [search.query])

    const suggestions = useMemo(
        () => (open ? buildSuggestions(draft, fields) : []),
        [open, draft, fields],
    )

    useEffect(() => {
        setActive(0)
    }, [draft])

    const applyPatch = (patch: Record<string, any>) => {
        navigate({ search: { ...search, ...patch }, replace: true })
    }

    /** Parse the draft, merge facets into the URL, keep free text in the input. */
    const commit = (raw = draft): boolean => {
        const value = raw.trim()
        if (!value) {
            applyPatch({ query: undefined })
            setDraft('')
            setError(null)
            return true
        }

        const parsed = parseEvsQuery(value, fields)
        if (!parsed.ok) {
            setError(parsed.errors.join(' · '))
            return false
        }
        setError(null)

        const patch: Record<string, any> = {}
        for (const [key, val] of Object.entries(parsed.filters)) {
            if (key === 'query' || val == null) continue
            if (key === 'tags' || key === 'metadata') {
                patch[key] = unionCsv(search[key], val)
            } else {
                patch[key] = val
            }
        }
        patch.query = parsed.filters.query || undefined
        applyPatch(patch)
        setDraft(parsed.filters.query || '')
        return true
    }

    const removePill = (pill: Pill) => {
        let next = pill.remove(search)
        for (const key of pill.clearKeys ?? []) next = { ...next, [key]: undefined }
        navigate({ search: next, replace: true })
        inputRef.current?.focus()
    }

    const editPill = (pill: Pill) => {
        removePill(pill)
        setDraft((prev) => `${prev ? `${prev} ` : ''}${pill.editToken} `)
        setOpen(true)
        requestAnimationFrame(() => inputRef.current?.focus())
    }

    const chooseSuggestion = (s: Suggestion) => {
        if (s.commitOnSelect) {
            // Substitute the chosen value into the caret fragment, then commit.
            commit(replaceFragment(draft, s.insert))
            setOpen(false)
        } else {
            setDraft(s.insert)
            setOpen(true)
        }
        requestAnimationFrame(() => inputRef.current?.focus())
    }

    const clearAll = () => {
        const cleared = Object.fromEntries(
            evsManagedSearchKeys(fields).map((key) => [key, undefined]),
        )
        navigate({ search: { ...search, ...cleared }, replace: true })
        setDraft('')
        setError(null)
        inputRef.current?.focus()
    }

    const hasContent = pills.length > 0 || draft.length > 0

    return (
        <div className={cn('relative', className)}>
            <div
                className={cn(
                    'flex min-h-8 min-w-0 flex-wrap items-center gap-1 rounded border bg-brand-main-900/70 px-1.5 py-1 text-sm transition-colors focus-within:border-brand-secondary-500/70 light:bg-white/90',
                    error ? 'border-rose-500/60' : 'border-brand-main-600',
                )}
                onClick={() => inputRef.current?.focus()}
            >
                <Search className="ml-0.5 size-4 shrink-0 text-white/35 light:text-black/35" />

                {pills.map((pill) => (
                    <span
                        key={pill.id}
                        className={cn(
                            'inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[11px] font-medium',
                            pill.tone === 'error' &&
                                'border-rose-500/30 bg-rose-500/15 text-rose-200 light:text-rose-700',
                            pill.tone === 'ok' &&
                                'border-emerald-500/30 bg-emerald-500/15 text-emerald-200 light:text-emerald-700',
                            pill.tone === 'meta' &&
                                'border-brand-main-500 bg-brand-main-700/60 text-white/80 light:text-black/70',
                            pill.tone === 'default' &&
                                'border-brand-secondary-500/30 bg-brand-secondary-500/15 text-brand-secondary-200 light:text-brand-secondary-800',
                        )}
                    >
                        <button
                            type="button"
                            className="inline-flex items-center gap-1 outline-none"
                            onMouseDown={(e) => e.preventDefault()}
                            onClick={(e) => {
                                e.stopPropagation()
                                editPill(pill)
                            }}
                            title="Edit filter"
                        >
                            <span className="opacity-60">{pill.prefix}</span>
                            <span>{pill.value}</span>
                        </button>
                        <button
                            type="button"
                            aria-label={`Remove ${pill.prefix}${pill.value}`}
                            className="text-current/60 hover:text-current"
                            onMouseDown={(e) => e.preventDefault()}
                            onClick={(e) => {
                                e.stopPropagation()
                                removePill(pill)
                            }}
                        >
                            <X className="size-2.5" />
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
                                setActive((a) => (a + 1) % suggestions.length)
                                return
                            }
                            if (e.key === 'ArrowUp') {
                                e.preventDefault()
                                setActive(
                                    (a) =>
                                        (a - 1 + suggestions.length) % suggestions.length,
                                )
                                return
                            }
                            if (e.key === 'Tab') {
                                e.preventDefault()
                                chooseSuggestion(suggestions[active])
                                return
                            }
                        }
                        if (e.key === 'Enter') {
                            e.preventDefault()
                            if (open && suggestions.length > 0 && suggestions[active]) {
                                chooseSuggestion(suggestions[active])
                            } else if (commit()) {
                                setOpen(false)
                            }
                            return
                        }
                        if (e.key === 'Escape') {
                            if (open) {
                                setOpen(false)
                            } else {
                                setDraft(search.query ? String(search.query) : '')
                                setError(null)
                            }
                            return
                        }
                        if (e.key === 'Backspace' && !draft && pills.length > 0) {
                            e.preventDefault()
                            removePill(pills[pills.length - 1])
                        }
                    }}
                    onBlur={() => {
                        // Let suggestion/guide clicks land before committing.
                        window.setTimeout(() => {
                            setOpen(false)
                            const value = draft.trim()
                            if (value && value !== (search.query ?? '')) commit()
                        }, 120)
                    }}
                    placeholder={pills.length === 0 ? placeholder : ''}
                    spellCheck={false}
                    className="min-w-[120px] flex-1 bg-transparent px-1 py-0.5 font-mono text-[13px] text-white/90 outline-none placeholder:font-sans placeholder:text-white/35 light:text-black/90 light:placeholder:text-black/35"
                />

                {hasContent && (
                    <button
                        type="button"
                        aria-label="Clear search"
                        className="rounded p-1 text-white/35 transition-colors hover:bg-brand-main-700 hover:text-white/75 light:text-black/35 light:hover:bg-black/10 light:hover:text-black/75"
                        onMouseDown={(e) => e.preventDefault()}
                        onClick={clearAll}
                    >
                        <X className="size-3.5" />
                    </button>
                )}

                <Popover open={guideOpen} onOpenChange={setGuideOpen}>
                    <PopoverTrigger asChild>
                        <button
                            type="button"
                            aria-label="Search syntax guide"
                            className="mr-0.5 rounded p-1 text-white/35 transition-colors hover:bg-brand-main-700 hover:text-white/75 light:text-black/35 light:hover:bg-black/10 light:hover:text-black/75"
                            onMouseDown={(e) => e.preventDefault()}
                        >
                            <HelpCircle className="size-3.5" />
                        </button>
                    </PopoverTrigger>
                    <PopoverContent
                        align="end"
                        side="bottom"
                        sideOffset={8}
                        className="w-[420px] border-brand-main-600 bg-brand-main-900 p-0 shadow-xl"
                    >
                        <div className="border-b border-brand-main-600 px-3 py-2">
                            <div className="text-xs font-semibold text-white light:text-brand-main-50">
                                Search syntax
                            </div>
                            <div className="mt-0.5 text-[11px] text-white/45 light:text-black/45">
                                Type a facet, pick a value, or add free text. Filters
                                combine with spaces.
                            </div>
                        </div>
                        <div className="grid gap-2 p-3">
                            <div className="grid gap-1">
                                {QUERY_EXAMPLES.map((example) => (
                                    <button
                                        key={example}
                                        type="button"
                                        className="rounded border border-brand-main-600 bg-brand-main-800/60 px-2 py-1.5 text-left font-mono text-[11px] text-white/75 transition-colors hover:border-brand-secondary-500/40 hover:text-white light:bg-black/5 light:text-black/70 light:hover:text-black"
                                        onClick={() => {
                                            setGuideOpen(false)
                                            commit(example)
                                            requestAnimationFrame(() =>
                                                inputRef.current?.focus(),
                                            )
                                        }}
                                    >
                                        {example}
                                    </button>
                                ))}
                            </div>
                            <div className="max-h-56 overflow-auto rounded border border-brand-main-600">
                                {fields.map((field) => (
                                    <div
                                        key={field.id}
                                        className="grid grid-cols-[92px_minmax(0,1fr)] gap-2 border-b border-brand-main-600 px-2 py-1.5 last:border-b-0"
                                    >
                                        <span className="font-mono text-[10px] text-brand-secondary-300">
                                            {field.token}
                                        </span>
                                        <span className="truncate text-[11px] text-white/60 light:text-black/60">
                                            {field.label}
                                        </span>
                                    </div>
                                ))}
                                <div className="grid grid-cols-[92px_minmax(0,1fr)] gap-2 border-t border-brand-main-600 px-2 py-1.5">
                                    <span className="font-mono text-[10px] text-brand-secondary-300">
                                        @key:
                                    </span>
                                    <span className="truncate text-[11px] text-white/60 light:text-black/60">
                                        Metadata shorthand
                                    </span>
                                </div>
                            </div>
                        </div>
                    </PopoverContent>
                </Popover>
            </div>

            {/* Suggestion dropdown — non-focus-stealing, anchored under the bar. */}
            {open && suggestions.length > 0 && (
                <div className="absolute left-0 top-full z-30 mt-1 w-full max-w-md overflow-hidden rounded border border-brand-main-600 bg-brand-main-900 shadow-xl">
                    {suggestions.map((s, i) => (
                        <button
                            key={`${s.label}-${i}`}
                            type="button"
                            className={cn(
                                'flex w-full items-center justify-between gap-2 px-3 py-1.5 text-left transition-colors',
                                i === active
                                    ? 'bg-brand-secondary-500/15'
                                    : 'hover:bg-brand-main-800',
                            )}
                            onMouseDown={(e) => e.preventDefault()}
                            onMouseEnter={() => setActive(i)}
                            onClick={() => chooseSuggestion(s)}
                        >
                            <span className="font-mono text-[12px] text-white/90 light:text-black/90">
                                {s.label}
                            </span>
                            {s.hint && (
                                <span className="truncate text-[11px] text-white/40 light:text-black/40">
                                    {s.hint}
                                </span>
                            )}
                        </button>
                    ))}
                </div>
            )}

            {error && (
                <div className="absolute left-0 top-full z-20 mt-1 rounded border border-rose-500/30 bg-rose-950/95 px-2 py-1 text-[11px] text-rose-100 shadow-xl light:bg-rose-50 light:text-rose-700">
                    {error}
                </div>
            )}
        </div>
    )
}
