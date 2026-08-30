import { Label, Switch } from '@everstack/ui/components'
import type { OutputGuardrailsConfig } from '../../types'

interface Props { config: OutputGuardrailsConfig; onChange: (config: OutputGuardrailsConfig) => void }

export function OutputGuardrailsConfigForm({ config, onChange }: Props) {
    return (
        <div className="space-y-4">
            <div className="flex items-center justify-between">
                <Label className="text-sm text-brand-main-200">Jailbreak Detection</Label>
                <Switch checked={config.jailbreakDetection} onCheckedChange={(v) => onChange({ ...config, jailbreakDetection: v })} />
            </div>
            <div className="flex items-center justify-between">
                <Label className="text-sm text-brand-main-200">Hallucination Detection</Label>
                <Switch checked={config.hallucinationDetection} onCheckedChange={(v) => onChange({ ...config, hallucinationDetection: v })} />
            </div>
            <div className="flex items-center justify-between">
                <Label className="text-sm text-brand-main-200">Toxicity Detection</Label>
                <Switch checked={config.toxicityDetection} onCheckedChange={(v) => onChange({ ...config, toxicityDetection: v })} />
            </div>
        </div>
    )
}
