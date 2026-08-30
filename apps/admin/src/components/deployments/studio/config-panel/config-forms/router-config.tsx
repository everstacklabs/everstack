import { Input, Label } from '@everstack/ui/components'
import { Icon } from '@iconify/react'
import type { RouterConfig } from '../../types'

interface Props { config: RouterConfig; onChange: (config: RouterConfig) => void }

export function RouterConfigForm({ config, onChange }: Props) {
    const addMapping = () => {
        onChange({ ...config, mappings: [...config.mappings, { model: '', provider: '' }] })
    }
    const removeMapping = (idx: number) => {
        onChange({ ...config, mappings: config.mappings.filter((_, i) => i !== idx) })
    }
    const updateMapping = (idx: number, field: 'model' | 'provider', value: string) => {
        const updated = config.mappings.map((m, i) => i === idx ? { ...m, [field]: value } : m)
        onChange({ ...config, mappings: updated })
    }

    return (
        <div className="space-y-4">
            <div className="flex items-center justify-between">
                <Label className="text-sm text-brand-main-200">Model Mappings</Label>
                <button onClick={addMapping} className="flex items-center gap-1 text-xs text-brand-secondary-400 hover:text-brand-secondary-300">
                    <Icon icon="lucide:plus" className="h-3 w-3" /> Add
                </button>
            </div>
            {config.mappings.map((m, i) => (
                <div key={i} className="flex gap-2 items-start">
                    <div className="flex-1 space-y-1">
                        <Input value={m.model} onChange={(e) => updateMapping(i, 'model', e.target.value)} placeholder="Model" className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 text-xs h-8" />
                        <Input value={m.provider} onChange={(e) => updateMapping(i, 'provider', e.target.value)} placeholder="Provider" className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 text-xs h-8" />
                    </div>
                    <button onClick={() => removeMapping(i)} className="mt-1 p-1 text-brand-main-400 hover:text-red-400 light:hover:text-red-600">
                        <Icon icon="lucide:x" className="h-3.5 w-3.5" />
                    </button>
                </div>
            ))}
            {config.mappings.length === 0 && (
                <p className="text-xs text-brand-main-500">No mappings. Click Add to create one.</p>
            )}
        </div>
    )
}
