import { Fragment, useMemo } from 'react'
import { FileCode2, Folder } from 'lucide-react'
import { cn } from '@/lib/utils'
import { ui } from '@everstack/ui'

interface MentionTextProps {
    children: string
    className?: string
    variant?: 'default' | 'composer'
}

interface Segment {
    type: 'text' | 'mention'
    value: string
}

const { Tooltip, TooltipProvider } = ui

/**
 * Regex to find raw @mentions (validated and normalized in code below).
 */
const RAW_MENTION_RE = /@(\S+)/g
const TRAILING_PUNCTUATION_RE = /[.,!?;:)\]}>"'`]+$/

function isMentionBoundary(prevChar: string | undefined): boolean {
    if (!prevChar) return true
    return /\s|[([{"'`]/.test(prevChar)
}

function looksLikeReference(value: string): boolean {
    return value.length > 0
}

function trimTrailingPunctuation(value: string): string {
    return value.replace(TRAILING_PUNCTUATION_RE, '')
}

function parseSegments(text: string): Segment[] {
    const segments: Segment[] = []
    let lastIndex = 0

    for (const match of text.matchAll(RAW_MENTION_RE)) {
        const start = match.index ?? 0
        const prevChar = start > 0 ? text[start - 1] : undefined
        if (!isMentionBoundary(prevChar)) continue

        const rawMention = match[1]
        const mention = trimTrailingPunctuation(rawMention)
        if (!mention || !looksLikeReference(mention)) continue

        const rawEnd = start + match[0].length
        const mentionEnd = start + 1 + mention.length

        if (start > lastIndex) {
            segments.push({ type: 'text', value: text.slice(lastIndex, start) })
        }

        segments.push({ type: 'mention', value: mention })

        // Keep punctuation (quotes/commas/etc.) outside the badge.
        if (mentionEnd < rawEnd) {
            segments.push({ type: 'text', value: text.slice(mentionEnd, rawEnd) })
        }

        lastIndex = rawEnd
    }

    // Trailing text
    if (lastIndex < text.length) {
        segments.push({ type: 'text', value: text.slice(lastIndex) })
    }

    return segments
}

function getPathTail(path: string): string {
    const normalized = path.replace(/\/+$/, '')
    if (!normalized) return path
    const parts = normalized.split('/')
    return parts[parts.length - 1] || normalized
}

function looksLikeDirectory(path: string): boolean {
    return path.endsWith('/') || !path.includes('.')
}

/**
 * Renders user message text with @file/path mentions as inline badges.
 */
export function MentionText({ children, className, variant = 'default' }: MentionTextProps) {
    const segments = useMemo(() => parseSegments(children), [children])

    // Fast path: no mentions, render plain text
    if (segments.length === 1 && segments[0].type === 'text') {
        return <span className={className}>{children}</span>
    }

    return (
        <TooltipProvider>
            <span className={cn(className)}>
                {segments.map((seg, i) => {
                    if (seg.type === 'text') {
                        return <Fragment key={i}>{seg.value}</Fragment>
                    }

                    if (variant === 'composer') {
                        const displayName = getPathTail(seg.value)
                        const isDir = looksLikeDirectory(seg.value)
                        return (
                            <Tooltip key={i} content={seg.value}>
                                <span
                                    aria-label={seg.value}
                                    title={seg.value}
                                    className="relative text-sm inline-flex items-center gap-1 align-baseline z-0 px-0.5 text-brand-secondary-200 leading-[inherit]"
                                >
                                    {isDir ? (
                                        <Folder className="w-3 h-3 shrink-0 opacity-75" />
                                    ) : (
                                        <FileCode2 className="w-3 h-3 shrink-0 opacity-75" />
                                    )}
                                    {displayName}
                                    <span
                                        aria-hidden
                                        className="pointer-events-none absolute inset-y-px inset-x-0 rounded bg-brand-secondary-500/22 [box-shadow:inset_0_0_0_1px_rgba(167,139,250,0.38),inset_0_1px_0_rgba(255,255,255,0.08)] -z-10"
                                    />
                                </span>
                            </Tooltip>
                        )
                    }

                    const isDir = looksLikeDirectory(seg.value)
                    const displayName = getPathTail(seg.value)

                    return (
                        <Tooltip key={i} content={seg.value}>
                            <span
                                aria-label={seg.value}
                                title={seg.value}
                                className="inline-flex items-center gap-1 px-1.5 py-0.5 mx-0.5 rounded-md bg-brand-secondary-500/15 border border-brand-secondary-500/20 text-brand-secondary-300 text-xs font-mono leading-tight align-baseline"
                            >
                                {isDir ? (
                                    <Folder className="w-3 h-3 shrink-0 opacity-60" />
                                ) : (
                                    <FileCode2 className="w-3 h-3 shrink-0 opacity-60" />
                                )}
                                {displayName}
                            </span>
                        </Tooltip>
                    )
                })}
            </span>
        </TooltipProvider>
    )
}
