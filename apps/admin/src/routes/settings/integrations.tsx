import { createFileRoute } from '@tanstack/react-router'
import { useEffect, useMemo, useState } from 'react'
import { ui } from '@everstack/ui'
import { Iconify } from '@everstack/ui/icons'
import { cn } from '@/lib/utils'
import { GitHubIntegration } from '@/components/settings/integrations/github-integration'
import { integrationsCatalog, type IntegrationCatalogItem } from '@/components/settings/integrations/integrations-catalog'
import { z } from 'zod'
import { Icon } from '@iconify/react'

export const Route = createFileRoute('/settings/integrations')({
    component: IntegrationsPage,
    validateSearch: z.object({
        integration: z.string().optional(),
    }),
})

const {
    Card,
    CardContent,
    Input,
    Button,
    Badge,
    Sheet,
    SheetContent,
    SheetHeader,
    SheetBody,
    SheetTitle,
} = ui

type IntegrationFilter = 'all' | 'available' | 'coming_soon'

const FILTER_OPTIONS: { value: IntegrationFilter; label: string; icon: string }[] = [
    { value: 'all', label: 'All', icon: 'lucide:layout-grid' },
    { value: 'available', label: 'Available', icon: 'lucide:plug-zap' },
    { value: 'coming_soon', label: 'Coming Soon', icon: 'lucide:clock-3' },
]

const STATUS_ORDER = {
    live: 0,
    beta: 1,
    coming_soon: 2,
} as const

const PAGE_SIZE = 12

const STATUS_META: Record<
    IntegrationCatalogItem['status'],
    { label: string; className: string }
> = {
    live: {
        label: 'Enabled',
        className: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-300',
    },
    beta: {
        label: 'Beta',
        className: 'border-blue-500/30 bg-blue-500/10 text-blue-300',
    },
    coming_soon: {
        label: 'Planned',
        className: 'border-brand-main-500/40 bg-brand-main-700/20 text-white/40 light:text-black/40',
    },
}

