import { Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@everstack/ui/components'
import type { CacheConfig } from '../../types'

interface Props { config: CacheConfig; onChange: (config: CacheConfig) => void }

export function CacheConfigForm({ config, onChange }: Props) {
    return (
        <div className="space-y-4">
            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">Cache Type</Label>
                <Select value={config.type} onValueChange={(v) => onChange({ ...config, type: v })}>
                    <SelectTrigger className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50">
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="semantic">Semantic</SelectItem>
                        <SelectItem value="exact">Exact Match</SelectItem>
                    </SelectContent>
                </Select>
            </div>
            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">TTL (seconds)</Label>
                <Input type="number" value={config.ttl} onChange={(e) => onChange({ ...config, ttl: Number(e.target.value) })} className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50" />
            </div>
            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">Max Entries</Label>
                <Input type="number" value={config.maxEntries} onChange={(e) => onChange({ ...config, maxEntries: Number(e.target.value) })} className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50" />
            </div>
            {config.type === 'semantic' && (
                <div className="space-y-1.5">
                    <Label className="text-sm text-brand-main-200">Similarity Threshold</Label>
                    <Input type="number" step="0.01" min="0" max="1" value={config.similarityThreshold} onChange={(e) => onChange({ ...config, similarityThreshold: Number(e.target.value) })} className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50" />
                </div>
            )}
        </div>
    )
}
