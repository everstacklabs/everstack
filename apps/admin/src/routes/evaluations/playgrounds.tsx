import { useMemo, useState } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import { usePlaygrounds, useCreatePlayground } from '@/hooks/evaluations/use-playgrounds'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { Button, Loader, toast } from '@everstack/ui/components'
import { formatTimestamp } from '@everstack/utils/functions/index'
import { Blocks, Plus, Search } from 'lucide-react'
import { ui } from '@everstack/ui'
import type { PlaygroundConfig } from '@/server/playgrounds'

const { Input } = ui

export const Route = createFileRoute('/evaluations/playgrounds')({
    component: PlaygroundsPage,
})

/**
 * Starter templates seed a new playground with a useful column structure so
 * users don't start from a blank canvas. The config blob is the same shape the
 * store serializes (see serializePlaygroundConfig / hydrateFromConfig).
 */
type Starter = { key: string; title: string; description: string; name: string; config: PlaygroundConfig }

const promptMsgs = (system: string) => [
    { id: 'sys', role: 'system', text: system },
    { id: 'usr', role: 'user', text: '{{input}}' },
]

const STARTERS: Starter[] = [
    {
        key: 'compare-prompts',
        title: 'Compare prompts',
        description: 'See how different prompts affect output and scores on the same model.',
        name: 'Compare prompts',
        config: {
            variants: [
                { model: '', temperature: 0.7, templating: 'mustache', messages: promptMsgs('') },
                { model: '', temperature: 0.7, templating: 'mustache', messages: promptMsgs('Be concise and precise.') },
            ],
        },
    },
    {
        key: 'compare-models',
        title: 'Compare models',
        description: 'Run the same prompt across models to compare quality, cost, and latency.',
        name: 'Compare models',
        config: {
            variants: [
                { model: '', temperature: 0.7, templating: 'mustache', messages: promptMsgs('') },
                { model: '', temperature: 0.7, templating: 'mustache', messages: promptMsgs('') },
            ],
        },
    },
    {
        key: 'custom-scorers',
        title: 'Custom scorers',
        description: 'Grade outputs with code and LLM-judge scorers on a single task.',
        name: 'Custom scorers',
        config: {
            variants: [{ model: '', temperature: 0.7, templating: 'mustache', messages: promptMsgs('') }],
        },
    },
]

function PlaygroundsPage() {
    const gate = useFeatureGate(FeatureKey.EVALUATIONS)
    if (gate.isBlocked) {
        return (
            <FeatureGateBanner
                featureName="Playgrounds"
                description="Tune prompts, models, scorers and datasets in an editor-like interface and run full evaluations side by side."
                requiredTier="Pro"
                upgradeUrl={gate.upgradeUrl}
                isCE={gate.isCE}
            />
        )
    }
    return <PlaygroundsPageContent />
}

