import { useState, useMemo } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import {
    useStorageConfigs,
    useStorageUsage,
    useUpdateStorageConfig,
    useDeleteStorageConfig,
    useStorageObjects,
    useDeleteObject,
    useDownloadObject,
    useSyncObjects,
} from '@/hooks/settings/use-storage'
import { StorageProvider, ObjectPurpose } from '@/server/storage'
import type { StorageConfig, StorageObject } from '@/server/storage'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { ui } from '@everstack/ui'
import { Iconify } from '@everstack/ui/icons'
import { Button, Loader, toast } from '@everstack/ui/components'
import { Trash2, Download, Folder, File, ChevronRight, Pencil } from 'lucide-react'
import { formatTimestamp } from '@everstack/utils/functions/index'
import { storageConfigActionPolicy } from './-storage-config-view-model'

const {
    Card,
    CardContent,
    Input,
    Label,
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
    Badge,
    Progress,
    Tabs,
    TabsList,
    TabsTrigger,
    TabsContent,
    Sheet,
    SheetContent,
    SheetDescription,
    SheetHeader,
    SheetTitle,
    SheetBody,
} = ui

export const Route = createFileRoute('/storage/overview')({
    component: StoragePage,
})

function formatBytes(bytes: number | bigint): string {
    const b = typeof bytes === 'bigint' ? Number(bytes) : bytes
    if (b === 0) return '0 B'
    const units = ['B', 'KB', 'MB', 'GB', 'TB']
    const i = Math.floor(Math.log(b) / Math.log(1024))
    return `${(b / Math.pow(1024, i)).toFixed(1)} ${units[i]}`
}

const PROVIDER_LABELS: Record<number, string> = {
    [StorageProvider.S3]: 'S3',
    [StorageProvider.GCS]: 'GCS',
    [StorageProvider.R2]: 'R2',
    [StorageProvider.MINIO]: 'MINIO',
    [StorageProvider.EVERSTACK]: 'Everstack Storage',
}

const PURPOSE_COLORS: Record<number, string> = {
    [ObjectPurpose.DATASET]: 'bg-purple-500/20 text-purple-300 light:text-purple-600',
    [ObjectPurpose.ARTIFACT]: 'bg-blue-500/20 text-blue-300 light:text-blue-600',
    [ObjectPurpose.UPLOAD]: 'bg-gray-500/20 text-gray-300 light:text-gray-700',
    [ObjectPurpose.EVAL_RESULT]: 'bg-green-500/20 text-green-300 light:text-green-600',
}

const PURPOSE_LABELS: Record<number, string> = {
    [ObjectPurpose.DATASET]: 'dataset',
    [ObjectPurpose.ARTIFACT]: 'artifact',
    [ObjectPurpose.UPLOAD]: 'upload',
    [ObjectPurpose.EVAL_RESULT]: 'eval_result',
}

const TAB_TRIGGER_CLASS = 'relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1'

function MetricCard({ label, value, subtext }: { label: string; value: string; subtext: string }) {
    return (
        <div className="rounded-md border border-brand-main-600 bg-brand-main-800/50 p-3 space-y-1">
            <p className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">{label}</p>
            <p className="text-xl font-semibold text-white light:text-brand-main-50">{value}</p>
            <p className="text-xs text-white/40 light:text-black/40">{subtext}</p>
        </div>
    )
}

// ─── Folder hierarchy types & helpers ────────────────────────────────

type FolderEntry = { type: 'folder'; name: string }
type FileEntry = { type: 'file'; name: string; object: StorageObject }
type DirEntry = FolderEntry | FileEntry

const TENANT_PREFIX_RE = /^tenants\/[^/]+\//

function stripTenantPrefix(key: string): string {
    return key.replace(TENANT_PREFIX_RE, '')
}

function getEntriesForPath(objects: StorageObject[], path: string[]): DirEntry[] {
    const prefix = path.length > 0 ? path.join('/') + '/' : ''
    const folders = new Set<string>()
    const files: FileEntry[] = []

    for (const obj of objects) {
        const key = stripTenantPrefix(obj.key)
        if (!key.startsWith(prefix)) continue

        const rest = key.slice(prefix.length)
        if (!rest) continue

        const slashIdx = rest.indexOf('/')
        if (slashIdx === -1) {
            files.push({ type: 'file', name: rest, object: obj })
        } else {
            folders.add(rest.slice(0, slashIdx))
        }
    }

    const folderEntries: FolderEntry[] = Array.from(folders)
        .sort()
        .map((name) => ({ type: 'folder', name }))

    files.sort((a, b) => a.name.localeCompare(b.name))

    return [...folderEntries, ...files]
}

