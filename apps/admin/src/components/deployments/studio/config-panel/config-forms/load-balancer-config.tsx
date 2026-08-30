import { Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@everstack/ui/components'
import { Icon } from '@iconify/react'
import type { LoadBalancerConfig } from '../../types'

interface Props { config: LoadBalancerConfig; onChange: (config: LoadBalancerConfig) => void }

export function LoadBalancerConfigForm({ config, onChange }: Props) {
    const isManualStrategy = config.strategy !== 'router'

    const addWeight = () => {
        onChange({ ...config, weights: [...config.weights, { provider: '', weight: 1 }] })
    }
    const removeWeight = (idx: number) => {
        onChange({ ...config, weights: config.weights.filter((_, i) => i !== idx) })
    }
    const updateWeight = (idx: number, field: 'provider' | 'weight', value: string | number) => {
        const updated = config.weights.map((w, i) => i === idx ? { ...w, [field]: value } : w)
        onChange({ ...config, weights: updated })
    }

    return (
        <div className="space-y-4">
            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">Strategy</Label>
                <Select value={config.strategy} onValueChange={(v) => onChange({ ...config, strategy: v })}>
                    <SelectTrigger className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50">
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="router">Router (Gateway Resolution)</SelectItem>
                        <SelectItem value="round_robin">Round Robin</SelectItem>
                        <SelectItem value="weighted">Weighted</SelectItem>
                        <SelectItem value="random">Random</SelectItem>
                    </SelectContent>
                </Select>
                {!isManualStrategy && (
                    <p className="text-xs text-brand-main-400">
                        Uses the gateway&apos;s full model resolution pipeline: FastPath cache, custom models, route map, and provider catalog.
                    </p>
                )}
            </div>

            {isManualStrategy && (
                <>
                    <div className="space-y-1.5">
                        <Label className="text-sm text-brand-main-200">Fallback</Label>
                        <Input value={config.fallback} onChange={(e) => onChange({ ...config, fallback: e.target.value })} className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50" placeholder="Fallback provider" />
                    </div>
                    <div className="space-y-2">
                        <div className="flex items-center justify-between">
                            <Label className="text-sm text-brand-main-200">Provider Weights</Label>
                            <button onClick={addWeight} className="flex items-center gap-1 text-xs text-brand-secondary-400 hover:text-brand-secondary-300">
                                <Icon icon="lucide:plus" className="h-3 w-3" /> Add
                            </button>
                        </div>
                        {config.weights.map((w, i) => (
                            <div key={i} className="flex gap-2 items-center">
                                <Input value={w.provider} onChange={(e) => updateWeight(i, 'provider', e.target.value)} placeholder="Provider" className="flex-1 bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 text-xs h-8" />
                                <Input type="number" value={w.weight} onChange={(e) => updateWeight(i, 'weight', Number(e.target.value))} className="w-16 bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 text-xs h-8" />
                                <button onClick={() => removeWeight(i)} className="p-1 text-brand-main-400 hover:text-red-400 light:hover:text-red-600">
                                    <Icon icon="lucide:x" className="h-3.5 w-3.5" />
                                </button>
                            </div>
                        ))}
                        <p className="text-xs text-brand-main-400">
                            Rate-limited providers are automatically filtered out during selection.
                        </p>
                    </div>
                </>
            )}
        </div>
    )
}
