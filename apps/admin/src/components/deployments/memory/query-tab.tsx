import { useState } from 'react'
import { useCollections, useQueryCollection } from '@/hooks/deployments/use-memory'
import {
    Button,
    Input,
    Label,
    Loader,
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@everstack/ui/components'
import { ui } from '@everstack/ui'
import { Iconify } from '@everstack/ui/icons'
import { SearchResultRow } from './query-playground'
import type { MemorySearchResult } from '@/server/memory'

const { Sheet, SheetContent, SheetHeader, SheetBody, SheetTitle } = ui

function scoreColor(score: number): string {
    if (score >= 0.8) return 'bg-brand-secondary-500/20 text-brand-secondary-300 border-brand-secondary-500/30'
    if (score >= 0.5) return 'bg-brand-secondary-500/10 text-brand-secondary-400 border-brand-secondary-500/20'
    return 'bg-brand-main-700/50 text-brand-main-300 border-brand-main-600'
}

export function QueryTab() {
    const { data: collections, isLoading: collectionsLoading } = useCollections()
    const [selectedCollection, setSelectedCollection] = useState('')
    const [query, setQuery] = useState('')
    const [topK, setTopK] = useState(5)
    const [minScore, setMinScore] = useState(0)
    const [selectedResult, setSelectedResult] = useState<MemorySearchResult | null>(null)

    const queryMutation = useQueryCollection()
    const results = queryMutation.data

    const handleSearch = () => {
        if (!query.trim() || !selectedCollection) return
        queryMutation.mutate({
            collectionName: selectedCollection,
            query: query.trim(),
            topK,
            minScore,
        })
    }

    if (collectionsLoading) {
        return (
            <div className="flex-1 flex items-center justify-center text-white/70 light:text-black/70">
                <Loader loaderText="Loading collections..." />
            </div>
        )
    }

    if (!collections || collections.length === 0) {
        return (
            <div className="flex-1 flex flex-col items-center justify-center pb-24">
                <div className="relative mb-6">
                    <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                    <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                        <Iconify.Icon icon="lucide:search" className="size-8 text-brand-secondary-400" />
                    </div>
                </div>
                <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No collections to query</h3>
                <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                    Create a collection and add documents first.
                </p>
            </div>
        )
    }

    return (
        <div className="p-4 space-y-4 overflow-y-auto">
            {/* Collection Selector */}
            <div className="space-y-1.5 max-w-sm">
                <Label className="text-sm text-brand-main-200">Collection</Label>
                <Select value={selectedCollection} onValueChange={setSelectedCollection}>
                    <SelectTrigger className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 w-full">
                        <SelectValue placeholder="Select a collection..." />
                    </SelectTrigger>
                    <SelectContent>
                        {collections.map((c) => (
                            <SelectItem key={c.id || c.name} value={c.name}>
                                {c.name}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
            </div>

            {!selectedCollection ? (
                <div className="flex flex-col items-center justify-center py-16 text-white/40 light:text-black/40">
                    <Iconify.Icon icon="lucide:arrow-up" className="size-6 mb-2" />
                    <p className="text-sm">Select a collection to start querying</p>
                </div>
            ) : (
                <>
                    {/* Query Controls */}
                    <div className="flex items-end gap-3">
                        <div className="flex-1 space-y-1.5">
                            <Label className="text-sm text-brand-main-200">Search Query</Label>
                            <Input
                                value={query}
                                onChange={(e) => setQuery(e.target.value)}
                                onKeyDown={(e) => e.key === 'Enter' && handleSearch()}
                                className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                                placeholder="Enter a search query..."
                            />
                        </div>
                        <div className="w-20 space-y-1.5">
                            <Label className="text-sm text-brand-main-200">Top K</Label>
                            <Input
                                type="number"
                                value={topK}
                                onChange={(e) => setTopK(Number(e.target.value))}
                                className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                                min={1}
                                max={100}
                            />
                        </div>
                        <div className="w-24 space-y-1.5">
                            <Label className="text-sm text-brand-main-200">Min Score</Label>
                            <Input
                                type="number"
                                step={0.05}
                                value={minScore}
                                onChange={(e) => setMinScore(Number(e.target.value))}
                                className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                                min={0}
                                max={1}
                            />
                        </div>
                        <Button
                            variant="default"
                            onClick={handleSearch}
                            disabled={queryMutation.isPending || !query.trim()}
                        >
                            {queryMutation.isPending ? 'Searching...' : 'Search'}
                        </Button>
                    </div>

                    {/* Error */}
                    {queryMutation.isError && (
                        <div className="flex items-center gap-2 rounded-lg border border-red-500/20 bg-red-500/5 px-3 py-2 text-sm text-red-400 light:text-red-600">
                            <Iconify.Icon icon="lucide:alert-triangle" className="size-4 shrink-0" />
                            {queryMutation.error.message}
                        </div>
                    )}

                    {/* Results */}
                    {results !== undefined && (
                        <div className="space-y-1">
                            <div className="text-sm text-white/50 light:text-black/50 mb-2">
                                {results.length} result{results.length !== 1 ? 's' : ''} found
                            </div>
                            {results.length === 0 ? (
                                <div className="text-sm text-white/40 light:text-black/40 bg-brand-main-800/50 border border-brand-main-700 rounded-lg p-4 text-center">
                                    No results matched your query
                                </div>
                            ) : (
                                <div className="space-y-1">
                                    {results.map((r, i) => (
                                        <SearchResultRow
                                            key={`${r.documentId}-${r.chunkIndex}`}
                                            result={r}
                                            rank={i + 1}
                                            onClick={() => setSelectedResult(r)}
                                        />
                                    ))}
                                </div>
                            )}
                        </div>
                    )}
                </>
            )}

            {/* Detail Sheet */}
            <Sheet open={selectedResult !== null} onOpenChange={() => setSelectedResult(null)}>
                <SheetContent side="right" className="min-w-[50%] max-h-[100vh] overflow-y-auto scrollbar-macos">
                    <SheetHeader>
                        <SheetTitle className="text-sm">
                            {selectedResult && (
                                <div className="flex items-center gap-2">
                                    <span>Search Result</span>
                                    <span className={`text-xs font-mono px-1.5 py-0.5 rounded border ${scoreColor(selectedResult.score)}`}>
                                        {selectedResult.score.toFixed(4)}
                                    </span>
                                </div>
                            )}
                        </SheetTitle>
                    </SheetHeader>
                    <SheetBody>
                        {selectedResult && (
                            <div className="space-y-4 mt-4">
                                {/* Document Info */}
                                <div className="space-y-2 border-b border-dotted border-white/10 light:border-black/10 pb-4">
                                    <h3 className="text-sm font-semibold text-white/80 light:text-black/80">Document Info</h3>
                                    <div className="grid grid-cols-2 gap-3 text-sm">
                                        <div className="space-y-1">
                                            <div className="text-white/50 light:text-black/50 text-xs">Document ID</div>
                                            <div className="text-white/90 light:text-black/90 font-mono text-xs break-all">{selectedResult.documentId}</div>
                                        </div>
                                        <div className="space-y-1">
                                            <div className="text-white/50 light:text-black/50 text-xs">Chunk Index</div>
                                            <div className="text-white/90 light:text-black/90 font-mono text-xs">{selectedResult.chunkIndex}</div>
                                        </div>
                                    </div>
                                </div>

                                {/* Chunk Content */}
                                <div className="space-y-2 border-b border-dotted border-white/10 light:border-black/10 pb-4">
                                    <h3 className="text-sm font-semibold text-white/80 light:text-black/80">Chunk Content</h3>
                                    <pre className="text-[13px] text-white/80 light:text-black/80 font-mono whitespace-pre-wrap break-words bg-brand-main-900/40 rounded px-3 py-2 leading-relaxed overflow-x-auto max-h-[60vh] overflow-y-auto scrollbar-macos">
                                        {selectedResult.chunkText}
                                    </pre>
                                </div>

                                {/* Metadata */}
                                {selectedResult.metadata && Object.keys(selectedResult.metadata).length > 0 && (
                                    <div className="space-y-2">
                                        <h3 className="text-sm font-semibold text-white/80 light:text-black/80">Metadata</h3>
                                        <div className="space-y-1.5">
                                            {Object.entries(selectedResult.metadata).map(([k, v]) => (
                                                <div key={k} className="flex items-start gap-2 text-xs">
                                                    <span className="text-white/50 light:text-black/50 font-mono shrink-0 min-w-[80px]">{k}</span>
                                                    <span className="text-white/80 light:text-black/80 font-mono break-all">{v}</span>
                                                </div>
                                            ))}
                                        </div>
                                    </div>
                                )}
                            </div>
                        )}
                    </SheetBody>
                </SheetContent>
            </Sheet>
        </div>
    )
}
