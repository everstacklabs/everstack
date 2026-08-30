import { createFileRoute, Link } from '@tanstack/react-router'
import { z } from 'zod'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import {
    usePrompt,
    usePromptVersions,
    useSetPromptLabels,
} from '@/hooks/evaluations/use-prompts'
import { versionConfig, type PromptVersion } from '@/server/prompts'
import { Loader, toast } from '@everstack/ui/components'
import { ui } from '@everstack/ui'
import { cn } from '@everstack/utils/functions/cn'
import { formatTimestamp } from '@everstack/utils/functions/index'
import { ArrowLeft, GitCompare, Tag, X } from 'lucide-react'

const { Badge, Card } = ui

const promptDetailSearchSchema = z.object({
    // Selected version, mirrored in the URL so the topbar can act on it.
    v: z.coerce.number().optional(),
})

export const Route = createFileRoute('/evaluations/prompts-library_/$promptId')({
    component: PromptDetailPage,
    validateSearch: promptDetailSearchSchema,
})

const roleStyles: Record<string, string> = {
    system: 'text-amber-300/90 light:text-amber-700/90 border-amber-400/30 bg-amber-400/5',
    user: 'text-brand-secondary-300 border-brand-secondary-500/30 bg-brand-secondary-500/5',
    assistant: 'text-emerald-300/90 light:text-emerald-600/90 border-emerald-400/30 bg-emerald-400/5',
}

function PromptDetailPage() {
    const gate = useFeatureGate(FeatureKey.PROMPT_MANAGEMENT)

    if (gate.isBlocked) {
        return (
            <FeatureGateBanner
                featureName="Prompt Management"
                description="Version and manage prompt templates for consistent evaluation workflows."
                requiredTier="Pro"
                upgradeUrl={gate.upgradeUrl}
                isCE={gate.isCE}
            />
        )
    }

    return <PromptDetailContent />
}

function PromptDetailContent() {
    const { promptId } = Route.useParams()
    const search = Route.useSearch()
    const navigate = Route.useNavigate()
    const { data: prompt, isLoading: promptLoading } = usePrompt(promptId)
    const { data: versions, isLoading: versionsLoading } = usePromptVersions(promptId)

    const setSelectedVersion = (version: number) =>
        void navigate({ search: (p) => ({ ...p, v: version }), replace: true })

    const sorted = versions ?? []
    const current =
        sorted.find((v) => v.version === search.v) ?? sorted[0] ?? null
    const previous = current
        ? sorted.find((v) => v.version < current.version) ?? null
        : null

    if (promptLoading || versionsLoading) {
        return (
            <div className="flex-1 h-full flex items-center justify-center text-white/70 light:text-black/70">
                <Loader loaderText="Loading prompt..." />
            </div>
        )
    }

    if (!prompt) {
        return (
            <div className="flex-1 h-full flex flex-col items-center justify-center gap-3 text-white/50 light:text-black/50 text-sm">
                Prompt not found.
                <Link
                    to="/evaluations/prompts-library"
                    className="text-brand-secondary-300 hover:text-brand-secondary-200 inline-flex items-center gap-1 text-xs"
                >
                    <ArrowLeft className="h-3 w-3" /> Back to library
                </Link>
            </div>
        )
    }

    return (
        <div className="flex flex-col h-full w-full overflow-hidden p-4 gap-3">
            {(prompt.description || (prompt.tags?.length ?? 0) > 0) && (
                <div className="shrink-0 flex items-center gap-2 flex-wrap">
                    {prompt.description && (
                        <span className="text-xs text-white/50 light:text-black/50">{prompt.description}</span>
                    )}
                    {prompt.tags?.map((tag) => (
                        <Badge
                            key={tag}
                            variant="outline"
                            className="text-[10px] border-brand-main-500 text-white/60 light:text-black/60"
                        >
                            {tag}
                        </Badge>
                    ))}
                </div>
            )}

            {sorted.length === 0 ? (
                <Card className="border-brand-main-500 bg-brand-main-900/40 flex-1 flex flex-col items-center justify-center gap-2">
                    <p className="text-sm text-white/50 light:text-black/50">No versions yet.</p>
                    <p className="text-xs text-white/30 light:text-black/30 max-w-sm text-center">
                        Create version 1 from the New version button above, or build the
                        conversation in the playground and save it to this prompt.
                    </p>
                </Card>
            ) : (
                <div className="flex gap-3 flex-1 min-h-0">
                    {/* Version history */}
                    <Card className="border-brand-main-500 bg-brand-main-900/40 w-[260px] shrink-0 overflow-hidden flex flex-col">
                        <div className="px-3 py-2 border-b border-brand-main-700 text-sm font-medium text-white light:text-brand-main-50">
                            Versions
                        </div>
                        <div className="flex-1 overflow-auto">
                            {sorted.map((v) => (
                                <button
                                    key={v.id}
                                    type="button"
                                    onClick={() => setSelectedVersion(v.version)}
                                    className={cn(
                                        'w-full text-left px-3 py-2 border-b border-brand-main-700/50 hover:bg-brand-main-800/60 transition-colors',
                                        current?.version === v.version && 'bg-brand-main-800/80',
                                    )}
                                >
                                    <div className="flex items-center gap-2">
                                        <span className="text-xs font-mono text-brand-secondary-200">
                                            v{v.version}
                                        </span>
                                        {v.labels?.map((label) => (
                                            <Badge
                                                key={label}
                                                variant="outline"
                                                className="text-[10px] border-emerald-400/30 text-emerald-300 light:text-emerald-600"
                                            >
                                                {label}
                                            </Badge>
                                        ))}
                                    </div>
                                    <div className="text-[11px] text-white/50 light:text-black/50 truncate mt-0.5">
                                        {v.commitMessage || 'No commit message'}
                                    </div>
                                    <div className="text-[10px] text-white/30 light:text-black/30 mt-0.5">
                                        {v.createdAt ? formatTimestamp(v.createdAt) : ''}
                                    </div>
                                </button>
                            ))}
                        </div>
                    </Card>

                    {/* Version content */}
                    {current && (
                        <Card className="border-brand-main-500 bg-brand-main-900/40 flex-1 min-w-0 overflow-hidden flex flex-col">
                            <div className="px-3 py-2 border-b border-brand-main-700 flex items-center gap-3">
                                <span className="text-sm font-medium text-white light:text-brand-main-50">
                                    Version {current.version}
                                </span>
                                <ConfigSummary version={current} />
                                <div className="ml-auto flex items-center gap-2">
                                    {previous && (
                                        <Link
                                            to="/evaluations/prompts-library/compare"
                                            search={{
                                                lp: promptId,
                                                lv: previous.version,
                                                rp: promptId,
                                                rv: current.version,
                                            }}
                                            className="inline-flex items-center gap-1 text-[11px] rounded border px-2 py-0.5 transition-colors border-brand-main-500 text-white/50 light:text-black/50 hover:text-white light:hover:text-brand-main-50"
                                        >
                                            <GitCompare className="h-3 w-3" />
                                            Diff vs v{previous.version}
                                        </Link>
                                    )}
                                    <LabelsEditor promptId={promptId} version={current} />
                                </div>
                            </div>
                            <div className="flex-1 overflow-auto p-3 space-y-2">
                                {current.messages.map((m, i) => (
                                    <div
                                        key={`${current.id}-${i}`}
                                        className="rounded border border-brand-main-600 bg-brand-main-800/40"
                                    >
                                        <div className="px-2 pt-1.5">
                                            <span
                                                className={cn(
                                                    'rounded border px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide',
                                                    roleStyles[m.role] ?? roleStyles.user,
                                                )}
                                            >
                                                {m.role}
                                            </span>
                                        </div>
                                        <div className="px-2 py-1.5 text-xs font-mono text-zinc-200 light:text-zinc-800 whitespace-pre-wrap leading-relaxed">
                                            {m.content}
                                        </div>
                                    </div>
                                ))}
                            </div>
                        </Card>
                    )}
                </div>
            )}
        </div>
    )
}

