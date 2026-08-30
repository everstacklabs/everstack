import { useCallback, useState } from 'react'
import { Icon } from '@iconify/react'
import { ui } from '@everstack/ui'
import { Route } from '@/routes/deployments/studio/$workflowId'
import { useStudioStore } from '@/stores/studio-store'
import { useWorkflowVersionHistory } from '@/hooks/deployments/use-workflows'
import type { WorkflowVersionEntry, WorkflowChangeDetail } from '@/server/workflows'

const { Sheet, SheetContent, SheetHeader, SheetTitle, Badge, Collapsible, CollapsibleTrigger, CollapsibleContent } = ui

interface VersionHistorySheetProps {
    open: boolean
    onOpenChange: (open: boolean) => void
}

const CATEGORY_ICONS: Record<string, string> = {
    nodes: 'lucide:box',
    edges: 'lucide:git-branch',
    name: 'lucide:type',
    description: 'lucide:file-text',
    status: 'lucide:zap',
}

const CATEGORY_LABELS: Record<string, string> = {
    nodes: 'Nodes',
    edges: 'Connections',
    name: 'Name',
    description: 'Description',
    status: 'Status',
}

function formatTimestamp(timestamp: { seconds: bigint; nanos: number } | undefined): string {
    if (!timestamp) return ''
    const date = new Date(Number(timestamp.seconds) * 1000)
    const now = new Date()
    const diffMs = now.getTime() - date.getTime()
    const diffMins = Math.floor(diffMs / 60000)
    const diffHours = Math.floor(diffMs / 3600000)
    const diffDays = Math.floor(diffMs / 86400000)

    if (diffMins < 1) return 'Just now'
    if (diffMins < 60) return `${diffMins}m ago`
    if (diffHours < 24) return `${diffHours}h ago`
    if (diffDays < 7) return `${diffDays}d ago`
    return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' })
}

function getCollapsedSummary(entry: WorkflowVersionEntry): string {
    if (entry.details && entry.details.length > 0) {
        return entry.details
            .map((d) => {
                const label = CATEGORY_LABELS[d.category] || d.category
                if (d.category === 'status') return d.summary
                return `${label} updated`
            })
            .join(', ')
    }
    return entry.changes.join(', ')
}

function ChangeDetailSection({ detail }: { detail: WorkflowChangeDetail }) {
    const icon = CATEGORY_ICONS[detail.category] || 'lucide:circle'
    const label = CATEGORY_LABELS[detail.category] || detail.category

    return (
        <div className="py-1">
            <div className="flex items-center gap-1.5 text-xs text-brand-main-200">
                <Icon icon={icon} className="h-3.5 w-3.5 text-brand-main-400 flex-shrink-0" />
                <span className="font-medium">{label}</span>
                <span className="text-brand-main-400">({detail.summary})</span>
            </div>
            {detail.items && detail.items.length > 0 && (
                <ul className="ml-5 mt-1 space-y-0.5">
                    {detail.items.map((item, idx) => (
                        <li key={idx} className="text-[11px] text-brand-main-300 flex items-center gap-1.5">
                            <span className="text-brand-main-500">•</span>
                            {item}
                        </li>
                    ))}
                </ul>
            )}
        </div>
    )
}

