import { Label, Switch, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@everstack/ui/components'
import type { ResponseConfig } from '../../types'

interface Props { config: ResponseConfig; onChange: (config: ResponseConfig) => void }

export function ResponseConfigForm({ config, onChange }: Props) {
    return (
        <div className="space-y-4">
            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">Format</Label>
                <Select value={config.format} onValueChange={(v) => onChange({ ...config, format: v })}>
                    <SelectTrigger className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50">
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="openai">OpenAI Compatible</SelectItem>
                        <SelectItem value="raw">Raw</SelectItem>
                    </SelectContent>
                </Select>
            </div>
            <div className="flex items-center justify-between">
                <Label className="text-sm text-brand-main-200">Streaming</Label>
                <Switch checked={config.streaming} onCheckedChange={(v) => onChange({ ...config, streaming: v })} />
            </div>
            <div className="flex items-center justify-between">
                <Label className="text-sm text-brand-main-200">Include Usage</Label>
                <Switch checked={config.includeUsage} onCheckedChange={(v) => onChange({ ...config, includeUsage: v })} />
            </div>
            <div className="flex items-center justify-between">
                <Label className="text-sm text-brand-main-200">Include Timings</Label>
                <Switch checked={config.includeTimings} onCheckedChange={(v) => onChange({ ...config, includeTimings: v })} />
            </div>
        </div>
    )
}
