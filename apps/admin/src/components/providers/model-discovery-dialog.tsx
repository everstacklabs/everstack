import { useState, useEffect } from 'react'
import { Iconify, ui } from '@everstack/ui'
import { Search, Plus, Loader2, RefreshCw } from 'lucide-react'
import { useSearchModels, useAddCustomModel, useCustomModels } from '../../hooks/use-model-discovery'
import { useDebounce } from '../../hooks/use-debounce'
import { InputWithIcon, toast } from '@everstack/ui/components'

const { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, Button, Input, Label, Tabs, TabsContent, TabsList, TabsTrigger, Badge } = ui

interface ModelDiscoveryDialogProps {
    open: boolean
    onOpenChange: (open: boolean) => void
    providerName: string
    providerDisplayName: string
    supportsDiscovery: boolean
    customBaseUrl?: string // Optional custom base URL (for Ollama, etc.)
}

export function ModelDiscoveryDialog({
    open,
    onOpenChange,
    providerName,
    providerDisplayName,
    supportsDiscovery,
    customBaseUrl,
}: ModelDiscoveryDialogProps) {
    const [searchQuery, setSearchQuery] = useState('')
    const [manualModel, setManualModel] = useState({
        name: '',
        displayName: '',
        metadata: {} as Record<string, string>,
    })
    const [isRefreshing, setIsRefreshing] = useState(false)

    const debouncedSearch = useDebounce(searchQuery, 300)

    // Search for models if provider supports discovery
    const { data: searchResults, isLoading: isSearching, error: searchError, refetch } = useSearchModels(
        providerName,
        debouncedSearch,
        {
            enabled: supportsDiscovery && open,
            useProviderApiKey: true,
            customBaseUrl: customBaseUrl, // Pass custom base URL if provided
        }
    )

    // Manual refresh handler
    const handleRefresh = async () => {
        setIsRefreshing(true)
        try {
            await refetch()
            toast.success('Models refreshed', { duration: 2000 })
        } catch (error) {
            // Error is already handled by the query
        } finally {
            // Keep spinning for at least 500ms for better UX
            setTimeout(() => setIsRefreshing(false), 500)
        }
    }

    // Get existing custom models for this provider
    const { data: customModelsData } = useCustomModels(providerName)
    const existingModels = new Set(customModelsData?.models?.map(m => m.modelName) || [])

    // Mutation to add custom model
    const addModelMutation = useAddCustomModel()

    const handleAddDiscoveredModel = async (model: any) => {
        await addModelMutation.mutateAsync({
            providerName,
            modelName: model.name,
            displayName: model.displayName,
            modelMetadata: model.metadata || {},
            source: 'discovered',
        })
    }

    const handleAddManualModel = async () => {
        if (!manualModel.name || !manualModel.displayName) {
            return
        }

        await addModelMutation.mutateAsync({
            providerName,
            modelName: manualModel.name,
            displayName: manualModel.displayName,
            modelMetadata: manualModel.metadata,
            source: 'manual',
        })

        // Reset form
        setManualModel({ name: '', displayName: '', metadata: {} })
    }

    useEffect(() => {
        if (!open) {
            setSearchQuery('')
            setManualModel({ name: '', displayName: '', metadata: {} })
        } else {
            // Clear search when opening to show all models
            setSearchQuery('')
        }
    }, [open])

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="max-w-4xl max-h-[80vh] overflow-hidden flex flex-col bg-brand-main-900 border-brand-main-500">
                <DialogHeader>
                    <DialogTitle>Discover Models - {providerDisplayName}</DialogTitle>
                    <DialogDescription>
                        Search for available models or manually add a custom model
                    </DialogDescription>
                </DialogHeader>

                <Tabs defaultValue={supportsDiscovery ? "search" : "manual"} className="flex-1 flex flex-col overflow-hidden">
                    <TabsList className="grid w-full" style={{ gridTemplateColumns: supportsDiscovery ? '1fr 1fr' : '1fr' }}>
                        {supportsDiscovery && <TabsTrigger value="search">Search Models</TabsTrigger>}
                        <TabsTrigger value="manual">Manual Entry</TabsTrigger>
                    </TabsList>

                    {supportsDiscovery && (
                        <TabsContent value="search" className="flex-1 overflow-auto mt-4 space-y-4">
                            <div className="flex gap-2">
                                <div className="relative flex-1">
                                    <InputWithIcon
                                        placeholder="Search models..."
                                        value={searchQuery}
                                        icon={<Search className="text-white/50 light:text-black/50 size-4" />}
                                        onChange={(e) => setSearchQuery(e.target.value)}
                                    />
                                </div>
                                <Button
                                    variant="outline"
                                    size="icon"
                                    onClick={handleRefresh}
                                    disabled={isRefreshing || isSearching}
                                    title="Refresh models"
                                    className="shrink-0"
                                >
                                    <RefreshCw className={`h-4 w-4 ${isRefreshing || isSearching ? 'animate-spin' : ''}`} />
                                </Button>
                            </div>

                            {isSearching && (
                                <div className="flex items-center justify-center py-8">
                                    <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                                </div>
                            )}

                            {/* Show warning from response (e.g., local Ollama down but showing cloud models) */}
                            {!isSearching && !searchError && searchResults?.warning && (
                                <div className="bg-brand-secondary-500/10 border border-brand-secondary-500/50 rounded-md py-2 px-4">
                                    <div className="flex items-start gap-2">
                                        {/* <div className="text-yellow-400 light:text-yellow-700 mt-0.5 text-xl">ℹ️</div> */}
                                        <Iconify.Icon icon="mdi:warning-outline" className="text-brand-secondary-500 size-4 mt-0.5" />
                                        <div className="flex-1">
                                            <h4 className="text-sm font-semibold text-brand-secondary-500 mb-1">
                                                {providerName === 'ollama' && searchResults.warning.toLowerCase().includes('connect')
                                                    ? 'Local Ollama Not Running'
                                                    : 'Notice'}
                                            </h4>
                                            {providerName === 'ollama' && searchResults.warning.toLowerCase().includes('connect') ? (
                                                <>
                                                    <p className="text-sm text-brand-secondary-500/90">
                                                        Your local Ollama instance is not running. Showing cloud models below.
                                                    </p>
                                                </>
                                            ) : (
                                                <p className="text-sm text-brand-secondary-500/90">{searchResults.warning}</p>
                                            )}
                                        </div>
                                    </div>
                                </div>
                            )}

                            {searchError && (
                                <div className="bg-yellow-500/10 border border-yellow-500/50 rounded-lg p-4 my-4">
                                    <div className="flex items-start gap-3">
                                        <div className="text-yellow-400 light:text-yellow-700 mt-0.5 text-xl">ℹ️</div>
                                        <div className="flex-1">
                                            <h4 className="text-sm font-semibold text-yellow-400 light:text-yellow-700 mb-1">
                                                {providerName === 'ollama' && searchError.message.toLowerCase().includes('connect')
                                                    ? 'Local Ollama Not Running'
                                                    : searchError.message.toLowerCase().includes('connect')
                                                        ? `Cannot Connect to ${providerDisplayName}`
                                                        : 'Discovery Error'}
                                            </h4>
                                            {providerName === 'ollama' && searchError.message.toLowerCase().includes('connect') ? (
                                                <>
                                                    <p className="text-sm text-yellow-300/90 light:text-yellow-700/90 mb-3">
                                                        Your local Ollama instance is not running. You can still browse and add cloud models below.
                                                    </p>
                                                    <div className="mt-3 pt-3 border-t border-yellow-500/30">
                                                        <p className="text-sm text-yellow-200/90 light:text-yellow-700/90 mb-2 font-medium">Options:</p>
                                                        <div className="space-y-2">
                                                            <div className="text-sm">
                                                                <span className="text-yellow-200/90 light:text-yellow-700/90 font-medium">Option 1:</span>
                                                                <span className="text-yellow-200/70 light:text-yellow-700/70 ml-1">Use cloud models directly</span>
                                                                <div className="ml-4 mt-1 text-yellow-200/70 light:text-yellow-700/70">
                                                                    • Change Base URL to <code className="bg-black/40 text-yellow-100 px-1.5 py-0.5 rounded font-mono text-xs">https://ollama.com</code>
                                                                </div>
                                                                <div className="ml-4 text-yellow-200/70 light:text-yellow-700/70">
                                                                    • Add your Ollama API key
                                                                </div>
                                                            </div>
                                                            <div className="text-sm">
                                                                <span className="text-yellow-200/90 light:text-yellow-700/90 font-medium">Option 2:</span>
                                                                <span className="text-yellow-200/70 light:text-yellow-700/70 ml-1">Run cloud models locally</span>
                                                                <div className="ml-4 mt-1 text-yellow-200/70 light:text-yellow-700/70">
                                                                    • Pull any cloud model: <code className="bg-black/40 text-yellow-100 px-1.5 py-0.5 rounded font-mono text-xs">ollama pull model-name</code>
                                                                </div>
                                                                <div className="ml-4 text-yellow-200/70 light:text-yellow-700/70">
                                                                    • Start Ollama: <code className="bg-black/40 text-yellow-100 px-1.5 py-0.5 rounded font-mono text-xs">ollama serve</code>
                                                                </div>
                                                            </div>
                                                        </div>
                                                    </div>
                                                </>
                                            ) : (
                                                <>
                                                    <p className="text-sm text-yellow-300/90 light:text-yellow-700/90">{searchError.message}</p>
                                                    {searchError.message.toLowerCase().includes('connect') && (
                                                        <div className="mt-3 pt-3 border-t border-yellow-500/30">
                                                            <p className="text-sm text-yellow-200/90 light:text-yellow-700/90 mb-2">Possible solutions:</p>
                                                            <div className="text-sm">
                                                                <span className="text-yellow-200/70 light:text-yellow-700/70">• Check your network connection</span>
                                                            </div>
                                                            <div className="text-sm mt-1">
                                                                <span className="text-yellow-200/70 light:text-yellow-700/70">• Verify the provider service is running</span>
                                                            </div>
                                                            {customBaseUrl && (
                                                                <div className="text-sm mt-1">
                                                                    <span className="text-yellow-200/70 light:text-yellow-700/70">• Verify custom URL is correct:</span>
                                                                    <code className="bg-black/40 text-yellow-100 px-2 py-1 rounded font-mono text-xs ml-2">
                                                                        {customBaseUrl}
                                                                    </code>
                                                                </div>
                                                            )}
                                                        </div>
                                                    )}
                                                </>
                                            )}
                                        </div>
                                    </div>
                                </div>
                            )}

                            {!isSearching && !searchError && searchResults?.models && searchResults.models.length === 0 && (
                                <div className="text-center py-8 text-muted-foreground">
                                    No models found. Try a different search query.
                                </div>
                            )}

                            <div className="space-y-2 overflow-y-auto max-h-96 scrollbar-macos">
                                {searchResults?.models?.map((model) => {
                                    const isAdded = existingModels.has(model.name)
                                    const modelBadge = model.metadata?.badge || ''
                                    const isCloudAvailable = model.metadata?.source === 'cloud-available'

                                    return (
                                        <div
                                            key={model.id}
                                            className="flex items-center justify-between p-3 rounded-lg border border-brand-main-600 transition-colors"
                                        >
                                            <div className="flex-1 min-w-0 flex items-center gap-3">
                                                <div className="flex-1 min-w-0">
                                                    <div className="flex items-center gap-2 mb-1 max-w-full">
                                                        <h4 className="text-sm font-medium truncate ellipses">{model.displayName}</h4>
                                                        {modelBadge && (
                                                            <Badge
                                                                variant={isCloudAvailable ? "default" : "secondary"}
                                                                className="text-xs shrink-0"
                                                            >
                                                                {modelBadge}
                                                            </Badge>
                                                        )}
                                                        {model.capabilities && model.capabilities.length > 0 && (
                                                            <div className="flex gap-1">
                                                                {model.capabilities.slice(0, 2).map((cap) => (
                                                                    <Badge key={cap} variant="outline" className="text-xs cursor-pointer">
                                                                        {cap}
                                                                    </Badge>
                                                                ))}
                                                            </div>
                                                        )}
                                                    </div>
                                                    <div className="flex items-center gap-2 text-xs text-muted-foreground">
                                                        <span className="truncate">{model.name}</span>
                                                        {model.metadata?.parameter_size && (
                                                            <>
                                                                <span>•</span>
                                                                <span>{model.metadata.parameter_size}</span>
                                                            </>
                                                        )}
                                                    </div>
                                                    {isCloudAvailable && (
                                                        <p className="text-xs text-blue-400 light:text-blue-600 mt-1">
                                                            Run <code className="bg-brand-main-700 px-1 rounded">ollama pull {model.name}</code> to use locally
                                                        </p>
                                                    )}
                                                </div>
                                            </div>
                                            <Button
                                                size="sm"
                                                variant={isAdded ? "secondary" : "default"}
                                                disabled={isAdded || addModelMutation.isPending}
                                                onClick={() => handleAddDiscoveredModel(model)}
                                                className="ml-3"
                                            >
                                                {isAdded ? 'Added' : <><Plus className="h-4 w-4 mr-1" /> Add</>}
                                            </Button>
                                        </div>
                                    )
                                })}
                            </div>
                        </TabsContent>
                    )}

                    <TabsContent value="manual" className="mt-4 space-y-4">
                        <div className="space-y-4">
                            <div className="space-y-2">
                                <Label htmlFor="model-name">Model Name *</Label>
                                <Input
                                    id="model-name"
                                    placeholder="e.g., llama2:13b"
                                    value={manualModel.name}
                                    onChange={(e) => setManualModel({ ...manualModel, name: e.target.value })}
                                />
                                <p className="text-xs text-muted-foreground">
                                    The exact model identifier as used in API calls
                                </p>
                            </div>

                            <div className="space-y-2">
                                <Label htmlFor="display-name">Display Name *</Label>
                                <Input
                                    id="display-name"
                                    placeholder="e.g., Llama 2 13B"
                                    value={manualModel.displayName}
                                    onChange={(e) => setManualModel({ ...manualModel, displayName: e.target.value })}
                                />
                                <p className="text-xs text-muted-foreground">
                                    A human-readable name for the model
                                </p>
                            </div>

                            <div className="flex justify-end gap-2 pt-4">
                                <Button variant="outline" onClick={() => onOpenChange(false)}>
                                    Cancel
                                </Button>
                                <Button
                                    onClick={handleAddManualModel}
                                    disabled={!manualModel.name || !manualModel.displayName || addModelMutation.isPending}
                                >
                                    {addModelMutation.isPending && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                                    Add Model
                                </Button>
                            </div>
                        </div>
                    </TabsContent>
                </Tabs>
            </DialogContent>
        </Dialog>
    )
}