function IntegrationsPage() {
    const search = Route.useSearch()
    const navigate = Route.useNavigate()
    const [query, setQuery] = useState('')
    const [filter, setFilter] = useState<IntegrationFilter>('all')
    const [category, setCategory] = useState<string>('all')

    const categoryOptions = useMemo(
        () => ['all', ...Array.from(new Set(integrationsCatalog.map((integration) => integration.category)))],
        []
    )

    const summary = useMemo(() => {
        let available = 0
        let comingSoon = 0

        for (const integration of integrationsCatalog) {
            if (integration.status === 'coming_soon') {
                comingSoon += 1
                continue
            }
            available += 1
        }

        return {
            total: integrationsCatalog.length,
            available,
            comingSoon,
            categories: categoryOptions.length - 1,
        }
    }, [categoryOptions.length])

    const filteredIntegrations = useMemo(() => {
        const search = query.trim().toLowerCase()

        return integrationsCatalog
            .filter((integration) => {
                if (filter === 'available' && integration.status === 'coming_soon') return false
                if (filter === 'coming_soon' && integration.status !== 'coming_soon') return false
                if (category !== 'all' && integration.category !== category) return false

                if (!search) return true

                const searchable = [
                    integration.name,
                    integration.category,
                    integration.description,
                    ...integration.capabilities,
                    ...integration.keywords,
                ]
                    .join(' ')
                    .toLowerCase()

                return searchable.includes(search)
            })
            .sort(
                (a, b) =>
                    STATUS_ORDER[a.status] - STATUS_ORDER[b.status] || a.name.localeCompare(b.name)
            )
    }, [category, filter, query])

    const activeQuery = query.trim()
    const hasActiveFilters = filter !== 'all' || category !== 'all' || activeQuery.length > 0
    const [visibleCount, setVisibleCount] = useState(PAGE_SIZE)
    const selectedIntegrationId = search.integration ?? null
    const selectedIntegration = useMemo(
        () => integrationsCatalog.find((integration) => integration.id === selectedIntegrationId) ?? null,
        [selectedIntegrationId]
    )

    const setSelectedIntegration = (id?: string) => {
        navigate({
            replace: true,
            search: (prev) => ({
                ...prev,
                integration: id || undefined,
            }),
        })
    }

    const visibleIntegrations = useMemo(
        () => filteredIntegrations.slice(0, visibleCount),
        [filteredIntegrations, visibleCount]
    )
    const hasMore = filteredIntegrations.length > visibleCount

    useEffect(() => {
        setVisibleCount(PAGE_SIZE)
    }, [query, filter, category])

    useEffect(() => {
        if (!selectedIntegrationId) return
        if (filteredIntegrations.some((integration) => integration.id === selectedIntegrationId)) return

        navigate({
            replace: true,
            search: (prev) => ({
                ...prev,
                integration: undefined,
            }),
        })
    }, [filteredIntegrations, navigate, selectedIntegrationId])

    useEffect(() => {
        if (!selectedIntegrationId) return

        const index = filteredIntegrations.findIndex((integration) => integration.id === selectedIntegrationId)
        if (index < 0 || index < visibleCount) return

        setVisibleCount(Math.max(PAGE_SIZE, index + 1))
    }, [filteredIntegrations, selectedIntegrationId, visibleCount])

    const openDetail = (integrationId: string) => {
        setSelectedIntegration(integrationId)
    }

    const closeDetail = (open: boolean) => {
        if (open) return
        setSelectedIntegration(undefined)
    }

    const showMore = () => {
        setVisibleCount((current) => Math.min(current + PAGE_SIZE, filteredIntegrations.length))
    }

    const showLess = () => {
        setVisibleCount(PAGE_SIZE)
        if (typeof window !== 'undefined') {
            window.requestAnimationFrame(() => {
                window.scrollTo({ top: 0, behavior: 'smooth' })
            })
        }
    }

    const resetFilters = () => {
        setQuery('')
        setFilter('all')
        setCategory('all')
    }

    const getFilterCount = (value: IntegrationFilter) => {
        switch (value) {
            case 'available':
                return summary.available
            case 'coming_soon':
                return summary.comingSoon
            default:
                return summary.total
        }
    }

    return (
        <div className="flex h-full w-full flex-col">
            <div className="flex-1 space-y-4 overflow-y-auto p-8 px-60 mx-auto w-full">

                {/* ── Metric cards ── */}
                <div className="grid grid-cols-2 md:grid-cols-4 gap-2">
                    <Card className="border-brand-main-600 bg-brand-main-900/50 rounded">
                        <CardContent>
                            <div className="flex items-center justify-between gap-2">
                                <div className="flex items-center gap-2 min-w-0">
                                    <div className="p-1.5 rounded bg-brand-secondary-600/20 border border-brand-secondary-500/30">
                                        <Icon icon="lucide:plug-zap" className="size-4 text-brand-secondary-300" />
                                    </div>
                                    <div className="text-xs text-white uppercase tracking-wide light:text-brand-main-50">Available</div>
                                </div>
                                <div className="text-sm font-semibold text-white light:text-brand-main-50">{summary.available}</div>
                            </div>
                        </CardContent>
                    </Card>
                    <Card className="border-brand-main-600 bg-brand-main-900/50 rounded">
                        <CardContent>
                            <div className="flex items-center justify-between gap-2">
                                <div className="flex items-center gap-2 min-w-0">
                                    <div className="p-1.5 rounded bg-brand-secondary-600/20 border border-brand-secondary-500/30">
                                        <Icon icon="lucide:clock-3" className="size-4 text-brand-secondary-300" />
                                    </div>
                                    <div className="text-xs text-white uppercase tracking-wide light:text-brand-main-50">Planned</div>
                                </div>
                                <div className="text-sm font-semibold text-white light:text-brand-main-50">{summary.comingSoon}</div>
                            </div>
                        </CardContent>
                    </Card>
                    <Card className="border-brand-main-600 bg-brand-main-900/50 rounded">
                        <CardContent>
                            <div className="flex items-center justify-between gap-2">
                                <div className="flex items-center gap-2 min-w-0">
                                    <div className="p-1.5 rounded bg-brand-secondary-600/20 border border-brand-secondary-500/30">
                                        <Icon icon="lucide:folder" className="size-4 text-brand-secondary-300" />
                                    </div>
                                    <div className="text-xs text-white uppercase tracking-wide light:text-brand-main-50">Categories</div>
                                </div>
                                <div className="text-sm font-semibold text-white light:text-brand-main-50">{summary.categories}</div>
                            </div>
                        </CardContent>
                    </Card>
                    <Card className="border-brand-main-600 bg-brand-main-900/50 rounded">
                        <CardContent>
                            <div className="flex items-center justify-between gap-2">
                                <div className="flex items-center gap-2 min-w-0">
                                    <div className="p-1.5 rounded bg-brand-secondary-600/20 border border-brand-secondary-500/30">
                                        <Icon icon="lucide:blocks" className="size-4 text-brand-secondary-300" />
                                    </div>
                                    <div className="text-xs text-white uppercase tracking-wide light:text-brand-main-50">Total</div>
                                </div>
                                <div className="text-sm font-semibold text-white light:text-brand-main-50">{summary.total}</div>
                            </div>
                        </CardContent>
                    </Card>
                </div>

                {/* ── Filters ── */}
                <div className="space-y-3">
                    <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
                        <div className="relative w-full lg:max-w-xl">
                            <Iconify.Icon icon="lucide:search" className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-white/30 light:text-black/30" />
                            <Input
                                value={query}
                                onChange={(event) => setQuery(event.target.value)}
                                placeholder="Search integrations, capabilities, or providers..."
                                className="h-9 border-brand-main-600 bg-brand-main-800 pl-9 pr-9 text-sm text-white placeholder:text-white/25 light:text-brand-main-50 light:placeholder:text-black/25"
                            />
                            {activeQuery ? (
                                <button
                                    type="button"
                                    aria-label="Clear search"
                                    onClick={() => setQuery('')}
                                    className="absolute right-3 top-1/2 -translate-y-1/2 text-white/30 transition-colors hover:text-white/60 light:text-black/30 light:hover:text-black/60"
                                >
                                    <Iconify.Icon icon="lucide:x" className="h-4 w-4" />
                                </button>
                            ) : null}
                        </div>
                        <div className="flex items-center gap-2">
                            <Badge variant="outline" className="border-brand-main-600 bg-brand-main-800/50 text-white/60 light:text-black/60">
                                {filteredIntegrations.length} result{filteredIntegrations.length === 1 ? '' : 's'}
                            </Badge>
                            {hasActiveFilters ? (
                                <Button variant="outline" size="sm" onClick={resetFilters}>
                                    <Iconify.Icon icon="lucide:rotate-ccw" className="mr-1 h-4 w-4" />
                                    Reset
                                </Button>
                            ) : null}
                        </div>
                    </div>

                    <div className="flex flex-wrap items-center gap-2">
                        {FILTER_OPTIONS.map((option) => (
                            <button
                                type="button"
                                key={option.value}
                                onClick={() => setFilter(option.value)}
                                className={cn(
                                    'inline-flex items-center gap-2 rounded border px-2.5 py-1 text-xs font-medium transition-colors',
                                    filter === option.value
                                        ? 'border-brand-main-400 bg-brand-main-800/60 text-white light:text-brand-main-50'
                                        : 'border-brand-main-600 bg-brand-main-800/30 text-white/45 hover:border-brand-main-500 hover:text-white/60 light:text-black/45 light:hover:text-black/60'
                                )}
                            >
                                <Iconify.Icon icon={option.icon} className="h-3.5 w-3.5" />
                                {option.label}
                                <span className="rounded-sm bg-black/25 px-1.5 py-0.5 text-[10px]">
                                    {getFilterCount(option.value)}
                                </span>
                            </button>
                        ))}

                        <span className="mx-1 h-4 w-px bg-brand-main-600" />

                        {categoryOptions.map((option) => (
                            <button
                                type="button"
                                key={option}
                                onClick={() => setCategory(option)}
                                className={cn(
                                    'inline-flex items-center rounded border px-2.5 py-1 text-xs transition-colors',
                                    category === option
                                        ? 'border-brand-main-400 bg-brand-main-800/60 text-white light:text-brand-main-50'
                                        : 'border-brand-main-600 bg-brand-main-800/30 text-white/45 hover:border-brand-main-500 hover:text-white/60 light:text-black/45 light:hover:text-black/60'
                                )}
                            >
                                {option === 'all' ? 'All categories' : option}
                            </button>
                        ))}
                    </div>

                    {hasActiveFilters ? (
                        <div className="flex flex-wrap items-center gap-2 rounded-lg border border-brand-main-600 bg-brand-main-800/30 px-3 py-2">
                            <span className="text-[11px] uppercase tracking-wide text-white/30 light:text-black/30">Active Filters</span>
                            {activeQuery ? (
                                <Badge variant="outline" className="border-brand-main-600 bg-brand-main-800/40 text-white/60 light:text-black/60">
                                    Search: {activeQuery}
                                </Badge>
                            ) : null}
                            {filter !== 'all' ? (
                                <Badge variant="outline" className="border-brand-main-600 bg-brand-main-800/40 text-white/60 light:text-black/60">
                                    Status: {FILTER_OPTIONS.find((option) => option.value === filter)?.label}
                                </Badge>
                            ) : null}
                            {category !== 'all' ? (
                                <Badge variant="outline" className="border-brand-main-600 bg-brand-main-800/40 text-white/60 light:text-black/60">
                                    Category: {category}
                                </Badge>
                            ) : null}
                        </div>
                    ) : null}
                </div>

                {/* ── Integration grid ── */}
                {filteredIntegrations.length === 0 ? (
                    <div className="rounded-lg border border-dashed border-brand-main-600 bg-brand-main-900/40 py-12 text-center">
                        <div className="mx-auto mb-3 flex h-10 w-10 items-center justify-center rounded-full border border-brand-main-600 bg-brand-main-800/60">
                            <Iconify.Icon icon="lucide:search-x" className="h-5 w-5 text-white/45 light:text-black/45" />
                        </div>
                        <p className="text-sm font-medium text-white/80 light:text-black/80">No integrations match your filters</p>
                        <p className="mt-1 text-sm text-white/45 light:text-black/45">
                            Try broadening the search, or clear filters to view the full catalog.
                        </p>
                        {hasActiveFilters ? (
                            <Button variant="outline" size="sm" className="mt-4" onClick={resetFilters}>
                                Clear all filters
                            </Button>
                        ) : null}
                    </div>
                ) : (
                    <div className="space-y-4">
                        <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
                            {visibleIntegrations.map((integration) => (
                                <button
                                    key={integration.id}
                                    type="button"
                                    onClick={() => openDetail(integration.id)}
                                    className={cn(
                                        'group rounded-lg border border-brand-main-600 bg-brand-main-800/50 p-3 text-left transition-colors hover:bg-brand-main-800/70',
                                        selectedIntegration?.id === integration.id
                                            ? 'border-brand-main-400 bg-brand-main-800/70'
                                            : ''
                                    )}
                                >
                                    <div className="flex min-w-0 items-center justify-between gap-2">
                                        <div className="flex min-w-0 items-center gap-2.5">
                                            <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded border border-brand-main-600 bg-brand-main-800/60">
                                                <Iconify.Icon icon={integration.icon} className="h-4 w-4 text-white light:text-brand-main-50" />
                                            </div>
                                            <p className="truncate text-sm font-medium text-white light:text-brand-main-50">{integration.name}</p>
                                        </div>
                                        <Badge className={cn('shrink-0', STATUS_META[integration.status].className)}>
                                            {STATUS_META[integration.status].label}
                                        </Badge>
                                    </div>
                                    <p className="mt-2 h-8 overflow-hidden text-[11px] leading-4 text-white/45 light:text-black/45">
                                        {integration.description}
                                    </p>
                                    <div className="mt-2 flex items-center justify-between gap-2">
                                        <span className="truncate rounded border border-brand-main-700 bg-brand-main-800/40 px-1.5 py-0.5 text-[10px] text-white/60 light:text-black/60">
                                            {integration.category}
                                        </span>
                                        <span className="inline-flex items-center gap-1 text-[10px] text-white/30 group-hover:text-white/60 light:text-black/30 light:group-hover:text-black/60">
                                            {integration.capabilities.length} capabilities
                                            <Iconify.Icon icon="lucide:chevron-right" className="h-3 w-3" />
                                        </span>
                                    </div>
                                </button>
                            ))}
                        </div>

                        {filteredIntegrations.length > PAGE_SIZE ? (
                            <div className="flex justify-center gap-2">
                                {hasMore ? (
                                    <Button variant="outline" size="sm" onClick={showMore}>
                                        Show more
                                    </Button>
                                ) : (
                                    <Button variant="outline" size="sm" onClick={showLess}>
                                        Show less
                                    </Button>
                                )}
                            </div>
                        ) : null}
                    </div>
                )}
            </div>

            <Sheet open={!!selectedIntegration} onOpenChange={closeDetail}>
                <SheetContent side="right" className="w-full sm:max-w-4xl max-h-[100vh] overflow-y-auto scrollbar-macos">
                    <SheetHeader>
                        <SheetTitle className="text-base">
                            {selectedIntegration ? `${selectedIntegration.name} Integration` : 'Integration'}
                        </SheetTitle>
                    </SheetHeader>
                    <SheetBody className="py-4">
                        {selectedIntegration?.id === 'github' ? (
                            <GitHubIntegration
                                name={selectedIntegration.name}
                                icon={selectedIntegration.icon}
                                category={selectedIntegration.category}
                                status={selectedIntegration.status}
                                description={selectedIntegration.description}
                                capabilities={selectedIntegration.capabilities}
                            />
                        ) : selectedIntegration ? (
                            <PlaceholderIntegrationDetail integration={selectedIntegration} />
                        ) : null}
                    </SheetBody>
                </SheetContent>
            </Sheet>
        </div>
    )
}