function PlaygroundsPageContent() {
    const navigate = useNavigate()
    const { data: playgrounds, isLoading } = usePlaygrounds()
    const createMutation = useCreatePlayground()
    const [search, setSearch] = useState('')

    const all = playgrounds ?? []
    const filtered = useMemo(() => {
        const q = search.trim().toLowerCase()
        if (!q) return all
        return all.filter((p) => (p.name ?? '').toLowerCase().includes(q))
    }, [all, search])

    const open = (id: string) => navigate({ to: '/evaluations/playground', search: { id } })

    const create = async (name: string, config: PlaygroundConfig) => {
        try {
            const pg = await createMutation.mutateAsync({ name, config })
            if (pg?.id) open(pg.id)
            else toast.error('Failed to create playground')
        } catch {
            toast.error('Failed to create playground')
        }
    }

    const columns: ColumnConfig<any>[] = [
        {
            id: 'name',
            header: 'Name',
            width: 320,
            minWidth: 200,
            render: (p: any) => (
                <span className="truncate text-xs font-medium text-white light:text-brand-main-50">
                    {p.name || 'Untitled playground'}
                </span>
            ),
        },
        {
            id: 'createdBy',
            header: 'Creator',
            width: 200,
            minWidth: 140,
            render: (p: any) => (
                <span className="truncate text-xs text-white/55 light:text-black/55">
                    {p.createdBy ? String(p.createdBy).slice(0, 12) : '—'}
                </span>
            ),
        },
        {
            id: 'createdAt',
            header: 'Created',
            width: 200,
            minWidth: 150,
            render: (p: any) => (
                <span className="truncate text-xs text-brand-main-100">
                    {p.createdAt ? formatTimestamp(p.createdAt) : '—'}
                </span>
            ),
        },
    ]

    if (!isLoading && all.length === 0) {
        return (
            <div className="flex h-full w-full flex-col items-center justify-center px-6 py-10">
                <div className="relative mb-5">
                    <div className="absolute inset-0 rounded-full bg-brand-secondary-500/20 blur-xl" />
                    <div className="relative rounded-lg border border-brand-main-600 bg-brand-main-800/80 p-4 light:bg-white light:border-brand-main-200">
                        <Blocks className="size-8 text-brand-secondary-400 light:text-brand-secondary-700" />
                    </div>
                </div>
                <h3 className="mb-2 text-base font-medium text-white light:text-brand-main-50">
                    Get started with playgrounds
                </h3>
                <p className="mb-4 max-w-lg text-center text-sm leading-relaxed text-white/50 light:text-black/50">
                    Playgrounds are a workspace for rapidly iterating on prompts, models, scorers and
                    datasets, and running full evaluations side by side.
                </p>
                <Button
                    variant="default"
                    onClick={() => create('Untitled playground', {})}
                    disabled={createMutation.isPending}
                >
                    <Plus className="h-4 w-4" />
                    Create empty playground
                </Button>

                <p className="mt-8 mb-3 text-sm text-white/40 light:text-black/40">
                    Or explore a starter example
                </p>
                <div className="grid w-full max-w-4xl grid-cols-1 gap-3 sm:grid-cols-3">
                    {STARTERS.map((s) => (
                        <button
                            key={s.key}
                            type="button"
                            disabled={createMutation.isPending}
                            onClick={() => create(s.name, s.config)}
                            className="rounded-lg border border-brand-main-600 bg-brand-main-900/40 p-4 text-left transition-colors hover:border-brand-secondary-500/50 disabled:opacity-50"
                        >
                            <div className="mb-1 text-sm font-medium text-white light:text-brand-main-50">
                                {s.title}
                            </div>
                            <div className="text-xs leading-relaxed text-white/50 light:text-black/50">
                                {s.description}
                            </div>
                        </button>
                    ))}
                </div>
            </div>
        )
    }

    return (
        <div className="flex h-full w-full flex-col overflow-hidden">
            <div className="shrink-0 border-b border-brand-main-700/70 bg-brand-main-950 light:bg-white light:border-brand-main-200">
                <div className="flex items-center gap-3 px-4 py-2">
                    <div className="relative min-w-0 flex-1">
                        <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-white/30 light:text-black/35" />
                        <Input
                            value={search}
                            onChange={(e) => setSearch(e.target.value)}
                            placeholder="Search playgrounds..."
                            className="h-8 border-brand-main-700 bg-brand-main-900/60 pl-7 text-xs light:bg-white light:border-brand-main-200"
                        />
                    </div>
                    <Button
                        variant="default"
                        size="sm"
                        onClick={() => create('Untitled playground', {})}
                        disabled={createMutation.isPending}
                    >
                        <Plus className="h-3.5 w-3.5" />
                        Playground
                    </Button>
                </div>
            </div>

            {isLoading ? (
                <div className="flex flex-1 items-center justify-center text-white/70 light:text-black/70">
                    <Loader loaderText="Loading playgrounds..." />
                </div>
            ) : (
                <ResponsiveTable
                    tableId="evaluations-playgrounds"
                    columns={columns}
                    data={filtered}
                    enableResizing={true}
                    minTableWidth="100%"
                    emptyMessage="No playgrounds match this search."
                    onRowClick={(p: any) => open(p.id)}
                    rowKey={(p: any) => p.id}
                />
            )}
        </div>
    )
}
