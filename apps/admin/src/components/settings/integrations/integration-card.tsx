import type { ReactNode } from 'react'
import { ui } from '@everstack/ui'
import { Iconify } from '@everstack/ui/icons'

const { Card, CardHeader, CardTitle, CardDescription, CardContent, Badge } = ui

export type IntegrationStatus = 'live' | 'beta' | 'coming_soon'

type IntegrationCardProps = {
    name: string
    icon: string
    category: string
    description: string
    status: IntegrationStatus
    capabilities?: string[]
    action?: ReactNode
    children?: ReactNode
}

const STATUS_META: Record<IntegrationStatus, { label: string; className: string }> = {
    live: {
        label: 'Live',
        className: 'border-zinc-500/40 bg-zinc-800/30 light:bg-zinc-200/60 text-zinc-300 light:text-zinc-700',
    },
    beta: {
        label: 'Beta',
        className: 'border-zinc-500/40 bg-zinc-800/30 light:bg-zinc-200/60 text-zinc-300 light:text-zinc-700',
    },
    coming_soon: {
        label: 'Coming Soon',
        className: 'border-zinc-600/40 bg-zinc-800/20 light:bg-zinc-200/40 text-zinc-400 light:text-zinc-600',
    },
}

export function IntegrationCard({
    name,
    icon,
    category,
    description,
    status,
    capabilities,
    action,
    children,
}: IntegrationCardProps) {
    const meta = STATUS_META[status]

    return (
        <Card className="border-brand-main-500/50 bg-brand-main-900/50 h-[260px] gap-4 py-4 rounded-lg">
            <CardHeader className="space-y-2 px-4">
                <div className="flex items-start justify-between gap-4">
                    <div className="space-y-1.5">
                        <div className="flex items-center gap-2">
                            <Iconify.Icon icon={icon} className="w-4.5 h-4.5 text-zinc-100 light:text-zinc-900" />
                            <CardTitle className="text-white light:text-brand-main-50 text-base">{name}</CardTitle>
                            <Badge className={meta.className}>{meta.label}</Badge>
                        </div>
                        <CardDescription className="text-zinc-400 light:text-zinc-600 text-xs">{description}</CardDescription>
                    </div>
                    {action}
                </div>
                <div className="flex items-center justify-between gap-2">
                    <Badge variant="outline" className="text-[10px] text-zinc-300 light:text-zinc-700 border-brand-main-600">
                        {category}
                    </Badge>
                    {capabilities && capabilities.length > 0 ? (
                        <span className="text-[11px] text-zinc-500">{capabilities.length} capabilities</span>
                    ) : null}
                </div>
            </CardHeader>
            <CardContent className="space-y-3 px-4 pt-0 h-full">
                {capabilities && capabilities.length > 0 ? (
                    <div className="flex flex-wrap gap-1.5">
                        {capabilities.map((capability) => (
                            <span
                                key={capability}
                                className="inline-flex items-center rounded border border-brand-main-600 bg-brand-main-900/45 px-2 py-0.5 text-[10px] text-zinc-300 light:text-zinc-700"
                            >
                                {capability}
                            </span>
                        ))}
                    </div>
                ) : null}
                {children}
            </CardContent>
        </Card>
    )
}
