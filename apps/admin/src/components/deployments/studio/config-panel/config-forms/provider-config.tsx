import { Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@everstack/ui/components'
import type { ProviderConfig } from '../../types'
import { useConfiguredProviders } from '@/hooks/vault/use-providers'
import { useMemo } from 'react'

interface Props { config: ProviderConfig; onChange: (config: ProviderConfig) => void }

export function ProviderConfigForm({ config, onChange }: Props) {
    const { data: configuredProvidersData } = useConfiguredProviders()
    const activeProviders = useMemo(() => {
        const providers = configuredProvidersData?.providers ?? []
        return providers.filter(p => p.isActive)
    }, [configuredProvidersData])

    const selectedProvider = useMemo(
        () => activeProviders.find(p => p.catalog?.name === config.providerType),
        [activeProviders, config.providerType],
    )

    const enabledModels = useMemo(() => {
        if (!selectedProvider) return []
        const enabledNames = new Set(selectedProvider.configuration?.enabledModels ?? [])
        const catalogModels = selectedProvider.catalog?.models ?? []
        // Return catalog models that are enabled, preserving displayName
        return catalogModels.filter(m => enabledNames.has(m.name))
    }, [selectedProvider])

    const handleProviderChange = (providerName: string) => {
        const provider = activeProviders.find(p => p.catalog?.name === providerName)
        const enabledNames = provider?.configuration?.enabledModels ?? []
        const catalogModels = provider?.catalog?.models ?? []
        // If current model isn't in the new provider's enabled list, reset to first enabled model
        const modelStillValid = enabledNames.includes(config.model)
        const firstEnabled = catalogModels.find(m => enabledNames.includes(m.name))
        onChange({
            ...config,
            providerType: providerName,
            model: modelStillValid ? config.model : (firstEnabled?.name ?? ''),
        })
    }

    return (
        <div className="space-y-4">
            <div className="space-y-1.5 w-full">
                <Label className="text-sm text-brand-main-200">Provider</Label>
                {activeProviders.length === 0 ? (
                    <p className="text-xs text-brand-main-500 py-1">
                        No active providers. Configure in Vault.
                    </p>
                ) : (
                    <Select value={config.providerType} onValueChange={handleProviderChange}>
                        <SelectTrigger className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 w-full">
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            {activeProviders.map((provider) => (
                                <SelectItem key={provider.catalog?.name} value={provider.catalog?.name || ''}>
                                    {provider.catalog?.displayName || provider.catalog?.name}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                )}
            </div>
            <div className="space-y-1.5 w-full">
                <Label className="text-sm text-brand-main-200">Model</Label>
                {enabledModels.length === 0 ? (
                    <p className="text-xs text-brand-main-500 py-1">
                        {selectedProvider
                            ? 'No models enabled for this provider. Enable models in Vault.'
                            : 'Select a provider first.'}
                    </p>
                ) : (
                    <Select value={config.model} onValueChange={(v) => onChange({ ...config, model: v })}>
                        <SelectTrigger className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50 w-full">
                            <SelectValue placeholder="Select a model" />
                        </SelectTrigger>
                        <SelectContent>
                            {enabledModels.map((model) => (
                                <SelectItem key={model.name} value={model.name}>
                                    {model.displayName || model.name}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                )}
            </div>
            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">Base URL (optional)</Label>
                <Input value={config.baseUrl} onChange={(e) => onChange({ ...config, baseUrl: e.target.value })} className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50" placeholder="https://api.openai.com" />
            </div>
            <div className="space-y-1.5">
                <Label className="text-sm text-brand-main-200">Max Tokens</Label>
                <Input type="number" value={config.maxTokens} onChange={(e) => onChange({ ...config, maxTokens: Number(e.target.value) })} className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50" />
            </div>
        </div>
    )
}
