import { useState } from 'react'
import { useSnapshots, useCreateSnapshot, useDeleteSnapshot } from '@/hooks/deployments/use-sandbox'
import { Loader, toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { ui } from '@everstack/ui'

const { Button, Input, Label, Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } = ui

// ─── Helpers ──────────────────────────────────────────────────────────

function formatDate(ts?: string | null): string {
    if (!ts) return '--'
    return new Date(ts).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

function formatBytes(bytes: number): string {
    if (!bytes) return '--'
    const units = ['B', 'KB', 'MB', 'GB', 'TB']
    let v = bytes
    let i = 0
    while (v >= 1024 && i < units.length - 1) {
        v /= 1024
        i++
    }
    return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${units[i]}`
}

const STATE_STYLES: Record<string, string> = {
    active: 'bg-green-500/20 text-green-400 light:text-green-600 border-green-500/30',
    building: 'bg-blue-500/20 text-blue-400 light:text-blue-600 border-blue-500/30',
    pending: 'bg-yellow-500/20 text-yellow-400 light:text-yellow-700 border-yellow-500/30',
    inactive: 'bg-gray-500/20 text-gray-400 light:text-gray-600 border-gray-500/30',
    error: 'bg-red-500/20 text-red-400 light:text-red-600 border-red-500/30',
}

// ─── Main Component ─────────────────────────────────────────────────

export function SnapshotsTab() {
    const [dialogOpen, setDialogOpen] = useState(false)
    const [newName, setNewName] = useState('')
    const [newImage, setNewImage] = useState('')
    const [newFromSandbox, setNewFromSandbox] = useState('')

    const { data, isLoading, error } = useSnapshots()
    const snapshots = data?.snapshots ?? []
    const createMutation = useCreateSnapshot()
    const deleteMutation = useDeleteSnapshot()

    const resetForm = () => {
        setNewName('')
        setNewImage('')
        setNewFromSandbox('')
    }

    const handleCreate = () => {
        if (!newName.trim()) {
            toast.error('Name is required')
            return
        }
        createMutation.mutate(
            {
                name: newName.trim(),
                image: newImage.trim() || undefined,
                fromSandboxId: newFromSandbox.trim() || undefined,
            },
            {
                onSuccess: () => {
                    toast.success(`Snapshot "${newName}" created`)
                    setDialogOpen(false)
                    resetForm()
                },
                onError: (err) => toast.error(err.message),
            },
        )
    }

    const handleDelete = (id: string, name: string) => {
        deleteMutation.mutate(id, {
            onSuccess: () => toast.success(`Snapshot "${name}" deleted`),
            onError: (err) => toast.error(err.message),
        })
    }

    return (
        <div className="flex flex-col h-full">
            {/* Controls */}
            <div className="flex items-center gap-3 px-4 py-2 border-b border-brand-main-600">
                <span className="text-xs text-white/40 light:text-black/40">{snapshots.length} snapshot{snapshots.length === 1 ? '' : 's'}</span>
                <div className="flex-1" />
                <button
                    onClick={() => setDialogOpen(true)}
                    className="flex items-center gap-1 text-xs bg-brand-secondary-600/20 text-brand-secondary-300 border border-brand-secondary-500/30 rounded px-2.5 py-1 hover:bg-brand-secondary-600/30"
                >
                    <Iconify.Icon icon="heroicons:plus" className="size-3" />
                    New Snapshot
                </button>
            </div>

            {/* Content */}
            <div className="flex-1 overflow-y-auto">
                {isLoading ? (
                    <div className="flex items-center justify-center h-full">
                        <Loader loaderText="Loading snapshots..." />
                    </div>
                ) : error ? (
                    <div className="flex items-center justify-center h-full text-red-400 light:text-red-600 text-sm">
                        Error loading snapshots: {error.message}
                    </div>
                ) : snapshots.length === 0 ? (
                    <div className="flex flex-col items-center justify-center h-full pb-16">
                        <div className="relative mb-6">
                            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                                <Iconify.Icon icon="heroicons:camera" className="size-8 text-brand-secondary-400" />
                            </div>
                        </div>
                        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No snapshots</h3>
                        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                            Create a named snapshot to pre-bake an environment and launch sandboxes from it.
                        </p>
                    </div>
                ) : (
                    <div className="p-4">
                        <table className="w-full text-sm">
                            <thead>
                                <tr className="border-b border-brand-main-600 text-white/40 light:text-black/40 text-xs uppercase">
                                    <th className="text-left py-2 px-3 font-medium">Name</th>
                                    <th className="text-left py-2 px-3 font-medium">State</th>
                                    <th className="text-left py-2 px-3 font-medium">Base Image</th>
                                    <th className="text-left py-2 px-3 font-medium">Size</th>
                                    <th className="text-left py-2 px-3 font-medium">Created</th>
                                    <th className="text-right py-2 px-3 font-medium">Actions</th>
                                </tr>
                            </thead>
                            <tbody>
                                {snapshots.map((snap) => (
                                    <tr key={snap.id} className="border-b border-brand-main-700/50 hover:bg-brand-main-800/50">
                                        <td className="py-2.5 px-3 text-white/80 light:text-black/80 font-medium">{snap.name}</td>
                                        <td className="py-2.5 px-3">
                                            <span className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium border ${STATE_STYLES[snap.state] ?? STATE_STYLES.inactive}`}>
                                                {snap.state}
                                            </span>
                                        </td>
                                        <td className="py-2.5 px-3 font-mono text-xs text-white/50 light:text-black/50 max-w-[220px] truncate" title={snap.baseImage}>
                                            {snap.baseImage || '--'}
                                        </td>
                                        <td className="py-2.5 px-3 text-white/50 light:text-black/50 text-xs">{formatBytes(snap.sizeBytes)}</td>
                                        <td className="py-2.5 px-3 text-white/50 light:text-black/50 text-xs">{formatDate(snap.createdAt)}</td>
                                        <td className="py-2.5 px-3 text-right">
                                            <button
                                                onClick={() => handleDelete(snap.id, snap.name)}
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

            {/* Create Snapshot Dialog */}
            <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
                <DialogContent className="bg-brand-main-900 border-brand-main-600">
                    <DialogHeader>
                        <DialogTitle className="text-white light:text-brand-main-50">New Snapshot</DialogTitle>
                    </DialogHeader>
                    <div className="space-y-4 py-2">
                        <div className="space-y-1.5">
                            <Label htmlFor="snapshotName" className="text-xs text-white/60 light:text-black/60">Name</Label>
                            <Input id="snapshotName" value={newName} onChange={(e) => setNewName(e.target.value)} placeholder="e.g. python-ml-base" className="w-full" />
                        </div>
                        <div className="space-y-1.5">
                            <Label htmlFor="snapshotImage" className="text-xs text-white/60 light:text-black/60">Base image (optional)</Label>
                            <Input id="snapshotImage" value={newImage} onChange={(e) => setNewImage(e.target.value)} placeholder="ghcr.io/everstacklabs/sandbox:python" className="w-full font-mono" />
                        </div>
                        <div className="space-y-1.5">
                            <Label htmlFor="snapshotFrom" className="text-xs text-white/60 light:text-black/60">From sandbox ID (optional)</Label>
                            <Input id="snapshotFrom" value={newFromSandbox} onChange={(e) => setNewFromSandbox(e.target.value)} placeholder="snapshot a running sandbox's filesystem" className="w-full font-mono" />
                            <p className="text-[10px] text-white/30 light:text-black/30">Provide either a base image or a source sandbox to snapshot from.</p>
                        </div>
                    </div>
                    <DialogFooter>
                        <Button variant="outline" onClick={() => { setDialogOpen(false); resetForm() }}>Cancel</Button>
                        <Button onClick={handleCreate} disabled={createMutation.isPending || !newName}>
                            {createMutation.isPending ? 'Creating...' : 'Create'}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    )
}