// ─── Main page ───────────────────────────────────────────────────────

function StoragePage() {
    return (
        <div className="flex flex-col h-full w-full">
            <div className="flex-1 overflow-y-auto space-y-4 flex flex-col">
                <Tabs defaultValue="configurations" className="w-full flex flex-col flex-1 h-full">
                    <TabsList className="w-fit bg-brand-main-800/50 mx-3 mt-2 border border-brand-main-600 rounded p-1 h-auto gap-1">
                        <TabsTrigger className={TAB_TRIGGER_CLASS} value="configurations">Configurations</TabsTrigger>
                        <TabsTrigger className={TAB_TRIGGER_CLASS} value="objects">Objects</TabsTrigger>
                        <TabsTrigger className={TAB_TRIGGER_CLASS} value="usage">Usage</TabsTrigger>
                    </TabsList>

                    <TabsContent value="configurations" className="space-y-6 mt-4 flex-1 flex flex-col">
                        <ConfigurationsTab />
                    </TabsContent>

                    <TabsContent value="objects" className="space-y-6 mt-4 flex-1 flex flex-col">
                        <ObjectsTab />
                    </TabsContent>

                    <TabsContent value="usage" className="space-y-6 mt-4 flex-1 flex flex-col">
                        <UsageTab />
                    </TabsContent>
                </Tabs>
            </div>
        </div>
    )
}

// ─── Configurations Tab ─────────────────────────────────────────────

