import { createFileRoute } from '@tanstack/react-router'
import { useState } from 'react'
import { useApiKeys } from '@/hooks/vault/use-api-keys'
import { ApiKeysTable, CreateApiKeyDialog } from '@/components/keys'
import { Iconify } from '@everstack/ui/icons'
import { Button, Loader } from '@everstack/ui/components'

export const Route = createFileRoute('/vault/api-keys/')({
    component: Vault,
})

function Vault() {
    const { data: apiKeys = [], isLoading, error } = useApiKeys()
    const [createApiKeyDialogOpen, setCreateApiKeyDialogOpen] = useState(false)

    return (
        <div className='flex flex-col h-full w-full'>
            {/* Table */}
            <div className='min-h-0 h-full justify-center items-center overflow-hidden flex flex-col'>
                {isLoading ? (
                    <div className='flex-1 flex items-center justify-center text-white/70 light:text-black/70'>
                        <Loader loaderText='Loading API keys...' />
                    </div>
                ) : error ? (
                    <div className='flex-1 flex items-center justify-center text-red-400 light:text-red-600'>
                        Error loading API keys: {error.message}
                    </div>
                ) : apiKeys.length === 0 ? (
                    <div className='flex-1 flex flex-col h-full items-center justify-center pb-24'>
                        <div className="relative mb-6">
                            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                                <Iconify.Icon icon="heroicons:key" className="size-8 text-brand-secondary-400" />
                            </div>
                        </div>
                        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No API keys yet</h3>
                        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed mb-4">
                            Create API keys to authenticate requests to your agents and services.
                        </p>
                        <Button variant='default' onClick={() => setCreateApiKeyDialogOpen(true)}>
                            <div className='flex items-center gap-2'>
                                <Iconify.Icon icon="heroicons:plus" className='size-4' />
                                Create API Key
                            </div>
                        </Button>
                    </div>
                ) : (
                    <ApiKeysTable apiKeys={apiKeys} />
                )}
            </div>

            <CreateApiKeyDialog
                open={createApiKeyDialogOpen}
                onOpenChange={setCreateApiKeyDialogOpen}
            />
        </div>
    )
}
