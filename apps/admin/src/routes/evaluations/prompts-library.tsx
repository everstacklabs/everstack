import { useMemo, useState } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import {
    useCreatePrompt,
    useDeletePrompt,
    usePrompts,
} from '@/hooks/evaluations/use-prompts'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { Iconify } from '@everstack/ui/icons'
import { Button, Loader, toast } from '@everstack/ui/components'
import { Trash2 } from 'lucide-react'
import { formatTimestamp } from '@everstack/utils/functions/index'
import { ui } from '@everstack/ui'
import { useOutcomeDashboard } from '@/hooks/observability/use-outcomes'
import type { Prompt } from '@/server/prompts'
import { Trophy } from 'lucide-react'

const {
    Badge,
    Card,
    CardContent,
    CardHeader,
    CardTitle,
    Input,
    Label,
    Sheet,
    SheetContent,
    SheetDescription,
    SheetHeader,
    SheetTitle,
    SheetBody,
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
    Textarea,
} = ui

const VERDICT_RANGE_OPTIONS = [
    { label: 'Last 24 h', valueMs: 24 * 60 * 60 * 1000 },
    { label: 'Last 7 d', valueMs: 7 * 24 * 60 * 60 * 1000 },
    { label: 'Last 30 d', valueMs: 30 * 24 * 60 * 60 * 1000 },
]

export const Route = createFileRoute('/evaluations/prompts-library')({
    component: EvaluationsPromptsLibraryPage,
})

function EvaluationsPromptsLibraryPage() {
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

    return <PromptsLibraryContent />
}

