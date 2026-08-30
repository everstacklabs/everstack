import { Icon } from '@iconify/react'
import { Search, ui } from '@everstack/ui'
import { NODE_CATEGORIES } from '../node-registry'

const { Accordion, AccordionItem, AccordionTrigger, AccordionContent } = ui
import { PaletteItem } from './palette-item'
import { useStudioStore } from '@/stores/studio-store'
import { InputWithIcon } from '@everstack/ui/components'
import { useCallback, useMemo } from 'react'
import { useConfiguredProviders } from '@/hooks/vault/use-providers'

export function NodePalette() {
    const isPaletteCollapsed = useStudioStore((s) => s.isPaletteCollapsed)
    const togglePalette = useStudioStore((s) => s.togglePalette)
    const pendingEdgeInsertId = useStudioStore((s) => s.pendingEdgeInsertId)
    const setPendingEdgeInsert = useStudioStore((s) => s.setPendingEdgeInsert)
    const { data: configuredProvidersData } = useConfiguredProviders()
    const hasActiveProviders = useMemo(() => {
        const providers = configuredProvidersData?.providers ?? []
        return providers.some(p => p.isActive)
    }, [configuredProvidersData])

    const handleSearch = useCallback((value: string) => {
        console.log(value)
    }, [])

    const handleCollapse = useCallback(() => {
        setPendingEdgeInsert(null)
        togglePalette()
    }, [setPendingEdgeInsert, togglePalette])

    return (
        <div
            className="flex h-full flex-col border-r border-brand-main-700 bg-brand-main-950 transition-all duration-200"
            style={{ width: isPaletteCollapsed ? 40 : 340 }}
        >
            {isPaletteCollapsed ? (
                <div className="flex flex-col items-center pt-3">
                    <button
                        onClick={handleCollapse}
                        className="rounded p-1.5 text-brand-main-400 hover:bg-brand-main-800 hover:text-white light:hover:text-brand-main-50 transition-colors"
                        title="Expand palette"
                    >
                        <Icon icon="lucide:panel-right" className="text-brand-main-400 h-4 w-4" />
                    </button>
                </div>
            ) : (
                <>
                    <div className="flex items-center justify-between border-b border-brand-main-700 px-3 py-3 gap-2.5">
                        <InputWithIcon
                            onChange={(e) => handleSearch(e.target.value)}
                            placeholder="Search nodes"
                            className="w-full"
                            icon={<Search className="text-brand-main-400 h-5 w-5" />}
                        />
                        <button
                            onClick={handleCollapse}
                            className="rounded p-1.5 text-brand-main-400 hover:bg-brand-main-800 hover:text-white light:hover:text-brand-main-50 transition-colors"
                            title="Collapse palette"
                        >
                            <Icon icon="lucide:panel-left" className="text-brand-main-400 h-4 w-4" />
                        </button>
                    </div>
                    {pendingEdgeInsertId && (
                        <div className="flex items-center justify-between gap-2 border-b border-brand-secondary-500/30 bg-brand-secondary-500/10 px-3 py-2">
                            <span className="text-xs text-brand-secondary-300">
                                Select a node to insert on edge
                            </span>
                            <button
                                onClick={() => setPendingEdgeInsert(null)}
                                className="text-brand-main-400 hover:text-white light:hover:text-brand-main-50 transition-colors"
                            >
                                <Icon icon="lucide:x" className="h-3.5 w-3.5" />
                            </button>
                        </div>
                    )}
                    <div className="flex-1 overflow-y-auto">
                        <Accordion
                            type="multiple"
                            defaultValue={NODE_CATEGORIES.map((cat) => cat.category)}
                        >
                            {NODE_CATEGORIES.map((cat) => (
                                <AccordionItem
                                    key={cat.category}
                                    value={cat.category}
                                    className="border-b border-brand-main-700 last:border-b-0"
                                >
                                    <AccordionTrigger className="px-4 py-2 text-xs font-medium text-brand-main-400 uppercase tracking-wider hover:no-underline hover:text-brand-main-300">
                                        {cat.label}
                                    </AccordionTrigger>
                                    <AccordionContent className="pb-0">
                                        <div className="flex flex-col">
                                            {cat.nodes.map((meta) => (
                                                <PaletteItem
                                                    key={meta.type}
                                                    meta={meta}
                                                />
                                            ))}
                                            {cat.category === 'ai' && !hasActiveProviders && (
                                                <p className="px-4 py-2 text-xs text-brand-main-500">
                                                    No providers configured. Add providers in Vault → LLM Providers.
                                                </p>
                                            )}
                                        </div>
                                    </AccordionContent>
                                </AccordionItem>
                            ))}
                        </Accordion>
                    </div>
                </>
            )}
        </div>
    )
}
