import { useState } from 'react'
import { Loader, toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { useSSHKeys, useDeleteSSHKey } from '@/hooks/settings/use-ssh-keys'

export function SSHKeysSection() {
    const { data, isLoading } = useSSHKeys()
    const deleteMutation = useDeleteSSHKey()
    const [confirmDelete, setConfirmDelete] = useState<number | null>(null)

    const keys = data?.keys ?? []

    const handleDelete = (keyId: number) => {
        deleteMutation.mutate(keyId, {
            onSuccess: () => {
                toast.success('SSH key deleted')
                setConfirmDelete(null)
            },
            onError: (e) => toast.error(`Failed to delete key: ${e.message}`),
        })
    }

    return (
        <div className="flex flex-col h-full space-y-4">
            {/* Keys List */}
            {isLoading ? (
                <Loader loaderText="Loading SSH keys..." />
            ) : keys.length === 0 ? (
                <div className="flex-1 flex flex-col items-center justify-center">
                    <div className="relative mb-6">
                        <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                        <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                            <Iconify.Icon icon="heroicons:key" className="size-8 text-brand-secondary-400" />
                        </div>
                    </div>
                    <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No SSH keys configured</h3>
                    <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                        Manage SSH keys for sandbox access. Keys are scoped to your user within this organization.
                    </p>
                </div>
            ) : (
                <div className="space-y-2">
                    {keys.map((key) => (
                        <div
                            key={key.id}
                            className="bg-brand-main-800/50 border border-brand-main-600 rounded-lg p-3 flex items-center justify-between"
                        >
                            <div className="flex-1 min-w-0">
                                <div className="flex items-center gap-2">
                                    <Iconify.Icon icon="heroicons:key" className="size-4 text-brand-secondary-400 shrink-0" />
                                    <span className="text-sm font-medium text-white light:text-brand-main-50 truncate">{key.name}</span>
                                    <span className="text-[10px] px-1.5 py-0.5 bg-brand-main-700 text-white/50 light:text-black/50 rounded">{key.keyType}</span>
                                </div>
                                <div className="mt-1 flex items-center gap-3 text-xs text-white/40 light:text-black/40">
                                    <span className="font-mono truncate">{key.fingerprint}</span>
                                    {key.lastUsedAt && (
                                        <span>Last used: {new Date(key.lastUsedAt).toLocaleDateString()}</span>
                                    )}
                                    {key.createdAt && (
                                        <span>Added: {new Date(key.createdAt).toLocaleDateString()}</span>
                                    )}
                                </div>
                            </div>
                            <div className="shrink-0 ml-3">
                                {confirmDelete === key.id ? (
                                    <div className="flex items-center gap-1">
                                        <button
                                            onClick={() => handleDelete(key.id)}
                                            disabled={deleteMutation.isPending}
                                            className="px-2 py-1 text-xs text-red-400 light:text-red-600 hover:text-red-300 light:hover:text-red-600 transition-colors disabled:opacity-50"
                                        >
                                            Confirm
                                        </button>
                                        <button
                                            onClick={() => setConfirmDelete(null)}
                                            className="px-2 py-1 text-xs text-white/40 light:text-black/40 hover:text-white/60 light:hover:text-black/60 transition-colors"
                                        >
                                            Cancel
                                        </button>
                                    </div>
                                ) : (
                                    <button
                                        onClick={() => setConfirmDelete(key.id)}
                                        className="p-1 rounded text-red-400/60 light:text-red-600/60 hover:text-red-400 light:hover:text-red-600 hover:bg-red-500/10 transition-colors"
                                        title="Delete key"
                                    >
                                        <Iconify.Icon icon="heroicons:trash" className="size-4" />
                                    </button>
                                )}
                            </div>
                        </div>
                    ))}
                </div>
            )}
        </div>
    )
}