function PromptsLibraryContent() {
    const navigate = useNavigate()
    const { data: prompts, isLoading } = usePrompts()
    const createMutation = useCreatePrompt()
    const deleteMutation = useDeletePrompt()

    const [dialogOpen, setDialogOpen] = useState(false)
    const [name, setName] = useState('')
    const [description, setDescription] = useState('')
    const [tags, setTags] = useState('')

    const [deleteConfirm, setDeleteConfirm] = useState<{ id: string; name: string } | null>(null)

    const handleCreate = async (e: React.FormEvent) => {
        e.preventDefault()
        try {
            await createMutation.mutateAsync({
                name: name.trim(),
                description: description || undefined,
                tags: tags
                    .split(',')
                    .map((t) => t.trim())
                    .filter(Boolean),
            })
            setDialogOpen(false)
            setName('')
            setDescription('')
            setTags('')
            toast.success('Prompt created successfully')
        } catch (err) {
            toast.error((err as Error)?.message ?? 'Failed to create prompt')
        }
    }

    const handleDelete = async (id: string) => {
        try {
            await deleteMutation.mutateAsync(id)
            setDeleteConfirm(null)
            toast.success('Prompt deleted successfully')
        } catch {
            toast.error('Failed to delete prompt')
        }
    }

    const columns: ColumnConfig<Prompt>[] = [
        {
            id: 'name',
            header: 'Name',
            width: 220,
            minWidth: 140,
            render: (prompt) => (
                <span className="truncate font-medium text-brand-secondary-100 text-xs">
                    {prompt.name}
                </span>
            ),
        },
        {
            id: 'description',
            header: 'Description',
            width: 260,
            minWidth: 120,
            render: (prompt) => (
                <span className="truncate text-xs text-brand-main-100">
                    {prompt.description || '-'}
                </span>
            ),
        },
        {
            id: 'versions',
            header: 'Versions',
            width: 90,
            minWidth: 70,
            render: (prompt) => (
                <span className="px-1.5 py-0.5 rounded text-xs font-medium bg-purple-500/20 text-purple-300 light:text-purple-600">
                    {prompt.versionCount > 0 ? `v${prompt.latestVersion}` : 'empty'}
                </span>
            ),
        },
        {
            id: 'labels',
            header: 'Labels',
            width: 180,
            minWidth: 100,
            render: (prompt) => {
                const entries = Object.entries(prompt.labels ?? {})
                if (entries.length === 0) {
                    return <span className="text-xs text-white/30 light:text-black/30">-</span>
                }
                return (
                    <div className="flex items-center gap-1 overflow-hidden">
                        {entries.map(([label, version]) => (
                            <Badge
                                key={label}
                                variant="outline"
                                className="text-[10px] border-emerald-400/30 text-emerald-300 light:text-emerald-600 shrink-0"
                            >
                                {label} → v{version}
                            </Badge>
                        ))}
                    </div>
                )
            },
        },
        {
            id: 'tags',
            header: 'Tags',
            width: 160,
            minWidth: 90,
            render: (prompt) =>
                prompt.tags?.length ? (
                    <div className="flex items-center gap-1 overflow-hidden">
                        {prompt.tags.map((tag) => (
                            <Badge
                                key={tag}
                                variant="outline"
                                className="text-[10px] border-brand-main-500 text-white/60 light:text-black/60 shrink-0"
                            >
                                {tag}
                            </Badge>
                        ))}
                    </div>
                ) : (
                    <span className="text-xs text-white/30 light:text-black/30">-</span>
                ),
        },
        {
            id: 'updatedAt',
            header: 'Updated',
            width: 160,
            minWidth: 140,
            render: (prompt) => (
                <span className="truncate text-xs text-brand-main-100">
                    {prompt.updatedAt ? formatTimestamp(prompt.updatedAt) : '-'}
                </span>
            ),
        },
        {
            id: 'actions',
            header: '',
            width: 60,
            minWidth: 60,
            maxWidth: 60,
            resizable: false,
            render: (prompt) => (
                <div className="flex items-center justify-center" data-row-actions>
                    <button
                        type="button"
                        className="p-1 rounded hover:bg-red-500/20 hover:text-red-400 light:hover:text-red-600 transition-colors"
                        onClick={() => setDeleteConfirm({ id: prompt.id, name: prompt.name })}
                        title="Delete prompt"
                    >
                        <Trash2 size={14} />
                    </button>
                </div>
            ),
        },
    ]

    return (
        <div className="flex flex-col h-full w-full overflow-hidden">
            <PromptVerdictPanel />
            {isLoading ? (
                <div className="flex-1 flex items-center justify-center text-white/70 light:text-black/70">
                    <Loader loaderText="Loading prompts..." />
                </div>
            ) : !prompts || prompts.length === 0 ? (
                <div className="flex-1 flex flex-col items-center justify-center">
                    <div className="relative mb-6">
                        <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                        <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                            <Iconify.Icon
                                icon="lucide:notebook-pen"
                                className="size-8 text-brand-secondary-400"
                            />
                        </div>
                    </div>
                    <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No prompts yet</h3>
                    <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed mb-4">
                        Create versioned prompt templates here, or save a conversation
                        directly from the playground.
                    </p>
                    <Button variant="default" onClick={() => setDialogOpen(true)}>
                        <div className="flex items-center gap-2">
                            <Iconify.Icon icon="heroicons:plus" className="size-4" />
                            Create Prompt
                        </div>
                    </Button>
                </div>
            ) : (
                <ResponsiveTable
                    columns={columns}
                    data={prompts}
                    enableResizing={true}
                    minTableWidth="100%"
                    emptyMessage="No prompts found."
                    onRowClick={(prompt) =>
                        navigate({
                            to: '/evaluations/prompts-library/$promptId',
                            params: { promptId: prompt.id },
                        })
                    }
                    rowKey={(prompt) => prompt.id}
                />
            )}

            {/* Create Prompt Sheet */}
            <Sheet open={dialogOpen} onOpenChange={setDialogOpen}>
                <SheetContent side="right" className="min-w-[400px]">
                    <SheetHeader>
                        <SheetTitle>Create Prompt</SheetTitle>
                        <SheetDescription className="text-white/60 light:text-black/60 mt-1 text-xs">
                            Name the prompt now; add content as version 1 from the prompt
                            page or by saving from the playground.
                        </SheetDescription>
                    </SheetHeader>

                    <SheetBody>
                        {createMutation.error && (
                            <div className="rounded-lg border border-red-500/20 bg-red-500/10 p-3 text-sm text-red-400 light:text-red-600">
                                {(createMutation.error as Error).message}
                            </div>
                        )}

                        <form onSubmit={handleCreate} className="space-y-4">
                            <div className="space-y-2">
                                <Label htmlFor="prompt-name" className="text-white light:text-brand-main-50 font-medium">
                                    Name
                                </Label>
                                <Input
                                    id="prompt-name"
                                    placeholder="support-triage"
                                    value={name}
                                    onChange={(e) => setName(e.target.value)}
                                    required
                                    className="bg-brand-main-900 border-brand-main-600 text-white light:text-brand-main-50 h-8 text-sm"
                                />
                            </div>
                            <div className="space-y-2">
                                <Label
                                    htmlFor="prompt-description"
                                    className="text-white light:text-brand-main-50 font-medium"
                                >
                                    Description
                                </Label>
                                <Textarea
                                    id="prompt-description"
                                    placeholder="What is this prompt for?"
                                    value={description}
                                    onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) =>
                                        setDescription(e.target.value)
                                    }
                                    rows={3}
                                    className="bg-brand-main-900 border-brand-main-600 text-white light:text-brand-main-50 text-sm"
                                />
                            </div>
                            <div className="space-y-2">
                                <Label htmlFor="prompt-tags" className="text-white light:text-brand-main-50 font-medium">
                                    Tags
                                </Label>
                                <Input
                                    id="prompt-tags"
                                    placeholder="support, triage (comma separated)"
                                    value={tags}
                                    onChange={(e) => setTags(e.target.value)}
                                    className="bg-brand-main-900 border-brand-main-600 text-white light:text-brand-main-50 h-8 text-sm"
                                />
                            </div>
                            <div className="flex justify-end gap-3 mt-6 border-t border-brand-main-700/60 pt-4">
                                <Button
                                    type="button"
                                    variant="outline"
                                    onClick={() => setDialogOpen(false)}
                                >
                                    Cancel
                                </Button>
                                <Button type="submit" disabled={createMutation.isPending}>
                                    {createMutation.isPending ? 'Creating...' : 'Create Prompt'}
                                </Button>
                            </div>
                        </form>
                    </SheetBody>
                </SheetContent>
            </Sheet>

            {/* Delete Confirmation Sheet */}
            <Sheet
                open={deleteConfirm !== null}
                onOpenChange={(open) => !open && setDeleteConfirm(null)}
            >
                <SheetContent side="right" className="min-w-[400px]">
                    <SheetHeader>
                        <SheetTitle>Delete Prompt</SheetTitle>
                        <SheetDescription className="text-white/60 light:text-black/60 mt-1 text-xs">
                            Are you sure you want to delete{' '}
                            <strong className="text-brand-main-100">{deleteConfirm?.name}</strong>?
                            All versions of this prompt will be archived. This action cannot
                            be undone.
                        </SheetDescription>
                    </SheetHeader>
                    <SheetBody>
                        <div className="flex justify-end gap-3 mt-6 border-t border-brand-main-700/60 pt-4">
                            <Button
                                variant="outline"
                                onClick={() => setDeleteConfirm(null)}
                                disabled={deleteMutation.isPending}
                            >
                                Cancel
                            </Button>
                            <Button
                                variant="destructive"
                                className="bg-destructive/60 text-brand-main-100 hover:bg-destructive/90"
                                onClick={() => deleteConfirm && handleDelete(deleteConfirm.id)}
                                disabled={deleteMutation.isPending}
                            >
                                {deleteMutation.isPending ? 'Deleting...' : 'Delete'}
                            </Button>
                        </div>
                    </SheetBody>
                </SheetContent>
            </Sheet>
        </div>
    )
}