function VersionEntry({
    entry,
    isLatest,
    onPreview,
}: {
    entry: WorkflowVersionEntry
    isLatest: boolean
    onPreview: (version: number) => void
}) {
    const [isOpen, setIsOpen] = useState(false)
    const hasDetails = entry.details && entry.details.length > 0
    const isExpandable = hasDetails || entry.changes.length > 0

    const handleVersionClick = useCallback(() => {
        onPreview(entry.version)
    }, [entry.version, onPreview])

    return (
        <div className="relative flex gap-3">
            {/* Timeline dot */}
            <div className="relative z-10 mt-1.5 flex-shrink-0">
                <div
                    className={`h-[15px] w-[15px] rounded-full border-2 ${isLatest
                        ? 'border-brand-secondary-500 bg-brand-secondary-500/30'
                        : 'border-brand-main-600 bg-brand-main-800'
                        }`}
                />
            </div>

            {/* Content */}
            <div className="flex-1 min-w-0 pb-1">
                {isExpandable ? (
                    <Collapsible open={isOpen} onOpenChange={setIsOpen}>
                        <CollapsibleTrigger className="w-full text-left cursor-pointer">
                            <div className="flex items-start justify-between gap-2">
                                <div className="min-w-0 flex-1">
                                    <div className="flex items-center gap-2 mb-0.5">
                                        <Badge
                                            className={`text-[10px] px-1.5 py-0 h-4 font-mono font-normal ${isLatest
                                                ? 'bg-brand-secondary-600/20 text-brand-secondary-300 border border-brand-secondary-500'
                                                : 'bg-brand-main-600/20 text-brand-main-300 border border-brand-main-500'
                                                }`}
                                        >
                                            v{entry.version}
                                        </Badge>
                                        <span className="text-[11px] text-brand-main-400">
                                            {formatTimestamp(entry.timestamp)}
                                        </span>
                                    </div>
                                    <p className="text-xs text-brand-main-300 truncate">
                                        {getCollapsedSummary(entry)}
                                    </p>
                                </div>
                                <Icon
                                    icon={isOpen ? 'lucide:chevron-down' : 'lucide:chevron-right'}
                                    className="h-3.5 w-3.5 text-brand-main-500 mt-1 flex-shrink-0"
                                />
                            </div>
                        </CollapsibleTrigger>
                        <CollapsibleContent>
                            <div className="mt-2 pl-1 border-l border-brand-main-700 ml-1">
                                {hasDetails ? (
                                    entry.details.map((detail, idx) => (
                                        <ChangeDetailSection key={idx} detail={detail} />
                                    ))
                                ) : (
                                    <div className="space-y-0.5 py-1">
                                        {entry.changes.map((change, idx) => (
                                            <p key={idx} className="text-xs text-brand-main-200">
                                                {change}
                                            </p>
                                        ))}
                                    </div>
                                )}

                                {/* Preview button */}
                                <button
                                    onClick={handleVersionClick}
                                    className="mt-2 flex items-center gap-1.5 text-[11px] rounded px-2 py-1 text-brand-main-400 hover:text-brand-main-200 hover:bg-brand-main-700/50 transition-colors"
                                >
                                    <Icon icon="lucide:eye" className="h-3 w-3" />
                                    View version
                                </button>
                            </div>
                        </CollapsibleContent>
                    </Collapsible>
                ) : (
                    <div>
                        <div className="flex items-center gap-2 mb-1">
                            <Badge
                                className={`text-[10px] px-1.5 py-0 h-4 font-mono font-normal ${isLatest
                                    ? 'bg-brand-secondary-600/20 text-brand-secondary-300 border border-brand-secondary-500'
                                    : 'bg-brand-main-600/20 text-brand-main-300 border border-brand-main-500'
                                    }`}
                            >
                                v{entry.version}
                            </Badge>
                            <span className="text-[11px] text-brand-main-400">
                                {formatTimestamp(entry.timestamp)}
                            </span>
                        </div>
                        <p className="text-xs text-brand-main-300">
                            {getCollapsedSummary(entry)}
                        </p>
                    </div>
                )}
            </div>
        </div>
    )
}

export function VersionHistorySheet({ open, onOpenChange }: VersionHistorySheetProps) {
    const navigate = Route.useNavigate()
    const workflowId = useStudioStore((s) => s.workflowId)
    const { data: versions, isLoading, error } = useWorkflowVersionHistory(open ? workflowId : null)

    const handlePreviewVersion = useCallback((version: number) => {
        onOpenChange(false)
        navigate({ search: (prev) => ({ ...prev, version }) })
    }, [navigate, onOpenChange])

    const sortedVersions = versions ? [...versions].reverse() : []

    return (
        <Sheet open={open} onOpenChange={onOpenChange}>
            <SheetContent
                overlayClassName="bg-transparent"
                className="bg-brand-main-900 border-l-brand-main-500 w-full sm:max-w-sm flex flex-col"
            >
                <SheetHeader className="flex items-center space-x-2">
                    <SheetTitle className="text-white light:text-brand-main-50 text-base font-semibold flex items-center gap-2">
                        <Icon icon="lucide:clock" className="h-4 w-4 text-brand-main-300" />
                        Version History
                    </SheetTitle>
                </SheetHeader>

                <div className="flex-1 overflow-y-auto px-4 py-3">
                    {isLoading && (
                        <div className="flex items-center justify-center py-12">
                            <Icon icon="lucide:loader-2" className="h-5 w-5 animate-spin text-brand-main-400" />
                        </div>
                    )}

                    {!isLoading && error && (
                        <div className="flex flex-col items-center justify-center py-12 text-red-400 light:text-red-600">
                            <Icon icon="lucide:alert-circle" className="h-8 w-8 mb-2 opacity-50" />
                            <p className="text-sm">Failed to load version history</p>
                            <p className="text-xs mt-1 opacity-70">{error.message}</p>
                        </div>
                    )}

                    {!isLoading && !error && sortedVersions.length === 0 && (
                        <div className="flex flex-col items-center justify-center py-12 text-brand-main-400">
                            <Icon icon="lucide:clock" className="h-8 w-8 mb-2 opacity-50" />
                            <p className="text-sm">No version history yet</p>
                            <p className="text-xs mt-1 opacity-70">Changes will appear here after saving</p>
                        </div>
                    )}

                    {!isLoading && sortedVersions.length > 0 && (
                        <div className="relative">
                            {/* Vertical timeline line */}
                            <div className="absolute left-[7px] top-2 bottom-2 w-px bg-brand-main-700" />

                            <div className="space-y-4">
                                {sortedVersions.map((entry, index) => (
                                    <VersionEntry
                                        key={entry.version}
                                        entry={entry}
                                        isLatest={index === 0}
                                        onPreview={handlePreviewVersion}
                                    />
                                ))}
                            </div>
                        </div>
                    )}
                </div>
            </SheetContent>
        </Sheet>
    )
}
