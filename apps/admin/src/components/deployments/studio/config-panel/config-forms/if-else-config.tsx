import { Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@everstack/ui/components'
import type { IfElseConfig } from '../../types'

interface Props { config: IfElseConfig; onChange: (config: IfElseConfig) => void }

const HELP_TEXT: Record<string, string> = {
    expression: 'JavaScript-like expression. e.g. response.status === 200',
    jsonpath: 'JSONPath expression. e.g. $.data.items[0].active',
    header: 'Header check. e.g. content-type contains application/json',
}

export function IfElseConfigForm({ config, onChange }: Props) {
    return (
        <div className="space-y-4">
            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">Condition Type</Label>
                <Select
                    value={config.conditionType}
                    onValueChange={(v) => onChange({ ...config, conditionType: v as IfElseConfig['conditionType'] })}
                >
                    <SelectTrigger className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50">
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="expression">Expression</SelectItem>
                        <SelectItem value="jsonpath">JSONPath</SelectItem>
                        <SelectItem value="header">Header</SelectItem>
                    </SelectContent>
                </Select>
            </div>
            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">Condition Expression</Label>
                <Input
                    value={config.conditionExpression}
                    onChange={(e) => onChange({ ...config, conditionExpression: e.target.value })}
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    placeholder={config.conditionType === 'jsonpath' ? '$.data.active' : config.conditionType === 'header' ? 'content-type contains json' : 'response.status === 200'}
                />
                <p className="text-xs text-brand-main-400">{HELP_TEXT[config.conditionType]}</p>
            </div>
        </div>
    )
}