function ConfigurationsTab() {
    const { data: configs, isLoading } = useStorageConfigs()
    const { data: usage } = useStorageUsage()
    const deleteMutation = useDeleteStorageConfig()
    const updateMutation = useUpdateStorageConfig()

    const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null)
    const [deleteConfirmBucket, setDeleteConfirmBucket] = useState('')
    const [editConfig, setEditConfig] = useState<StorageConfig | null>(null)
    const [editBucket, setEditBucket] = useState('')
    const [editRegion, setEditRegion] = useState('')
    const [editEndpoint, setEditEndpoint] = useState('')
    const [editAccessKeyId, setEditAccessKeyId] = useState('')
    const [editSecretAccessKey, setEditSecretAccessKey] = useState('')
    const [editPathPrefix, setEditPathPrefix] = useState('')

    const openEdit = (config: StorageConfig) => {
        setEditConfig(config)
        setEditBucket(config.bucket)
        setEditRegion(config.region)
        setEditEndpoint(config.endpoint)
        setEditAccessKeyId('')
        setEditSecretAccessKey('')
        setEditPathPrefix(config.pathPrefix)
    }

    const handleUpdate = async (e: React.FormEvent) => {
        e.preventDefault()
        if (!editConfig) return
        try {
            await updateMutation.mutateAsync({
                id: editConfig.id,
                bucket: editBucket || undefined,
                region: editRegion || undefined,
                endpoint: editEndpoint || undefined,
                accessKeyId: editAccessKeyId || undefined,
                secretAccessKey: editSecretAccessKey || undefined,
                pathPrefix: editPathPrefix || undefined,
            })
            setEditConfig(null)
            toast.success('Storage configuration updated')
        } catch {
            toast.error('Failed to update storage configuration')
        }
    }

    const handleDelete = async (id: string) => {
        try {
            await deleteMutation.mutateAsync(id)
            setDeleteConfirmId(null)
            setDeleteConfirmBucket('')
            toast.success('Storage configuration deleted')
        } catch {
            toast.error('Failed to delete storage configuration')
        }
    }

    if (isLoading) {
        return (
            <div className="flex items-center justify-center text-white/70 light:text-black/70 py-16">
                <Loader loaderText="Loading storage configurations..." />
            </div>
        )
    }

    const configCount = configs?.length ?? 0
    const bytesUsed = usage?.bytesUsed ? Number(usage.bytesUsed) : 0
    const objectCount = usage?.objectCount ?? 0

    if (configCount === 0) {
        return (
            <div className="flex flex-col items-center justify-center flex-1 h-full px-4 pb-24">
                <div className="relative mb-6">
                    <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                    <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                        <Iconify.Icon icon="heroicons:cloud-arrow-up" className="size-8 text-brand-secondary-400" />
                    </div>
                </div>
                <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No storage configurations</h3>
                <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                    Configure object storage backends (S3, GCS, R2, MinIO) for datasets, uploads, and artifacts.
                </p>
                <p className="text-xs text-white/30 light:text-black/30 mt-3">
                    Use the <strong className="text-white/50 light:text-black/50">Add Storage Config</strong> button in the topbar to get started.
                </p>
            </div>
        )
    }

    const providerSet = new Set((configs ?? []).map((c) => PROVIDER_LABELS[c.provider] ?? 'Unknown'))

    const configColumns: ColumnConfig<StorageConfig>[] = [
        {
            id: 'provider',
            header: 'Provider',
            width: 120,
            minWidth: 80,
            render: (config) => (
                <Badge variant="secondary" className="bg-brand-main-900/60">
                    {PROVIDER_LABELS[config.provider] ?? 'S3'}
                </Badge>
            ),
        },
        {
            id: 'bucket',
            header: 'Bucket',
            width: 180,
            minWidth: 120,
            render: (config) => (
                <span className="text-white light:text-brand-main-50 font-mono text-sm">{config.bucket}</span>
            ),
        },
        {
            id: 'region',
            header: 'Region',
            width: 120,
            minWidth: 80,
            render: (config) => (
                <span className="text-white/60 light:text-black/60">{config.region || '-'}</span>
            ),
        },
        {
            id: 'endpoint',
            header: 'Endpoint',
            width: 200,
            minWidth: 100,
            render: (config) => (
                <span className="text-white/60 light:text-black/60 text-sm truncate max-w-[200px]">
                    {config.endpoint || '-'}
                </span>
            ),
        },
        {
            id: 'pathPrefix',
            header: 'Prefix',
            width: 140,
            minWidth: 80,
            render: (config) => (
                <span className="text-white/60 light:text-black/60 font-mono text-sm">{config.pathPrefix || '-'}</span>
            ),
        },
        {
            id: 'actions',
            header: '',
            width: 90,
            minWidth: 90,
            maxWidth: 90,
            resizable: false,
            render: (config) => {
                const policy = storageConfigActionPolicy(config.systemManaged)
                if (policy.label) {
                    return (
                        <Badge variant="secondary" className="bg-brand-main-900/60 text-white/60 light:text-black/60">
                            {policy.label}
                        </Badge>
                    )
                }

                return (
                    <div className="flex items-center justify-end gap-1" data-row-actions>
                        {policy.canEdit && (
                            <Button
                                variant="ghost"
                                onClick={() => openEdit(config)}
                                className="text-white/60 light:text-black/60 hover:text-white light:hover:text-brand-main-50 hover:bg-brand-main-700/40"
                            >
                                <Pencil className="h-2.5 w-2.5" />
                            </Button>
                        )}
                        {policy.canDelete && (
                            <Button
                                variant="ghost"
                                onClick={() => {
                                    setDeleteConfirmId(config.id)
                                    setDeleteConfirmBucket(config.bucket)
                                }}
                                disabled={deleteMutation.isPending}
                                className="text-red-400 light:text-red-600 hover:text-red-300 light:hover:text-red-600 hover:bg-red-500/10"
                            >
                                <Trash2 className="h-2.5 w-2.5" />
                            </Button>
                        )}
                    </div>
                )
            },
        },
    ]

    return (
        <>
            <div className='px-4'>
                <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">Storage Backends</div>
                <p className="text-sm text-brand-main-200 mt-1">Configure storage backends for datasets, uploads, and artifacts.</p>
            </div>

            <div className="grid grid-cols-2 md:grid-cols-4 gap-3 px-4">
                <MetricCard label="Backends" value={String(configCount)} subtext={`${configCount} configured`} />
                <MetricCard label="Providers" value={String(providerSet.size)} subtext={Array.from(providerSet).join(', ') || '-'} />
                <MetricCard label="Objects" value={String(objectCount)} subtext="total stored" />
                <MetricCard label="Storage Used" value={formatBytes(bytesUsed)} subtext="across all backends" />
            </div>

            <div className="flex-1 min-h-0 w-full h-full overflow-hidden flex flex-col">
                <ResponsiveTable
                    columns={configColumns}
                    data={configs ?? []}
                    enableResizing={true}
                    minTableWidth="100%"
                    emptyMessage="No configurations found."
                    rowKey={(c) => c.id}
                />
            </div>

            {/* Edit Configuration Sheet */}
            <Sheet
                open={editConfig !== null}
                onOpenChange={(open) => !open && setEditConfig(null)}
            >
                <SheetContent side="right" className="min-w-[480px]">
                    <SheetHeader>
                        <SheetTitle>Edit Storage Configuration</SheetTitle>
                    </SheetHeader>

                    {updateMutation.error && (
                        <div className="rounded-lg border border-red-500/20 bg-red-500/10 p-3 text-sm text-red-400 light:text-red-600 mx-4">
                            {(updateMutation.error as Error).message}
                        </div>
                    )}

                    <SheetBody className='py-2'>
                        <SheetDescription className="border bg-brand-secondary-800/20 text-brand-secondary-300 transition-colors border-brand-secondary-600 text-xs p-2 rounded-sm mb-4">
                            Update the configuration for{' '}
                            <strong className="text-brand-secondary-200">{editConfig?.bucket}</strong>.
                            Leave credential fields blank to keep existing values.
                        </SheetDescription>
                        <form onSubmit={handleUpdate} className="space-y-4">
                            <div className="space-y-2">
                                <Label className="text-brand-main-200">Bucket</Label>
                                <Input
                                    placeholder="my-bucket"
                                    value={editBucket}
                                    onChange={(e) => setEditBucket(e.target.value)}
                                    className="border-brand-main-700 bg-brand-main-900/60 text-white light:text-brand-main-50"
                                />
                            </div>
                            <div className="grid grid-cols-2 gap-4">
                                <div className="space-y-2">
                                    <Label className="text-brand-main-200">Region</Label>
                                    <Input
                                        placeholder="us-east-1"
                                        value={editRegion}
                                        onChange={(e) => setEditRegion(e.target.value)}
                                        className="border-brand-main-700 bg-brand-main-900/60 text-white light:text-brand-main-50"
                                    />
                                </div>
                                <div className="space-y-2">
                                    <Label className="text-brand-main-200">Endpoint</Label>
                                    <Input
                                        placeholder="https://s3.amazonaws.com"
                                        value={editEndpoint}
                                        onChange={(e) => setEditEndpoint(e.target.value)}
                                        className="border-brand-main-700 bg-brand-main-900/60 text-white light:text-brand-main-50"
                                    />
                                </div>
                            </div>
                            <div className="grid grid-cols-2 gap-4">
                                <div className="space-y-2">
                                    <Label className="text-brand-main-200">Access Key ID</Label>
                                    <Input
                                        placeholder="Leave blank to keep existing"
                                        value={editAccessKeyId}
                                        onChange={(e) => setEditAccessKeyId(e.target.value)}
                                        className="border-brand-main-700 bg-brand-main-900/60 text-white light:text-brand-main-50"
                                    />
                                </div>
                                <div className="space-y-2">
                                    <Label className="text-brand-main-200">Secret Access Key</Label>
                                    <Input
                                        type="password"
                                        placeholder="Leave blank to keep existing"
                                        value={editSecretAccessKey}
                                        onChange={(e) => setEditSecretAccessKey(e.target.value)}
                                        className="border-brand-main-700 bg-brand-main-900/60 text-white light:text-brand-main-50"
                                    />
                                </div>
                            </div>
                            <div className="space-y-2">
                                <Label className="text-brand-main-200">Path Prefix (optional)</Label>
                                <Input
                                    placeholder="data/"
                                    value={editPathPrefix}
                                    onChange={(e) => setEditPathPrefix(e.target.value)}
                                    className="border-brand-main-700 bg-brand-main-900/60 text-white light:text-brand-main-50"
                                />
                            </div>
                            <div className="flex justify-end gap-3 mt-6 border-t border-brand-main-700/60 pt-4">
                                <Button type="button" variant="outline" onClick={() => setEditConfig(null)}>
                                    Cancel
                                </Button>
                                <Button type="submit" disabled={updateMutation.isPending}>
                                    {updateMutation.isPending ? 'Saving...' : 'Save Changes'}
                                </Button>
                            </div>
                        </form>
                    </SheetBody>
                </SheetContent>
            </Sheet>

            {/* Delete Confirmation Sheet */}
            <Sheet
                open={deleteConfirmId !== null}
                onOpenChange={(open) => !open && setDeleteConfirmId(null)}
            >
                <SheetContent side="right" className="min-w-[400px]">
                    <SheetHeader>
                        <SheetTitle>Delete Storage Configuration</SheetTitle>
                        <SheetDescription className="text-white/60 light:text-black/60 mt-1 text-xs">
                            Are you sure you want to delete the storage configuration for bucket{' '}
                            <strong className="text-brand-main-100">{deleteConfirmBucket}</strong>?
                            This action cannot be undone.
                        </SheetDescription>
                    </SheetHeader>
                    <SheetBody>
                        <div className="flex justify-end gap-3 mt-6 border-t border-brand-main-700/60 pt-4">
                            <Button
                                variant="outline"
                                onClick={() => setDeleteConfirmId(null)}
                                disabled={deleteMutation.isPending}
                            >
                                Cancel
                            </Button>
                            <Button
                                variant="destructive"
                                className="bg-destructive/60 text-brand-main-100 hover:bg-destructive/90"
                                onClick={() => deleteConfirmId && handleDelete(deleteConfirmId)}
                                disabled={deleteMutation.isPending}
                            >
                                {deleteMutation.isPending ? 'Deleting...' : 'Delete'}
                            </Button>
                        </div>
                    </SheetBody>
                </SheetContent>
            </Sheet>
        </>
    )
}