type PromptVerdictBreakdown = {
    dimension: string
    entries: Array<{
        groupKey: string
        rates?: {
            winRate: number
            failRate: number
            drawRate: number
            noChangeRate: number
            sampleSize: bigint
        }
    }>
}

function PromptVerdictPanel() {
    const [rangeMs, setRangeMs] = useState(VERDICT_RANGE_OPTIONS[2].valueMs)
    const filters = useMemo(() => {
        const now = Date.now()
        return {
            startTime: new Date(now - rangeMs).toISOString(),
            endTime: new Date(now).toISOString(),
            groupBy: ['prompt_template_id', 'prompt_version'],
        }
    }, [rangeMs])
    const { data, isLoading } = useOutcomeDashboard(filters)
    const breakdowns = data?.verdictBreakdowns ?? []

    return (
        <Card className="m-3 mb-0 shrink-0 border-brand-main-600 bg-brand-main-900/50">
            <CardHeader className="!pb-2">
                <div className="flex items-center justify-between gap-3">
                    <div>
                        <CardTitle className="flex items-center gap-2 text-sm font-medium text-white">
                            <Trophy className="h-4 w-4" />
                            Verdict rates by prompt
                        </CardTitle>
                        <p className="mt-1 text-xs text-white/50">
                            Compare win, fail, draw, and no-change rates by prompt template and version.
                        </p>
                    </div>
                    <Select value={String(rangeMs)} onValueChange={(value) => setRangeMs(Number(value))}>
                        <SelectTrigger className="w-32">
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            {VERDICT_RANGE_OPTIONS.map((option) => (
                                <SelectItem key={option.valueMs} value={String(option.valueMs)}>
                                    {option.label}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                </div>
            </CardHeader>
            <CardContent className="grid gap-4 !pt-0 lg:grid-cols-2">
                <VerdictBreakdownTable
                    title="By prompt version"
                    breakdown={breakdowns.find((item) => item.dimension === 'prompt_version')}
                    isLoading={isLoading}
                />
                <VerdictBreakdownTable
                    title="By prompt template"
                    breakdown={breakdowns.find((item) => item.dimension === 'prompt_template_id')}
                    isLoading={isLoading}
                />
            </CardContent>
        </Card>
    )
}

function VerdictBreakdownTable({
    title,
    breakdown,
    isLoading,
}: {
    title: string
    breakdown?: PromptVerdictBreakdown
    isLoading: boolean
}) {
    if (isLoading) {
        return <div className="text-xs text-white/40">Loading verdicts...</div>
    }
    const entries = breakdown?.entries ?? []
    if (entries.length === 0) {
        return (
            <div className="text-xs text-white/40">
                <span className="font-medium text-white/60">{title}:</span> no labeled verdicts in this window.
            </div>
        )
    }
    return (
        <div className="overflow-x-auto">
            <div className="mb-2 text-xs font-medium text-white/60">{title}</div>
            <table className="w-full text-xs">
                <thead>
                    <tr className="text-left text-white/40">
                        <th className="py-1 pr-3 font-medium">Key</th>
                        <th className="py-1 pr-3 text-right font-medium">Win</th>
                        <th className="py-1 pr-3 text-right font-medium">Fail</th>
                        <th className="py-1 pr-3 text-right font-medium">Draw</th>
                        <th className="py-1 pr-3 text-right font-medium">No change</th>
                        <th className="py-1 pl-3 text-right font-medium">Sample</th>
                    </tr>
                </thead>
                <tbody>
                    {entries.map((entry) => (
                        <tr key={entry.groupKey} className="border-t border-brand-main-700/40">
                            <td className="py-1.5 pr-3"><Badge variant="secondary">{entry.groupKey}</Badge></td>
                            <td className="py-1.5 pr-3 text-right text-white">{formatVerdictRate(entry.rates?.winRate)}</td>
                            <td className="py-1.5 pr-3 text-right text-white/80">{formatVerdictRate(entry.rates?.failRate)}</td>
                            <td className="py-1.5 pr-3 text-right text-white/80">{formatVerdictRate(entry.rates?.drawRate)}</td>
                            <td className="py-1.5 pr-3 text-right text-white/80">{formatVerdictRate(entry.rates?.noChangeRate)}</td>
                            <td className="py-1.5 pl-3 text-right text-white/40">{Number(entry.rates?.sampleSize ?? 0).toLocaleString()}</td>
                        </tr>
                    ))}
                </tbody>
            </table>
        </div>
    )
}

function formatVerdictRate(value?: number): string {
    return typeof value === 'number' && Number.isFinite(value) ? `${(value * 100).toFixed(1)}%` : '-'
}
