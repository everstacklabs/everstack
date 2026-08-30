import { Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@everstack/ui/components'
import type { WebhookConfig } from '../../types'

interface Props { config: WebhookConfig; onChange: (config: WebhookConfig) => void }

export function WebhookConfigForm({ config, onChange }: Props) {
    const headerEntries = Object.entries(config.headers ?? {})

    const updateHeader = (index: number, field: 'key' | 'value', val: string) => {
        const entries = [...headerEntries]
        const [oldKey, oldValue] = entries[index]
        if (field === 'key') {
            entries[index] = [val, oldValue]
        } else {
            entries[index] = [oldKey, val]
        }
        onChange({ ...config, headers: Object.fromEntries(entries) })
    }

    const addHeader = () => {
        onChange({ ...config, headers: { ...config.headers, '': '' } })
    }

    const removeHeader = (index: number) => {
        const entries = [...headerEntries]
        entries.splice(index, 1)
        onChange({ ...config, headers: Object.fromEntries(entries) })
    }

    return (
        <div className="space-y-4">
            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">URL</Label>
                <Input
                    value={config.url}
                    onChange={(e) => onChange({ ...config, url: e.target.value })}
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    placeholder="https://hooks.example.com/webhook"
                />
            </div>
            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">Method</Label>
                <Select value={config.method} onValueChange={(v) => onChange({ ...config, method: v })}>
                    <SelectTrigger className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50">
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="GET">GET</SelectItem>
                        <SelectItem value="POST">POST</SelectItem>
                        <SelectItem value="PUT">PUT</SelectItem>
                        <SelectItem value="PATCH">PATCH</SelectItem>
                        <SelectItem value="DELETE">DELETE</SelectItem>
                    </SelectContent>
                </Select>
            </div>
            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">Headers</Label>
                <div className="space-y-2">
                    {headerEntries.map(([key, value], i) => (
                        <div key={i} className="flex gap-2">
                            <Input
                                value={key}
                                onChange={(e) => updateHeader(i, 'key', e.target.value)}
                                className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 flex-1"
                                placeholder="Key"
                            />
                            <Input
                                value={value}
                                onChange={(e) => updateHeader(i, 'value', e.target.value)}
                                className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 flex-1"
                                placeholder="Value"
                            />
                            <button
                                type="button"
                                onClick={() => removeHeader(i)}
                                className="text-brand-main-400 hover:text-red-400 light:hover:text-red-600 px-1 text-sm"
                            >
                                &times;
                            </button>
                        </div>
                    ))}
                    <button
                        type="button"
                        onClick={addHeader}
                        className="text-xs text-brand-main-300 hover:text-white light:hover:text-brand-main-50 transition-colors"
                    >
                        + Add header
                    </button>
                </div>
            </div>
            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">Body Template</Label>
                <textarea
                    value={config.bodyTemplate ?? ''}
                    onChange={(e) => onChange({ ...config, bodyTemplate: e.target.value })}
                    className="w-full rounded-md bg-brand-main-700/50 border border-brand-main-500 text-white light:text-brand-main-50 text-sm px-3 py-2 min-h-[80px] resize-y placeholder:text-brand-main-400 focus:outline-none focus:ring-2 focus:ring-brand-secondary-500"
                    placeholder='{"event": "{{event}}", "data": {{payload}}}'
                />
            </div>
            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">Retries</Label>
                <Input
                    type="number"
                    min="0"
                    value={config.retries}
                    onChange={(e) => onChange({ ...config, retries: Number(e.target.value) })}
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                />
            </div>
            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">Timeout (ms)</Label>
                <Input
                    type="number"
                    value={config.timeoutMs}
                    onChange={(e) => onChange({ ...config, timeoutMs: Number(e.target.value) })}
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                />
            </div>
        </div>
    )
}