// ─── Objects Tab ────────────────────────────────────────────────────

function ObjectsTab() {
    const [purposeFilter, setPurposeFilter] = useState<string>('all')
    const [currentPath, setCurrentPath] = useState<string[]>([])
    const { data: objects, isLoading } = useStorageObjects(purposeFilter === 'all' ? undefined : Number(purposeFilter) as ObjectPurpose)
    const { data: configs } = useStorageConfigs()
    const { data: usage } = useStorageUsage()
    const deleteMutation = useDeleteObject()
    const downloadObject = useDownloadObject()
    const syncMutation = useSyncObjects()

    const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null)
    const [deleteConfirmName, setDeleteConfirmName] = useState('')

    const handleDelete = async (id: string) => {
        try {
            await deleteMutation.mutateAsync(id)
            setDeleteConfirmId(null)
            setDeleteConfirmName('')
            toast.success('Object deleted successfully')
        } catch {
            toast.error('Failed to delete object')
        }
    }

    const handleDownload = async (objectId: string) => {
        try {
            await downloadObject(objectId)
        } catch {
            toast.error('Failed to get download URL')
        }
    }

    const configMap = useMemo(() => {
        const map = new Map<string, StorageConfig>()
        for (const c of configs ?? []) map.set(c.id, c)
        return map
    }, [configs])

    const multipleConfigs = (configs?.length ?? 0) > 1

    const entries = useMemo(() => {
        const allObjects = objects ?? []
        if (allObjects.length === 0) return []

        if (multipleConfigs && currentPath.length === 0) {
            const bucketNames = new Set<string>()
            for (const obj of allObjects) {
                const cfg = configMap.get(obj.configId)
                if (cfg) bucketNames.add(cfg.bucket)
            }
            return Array.from(bucketNames)
                .sort()
                .map((name): DirEntry => ({ type: 'folder', name }))
        }

        let filteredObjects = allObjects
        let keyPath = currentPath

        if (multipleConfigs) {
            const bucketName = currentPath[0]
            filteredObjects = allObjects.filter((obj) => {
                const cfg = configMap.get(obj.configId)
                return cfg?.bucket === bucketName
            })
            keyPath = currentPath.slice(1)
        }

        return getEntriesForPath(filteredObjects, keyPath)
    }, [objects, currentPath, multipleConfigs, configMap])

    const columns: ColumnConfig<DirEntry>[] = [
        {
            id: 'name',
            header: 'Name',
            width: 280,
            minWidth: 180,
            render: (entry) => {
                if (entry.type === 'folder') {
                    return (
                        <button
                            type="button"
                            className="flex items-center gap-2 hover:text-brand-secondary-300 transition-colors text-left"
                            onClick={() => setCurrentPath([...currentPath, entry.name])}
                        >
                            <Folder size={15} className="text-brand-secondary-400 flex-shrink-0" />
                            <span className="truncate font-mono text-xs text-brand-secondary-100">{entry.name}</span>
                        </button>
                    )
                }
                return (
                    <div className="flex items-center gap-2">
                        <File size={15} className="text-white/40 light:text-black/40 flex-shrink-0" />
                        <span className="truncate font-mono text-xs text-brand-secondary-100">{entry.name}</span>
                    </div>
                )
            },
        },
        {
            id: 'purpose',
            header: 'Purpose',
            width: 120,
            minWidth: 80,
            render: (entry) => {
                if (entry.type === 'folder') return null
                const purpose = entry.object.purpose ?? 0
                const colorClass = PURPOSE_COLORS[purpose] || 'bg-gray-500/20 text-gray-300 light:text-gray-700'
                const label = PURPOSE_LABELS[purpose] || '-'
                return (
                    <span className={`px-1.5 py-0.5 rounded text-xs font-medium ${colorClass}`}>
                        {label}
                    </span>
                )
            },
        },
        {
            id: 'contentType',
            header: 'Type',
            width: 140,
            minWidth: 100,
            render: (entry) => (
                <span className="truncate text-xs text-brand-main-100">
                    {entry.type === 'file' ? (entry.object.contentType || '-') : '\u2014'}
                </span>
            ),
        },
        {
            id: 'size',
            header: 'Size',
            width: 100,
            minWidth: 70,
            render: (entry) => (
                <span className="text-xs text-brand-main-100">
                    {entry.type === 'file' && entry.object.sizeBytes ? formatBytes(entry.object.sizeBytes) : '\u2014'}
                </span>
            ),
        },
        {
            id: 'createdAt',
            header: 'Created',
            width: 160,
            minWidth: 140,
            render: (entry) => (
                <span className="truncate text-xs text-brand-main-100">
                    {entry.type === 'file' && entry.object.createdAt ? formatTimestamp(entry.object.createdAt) : '\u2014'}
                </span>
            ),
        },
        {
            id: 'actions',
            header: '',
            width: 80,
            minWidth: 80,
            maxWidth: 80,
            resizable: false,
            render: (entry) => {
                if (entry.type === 'folder') return null
                const obj = entry.object
                return (
                    <div className="flex items-center justify-center gap-1" data-row-actions>
                        <button
                            type="button"
                            className="p-1 rounded hover:bg-brand-secondary-600/20 hover:text-brand-secondary-300 transition-colors"
                            onClick={() => handleDownload(obj.id)}
                            title="Download"
                        >
                            <Download size={14} />
                        </button>
                        <button
                            type="button"
                            className="p-1 rounded hover:bg-red-500/20 hover:text-red-400 light:hover:text-red-600 transition-colors"
                            onClick={() => {
                                setDeleteConfirmId(obj.id)
                                setDeleteConfirmName(obj.filename || 'this object')
                            }}
                            title="Delete object"
                        >
                            <Trash2 size={14} />
                        </button>
                    </div>
                )
            },
        },
    ]

    if (isLoading) {
        return (
            <div className="flex items-center justify-center text-white/70 light:text-black/70 py-16">
                <Loader loaderText="Loading objects..." />
            </div>
        )
    }

    const objectCount = objects?.length ?? 0
    const totalBytes = usage?.bytesUsed ? Number(usage.bytesUsed) : 0
    const purposeCounts: Record<string, number> = {}
    for (const obj of objects ?? []) {
        const p = PURPOSE_LABELS[obj.purpose ?? 0] || 'unknown'
        purposeCounts[p] = (purposeCounts[p] ?? 0) + 1
    }
    const uniquePurposes = Object.keys(purposeCounts).length

    return (
        <>
            <div className='px-4'>
                <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">Stored Objects</div>
                <p className="text-sm text-brand-main-200 mt-1">Files uploaded through datasets, evaluations, and the presigned upload API.</p>
            </div>

            <div className="grid grid-cols-2 md:grid-cols-4 gap-3 px-4">
                <MetricCard label="Total Objects" value={String(objectCount)} subtext={purposeFilter !== 'all' ? `filtered by ${PURPOSE_LABELS[Number(purposeFilter)] ?? purposeFilter}` : 'across all purposes'} />
                <MetricCard label="Storage Used" value={formatBytes(totalBytes)} subtext="total size" />
                <MetricCard label="Purposes" value={String(uniquePurposes)} subtext={Object.keys(purposeCounts).join(', ') || '-'} />
                <MetricCard label="Filtered" value={purposeFilter === 'all' ? 'All' : (PURPOSE_LABELS[Number(purposeFilter)] ?? purposeFilter)} subtext={purposeFilter === 'all' ? 'showing everything' : `${objectCount} matching`} />
            </div>

            <div className="flex items-center gap-3 px-4">
                <Select value={purposeFilter} onValueChange={setPurposeFilter}>
                    <SelectTrigger className="w-[180px] border-brand-main-700 bg-brand-main-900/60 text-white light:text-brand-main-50 h-8 text-sm">
                        <SelectValue placeholder="Filter by purpose" />
                    </SelectTrigger>
                    <SelectContent className="bg-brand-main-900/60 border-brand-main-700">
                        <SelectItem value="all">All Purposes</SelectItem>
                        <SelectItem value={String(ObjectPurpose.DATASET)}>Dataset</SelectItem>
                        <SelectItem value={String(ObjectPurpose.ARTIFACT)}>Artifact</SelectItem>
                        <SelectItem value={String(ObjectPurpose.UPLOAD)}>Upload</SelectItem>
                        <SelectItem value={String(ObjectPurpose.EVAL_RESULT)}>Eval Result</SelectItem>
                    </SelectContent>
                </Select>
                <Button
                    variant="outline"
                    className="gap-1.5 border-brand-main-700 bg-brand-main-900/60 text-white light:text-brand-main-50 hover:bg-brand-main-800"
                    disabled={syncMutation.isPending}
                    onClick={() => {
                        syncMutation.mutate(undefined, {
                            onSuccess: (data) => {
                                if (data.synced > 0) {
                                    toast.success(`Synced ${data.synced} new object${data.synced === 1 ? '' : 's'} from bucket`)
                                } else {
                                    toast.info('Bucket is already in sync')
                                }
                            },
                            onError: () => {
                                toast.error('Failed to sync objects from bucket')
                            },
                        })
                    }}
                >
                    <Iconify.Icon icon="solar:refresh-bold" className={syncMutation.isPending ? 'animate-spin' : ''} />
                    {syncMutation.isPending ? 'Syncing...' : 'Sync Buckets'}
                </Button>
            </div>

            {/* Breadcrumb */}
            <div className="flex items-center gap-1 text-sm flex-wrap px-4">
                <button
                    type="button"
                    className={`hover:text-white light:hover:text-brand-main-50 transition-colors ${currentPath.length === 0 ? 'text-white light:text-brand-main-50 font-medium' : 'text-white/60 light:text-black/60'}`}
                    onClick={() => setCurrentPath([])}
                >
                    Objects
                </button>
                {currentPath.map((segment, i) => (
                    <span key={i} className="flex items-center gap-1">
                        <ChevronRight size={14} className="text-white/30 light:text-black/30" />
                        <button
                            type="button"
                            className={`hover:text-white light:hover:text-brand-main-50 transition-colors ${i === currentPath.length - 1 ? 'text-white light:text-brand-main-50 font-medium' : 'text-white/60 light:text-black/60'}`}
                            onClick={() => setCurrentPath(currentPath.slice(0, i + 1))}
                        >
                            {segment}
                        </button>
                    </span>
                ))}
            </div>

            {objectCount === 0 ? (
                <div className="flex flex-col items-center justify-center py-16 px-4">
                    <div className="relative mb-6">
                        <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                        <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                            <Iconify.Icon icon="heroicons:archive-box" className="size-8 text-brand-secondary-400" />
                        </div>
                    </div>
                    <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">
                        {purposeFilter !== 'all' ? 'No matching objects' : 'No objects yet'}
                    </h3>
                    <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                        {purposeFilter !== 'all'
                            ? `No objects with purpose "${PURPOSE_LABELS[Number(purposeFilter)] ?? purposeFilter}" found.`
                            : 'Objects appear here when uploaded through datasets, evaluations, or the presigned upload API.'}
                    </p>
                </div>
            ) : entries.length === 0 ? (
                <div className="flex flex-col items-center justify-center py-12 px-4">
                    <div className="relative mb-5">
                        <div className="absolute inset-0 bg-brand-secondary-500/15 rounded-full blur-lg" />
                        <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-3.5">
                            <Iconify.Icon icon="heroicons:folder-open" className="size-7 text-brand-secondary-400/70" />
                        </div>
                    </div>
                    <h3 className="text-sm font-medium text-white light:text-brand-main-50 mb-1">Empty folder</h3>
                    <p className="text-xs text-white/40 light:text-black/40">This folder has no files or subfolders.</p>
                </div>
            ) : (
                <div className="flex-1 min-h-0 w-full h-full overflow-hidden flex flex-col">
                    <ResponsiveTable
                        columns={columns}
                        data={entries}
                        enableResizing={true}
                        minTableWidth="100%"
                        emptyMessage="No objects found."
                        rowKey={(entry) => entry.type === 'file' ? entry.object.id : `folder-${entry.name}`}
                        onRowClick={(entry) => {
                            if (entry.type === 'folder') {
                                setCurrentPath([...currentPath, entry.name])
                            }
                        }}
                    />
                </div>
            )}

            {/* Delete Confirmation Sheet */}
            <Sheet
                open={deleteConfirmId !== null}
                onOpenChange={(open) => !open && setDeleteConfirmId(null)}
            >
                <SheetContent side="right" className="min-w-[400px]">
                    <SheetHeader>
                        <SheetTitle>Delete Object</SheetTitle>
                        <SheetDescription className="text-white/60 light:text-black/60 mt-1 text-xs">
                            Are you sure you want to delete{' '}
                            <strong className="text-brand-main-100">{deleteConfirmName}</strong>?
                            This action cannot be undone.
                        </SheetDescription>
                    </SheetHeader>
                    <SheetBody>
                        <div className="flex justify-end gap-3 mt-6 border-t border-brand-main-700/60 pt-4">
                            <Button
                                variant="outline"
                                onClick={() => setDeleteConfirmId(null)}
                                disabled={deleteMutation.isPending}
                            >
                                Cancel
                            </Button>
                            <Button
                                variant="destructive"
                                className="bg-destructive/60 text-brand-main-100 hover:bg-destructive/90"
                                onClick={() => deleteConfirmId && handleDelete(deleteConfirmId)}
                                disabled={deleteMutation.isPending}
                            >
                                {deleteMutation.isPending ? 'Deleting...' : 'Delete'}
                            </Button>
                        </div>
                    </SheetBody>
                </SheetContent>
            </Sheet>
        </>
    )
}

