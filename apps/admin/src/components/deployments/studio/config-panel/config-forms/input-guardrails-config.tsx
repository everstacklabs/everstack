import { Label, Switch } from '@everstack/ui/components'
import type { InputGuardrailsConfig } from '../../types'

interface Props { config: InputGuardrailsConfig; onChange: (config: InputGuardrailsConfig) => void }

export function InputGuardrailsConfigForm({ config, onChange }: Props) {
    return (
        <div className="space-y-4">
            <div className="flex items-center justify-between">
                <Label className="text-sm text-brand-main-200">PII Detection</Label>
                <Switch checked={config.piiDetection} onCheckedChange={(v) => onChange({ ...config, piiDetection: v })} />
            </div>
            <div className="flex items-center justify-between">
                <Label className="text-sm text-brand-main-200">Prompt Injection</Label>
                <Switch checked={config.promptInjection} onCheckedChange={(v) => onChange({ ...config, promptInjection: v })} />
            </div>
            <div className="flex items-center justify-between">
                <Label className="text-sm text-brand-main-200">Content Filter</Label>
                <Switch checked={config.contentFilter} onCheckedChange={(v) => onChange({ ...config, contentFilter: v })} />
            </div>
        </div>
    )
}