function ConfigSummary({ version }: { version: PromptVersion }) {
    const config = versionConfig(version)
    const parts: string[] = []
    if (config.model) parts.push(config.model)
    if (config.temperature !== undefined) parts.push(`temp ${config.temperature}`)
    if (config.maxTokens !== undefined) parts.push(`max ${config.maxTokens}`)
    if (parts.length === 0) return null
    return <span className="text-[10px] font-mono text-white/40 light:text-black/40">{parts.join(' · ')}</span>
}

function LabelsEditor({ promptId, version }: { promptId: string; version: PromptVersion }) {
    const setLabelsMutation = useSetPromptLabels()
    const labels = version.labels ?? []

    const save = async (next: string[]) => {
        try {
            await setLabelsMutation.mutateAsync({
                promptId,
                version: version.version,
                labels: next,
            })
            toast.success('Labels updated')
        } catch {
            toast.error('Failed to update labels')
        }
    }

    return (
        <div className="flex items-center gap-1">
            {labels.map((label) => (
                <Badge
                    key={label}
                    variant="outline"
                    className="text-[10px] border-emerald-400/30 text-emerald-300 light:text-emerald-600 inline-flex items-center gap-1"
                >
                    {label}
                    <button
                        type="button"
                        onClick={() => void save(labels.filter((l) => l !== label))}
                        className="hover:text-rose-300 light:hover:text-rose-600"
                        title={`Remove ${label}`}
                    >
                        <X className="h-2.5 w-2.5" />
                    </button>
                </Badge>
            ))}
            {!labels.includes('production') && (
                <button
                    type="button"
                    onClick={() => void save([...labels, 'production'])}
                    className="inline-flex items-center gap-1 text-[11px] text-white/50 light:text-black/50 hover:text-emerald-300 light:hover:text-emerald-600 border border-brand-main-500 rounded px-2 py-0.5 transition-colors"
                    title="Mark this version as production"
                >
                    <Tag className="h-3 w-3" /> production
                </button>
            )}
        </div>
    )
}
