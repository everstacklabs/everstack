import { ui } from '@everstack/ui'
import { Plus, X, Triangle } from 'lucide-react'
import { useScoreConfigs } from '@/hooks/evaluations/use-score-configs'
import { usePlaygroundStore } from '@/stores/playground-store'

const { Popover, PopoverTrigger, PopoverContent, Checkbox, Badge } = ui

/**
 * Attaches score configs to the playground grid. Every selected scorer runs
 * (via the synchronous ScoreOutput RPC) against each generated cell. Selected
 * scorers render as removable chips next to the trigger, matching the grid
 * toolbar in the reference design.
 */
export function ScorerPicker() {
    const { data: configs } = useScoreConfigs()
    const selectedIds = usePlaygroundStore((s) => s.scorerConfigIds)
    const setScorerConfigIds = usePlaygroundStore((s) => s.setScorerConfigIds)

    const all = configs ?? []
    const selected = all.filter((c) => selectedIds.includes(c.id))

    const toggle = (id: string) => {
        setScorerConfigIds(
            selectedIds.includes(id)
                ? selectedIds.filter((x) => x !== id)
                : [...selectedIds, id],
        )
    }

    return (
        <div className="flex items-center gap-1.5">
            <Popover>
                <PopoverTrigger asChild>
                    <button
                        type="button"
                        className="inline-flex items-center gap-1 rounded border border-brand-main-700 bg-brand-main-950/20 px-2 py-1 text-xs text-white/65 transition-colors hover:border-brand-secondary-500/60 hover:text-white light:text-black/65 light:hover:text-brand-main-50"
                    >
                        <Plus className="h-3 w-3" />
                        Scorer
                        {selected.length > 0 && (
                            <span className="ml-0.5 text-brand-secondary-300">{selected.length}</span>
                        )}
                    </button>
                </PopoverTrigger>
                <PopoverContent align="end" className="w-64 p-1">
                    {all.length === 0 ? (
                        <div className="px-2 py-3 text-center text-xs text-white/40 light:text-black/40">
                            No scorers yet. Create one in Scorers.
                        </div>
                    ) : (
                        <div className="max-h-64 overflow-auto">
                            {all.map((c) => (
                                <label
                                    key={c.id}
                                    className="flex items-center gap-2 rounded px-2 py-1.5 text-xs text-white/80 light:text-black/80 hover:bg-brand-main-800/60 cursor-pointer"
                                >
                                    <Checkbox
                                        checked={selectedIds.includes(c.id)}
                                        onCheckedChange={() => toggle(c.id)}
                                    />
                                    <Triangle className="h-3 w-3 text-brand-secondary-300 shrink-0" />
                                    <span className="truncate">{c.name}</span>
                                </label>
                            ))}
                        </div>
                    )}
                </PopoverContent>
            </Popover>

            {selected.map((c) => (
                <Badge
                    key={c.id}
                    variant="outline"
                    className="gap-1 border-brand-secondary-500/40 bg-transparent text-brand-secondary-200 text-[11px]"
                >
                    {c.name}
                    <button
                        type="button"
                        onClick={() => toggle(c.id)}
                        className="hover:text-white light:hover:text-black"
                        aria-label={`Remove ${c.name}`}
                    >
                        <X className="h-2.5 w-2.5" />
                    </button>
                </Badge>
            ))}
        </div>
    )
}
