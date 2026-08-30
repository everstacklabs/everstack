import { Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Textarea } from '@everstack/ui/components'
import type { MemoryConfig } from '../../types'

interface Props { config: MemoryConfig; onChange: (config: MemoryConfig) => void }

export function MemoryConfigForm({ config, onChange }: Props) {
    return (
        <div className="space-y-4">
            <div className="space-y-1.5 w-full">
                <Label className="text-sm text-brand-main-200">Operation</Label>
                <Select
                    value={config.operation}
                    onValueChange={(v) => onChange({ ...config, operation: v as 'store' | 'query' })}
                >
                    <SelectTrigger className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 w-full">
                        <SelectValue placeholder="Select operation" />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="query">Query (search memory)</SelectItem>
                        <SelectItem value="store">Store (save to memory)</SelectItem>
                    </SelectContent>
                </Select>
            </div>

            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">Collection</Label>
                <Input
                    value={config.collection}
                    onChange={(e) => onChange({ ...config, collection: e.target.value })}
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    placeholder="Collection name"
                />
            </div>

            <div className="space-y-1.5 w-full">
                <Label className="text-sm text-brand-main-200">Content Source</Label>
                <Select
                    value={config.contentSource}
                    onValueChange={(v) => onChange({ ...config, contentSource: v as MemoryConfig['contentSource'] })}
                >
                    <SelectTrigger className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 w-full">
                        <SelectValue placeholder="Select content source" />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="input">User Input</SelectItem>
                        <SelectItem value="variable">Variable</SelectItem>
                        <SelectItem value="previous">Previous Node Output</SelectItem>
                        <SelectItem value="static">Static Text</SelectItem>
                    </SelectContent>
                </Select>
            </div>

            {config.contentSource === 'variable' && (
                <div className="space-y-1.5">
                    <Label className="text-sm text-brand-main-200">Variable Name</Label>
                    <Input
                        value={config.variableName ?? ''}
                        onChange={(e) => onChange({ ...config, variableName: e.target.value })}
                        className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                        placeholder="e.g. agent_output"
                    />
                </div>
            )}

            {config.contentSource === 'static' && (
                <div className="space-y-1.5">
                    <Label className="text-sm text-brand-main-200">Static Content</Label>
                    <Textarea
                        value={config.staticContent ?? ''}
                        onChange={(e) => onChange({ ...config, staticContent: e.target.value })}
                        className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                        placeholder="Enter text or use {{node_label.field}} templates..."
                        rows={4}
                    />
                </div>
            )}

            {config.operation === 'store' && (
                <>
                    <div className="space-y-1.5">
                        <Label className="text-sm text-brand-main-200">Source Label</Label>
                        <Input
                            value={config.source ?? ''}
                            onChange={(e) => onChange({ ...config, source: e.target.value })}
                            className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                            placeholder="e.g. user-upload, api-response"
                        />
                    </div>
                    <div className="space-y-1.5">
                        <Label className="text-sm text-brand-main-200">Chunk Size</Label>
                        <Input
                            type="number"
                            min={100}
                            max={4000}
                            value={config.chunkSize}
                            onChange={(e) => onChange({ ...config, chunkSize: Number(e.target.value) })}
                            className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                        />
                    </div>
                </>
            )}

            {config.operation === 'query' && (
                <>
                    <div className="space-y-1.5">
                        <Label className="text-sm text-brand-main-200">Top K Results</Label>
                        <Input
                            type="number"
                            min={1}
                            max={50}
                            value={config.topK}
                            onChange={(e) => onChange({ ...config, topK: Number(e.target.value) })}
                            className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                        />
                    </div>
                    <div className="space-y-1.5">
                        <Label className="text-sm text-brand-main-200">Min Score (0.0 - 1.0)</Label>
                        <Input
                            type="number"
                            step={0.05}
                            min={0}
                            max={1}
                            value={config.minScore}
                            onChange={(e) => onChange({ ...config, minScore: Number(e.target.value) })}
                            className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                        />
                    </div>
                </>
            )}

            <div className="text-xs font-medium text-brand-main-400 uppercase tracking-wider mt-6 mb-2">Advanced</div>

            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">Embedding Model</Label>
                <Input
                    value={config.embeddingModel ?? ''}
                    onChange={(e) => onChange({ ...config, embeddingModel: e.target.value || undefined })}
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    placeholder="Leave blank for system default"
                />
            </div>
            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">Output Variable</Label>
                <Input
                    value={config.outputVariable}
                    onChange={(e) => onChange({ ...config, outputVariable: e.target.value })}
                    className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                    placeholder="memory_results"
                />
            </div>
        </div>
    )
}
