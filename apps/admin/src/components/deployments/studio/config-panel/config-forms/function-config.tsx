import { Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@everstack/ui/components'
import { useFunctions } from '@/hooks/deployments/use-functions'
import type { FunctionConfig } from '../../types'

interface Props { config: FunctionConfig; onChange: (config: FunctionConfig) => void }

export function FunctionConfigForm({ config, onChange }: Props) {
    const { data: functions = [], isLoading } = useFunctions()

    const handleFunctionSelect = (functionId: string) => {
        const fn = functions.find((f) => f.id === functionId)
        if (fn) {
            onChange({
                ...config,
                functionId: fn.id,
                functionName: fn.name,
                functionMode: fn.mode === 1 ? 'Webhook' : fn.mode === 2 ? 'Proxy' : fn.mode === 3 ? 'Isolated' : 'Unknown',
            })
        }
    }

    return (
        <div className="space-y-4">
            <div className="space-y-1.5 w-full">
                <Label className="text-sm text-brand-main-200">Function</Label>
                <Select value={config.functionId} onValueChange={handleFunctionSelect}>
                    <SelectTrigger className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 w-full">
                        <SelectValue placeholder={isLoading ? 'Loading...' : 'Select a function'} />
                    </SelectTrigger>
                    <SelectContent>
                        {functions.map((fn) => (
                            <SelectItem key={fn.id} value={fn.id}>
                                {fn.name}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
            </div>
            {config.functionMode && (
                <div className="space-y-1.5">
                    <Label className="text-sm text-brand-main-200">Mode</Label>
                    <div className="text-sm text-brand-main-300 bg-brand-main-800 rounded px-3 py-1.5">
                        {config.functionMode}
                    </div>
                </div>
            )}
            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">Timeout (ms)</Label>
                <Input type="number" value={config.timeoutMs} onChange={(e) => onChange({ ...config, timeoutMs: Number(e.target.value) })} className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50" />
            </div>
        </div>
    )
}
