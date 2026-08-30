import { ui } from '@everstack/ui'
import { Iconify } from '@everstack/ui/icons'
import type { ProviderStatus } from '@/server/providers'
import { cn } from '@everstack/utils/functions/cn'
import { ProviderDisplay } from './provider-icon'
import { NewModelsIndicator } from '@/components/catalog'
import { Button } from '@everstack/ui/components'
import { useProviderAPIKeys } from '@/hooks/vault'

const { Card, CardContent, CardHeader, CardTitle, Badge } = ui

interface ProviderCardProps {
    provider: ProviderStatus
    onConfigure: (providerName: string) => void
    onToggle: (providerName: string, isActive: boolean) => void
    onDelete: (providerName: string) => void
    onToggleDefault: (providerName: string, setAsDefault: boolean) => void
    useImage?: boolean // Optional prop to enable/disable image usage
}

export function ProviderCard({ provider, onConfigure, onToggle, onDelete, onToggleDefault, useImage = true }: ProviderCardProps) {
    const isConfigured = provider.isConfigured
    const isActive = provider.isActive
    const catalog = provider.catalog
    const configuration = provider.configuration
    const isDefault = configuration?.customSettings?.default === 'true'
    const { data: apiKeysData } = useProviderAPIKeys(configuration?.id || '')
    const apiKeys = apiKeysData?.keys || []

    return (
        <Card className={cn(
            'bg-brand-main-800 border-brand-main-500 hover:border-brand-main-600 transition-colors',
            // isActive && 'ring-1 ring-brand-secondary-500/50',
            isDefault && 'ring-2 ring-brand-secondary-500/50'
        )}>
            <CardHeader className="flex flex-row items-start justify-between space-y-0 pb-2">
                <div className="flex items-center gap-3">
                    <div className={cn(
                        'p-2 rounded-md',
                        isActive ? 'bg-brand-secondary-500/10' : 'bg-brand-main-700'
                    )}>
                        <ProviderDisplay
                            providerName={catalog?.name || ''}
                            isActive={isActive}
                            useImage={useImage}
                        />
                    </div>
                    <div className="flex flex-col">
                        <CardTitle className="text-base text-white light:text-brand-main-50">
                            {catalog?.displayName}
                        </CardTitle>
                        <p className="text-xs text-white/50 light:text-black/50">
                            {catalog?.name}
                        </p>
                    </div>
                </div>
                <div className="flex items-center gap-2">
                    <NewModelsIndicator providerName={catalog?.name} />
                    {apiKeys.length > 0 ? (
                        <Badge className="text-xs rounded-sm px-1.5 py-0.5 bg-blue-600/20 text-blue-400 light:text-blue-600 border border-blue-600/30">
                            {apiKeys.length} API Keys
                        </Badge>
                    ) : isConfigured && (
                        <Badge variant="outline" className="text-xs rounded-sm px-1.5 py-0.5 bg-orange-600/20 text-orange-400 light:text-orange-600 border border-orange-600/30">
                            No API Keys
                        </Badge>
                    )}
                    {isDefault && (
                        <Badge className="text-xs rounded-sm px-1.5 py-0.5 bg-blue-600/20 text-blue-400 light:text-blue-600 border border-blue-600/30">
                            Default
                        </Badge>
                    )}
                    {isConfigured && (
                        <Badge variant={isActive ? "active" : "secondary"} className="text-xs rounded-sm px-1 py-0.5 border-brand-main-400">
                            {isActive ? 'Active' : 'Inactive'}
                        </Badge>
                    )}
                    {!isConfigured && (
                        <Badge variant="outline" className="rounded-sm px-1 py-0.5 text-xs border-brand-main-400 text-white/60 light:text-black/60">
                            Not Configured
                        </Badge>
                    )}
                </div>
            </CardHeader>
            <CardContent>
                <div className="space-y-3">
                    {/* Models info */}
                    <div className="flex items-center justify-between text-sm">
                        <span className="text-white/60 light:text-black/60">Models</span>
                        <span className="text-white light:text-brand-main-50">
                            {isConfigured
                                ? `${provider.configuredModelsCount} enabled`
                                : `${provider.availableModelsCount} available`
                            }
                        </span>
                    </div>

                    {/* Base URL */}
                    {catalog?.baseUrl && (
                        <div className="flex items-center justify-between text-sm">
                            <span className="text-white/60 light:text-black/60">Base URL</span>
                            <span className="text-white/80 light:text-black/80 text-xs truncate max-w-[200px]">
                                {configuration?.customBaseUrl || catalog.baseUrl}
                            </span>
                        </div>
                    )}

                    {/* Actions */}
                    <div className="flex items-center gap-2 pt-2">
                        <Button
                            variant={isConfigured ? 'secondary' : 'default'}
                            onClick={() => onConfigure(catalog?.name || '')}
                            className="flex-1"
                        >
                            {isConfigured ? 'Configure' : 'Set Up'}
                        </Button>

                        {/* Enable/Disable toggle for configured providers */}
                        {isConfigured && (
                            <Button
                                variant="ghost"
                                onClick={() => onToggle(catalog?.name || '', !isActive)}
                                className={cn(
                                    "px-3 py-2 rounded-md text-sm font-medium transition-colors",
                                    isActive
                                        ? "bg-green-600/20 hover:bg-green-600/30 text-green-400 light:text-green-600"
                                        : "bg-gray-600/20 hover:bg-gray-600/30 text-gray-400 light:text-gray-600"
                                )}
                                title={isActive ? "Disable provider" : "Enable provider"}
                            >
                                <Iconify.Icon icon={isActive ? "heroicons:eye" : "heroicons:eye-slash"} className="size-4" />
                            </Button>
                        )}

                        {/* Default toggle button for configured providers */}
                        {isConfigured && (
                            <Button
                                variant="ghost"
                                onClick={() => onToggleDefault(catalog?.name || '', !isDefault)}
                                disabled={!isActive && !isDefault}
                                className={cn(
                                    "px-3 py-2 rounded-md text-sm font-medium transition-colors",
                                    isDefault
                                        ? "bg-blue-600/20 hover:bg-blue-600/30 text-blue-400 light:text-blue-600"
                                        : "bg-brand-main-700 hover:bg-brand-main-600 text-white/60 light:text-black/60",
                                    !isActive && !isDefault && "opacity-50 cursor-not-allowed"
                                )}
                                title={
                                    !isActive && !isDefault
                                        ? "Enable provider first to set as default"
                                        : isDefault
                                            ? "Unset as default"
                                            : "Set as default"
                                }
                            >
                                <Iconify.Icon icon={isDefault ? "heroicons:star-solid" : "heroicons:star"} className="size-4" />
                            </Button>
                        )}

                        {isConfigured && (
                            <Button
                                variant="destructive"
                                onClick={() => onDelete(catalog?.name || '')}
                                className="px-3 py-2 rounded-md text-sm font-medium bg-red-600/10 hover:bg-red-600/20 text-red-400 light:text-red-600 transition-colors"
                            >
                                <Iconify.Icon icon="heroicons:trash" className="size-4" />
                            </Button>
                        )}
                    </div>
                </div>
            </CardContent>
        </Card>
    )
}
