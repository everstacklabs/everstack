import { useState } from 'react'
import { useVolumes, useCreateVolume, useDeleteVolume } from '@/hooks/deployments/use-sandbox'
import { Loader, toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { ui } from '@everstack/ui'

const { Button, Input, Label, Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } = ui

function formatDate(ts?: string | null): string {
    if (!ts) return '--'
    return new Date(ts).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

function formatBytes(bytes: number): string {
    if (!bytes) return '0 B'
    const units = ['B', 'KB', 'MB', 'GB', 'TB']
    let v = bytes
    let i = 0
    while (v >= 1024 && i < units.length - 1) {
        v /= 1024
        i++
    }
    return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${units[i]}`
}

export function VolumesTab() {
    const [dialogOpen, setDialogOpen] = useState(false)
    const [newName, setNewName] = useState('')
    const [newSizeGb, setNewSizeGb] = useState('')

    const { data, isLoading, error } = useVolumes()
    const volumes = data?.volumes ?? []
    const createMutation = useCreateVolume()
    const deleteMutation = useDeleteVolume()

    const handleCreate = () => {
        if (!newName.trim()) {
            toast.error('Name is required')
            return
        }
        const sizeGb = Number(newSizeGb)
        createMutation.mutate(
            {
                name: newName.trim(),
                sizeGb: Number.isFinite(sizeGb) && sizeGb > 0 ? sizeGb : undefined,
            },
            {
                onSuccess: () => {
                    toast.success(`Volume "${newName}" created`)
                    setDialogOpen(false)
                    setNewName('')
                    setNewSizeGb('')
                },
                onError: (err) => toast.error(err.message),
            },
        )
    }

    const handleDelete = (id: string, name: string) => {
        deleteMutation.mutate(id, {
            onSuccess: () => toast.success(`Volume "${name}" deleted`),
            onError: (err) => toast.error(err.message),
        })
    }

    return (
        <div className="flex flex-col h-full">
            {/* Controls */}
            <div className="flex items-center gap-3 px-4 py-2 border-b border-brand-main-600">
                <span className="text-xs text-white/40 light:text-black/40">{volumes.length} volume{volumes.length === 1 ? '' : 's'}</span>
                <div className="flex-1" />
                <button
                    onClick={() => setDialogOpen(true)}
                    className="flex items-center gap-1 text-xs bg-brand-secondary-600/20 text-brand-secondary-300 border border-brand-secondary-500/30 rounded px-2.5 py-1 hover:bg-brand-secondary-600/30"
                >
                    <Iconify.Icon icon="heroicons:plus" className="size-3" />
                    New Volume
                </button>
            </div>

            {/* Content */}
            <div className="flex-1 overflow-y-auto">
                {isLoading ? (
                    <div className="flex items-center justify-center h-full">
                        <Loader loaderText="Loading volumes..." />
                    </div>
                ) : error ? (
                    <div className="flex items-center justify-center h-full text-red-400 light:text-red-600 text-sm">
                        Error loading volumes: {error.message}
                    </div>
                ) : volumes.length === 0 ? (
                    <div className="flex flex-col items-center justify-center h-full pb-16">
                        <div className="relative mb-6">
                            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                                <Iconify.Icon icon="heroicons:circle-stack" className="size-8 text-brand-secondary-400" />
                            </div>
                        </div>
                        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No volumes</h3>
                        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                            Create a persistent volume to share data across sandboxes and survive restarts.
                        </p>
                    </div>
                ) : (
                    <div className="p-4">
                        <table className="w-full text-sm">
                            <thead>
                                <tr className="border-b border-brand-main-600 text-white/40 light:text-black/40 text-xs uppercase">
                                    <th className="text-left py-2 px-3 font-medium">Name</th>
                                    <th className="text-left py-2 px-3 font-medium">ID</th>
                                    <th className="text-left py-2 px-3 font-medium">Used</th>
                                    <th className="text-left py-2 px-3 font-medium">Quota</th>
                                    <th className="text-left py-2 px-3 font-medium">Created</th>
                                    <th className="text-right py-2 px-3 font-medium">Actions</th>
                                </tr>
                            </thead>
                            <tbody>
                                {volumes.map((vol) => (
                                    <tr key={vol.id} className="border-b border-brand-main-700/50 hover:bg-brand-main-800/50">
                                        <td className="py-2.5 px-3 text-white/80 light:text-black/80 font-medium">{vol.name}</td>
                                        <td className="py-2.5 px-3 font-mono text-xs text-white/40 light:text-black/40 max-w-[180px] truncate" title={vol.id}>{vol.id}</td>
                                        <td className="py-2.5 px-3 text-white/50 light:text-black/50 text-xs">{formatBytes(vol.usedBytes ?? 0)}</td>
                                        <td className="py-2.5 px-3 text-white/50 light:text-black/50 text-xs">{vol.sizeBytes ? formatBytes(vol.sizeBytes) : 'Unlimited'}</td>
                                        <td className="py-2.5 px-3 text-white/50 light:text-black/50 text-xs">{formatDate(vol.createdAt)}</td>
                                        <td className="py-2.5 px-3 text-right">
                                            <button
                                                onClick={() => handleDelete(vol.id, vol.name)}
                                                disabled={deleteMutation.isPending}
                                                className="p-1 rounded text-red-400 light:text-red-600 hover:text-red-300 light:hover:text-red-600 hover:bg-red-500/10 transition-colors disabled:opacity-50"
                                                title="Delete"
                                            >
                                                <Iconify.Icon icon="heroicons:trash" className="size-4" />
                                            </button>
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    </div>
                )}
            </div>

            {/* Create Volume Dialog */}
            <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
                <DialogContent className="bg-brand-main-900 border-brand-main-600">
                    <DialogHeader>
                        <DialogTitle className="text-white light:text-brand-main-50">New Volume</DialogTitle>
                    </DialogHeader>
                    <div className="space-y-4 py-2">
                        <div className="space-y-1.5">
                            <Label htmlFor="volumeName" className="text-xs text-white/60 light:text-black/60">Name</Label>
                            <Input id="volumeName" value={newName} onChange={(e) => setNewName(e.target.value)} placeholder="e.g. shared-data" className="w-full" />
                            <p className="text-[10px] text-white/30 light:text-black/30">Mount it into a sandbox at create time via the volume mounts field.</p>
                        </div>
                        <div className="space-y-1.5">
                            <Label htmlFor="volumeSize" className="text-xs text-white/60 light:text-black/60">Capacity quota (GiB, optional)</Label>
                            <Input id="volumeSize" type="number" min={0} value={newSizeGb} onChange={(e) => setNewSizeGb(e.target.value)} placeholder="Unlimited" className="w-full" />
                            <p className="text-[10px] text-white/30 light:text-black/30">Storage is billed by measured usage; the quota only caps growth.</p>
                        </div>
                    </div>
                    <DialogFooter>
                        <Button variant="outline" onClick={() => { setDialogOpen(false); setNewName('') }}>Cancel</Button>
                        <Button onClick={handleCreate} disabled={createMutation.isPending || !newName}>
                            {createMutation.isPending ? 'Creating...' : 'Create'}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    )
}
