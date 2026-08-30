import { Label } from '@everstack/ui/components'
import type { StartConfig } from '../../types'

interface Props { config: StartConfig; onChange: (config: StartConfig) => void }

export function StartConfigForm({ config, onChange }: Props) {
    return (
        <div className="space-y-4">
            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">System Prompt</Label>
                <textarea
                    value={config.systemPrompt ?? ''}
                    onChange={(e) => onChange({ ...config, systemPrompt: e.target.value })}
                    className="w-full rounded-md bg-brand-main-700/50 border border-brand-main-500 text-white light:text-brand-main-50 text-sm px-3 py-2 min-h-[80px] resize-y placeholder:text-brand-main-400 focus:outline-none focus:ring-2 focus:ring-brand-secondary-500"
                    placeholder="You are a helpful assistant..."
                />
            </div>
        </div>
    )
}
