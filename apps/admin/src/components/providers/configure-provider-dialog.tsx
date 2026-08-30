import { useState, useEffect, useCallback } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ui, useCopyToClipboard } from '@everstack/ui'
import { Iconify } from '@everstack/ui/icons'
import { useConfigureProvider, useProvider, providerKeys } from '@/hooks/vault/use-providers'
import type { ConfigureProviderParams } from '@/server/providers'
import { toast } from '@everstack/ui/components'
import { cn } from '@everstack/utils/functions/cn'
import { ModelDiscoveryDialog } from './model-discovery-dialog'
import { useCustomModels } from '@/hooks/use-model-discovery'
import { APIKeyRow } from './api-key-row'
import {
    useProviderAPIKeys,
    useAddProviderAPIKey,
    useUpdateAPIKeyWeight,
    useToggleAPIKey,
    useDeleteProviderAPIKey,
} from '@/hooks/vault/use-provider-api-keys'

const { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter, Button, Input, Label } = ui

interface ConfigureProviderDialogProps {
    open: boolean
    onOpenChange: (open: boolean) => void
    providerName: string | null
}

export function ConfigureProviderDialog({ open, onOpenChange, providerName }: ConfigureProviderDialogProps) {
    const queryClient = useQueryClient()
    const [customBaseUrl, setCustomBaseUrl] = useState('')
    const [maxTokens, setMaxTokens] = useState(4096)
    const [isDefaultProvider, setIsDefaultProvider] = useState(false)
    const [defaultModel, setDefaultModel] = useState<string>('')
    const [selectedModels, setSelectedModels] = useState<Set<string>>(new Set())
    const [discoveryDialogOpen, setDiscoveryDialogOpen] = useState(false)
    const [showAddKeyForm, setShowAddKeyForm] = useState(false)
    const [newKeyName, setNewKeyName] = useState('')
    const [newKeyValue, setNewKeyValue] = useState('')
    const [newKeyWeight, setNewKeyWeight] = useState(1)
    const [copy] = useCopyToClipboard()
    const { data: providerData, isLoading: isLoadingProvider } = useProvider(providerName ?? undefined)
    const configureProviderMutation = useConfigureProvider()
    const { data: customModelsData } = useCustomModels(providerName || undefined)

    const providerStatus = providerData?.provider
    const catalog = providerStatus?.catalog
    const configuration = providerStatus?.configuration
    const providerConfigId = configuration?.id

    // API Key Management Hooks
    const { data: apiKeysData } = useProviderAPIKeys(providerConfigId || '')
    const addKeyMutation = useAddProviderAPIKey()
    const updateWeightMutation = useUpdateAPIKeyWeight()
    const toggleKeyMutation = useToggleAPIKey()
    const deleteKeyMutation = useDeleteProviderAPIKey()

    const apiKeys = apiKeysData?.keys || []

    // Check if this is a meta-provider that supports model discovery
    const isMetaProvider = catalog?.providerType === 'meta'
    const supportsDiscovery = catalog?.supportsModelDiscovery === true
    const customModels = customModelsData?.models || []

    const handleCopy = useCallback((modelName: string) => {
        copy(modelName)
        toast.success(`Copied "${modelName}" to clipboard`)
    }, [copy])


    // Reset form when provider changes or dialog opens
    useEffect(() => {
        if (open && providerStatus) {
            setCustomBaseUrl(configuration?.customBaseUrl || catalog?.baseUrl || '')
            setMaxTokens(parseInt(configuration?.customSettings?.max_tokens || '4096'))
            setIsDefaultProvider(configuration?.customSettings?.default === 'true')
            setDefaultModel(configuration?.customSettings?.default_alias || '')
            setSelectedModels(new Set(configuration?.enabledModels || []))
            setShowAddKeyForm(false)
            setNewKeyName('')
            setNewKeyValue('')
            setNewKeyWeight(1)
        }
    }, [open, providerStatus, configuration, catalog])

    // API Key Management Handlers
    const handleAddKey = async () => {
        if (!newKeyName.trim() || !newKeyValue.trim()) {
            toast.error('Please fill in all API key fields')
            return
        }

        try {
            let configId = providerConfigId
            const isInitialSetup = !configId

            // If provider is not configured yet, configure it first with the API key
            if (isInitialSetup) {

                // Use selected models if any, otherwise use just the first model as minimal default
                // The user can later select more models and click "Save Configuration"
                const modelsToUse = selectedModels.size > 0
                    ? Array.from(selectedModels)
                    : (catalog?.models?.[0]?.name ? [catalog.models[0].name] : [])

                // Configure the provider with the API key
                // This creates the provider config with the old single-key system
                // Build custom settings without undefined values
                const customSettings: Record<string, string> = {
                    max_tokens: maxTokens.toString(),
                }
                if (isDefaultProvider) {
                    customSettings.default = 'true'
                }
                if (defaultModel) {
                    customSettings.default_alias = defaultModel
                }

                const configureResult = await configureProviderMutation.mutateAsync({
                    providerName: providerName!,
                    apiKey: newKeyValue.trim(), // Provide the API key for initial setup
                    enabledModels: modelsToUse,
                    customBaseUrl: customBaseUrl.trim() || catalog?.baseUrl || undefined,
                    customSettings,
                })


                // Get the config ID from the mutation response
                configId = configureResult?.provider?.configuration?.id


                if (!configId) {
                    throw new Error('Failed to get provider configuration ID after setup')
                }
            }

            // Ensure we have a config ID before adding the key
            if (!configId) {
                throw new Error('Provider configuration ID is missing')
            }

            // Add the API key with the user's specified name to the multi-key system

            await addKeyMutation.mutateAsync({
                providerConfigId: configId,
                keyName: newKeyName.trim(),
                apiKey: newKeyValue.trim(),
                weight: newKeyWeight,
            })

            // Force invalidate all provider queries to update UI
            await queryClient.invalidateQueries({ queryKey: providerKeys.all })

            toast.success(`API key "${newKeyName}" added successfully`)
            setShowAddKeyForm(false)
            setNewKeyName('')
            setNewKeyValue('')
            setNewKeyWeight(1)
        } catch (error) {
            console.error('[AddKey] Error:', error)
            toast.error(`Failed to add API key: ${error instanceof Error ? error.message : 'Unknown error'}`)
        }
    }

    const handleUpdateWeight = async (keyId: string, weight: number) => {
        try {
            await updateWeightMutation.mutateAsync({ keyId, weight })
            toast.success('API key weight updated')
        } catch (error) {
            toast.error(`Failed to update weight: ${error instanceof Error ? error.message : 'Unknown error'}`)
        }
    }

    const handleToggleKey = async (keyId: string, isActive: boolean) => {
        try {
            await toggleKeyMutation.mutateAsync({ keyId, isActive })
            toast.success(`API key ${isActive ? 'activated' : 'deactivated'}`)
        } catch (error) {
            toast.error(`Failed to toggle API key: ${error instanceof Error ? error.message : 'Unknown error'}`)
        }
    }

    const handleDeleteKey = async (keyId: string) => {
        if (!confirm('Are you sure you want to delete this API key?')) return

        try {
            await deleteKeyMutation.mutateAsync({ keyId })
            toast.success('API key deleted successfully')
        } catch (error) {
            toast.error(`Failed to delete API key: ${error instanceof Error ? error.message : 'Unknown error'}`)
        }
    }

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault()

        if (!providerName) {
            return
        }

        // For unconfigured providers, require at least one API key to be added first
        if (!providerConfigId) {
            toast.error('Please add at least one API key to configure this provider')
            return
        }

        if (selectedModels.size === 0) {
            toast.error('Please select at least one model')
            return
        }

        if (!customBaseUrl.trim()) {
            toast.error('Base URL is required')
            return
        }

        // Build custom settings without undefined values
        const customSettings: Record<string, string> = {
            max_tokens: maxTokens.toString(),
        }
        if (isDefaultProvider) {
            customSettings.default = 'true'
        }
        if (defaultModel) {
            customSettings.default_alias = defaultModel
        }

        const params: ConfigureProviderParams = {
            providerName,
            apiKey: undefined, // API keys are managed separately via multi-key system
            enabledModels: Array.from(selectedModels),
            customBaseUrl: customBaseUrl.trim() || undefined,
            customSettings,
        }

        try {
            await configureProviderMutation.mutateAsync(params)
            toast.success(`${catalog?.displayName || providerName} settings updated successfully`)
            onOpenChange(false)
        } catch (error) {
            toast.error(`Failed to update provider: ${error instanceof Error ? error.message : 'Unknown error'}`)
        }
    }

    const toggleModel = (modelName: string) => {
        setSelectedModels(prev => {
            const newSet = new Set(prev)
            if (newSet.has(modelName)) {
                newSet.delete(modelName)
                // If we're removing the default model, clear it
                if (defaultModel === modelName) {
                    setDefaultModel('')
                }
            } else {
                newSet.add(modelName)
                // If this is the first model, auto-set it as default
                if (newSet.size === 1) {
                    setDefaultModel(modelName)
                }
            }
            return newSet
        })
    }

    const selectAllModels = () => {
        if (catalog?.models) {
            const allModels = new Set(catalog.models.map(m => m.name))
            setSelectedModels(allModels)
            // Set first model as default if no default is set
            if (!defaultModel && allModels.size > 0) {
                setDefaultModel(Array.from(allModels)[0])
            }
        }
    }

    const deselectAllModels = () => {
        setSelectedModels(new Set())
        setDefaultModel('')
    }

    return (
        <>
            <Dialog open={open} onOpenChange={onOpenChange}>
                <DialogContent className="bg-brand-main-900 border-brand-main-500 max-w-2xl max-h-[80vh] overflow-y-auto scrollbar-macos">
                    <DialogHeader>
                        <DialogTitle className="text-white light:text-brand-main-50">
                            Configure {catalog?.displayName || providerName}
                        </DialogTitle>
                        <DialogDescription className="text-white/60 light:text-black/60">
                            Set up your API credentials and select which models to enable for this provider.
                        </DialogDescription>
                    </DialogHeader>

                    {isLoadingProvider ? (
                        <div className="flex items-center justify-center py-8">
                            <div className="text-white/60 light:text-black/60">Loading provider details...</div>
                        </div>
                    ) : (
                        <form onSubmit={handleSubmit} className="space-y-6">
                            {/* Multi-API Key Management - Always show for consistency */}
                            <div className="space-y-3">
                                <div className="flex items-center justify-between">
                                    <Label className="text-white light:text-brand-main-50">API Keys</Label>
                                    <Button
                                        type="button"
                                        variant="ghost"
                                        size="sm"
                                        onClick={() => setShowAddKeyForm(!showAddKeyForm)}
                                        className="text-blue-400 light:text-blue-600 hover:text-blue-300 light:hover:text-blue-600 hover:bg-blue-500/10"
                                    >
                                        <Iconify.Icon icon={showAddKeyForm ? "heroicons:x-mark" : "heroicons:plus"} className="size-4 mr-1" />
                                        {showAddKeyForm ? 'Cancel' : 'Add Key'}
                                    </Button>
                                </div>

                                {/* Add New Key Form */}
                                {showAddKeyForm && (
                                    <div className="border border-brand-main-500 rounded-md p-4 space-y-3 bg-brand-main-800/50">
                                        <div className="space-y-2">
                                            <Label htmlFor="newKeyName" className="text-white light:text-brand-main-50 text-sm">Key Name</Label>
                                            <Input
                                                id="newKeyName"
                                                value={newKeyName}
                                                onChange={(e) => setNewKeyName(e.target.value)}
                                                placeholder="e.g., Production Key, Backup Key"
                                                className="bg-brand-main-700 border-brand-main-500 text-white light:text-brand-main-50 placeholder:text-white/40 light:placeholder:text-black/40"
                                                required
                                            />
                                        </div>
                                        <div className="space-y-2">
                                            <Label htmlFor="newKeyValue" className="text-white light:text-brand-main-50 text-sm">API Key</Label>
                                            <Input
                                                id="newKeyValue"
                                                type="password"
                                                value={newKeyValue}
                                                onChange={(e) => setNewKeyValue(e.target.value)}
                                                placeholder="Enter API key"
                                                className="bg-brand-main-700 border-brand-main-500 text-white light:text-brand-main-50 placeholder:text-white/40 light:placeholder:text-black/40"
                                                required
                                            />
                                        </div>
                                        <div className="space-y-2">
                                            <Label htmlFor="newKeyWeight" className="text-white light:text-brand-main-50 text-sm">Weight (for load balancing)</Label>
                                            <Input
                                                id="newKeyWeight"
                                                type="number"
                                                min="1"
                                                value={newKeyWeight}
                                                onChange={(e) => setNewKeyWeight(parseInt(e.target.value) || 1)}
                                                className="bg-brand-main-700 border-brand-main-500 text-white light:text-brand-main-50"
                                            />
                                            <p className="text-xs text-white/50 light:text-black/50">Higher weight = more traffic</p>
                                        </div>
                                        <Button
                                            type="button"
                                            onClick={handleAddKey}
                                            size="sm"
                                            disabled={addKeyMutation.isPending}
                                            className="w-full"
                                        >
                                            {addKeyMutation.isPending ? (
                                                <>
                                                    <Iconify.Icon icon="heroicons:arrow-path" className="size-4 animate-spin mr-2" />
                                                    Adding...
                                                </>
                                            ) : (
                                                <>
                                                    <Iconify.Icon icon="heroicons:plus" className="size-4 mr-2" />
                                                    Add API Key
                                                </>
                                            )}
                                        </Button>
                                    </div>
                                )}

                                {/* Existing API Keys List */}
                                <div className="space-y-2">
                                    {apiKeys.length > 0 ? (
                                        apiKeys.map((key) => (
                                            <APIKeyRow
                                                key={key.id}
                                                apiKey={key}
                                                onUpdateWeight={handleUpdateWeight}
                                                onToggle={handleToggleKey}
                                                onDelete={handleDeleteKey}
                                            />
                                        ))
                                    ) : (
                                        <div className="text-center text-white/50 light:text-black/50 py-4 border border-brand-main-500 rounded-md bg-brand-main-900/50">
                                            No API keys configured yet. Click "Add Key" to get started.
                                        </div>
                                    )}
                                </div>
                            </div>

                            {/* Base URL */}
                            <div className="space-y-2">
                                <Label htmlFor="baseUrl" className="text-white light:text-brand-main-50">
                                    Base URL <span className="text-red-400 light:text-red-600">*</span>
                                </Label>
                                <Input
                                    id="baseUrl"
                                    type="url"
                                    value={customBaseUrl}
                                    onChange={(e) => setCustomBaseUrl(e.target.value)}
                                    placeholder={catalog?.baseUrl || 'https://api.example.com/v1'}
                                    className="bg-brand-main-700 border-brand-main-500 text-white light:text-brand-main-50 placeholder:text-white/40 light:placeholder:text-black/40"
                                    required
                                />
                                <p className="text-xs text-white/50 light:text-black/50">
                                    Default: {catalog?.baseUrl}
                                </p>
                            </div>

                            {/* Max Tokens */}
                            <div className="space-y-2">
                                <Label htmlFor="maxTokens" className="text-white light:text-brand-main-50">
                                    Max Tokens <span className="text-red-400 light:text-red-600">*</span>
                                </Label>
                                <Input
                                    id="maxTokens"
                                    type="number"
                                    min="1"
                                    max="1000000"
                                    value={maxTokens}
                                    onChange={(e) => setMaxTokens(parseInt(e.target.value) || 4096)}
                                    className="bg-brand-main-700 border-brand-main-500 text-white light:text-brand-main-50"
                                    required
                                />
                                <p className="text-xs text-white/50 light:text-black/50">
                                    Maximum tokens for responses (default: 4096)
                                </p>
                            </div>

                            {/* Model Selection */}
                            <div className="space-y-3">
                                <div className="flex items-center justify-between">
                                    <Label className="text-white light:text-brand-main-50">
                                        Enabled Models <span className="text-red-400 light:text-red-600">*</span>
                                    </Label>
                                    <div className="flex gap-2 items-center">
                                        <button
                                            type="button"
                                            onClick={selectAllModels}
                                            className="text-xs text-blue-400 light:text-blue-600 hover:text-blue-300 light:hover:text-blue-600 transition-colors"
                                        >
                                            Select All
                                        </button>
                                        <span className="text-white/30 light:text-black/30">|</span>
                                        <button
                                            type="button"
                                            onClick={deselectAllModels}
                                            className="text-xs text-blue-400 light:text-blue-600 hover:text-blue-300 light:hover:text-blue-600 transition-colors"
                                        >
                                            Deselect All
                                        </button>
                                        {isMetaProvider && (
                                            <>
                                                <span className="text-white/30 light:text-black/30">|</span>
                                                <button
                                                    type="button"
                                                    onClick={() => setDiscoveryDialogOpen(true)}
                                                    className="text-xs text-green-400 light:text-green-600 hover:text-green-300 light:hover:text-green-600 transition-colors flex items-center gap-1"
                                                >
                                                    <Iconify.Icon icon="heroicons:plus-circle" className="size-3" />
                                                    Discover Models
                                                </button>
                                            </>
                                        )}
                                    </div>
                                </div>

                                <div className="border border-brand-main-500 rounded-md p-3 space-y-2.5 max-h-60 overflow-y-auto bg-brand-main-900/50 scrollbar-macos">
                                    {/* Catalog Models */}
                                    {catalog?.models && catalog.models.length > 0 && catalog.models.map((model) => (
                                        <div key={model.name} className="group/model">
                                            <button
                                                type="button"
                                                onClick={() => {
                                                    toggleModel(model.name)
                                                }}
                                                className={cn(
                                                    'w-full flex items-start gap-3 p-3 rounded-sm transition-all text-left',
                                                    selectedModels.has(model.name)
                                                        ? 'bg-blue-600/20 border border-blue-500/50 ring-1 ring-blue-500/30'
                                                        : 'bg-brand-main-700 border border-brand-main-600 hover:bg-brand-main-600'
                                                )}
                                            >
                                                <div className="flex items-center justify-center mt-0.5">
                                                    {selectedModels.has(model.name) ? (
                                                        <Iconify.Icon icon="heroicons:check-circle-solid" className="size-5 text-blue-400 light:text-blue-600" />
                                                    ) : (
                                                        <div className="size-5 rounded-full border-2 border-brand-main-400" />
                                                    )}
                                                </div>
                                                <div className="flex items-start justify-between w-full">
                                                    <div className="text-white light:text-brand-main-50 font-medium text-sm flex-1">
                                                        <div className="flex items-center gap-1.5">
                                                            {model.displayName}
                                                            <button
                                                                type="button"
                                                                onClick={(e) => {
                                                                    e.stopPropagation()
                                                                    handleCopy(model.name)
                                                                }}
                                                                className="invisible group-hover/model:visible p-1 hover:bg-white/20 light:hover:bg-black/20 rounded flex-shrink-0 transition-all"
                                                                title="Copy model name"
                                                            >
                                                                <Iconify.Icon icon="heroicons:clipboard-document" className="size-3.5 text-white/70 light:text-black/70 hover:text-white light:hover:text-brand-main-50" />
                                                            </button>
                                                        </div>
                                                        <p className="text-xs text-white/50 light:text-black/50 truncate mt-0.5">
                                                            {model.name}
                                                        </p>
                                                    </div>
                                                    {model.maxTokens && (
                                                        <p className="text-xs text-white/40 light:text-black/40 ml-2">
                                                            Max tokens: {model.maxTokens.toString()}
                                                        </p>
                                                    )}
                                                </div>
                                            </button>
                                        </div>
                                    ))}

                                    {/* Custom Models */}
                                    {customModels.length > 0 && customModels.map((model) => (
                                        <div key={model.modelName} className="group/model">
                                            <button
                                                type="button"
                                                onClick={() => toggleModel(model.modelName)}
                                                className={cn(
                                                    'w-full flex items-start gap-3 p-3 rounded-sm transition-all text-left',
                                                    selectedModels.has(model.modelName)
                                                        ? 'bg-green-600/20 border border-green-500/50 ring-1 ring-green-500/30'
                                                        : 'bg-brand-main-700 border border-brand-main-600 hover:bg-brand-main-600'
                                                )}
                                            >
                                                <div className="flex items-center justify-center mt-0.5">
                                                    {selectedModels.has(model.modelName) ? (
                                                        <Iconify.Icon icon="heroicons:check-circle-solid" className="size-5 text-green-400 light:text-green-600" />
                                                    ) : (
                                                        <div className="size-5 rounded-full border-2 border-brand-main-400" />
                                                    )}
                                                </div>
                                                <div className="flex-1 min-w-0">
                                                    <div className="text-white light:text-brand-main-50 font-medium text-sm flex items-center gap-1.5">
                                                        {model.displayName}
                                                        <button
                                                            type="button"
                                                            onClick={(e) => {
                                                                e.stopPropagation()
                                                                handleCopy(model.modelName)
                                                            }}
                                                            className="invisible group-hover/model:visible p-1 hover:bg-white/20 light:hover:bg-black/20 rounded flex-shrink-0 transition-all"
                                                            title="Copy model name"
                                                        >
                                                            <Iconify.Icon icon="heroicons:clipboard-document" className="size-3.5 text-white/70 light:text-black/70 hover:text-white light:hover:text-brand-main-50" />
                                                        </button>
                                                        <span className="text-[10px] px-1.5 py-0.5 rounded bg-green-500/20 text-green-400 light:text-green-600">Custom</span>
                                                    </div>
                                                    <p className="text-xs text-white/50 light:text-black/50 truncate mt-0.5">
                                                        {model.modelName}
                                                    </p>
                                                </div>
                                            </button>
                                        </div>
                                    ))}

                                    {/* No Models Available */}
                                    {(!catalog?.models || catalog.models.length === 0) && customModels.length === 0 && (
                                        <div className="text-center text-white/50 light:text-black/50 py-4">
                                            {isMetaProvider ? 'No models configured. Click "Discover Models" to add some.' : 'No models available for this provider'}
                                        </div>
                                    )}
                                </div>

                                <p className="text-xs text-white/50 light:text-black/50">
                                    Selected: {selectedModels.size} of {catalog?.models?.length || 0} models
                                </p>
                            </div>

                            {/* Provider Settings */}
                            <div className="space-y-4">
                                {/* Set as Default Provider */}
                                <div className="space-y-2">
                                    <div className="flex items-center gap-3">
                                        <input
                                            id="isDefaultProvider"
                                            type="checkbox"
                                            checked={isDefaultProvider}
                                            onChange={(e) => setIsDefaultProvider(e.target.checked)}
                                            className="w-4 h-4 text-blue-600 bg-brand-main-700 border-brand-main-500 rounded focus:ring-blue-500 focus:ring-2"
                                        />
                                        <Label htmlFor="isDefaultProvider" className="text-white light:text-brand-main-50 cursor-pointer">
                                            Set as default provider
                                        </Label>
                                    </div>
                                    <p className="text-xs text-white/50 light:text-black/50 ml-7">
                                        Default provider will be used when no specific provider is requested. Only one provider can be default at a time.
                                    </p>
                                </div>

                                {/* Default Model Selection */}
                                {selectedModels.size > 0 && (
                                    <div className="space-y-2">
                                        <Label htmlFor="defaultModel" className="text-white light:text-brand-main-50">
                                            Default Model (Optional)
                                        </Label>
                                        <select
                                            id="defaultModel"
                                            value={defaultModel}
                                            onChange={(e) => setDefaultModel(e.target.value)}
                                            className="w-full bg-brand-main-700 border border-brand-main-500 text-white light:text-brand-main-50 rounded-md px-3 py-2 focus:outline-none focus:ring-2 focus:ring-blue-500"
                                        >
                                            <option value="">No default model</option>
                                            {Array.from(selectedModels).map((modelName) => (
                                                <option key={modelName} value={modelName}>
                                                    {modelName}
                                                </option>
                                            ))}
                                        </select>
                                        <p className="text-xs text-white/50 light:text-black/50">
                                            Default model for this provider when no specific model is requested
                                        </p>
                                    </div>
                                )}
                            </div>

                            <DialogFooter>
                                <Button
                                    type="button"
                                    variant="ghost"
                                    onClick={() => onOpenChange(false)}
                                    className="text-white light:text-brand-main-50 hover:bg-brand-main-700"
                                >
                                    Cancel
                                </Button>
                                <Button
                                    type="submit"
                                    disabled={configureProviderMutation.isPending || !providerConfigId || selectedModels.size === 0}
                                >
                                    {configureProviderMutation.isPending ? (
                                        <>
                                            <Iconify.Icon icon="heroicons:arrow-path" className="size-4 animate-spin mr-2" />
                                            Saving...
                                        </>
                                    ) : (
                                        <>
                                            <Iconify.Icon icon="heroicons:check" className="size-4 mr-2" />
                                            {providerConfigId ? 'Save Configuration' : 'Add API Key First'}
                                        </>
                                    )}
                                </Button>
                            </DialogFooter>
                        </form>
                    )}
                </DialogContent>
            </Dialog>

            {/* Model Discovery Dialog for Meta-Providers */}
            {isMetaProvider && providerName && (
                <ModelDiscoveryDialog
                    open={discoveryDialogOpen}
                    onOpenChange={setDiscoveryDialogOpen}
                    providerName={providerName}
                    providerDisplayName={catalog?.displayName || providerName}
                    supportsDiscovery={supportsDiscovery}
                    customBaseUrl={customBaseUrl}
                />
            )}
        </>
    )
}