// ─── Usage Tab ──────────────────────────────────────────────────────

function UsageTab() {
    const { data: usage, isLoading } = useStorageUsage()

    const bytesUsed = usage?.bytesUsed ? Number(usage.bytesUsed) : 0
    const bytesQuota = usage?.bytesQuota ? Number(usage.bytesQuota) : 0
    const usagePercent = bytesQuota > 0 ? (bytesUsed / bytesQuota) * 100 : 0

    if (isLoading) {
        return (
            <div className="flex items-center justify-center text-white/70 light:text-black/70 py-16">
                <Loader loaderText="Loading usage data..." />
            </div>
        )
    }

    return (
        <>
            <div className="px-4">
                <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">Current Usage</div>
                <p className="text-sm text-brand-main-200 mt-1">Storage consumption and quota limits.</p>
            </div>

            <div className="grid grid-cols-2 md:grid-cols-3 gap-3 px-4">
                <MetricCard label="Bytes Used" value={formatBytes(bytesUsed)} subtext={bytesQuota > 0 ? `of ${formatBytes(bytesQuota)} quota` : 'no quota set'} />
                <MetricCard label="Quota" value={bytesQuota > 0 ? formatBytes(bytesQuota) : 'Unlimited'} subtext={bytesQuota > 0 ? `${Math.round(usagePercent)}% used` : 'no limit configured'} />
                <MetricCard label="Objects" value={String(usage?.objectCount ?? 0)} subtext="total stored objects" />
            </div>

            {bytesQuota > 0 && (
                <Card className="rounded-md border-brand-main-600 bg-brand-main-800/50 mx-4">
                    <CardContent className="pt-4">
                        <div className="space-y-2">
                            <div className="flex justify-between text-sm">
                                <span className="text-white/60 light:text-black/60">
                                    {formatBytes(bytesUsed)} of {formatBytes(bytesQuota)} used
                                </span>
                                <span className="text-white/60 light:text-black/60">{Math.round(usagePercent)}%</span>
                            </div>
                            <Progress value={usagePercent} className="h-2" />
                        </div>
                    </CardContent>
                </Card>
            )}

            {!usage && (
                <div className="flex flex-col items-center justify-center py-16 px-4">
                    <div className="relative mb-6">
                        <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                        <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                            <Iconify.Icon icon="lucide:hard-drive" className="size-8 text-brand-secondary-400" />
                        </div>
                    </div>
                    <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No usage data</h3>
                    <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                        Configure a storage backend to start tracking usage and quota consumption.
                    </p>
                </div>
            )}
        </>
    )
}
