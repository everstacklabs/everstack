import { Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@everstack/ui/components'
import type { STTConfig } from '../../types'

interface Props { config: STTConfig; onChange: (config: STTConfig) => void }

export function STTConfigForm({ config, onChange }: Props) {
    return (
        <div className="space-y-4">
            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">Provider</Label>
                <Select value={config.provider} onValueChange={(v) => onChange({ ...config, provider: v })}>
                    <SelectTrigger className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 w-full">
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="openai">OpenAI</SelectItem>
                    </SelectContent>
                </Select>
            </div>
            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">Model</Label>
                <Select value={config.model} onValueChange={(v) => onChange({ ...config, model: v })}>
                    <SelectTrigger className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 w-full">
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="whisper-1">whisper-1</SelectItem>
                    </SelectContent>
                </Select>
            </div>
            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">Language (optional)</Label>
                <Input
                    value={config.language}
                    onChange={(e) => onChange({ ...config, language: e.target.value })}
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    placeholder="en (ISO-639-1)"
                />
            </div>
            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">Response Format</Label>
                <Select value={config.responseFormat} onValueChange={(v) => onChange({ ...config, responseFormat: v })}>
                    <SelectTrigger className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 w-full">
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="json">JSON</SelectItem>
                        <SelectItem value="text">Text</SelectItem>
                        <SelectItem value="verbose_json">Verbose JSON</SelectItem>
                        <SelectItem value="srt">SRT</SelectItem>
                        <SelectItem value="vtt">VTT</SelectItem>
                    </SelectContent>
                </Select>
            </div>
        </div>
    )
}
