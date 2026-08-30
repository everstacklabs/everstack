import { cn } from '@everstack/utils/functions/cn'
import type { CSSProperties } from 'react'

type TraceSheetSkeletonVariant = 'full' | 'tree' | 'detail'

interface TraceSheetSkeletonProps {
    variant?: TraceSheetSkeletonVariant
    label?: string
    className?: string
}

function SkeletonBlock({
    className,
    style,
}: {
    className?: string
    style?: CSSProperties
}) {
    return (
        <div
            className={cn(
                'animate-pulse rounded bg-brand-main-600/50 light:bg-black/10',
                className,
            )}
            style={style}
        />
    )
}

function TraceTreeSkeleton() {
    const rows = [
        { width: '72%', depth: 0 },
        { width: '58%', depth: 1 },
        { width: '64%', depth: 1 },
        { width: '52%', depth: 2 },
        { width: '70%', depth: 1 },
        { width: '46%', depth: 2 },
        { width: '61%', depth: 2 },
        { width: '55%', depth: 1 },
    ]

    return (
        <div className="flex h-full min-h-0 flex-col overflow-hidden border-r border-brand-main-500/80 bg-brand-main-800/35 light:border-border light:bg-black/[0.02]">
            <div className="flex shrink-0 items-center gap-2 border-b border-brand-main-600/70 px-2 py-2 light:border-border">
                <SkeletonBlock className="h-7 flex-1" />
                <SkeletonBlock className="size-7" />
                <SkeletonBlock className="size-7" />
            </div>
            <div className="min-h-0 flex-1 space-y-1.5 overflow-hidden px-2 py-2">
                {rows.map((row, index) => (
                    <div
                        key={index}
                        className="flex h-8 items-center gap-2 rounded border border-transparent px-2"
                        style={{ paddingLeft: `${8 + row.depth * 20}px` }}
                    >
                        <SkeletonBlock className="size-5 shrink-0 rounded" />
                        <SkeletonBlock className="h-3.5 min-w-0" style={{ width: row.width }} />
                        <div className="ml-auto flex shrink-0 items-center gap-2">
                            <SkeletonBlock className="h-4 w-14" />
                            <SkeletonBlock className="h-4 w-10" />
                        </div>
                    </div>
                ))}
            </div>
        </div>
    )
}

function TraceDetailSkeleton() {
    return (
        <div className="flex h-full min-h-0 flex-col overflow-hidden bg-brand-main-700 light:bg-background">
            <div className="shrink-0 border-b border-brand-main-500 px-3 py-2 light:border-border">
                <div className="flex items-start justify-between gap-3">
                    <div className="flex min-w-0 items-center gap-2">
                        <SkeletonBlock className="size-8 shrink-0 rounded" />
                        <div className="min-w-0 space-y-1.5">
                            <SkeletonBlock className="h-4 w-40" />
                            <SkeletonBlock className="h-3 w-56" />
                        </div>
                    </div>
                    <div className="flex shrink-0 items-center gap-2">
                        <SkeletonBlock className="h-7 w-24" />
                        <SkeletonBlock className="h-7 w-20" />
                    </div>
                </div>
                <div className="mt-2 grid grid-cols-4 gap-1.5">
                    {Array.from({ length: 4 }).map((_, index) => (
                        <div
                            key={index}
                            className="rounded border border-brand-main-500 bg-brand-main-600/10 p-2 light:border-border"
                        >
                            <SkeletonBlock className="h-3 w-16" />
                            <SkeletonBlock className="mt-2 h-4 w-20" />
                        </div>
                    ))}
                </div>
            </div>
            <div className="flex shrink-0 items-center gap-2 border-b border-brand-main-500 px-3 py-1.5 light:border-border">
                {Array.from({ length: 5 }).map((_, index) => (
                    <SkeletonBlock key={index} className="h-7 w-24" />
                ))}
            </div>
            <div className="min-h-0 flex-1 space-y-2 overflow-hidden p-3">
                <div className="rounded border border-brand-main-500 bg-brand-main-900/25 light:border-border light:bg-black/[0.02]">
                    <div className="flex h-9 items-center gap-2 border-b border-brand-main-600 px-3 light:border-border">
                        <SkeletonBlock className="size-4 rounded" />
                        <SkeletonBlock className="h-3.5 w-20" />
                    </div>
                    <div className="space-y-2 p-3">
                        <SkeletonBlock className="h-3.5 w-full" />
                        <SkeletonBlock className="h-3.5 w-[86%]" />
                        <SkeletonBlock className="h-3.5 w-[68%]" />
                    </div>
                </div>
                <div className="rounded border border-brand-main-500 bg-brand-main-900/25 light:border-border light:bg-black/[0.02]">
                    <div className="flex h-9 items-center gap-2 border-b border-brand-main-600 px-3 light:border-border">
                        <SkeletonBlock className="size-4 rounded" />
                        <SkeletonBlock className="h-3.5 w-24" />
                    </div>
                    <div className="space-y-2 p-3">
                        <SkeletonBlock className="h-3.5 w-[92%]" />
                        <SkeletonBlock className="h-3.5 w-[74%]" />
                        <SkeletonBlock className="h-3.5 w-[81%]" />
                    </div>
                </div>
            </div>
        </div>
    )
}

function TraceSheetSkeletonContent({ variant }: { variant: TraceSheetSkeletonVariant }) {
    if (variant === 'tree') return <TraceTreeSkeleton />
    if (variant === 'detail') return <TraceDetailSkeleton />

    return (
        <div className="grid h-full min-h-0 grid-cols-[minmax(260px,33%)_1fr] overflow-hidden">
            <TraceTreeSkeleton />
            <TraceDetailSkeleton />
        </div>
    )
}

export function TraceSheetSkeleton({
    variant = 'full',
    label = 'Loading trace details',
    className,
}: TraceSheetSkeletonProps) {
    return (
        <div
            aria-busy="true"
            aria-label={label}
            className={cn('min-h-0 flex-1 overflow-hidden', className)}
        >
            <span className="sr-only">{label}</span>
            <TraceSheetSkeletonContent variant={variant} />
        </div>
    )
}
