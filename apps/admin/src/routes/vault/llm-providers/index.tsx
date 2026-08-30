import { createFileRoute } from '@tanstack/react-router'
import { useState } from 'react'
import { useConfiguredProviders, useToggleProvider, useDeleteProvider, useSyncStatus, useReloadConfig, providerKeys } from '@/hooks/vault/use-providers'
import { ConfigureProviderSheet, ProvidersTable } from '@/components/providers'
import { CatalogUpdateBanner } from '@/components/catalog'
import { Iconify } from '@everstack/ui/icons'
import { Button, Loader } from '@everstack/ui/components'
import { ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import { configureProvider, getProvider } from '@/server/providers'
import { useQueryClient } from '@tanstack/react-query'
import { isCloudManaged } from '@/lib/cloud-mode'

const { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } = ui

export const Route = createFileRoute('/vault/llm-providers/')({
    component: LLMProvidersPage,
})

function LLMProvidersPage() {
    const { data, isLoading, error } = useConfiguredProviders()
    const [cloudManaged] = useState(isCloudManaged)
    const providers = data?.providers || []
    const [configureDialogOpen, setConfigureDialogOpen] = useState(false)
    const [selectedProviderName, setSelectedProviderName] = useState<string | null>(null)
    const [deleteConfirmProvider, setDeleteConfirmProvider] = useState<string | null>(null)
    const deleteProviderMutation = useDeleteProvider()
    const toggleProviderMutation = useToggleProvider()
    const queryClient = useQueryClient()

    const handleConfigure = (providerName: string) => {
        setSelectedProviderName(providerName)
        setConfigureDialogOpen(true)
    }

    const handleToggle = async (providerName: string, isActive: boolean) => {
        const provider = providers.find(p => p.catalog?.name === providerName)
        if (!provider) return

        try {
            await toggleProviderMutation.mutateAsync({ providerName, isActive })
            toast.success(`${provider.catalog?.displayName || providerName} ${isActive ? 'enabled' : 'disabled'}`)
        } catch (error) {
            toast.error(`Failed to toggle provider: ${error instanceof Error ? error.message : 'Unknown error'}`)
        }
    }

    const handleToggleDefault = async (providerName: string, setAsDefault: boolean) => {
        try {
            // Get current provider config
            const providerResponse = await getProvider(providerName)
            const provider = providerResponse.provider
            const config = provider?.configuration

            if (!config) {
                toast.error('Provider not configured')
                return
            }

            // Prevent setting inactive provider as default
            if (setAsDefault && !provider.isActive) {
                toast.error('Cannot set inactive provider as default. Please enable the provider first.')
                return
            }

            // If trying to remove default and this is the only provider, prevent it
            if (!setAsDefault) {
                const configuredProviders = providers.filter(p => p.isConfigured)
                if (configuredProviders.length === 1) {
                    toast.error('Cannot remove default status from the only configured provider')
                    return
                }
            }

            // Update custom settings
            const customSettings = { ...(config.customSettings || {}) }
            if (setAsDefault) {
                customSettings.default = 'true'
            } else {
                delete customSettings.default
            }

            // Update provider configuration
            await configureProvider({
                providerName,
                apiKey: undefined, // Keep existing
                enabledModels: config.enabledModels || [],
                customBaseUrl: config.customBaseUrl || undefined,
                customSettings,
            })

            // Refresh providers list
            queryClient.invalidateQueries({ queryKey: providerKeys.all })

            toast.success(setAsDefault
                ? `${providerName} set as default provider`
                : `${providerName} removed as default provider`
            )
        } catch (error) {
            toast.error(`Failed to update default provider: ${error instanceof Error ? error.message : 'Unknown error'}`)
        }
    }

    const handleDelete = async (providerName: string) => {
        const provider = providers.find(p => p.catalog?.name === providerName)

        if (!provider) return

        try {
            await deleteProviderMutation.mutateAsync(providerName)
            toast.success(`${provider.catalog?.displayName || providerName} configuration deleted`)
            setDeleteConfirmProvider(null)
        } catch (error) {
            toast.error(`Failed to delete: ${error instanceof Error ? error.message : 'Unknown error'}`)
        }
    }

    // Separate providers into configured and unconfigured, sorted alphabetically
    const configuredProviders = providers
        .filter(p => p.isConfigured)
        .sort((a, b) => (a.catalog?.displayName || '').localeCompare(b.catalog?.displayName || ''))
    const unconfiguredProviders = providers
        .filter(p => !p.isConfigured)
        .sort((a, b) => (a.catalog?.displayName || '').localeCompare(b.catalog?.displayName || ''))

    return (
        <div className='flex flex-col w-full py-4'>
            {/* Loading State */}
            {isLoading && (
                <div className='flex-1 flex items-center justify-center'>
                    <Loader loaderText='Loading providers...' />
                </div>
            )}

            {/* Error State */}
            {error && (
                <div className='flex-1 flex items-center justify-center text-red-400 light:text-red-600'>
                    Error loading providers: {error.message}
                </div>
            )}

            {/* Content */}
            {!isLoading && !error && (
                <div className='space-y-6'>
                    <div className='space-y-2'>
                        <CatalogUpdateBanner />
                        {!cloudManaged && <SelfHostedConfigSyncNotice />}
                    </div>

                    {/* Configured Providers Section */}
                    {configuredProviders.length > 0 && (
                        <div className='space-y-1'>
                            <div className='flex items-center justify-between'>
                                <h2 className='text-xl px-2.5 font-semibold text-white light:text-brand-main-50'>
                                    Configured Providers
                                </h2>
                                <span className='text-sm px-2.5 text-white/60 light:text-black/60'>
                                    {configuredProviders.length} provider{configuredProviders.length !== 1 ? 's' : ''}
                                </span>
                            </div>
                            <ProvidersTable
                                providers={configuredProviders}
                                onConfigure={handleConfigure}
                                onToggle={handleToggle}
                                onDelete={(name: string) => setDeleteConfirmProvider(name)}
                                onToggleDefault={handleToggleDefault}
                                isConfiguredTable={true}
                            />
                        </div>
                    )}

                    {/* Available Providers Section */}
                    {unconfiguredProviders.length > 0 && (
                        <div className='space-y-1'>
                            <div className='flex items-center justify-between'>
                                <h2 className='text-xl px-2.5 font-semibold text-white light:text-brand-main-50'>
                                    Available Providers
                                </h2>
                                <span className='text-sm px-2.5 text-white/60 light:text-black/60'>
                                    {unconfiguredProviders.length} provider{unconfiguredProviders.length !== 1 ? 's' : ''} available
                                </span>
                            </div>
                            <ProvidersTable
                                providers={unconfiguredProviders}
                                onConfigure={handleConfigure}
                                onToggle={handleToggle}
                                onDelete={(name: string) => setDeleteConfirmProvider(name)}
                                onToggleDefault={handleToggleDefault}
                                isConfiguredTable={false}
                            />
                        </div>
                    )}

                    {/* Empty State */}
                    {providers.length === 0 && (
                        <div className='flex-1 flex flex-col h-full items-center justify-center text-white/70 light:text-black/70 gap-4 py-20'>
                            <div className='text-center flex flex-col justify-center items-center space-y-2'>
                                <span className='bg-brand-secondary-200 rounded-md p-2 inline-block mb-4'>
                                    <Iconify.Icon icon="hugeicons:ai-cloud-01" className='size-10 text-brand-secondary-700' />
                                </span>
                                <h3 className='text-lg font-medium text-white light:text-brand-main-50'>No providers available</h3>
                                <p className='text-sm w-2/3 mb-4 text-center text-white/60 light:text-black/60'>
                                    No LLM providers found in the catalog
                                </p>
                            </div>
                        </div>
                    )}
                </div>
            )}

            {/* Configure Provider Sheet */}
            <ConfigureProviderSheet
                open={configureDialogOpen}
                onOpenChange={(open) => {
                    setConfigureDialogOpen(open)
                    if (!open) setSelectedProviderName(null)
                }}
                providerName={selectedProviderName}
            />

            {/* Delete Confirmation Dialog */}
            <Dialog open={!!deleteConfirmProvider} onOpenChange={(open) => !open && setDeleteConfirmProvider(null)}>
                <DialogContent className="bg-brand-main-800 border-brand-main-500">
                    <DialogHeader className='flex flex-col gap-2 space-y-4'>
                        <DialogTitle className="text-white light:text-brand-main-50 text-left">Delete Provider Configuration</DialogTitle>
                        <DialogDescription className="text-white/60 light:text-black/60 text-left">
                            Are you sure you want to delete the configuration for{' '}
                            <span className="font-semibold text-white light:text-brand-main-50">
                                {providers.find(p => p.catalog?.name === deleteConfirmProvider)?.catalog?.displayName}
                            </span>
                            ? This will remove the API key and all model configurations.
                        </DialogDescription>
                    </DialogHeader>
                    <DialogFooter>
                        <Button
                            variant="ghost"
                            onClick={() => setDeleteConfirmProvider(null)}
                            className="text-white light:text-brand-main-50 hover:bg-brand-main-700"
                        >
                            Cancel
                        </Button>
                        <Button
                            variant="destructive"
                            onClick={() => deleteConfirmProvider && handleDelete(deleteConfirmProvider)}
                            disabled={deleteProviderMutation.isPending}
                        >
                            {deleteProviderMutation.isPending ? (
                                <>
                                    <Iconify.Icon icon="heroicons:arrow-path" className="size-4 animate-spin" />
                                    Deleting...
                                </>
                            ) : (
                                <>
                                    <Iconify.Icon icon="heroicons:trash" className="size-4" />
                                    Delete
                                </>
                            )}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    )
}

function SelfHostedConfigSyncNotice() {
    const { data: syncStatus } = useSyncStatus()
    const reloadMutation = useReloadConfig()

    if (!syncStatus || syncStatus.inSync) {
        return null
    }

    return (
        <div className='flex items-center justify-between gap-3 border border-amber-500/25 bg-amber-500/10 px-3 py-2 text-xs text-amber-100 light:text-amber-900'>
            <div className='flex min-w-0 items-center gap-2.5'>
                <span className='flex size-6 shrink-0 items-center justify-center rounded-sm bg-amber-500/10 text-amber-200 light:text-amber-700'>
                    <Iconify.Icon icon='mdi:file-refresh-outline' className='h-4 w-4' />
                </span>
                <div className='min-w-0'>
                    <p className='font-medium text-amber-100 light:text-amber-900'>Provider config file changed</p>
                    <p className='mt-0.5 truncate text-amber-100/65 light:text-amber-900/65'>
                        Reload to apply the latest YAML provider settings to the database.
                    </p>
                </div>
            </div>
            <button
                onClick={async () => {
                    try {
                        await reloadMutation.mutateAsync()
                        toast.success('Config reloaded successfully')
                    } catch (error) {
                        toast.error(`Reload failed: ${error instanceof Error ? error.message : 'Unknown error'}`)
                    }
                }}
                disabled={reloadMutation.isPending}
                className='shrink-0 rounded-xs border border-amber-400/25 px-2 py-1 text-amber-100 light:text-amber-900 hover:bg-amber-400/10 disabled:cursor-not-allowed disabled:opacity-50'
            >
                {reloadMutation.isPending ? 'Reloading...' : 'Reload config'}
            </button>
        </div>
    )
}