function PlaceholderIntegrationDetail({ integration }: { integration: IntegrationCatalogItem }) {
    return (
        <div className="space-y-3">
            <Card className="border-brand-main-600 bg-brand-main-900/50 py-4 gap-3">
                <CardContent className="space-y-3">
                    <div className="flex items-start gap-3">
                        <div className="flex h-10 w-10 items-center justify-center rounded border border-brand-main-600 bg-brand-main-800/60">
                            <Iconify.Icon icon={integration.icon} className="h-5 w-5 text-white light:text-brand-main-50" />
                        </div>
                        <div className="space-y-1">
                            <div className="flex items-center gap-2">
                                <h3 className="text-lg font-semibold text-white light:text-brand-main-50">{integration.name}</h3>
                                <Badge className={STATUS_META[integration.status].className}>
                                    {STATUS_META[integration.status].label}
                                </Badge>
                            </div>
                            <p className="text-sm text-white/45 light:text-black/45">{integration.description}</p>
                        </div>
                    </div>
                </CardContent>
            </Card>

            <Card className="border-brand-main-600 bg-brand-main-900/50 py-4 gap-3">
                <CardContent className="space-y-3">
                    <div className="rounded border border-brand-main-600 bg-brand-main-800/40 p-3">
                        <p className="text-sm text-white/80 light:text-black/80">This integration is currently on the roadmap.</p>
                        <p className="mt-1 text-xs text-white/45 light:text-black/45">
                            We will activate this panel as soon as backend APIs and auth flows are ready.
                        </p>
                    </div>
                    <div className="space-y-2">
                        <p className="text-xs uppercase tracking-wide text-white/30 light:text-black/30">Planned Capabilities</p>
                        <div className="space-y-1.5">
                            {integration.capabilities.map((capability) => (
                                <div
                                    key={capability}
                                    className="rounded border border-brand-main-700 bg-brand-main-800/30 px-3 py-1.5 text-xs text-white/60 light:text-black/60"
                                >
                                    {capability}
                                </div>
                            ))}
                        </div>
                    </div>
                </CardContent>
            </Card>
        </div>
    )
}
