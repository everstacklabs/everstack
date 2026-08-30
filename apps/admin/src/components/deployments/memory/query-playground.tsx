import { useState } from 'react'
import { Input, Label, Button } from '@everstack/ui/components'
import { ui } from '@everstack/ui'
import { useQueryCollection } from '@/hooks/deployments/use-memory'
import type { MemorySearchResult } from '@/server/memory'

const { Sheet, SheetContent, SheetHeader, SheetBody, SheetTitle } = ui

interface Props {
    collectionName: string
}

export function QueryPlayground({ collectionName }: Props) {
    const [query, setQuery] = useState('')
    const [topK, setTopK] = useState(5)
    const [minScore, setMinScore] = useState(0)
    const [selectedResult, setSelectedResult] = useState<MemorySearchResult | null>(null)

    const queryMutation = useQueryCollection()
    const results = queryMutation.data

    const handleSearch = () => {
        if (!query.trim()) return
        queryMutation.mutate({
            collectionName,
            query: query.trim(),
            topK,
            minScore,
        })
    }

    return (
        <div className="flex flex-col gap-4">
            <div className="text-xs font-medium text-brand-main-400 uppercase tracking-wider">
                Query Playground
            </div>

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

            {queryMutation.isError && (
                <div className="text-sm text-red-400 light:text-red-600">
                    Error: {queryMutation.error.message}
                </div>
            )}

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

            <ResultDetailSheet
                result={selectedResult}
                open={selectedResult !== null}
                onClose={() => setSelectedResult(null)}
            />
        </div>
    )
}

// ── Compact result row ──────────────────────────────────────────────

function scoreColor(score: number): string {
    if (score >= 0.8) return 'bg-brand-secondary-500/20 text-brand-secondary-300 border-brand-secondary-500/30'
    if (score >= 0.5) return 'bg-brand-secondary-500/10 text-brand-secondary-400 border-brand-secondary-500/20'
    return 'bg-brand-main-700/50 text-brand-main-300 border-brand-main-600'
}

const PREVIEW_LENGTH = 120

export function SearchResultRow({ result, rank, onClick }: { result: MemorySearchResult; rank: number; onClick: () => void }) {
    const preview = result.chunkText.length > PREVIEW_LENGTH
        ? result.chunkText.slice(0, PREVIEW_LENGTH).trimEnd() + '...'
        : result.chunkText

    return (
        <button
            onClick={onClick}
            className="w-full text-left bg-brand-main-800/50 border border-brand-main-700 rounded px-2.5 py-1.5 hover:border-brand-main-500 hover:bg-brand-main-800/80 transition-colors flex items-center gap-2.5 group"
        >
            <span className="text-[10px] text-brand-main-300 font-mono shrink-0">#{rank}</span>
            <div className="flex-1 min-w-0">
                <p className="text-xs text-brand-main-100 truncate font-mono">{preview}</p>
                {result.metadata && Object.keys(result.metadata).length > 0 && (
                    <div className="flex gap-1 mt-0.5 overflow-hidden">
                        {Object.entries(result.metadata).slice(0, 3).map(([k, v]) => (
                            <span key={k} className="text-[9px] bg-brand-main-700/50 text-brand-main-300 px-1 py-px rounded-sm font-mono truncate">
                                {k}={v}
                            </span>
                        ))}
                        {Object.keys(result.metadata).length > 3 && (
                            <span className="text-[9px] text-brand-main-400">+{Object.keys(result.metadata).length - 3}</span>
                        )}
                    </div>
                )}
            </div>
            <span className={`text-[10px] font-mono px-1.5 py-0.5 rounded-sm border shrink-0 ${scoreColor(result.score)}`}>
                {result.score.toFixed(4)}
            </span>
        </button>
    )
}

// ── Detail sheet ────────────────────────────────────────────────────

function ResultDetailSheet({ result, open, onClose }: { result: MemorySearchResult | null; open: boolean; onClose: () => void }) {
    if (!result) return null

    return (
        <Sheet open={open} onOpenChange={onClose}>
            <SheetContent side="right" className="min-w-[50%] max-h-[100vh] overflow-y-auto scrollbar-macos">
                <SheetHeader>
                    <SheetTitle className="text-sm">
                        <div className="flex items-center gap-2">
                            <span>Search Result</span>
                            <span className={`text-xs font-mono px-1.5 py-0.5 rounded border ${scoreColor(result.score)}`}>
                                {result.score.toFixed(4)}
                            </span>
                        </div>
                    </SheetTitle>
                </SheetHeader>
                <SheetBody>
                    <div className="space-y-4 mt-4">
                        {/* Document Info */}
                        <div className="space-y-2 border-b border-dotted border-white/10 light:border-black/10 pb-4">
                            <h3 className="text-sm font-semibold text-white/80 light:text-black/80">Document Info</h3>
                            <div className="grid grid-cols-2 gap-3 text-sm">
                                <div className="space-y-1">
                                    <div className="text-white/50 light:text-black/50 text-xs">Document ID</div>
                                    <div className="text-white/90 light:text-black/90 font-mono text-xs break-all">{result.documentId}</div>
                                </div>
                                <div className="space-y-1">
                                    <div className="text-white/50 light:text-black/50 text-xs">Chunk Index</div>
                                    <div className="text-white/90 light:text-black/90 font-mono text-xs">{result.chunkIndex}</div>
                                </div>
                            </div>
                        </div>

                        {/* Chunk Content */}
                        <div className="space-y-2 border-b border-dotted border-white/10 light:border-black/10 pb-4">
                            <h3 className="text-sm font-semibold text-white/80 light:text-black/80">Chunk Content</h3>
                            <pre className="text-[13px] text-white/80 light:text-black/80 font-mono whitespace-pre-wrap break-words bg-brand-main-900/40 rounded px-3 py-2 leading-relaxed overflow-x-auto max-h-[60vh] overflow-y-auto scrollbar-macos">
                                {result.chunkText}
                            </pre>
                        </div>

                        {/* Metadata */}
                        {result.metadata && Object.keys(result.metadata).length > 0 && (
                            <div className="space-y-2">
                                <h3 className="text-sm font-semibold text-white/80 light:text-black/80">Metadata</h3>
                                <div className="space-y-1.5">
                                    {Object.entries(result.metadata).map(([k, v]) => (
                                        <div key={k} className="flex items-start gap-2 text-xs">
                                            <span className="text-white/50 light:text-black/50 font-mono shrink-0 min-w-[80px]">{k}</span>
                                            <span className="text-white/80 light:text-black/80 font-mono break-all">{v}</span>
                                        </div>
                                    ))}
                                </div>
                            </div>
                        )}
                    </div>
                </SheetBody>
            </SheetContent>
        </Sheet>
    )
}
