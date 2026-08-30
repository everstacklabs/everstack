import { useCallback, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Iconify } from '@everstack/ui/icons'
import { Loader, toast } from '@everstack/ui/components'
import { useSandboxContext } from './sandbox-context'
import { isSandboxRunning } from './lifecycle'
import {
    listSandboxFs,
    uploadSandboxFile,
    downloadSandboxFile,
    mkdirSandboxFs,
    deleteSandboxFs,
    type SandboxFsEntry,
} from '@/server/sandbox'

// FilesTab is a Daytona-style file browser over the sandbox fs API:
// breadcrumb navigation, upload into the current directory, download
// and delete per entry, new-folder. Defaults to /workspace (the
// workspace) since that is where user data lives.

const filesQueryKey = (sandboxId: string, path: string) =>
    ['sandbox', sandboxId, 'fs', path] as const

function formatSize(size?: number): string {
    if (size === undefined || size === null) return ''
    if (size < 1024) return `${size} B`
    if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
    if (size < 1024 * 1024 * 1024) return `${(size / (1024 * 1024)).toFixed(1)} MB`
    return `${(size / (1024 * 1024 * 1024)).toFixed(2)} GB`
}

function entryIsDir(e: SandboxFsEntry): boolean {
    return Boolean(e.is_dir ?? e.isDir)
}

export function FilesTab() {
    const { instances, activeSandboxId } = useSandboxContext()
    const inst = instances.find((i) => i.id === activeSandboxId)
    const running = inst ? isSandboxRunning(inst) : false

    const [path, setPath] = useState('/workspace')
    const queryClient = useQueryClient()
    const fileInputRef = useRef<HTMLInputElement>(null)
    const [busy, setBusy] = useState(false)

    const { data, isLoading, error, refetch } = useQuery({
        queryKey: activeSandboxId ? filesQueryKey(activeSandboxId, path) : ['sandbox-fs-idle'],
        queryFn: () => listSandboxFs(activeSandboxId!, path),
        enabled: Boolean(activeSandboxId) && running,
        staleTime: 5_000,
    })

    const invalidate = useCallback(() => {
        if (activeSandboxId) {
            queryClient.invalidateQueries({ queryKey: filesQueryKey(activeSandboxId, path) })
        }
    }, [queryClient, activeSandboxId, path])

    const handleUpload = useCallback(
        async (files: FileList | null) => {
            if (!files || files.length === 0 || !activeSandboxId) return
            setBusy(true)
            try {
                for (const file of Array.from(files)) {
                    await uploadSandboxFile(activeSandboxId, `${path}/${file.name}`, file)
                }
                toast.success(files.length === 1 ? `Uploaded ${files[0].name}` : `Uploaded ${files.length} files`)
                invalidate()
            } catch (e) {
                toast.error(`Upload failed: ${(e as Error).message}`)
            } finally {
                setBusy(false)
                if (fileInputRef.current) fileInputRef.current.value = ''
            }
        },
        [activeSandboxId, path, invalidate],
    )

    const handleDownload = useCallback(
        async (entry: SandboxFsEntry) => {
            if (!activeSandboxId) return
            try {
                const blob = await downloadSandboxFile(activeSandboxId, `${path}/${entry.name}`)
                const url = URL.createObjectURL(blob)
                const a = document.createElement('a')
                a.href = url
                a.download = entry.name
                a.click()
                URL.revokeObjectURL(url)
            } catch (e) {
                toast.error(`Download failed: ${(e as Error).message}`)
            }
        },
        [activeSandboxId, path],
    )

    const handleDelete = useCallback(
        async (entry: SandboxFsEntry) => {
            if (!activeSandboxId) return
            const target = `${path}/${entry.name}`
            if (!window.confirm(`Delete ${target}${entryIsDir(entry) ? ' and all its contents' : ''}?`)) return
            try {
                await deleteSandboxFs(activeSandboxId, target, entryIsDir(entry))
                toast.success(`Deleted ${entry.name}`)
                invalidate()
            } catch (e) {
                toast.error(`Delete failed: ${(e as Error).message}`)
            }
        },
        [activeSandboxId, path, invalidate],
    )

    const handleNewFolder = useCallback(async () => {
        if (!activeSandboxId) return
        const name = window.prompt('Folder name')
        if (!name?.trim()) return
        try {
            await mkdirSandboxFs(activeSandboxId, `${path}/${name.trim()}`)
            invalidate()
        } catch (e) {
            toast.error(`Create folder failed: ${(e as Error).message}`)
        }
    }, [activeSandboxId, path, invalidate])

    if (!activeSandboxId) {
        return <EmptyState message="Select a sandbox to browse its files." />
    }
    if (!running) {
        return <EmptyState message="Files are only browsable while the sandbox is started." />
    }

    const segments = path.split('/').filter(Boolean)
    const entries = [...(data?.files ?? [])].sort((a, b) => {
        if (entryIsDir(a) !== entryIsDir(b)) return entryIsDir(a) ? -1 : 1
        return a.name.localeCompare(b.name)
    })

    return (
        <div className="flex flex-col h-full p-4 gap-3 overflow-hidden">
            {/* Toolbar: breadcrumbs + actions */}
            <div className="flex items-center gap-2">
                <nav className="flex items-center gap-1 text-sm flex-1 min-w-0 overflow-x-auto">
                    <button
                        onClick={() => setPath('/')}
                        className="text-brand-secondary-400 hover:text-brand-secondary-300 shrink-0"
                    >
                        /
                    </button>
                    {segments.map((seg, i) => (
                        <span key={i} className="flex items-center gap-1 shrink-0">
                            <button
                                onClick={() => setPath('/' + segments.slice(0, i + 1).join('/'))}
                                className={i === segments.length - 1 ? 'text-white/90 light:text-black/90' : 'text-brand-secondary-400 hover:text-brand-secondary-300'}
                            >
                                {seg}
                            </button>
                            {i < segments.length - 1 && <span className="text-white/30 light:text-black/30">/</span>}
                        </span>
                    ))}
                </nav>
                <button
                    onClick={() => refetch()}
                    className="p-1.5 rounded text-white/60 light:text-black/60 hover:text-white light:hover:text-brand-main-50 hover:bg-brand-main-700/60"
                    title="Refresh"
                    aria-label="Refresh file list"
                >
                    <Iconify.Icon icon="heroicons:arrow-path" className="size-4" />
                </button>
                <button
                    onClick={handleNewFolder}
                    className="flex items-center gap-1.5 px-2.5 py-1.5 text-xs rounded border border-brand-main-600 text-white/75 light:text-black/75 hover:text-white light:hover:text-brand-main-50 hover:bg-brand-main-700/60"
                >
                    <Iconify.Icon icon="heroicons:folder-plus" className="size-4" />
                    New folder
                </button>
                <button
                    onClick={() => fileInputRef.current?.click()}
                    disabled={busy}
                    className="flex items-center gap-1.5 px-2.5 py-1.5 text-xs rounded bg-brand-secondary-600/20 border border-brand-secondary-500/40 text-brand-secondary-200 hover:bg-brand-secondary-600/30 disabled:opacity-50"
                >
                    <Iconify.Icon icon="heroicons:arrow-up-tray" className="size-4" />
                    {busy ? 'Uploading…' : 'Upload'}
                </button>
                <input
                    ref={fileInputRef}
                    type="file"
                    multiple
                    className="hidden"
                    onChange={(e) => handleUpload(e.target.files)}
                />
            </div>

            {/* Listing */}
            <div className="flex-1 min-h-0 overflow-y-auto rounded border border-brand-main-700 bg-brand-main-900/40">
                {isLoading ? (
                    <div className="flex items-center justify-center h-32">
                        <Loader loaderText="Loading files..." />
                    </div>
                ) : error ? (
                    <div className="p-4 text-sm text-white/60 light:text-black/60">
                        Failed to list directory: {(error as Error).message}
                    </div>
                ) : entries.length === 0 ? (
                    <div className="p-6 text-sm text-white/40 light:text-black/40 text-center">Empty directory</div>
                ) : (
                    <table className="w-full text-sm">
                        <thead>
                            <tr className="text-left text-xs uppercase tracking-wider text-white/40 light:text-black/40 border-b border-brand-main-700">
                                <th className="px-3 py-2 font-medium">Name</th>
                                <th className="px-3 py-2 font-medium w-28">Size</th>
                                <th className="px-3 py-2 font-medium w-24" />
                            </tr>
                        </thead>
                        <tbody>
                            {path !== '/' && (
                                <tr
                                    className="border-b border-brand-main-800 hover:bg-brand-main-800/40 cursor-pointer"
                                    onClick={() => setPath('/' + segments.slice(0, -1).join('/') || '/')}
                                >
                                    <td className="px-3 py-2 text-white/60 light:text-black/60" colSpan={3}>
                                        <span className="inline-flex items-center gap-2">
                                            <Iconify.Icon icon="heroicons:arrow-uturn-left" className="size-4 text-white/40 light:text-black/40" />
                                            ..
                                        </span>
                                    </td>
                                </tr>
                            )}
                            {entries.map((entry) => (
                                <tr
                                    key={entry.name}
                                    className="border-b border-brand-main-800 last:border-0 hover:bg-brand-main-800/40"
                                >
                                    <td
                                        className={`px-3 py-2 ${entryIsDir(entry) ? 'cursor-pointer' : ''}`}
                                        onClick={() => entryIsDir(entry) && setPath(`${path === '/' ? '' : path}/${entry.name}`)}
                                    >
                                        <span className="inline-flex items-center gap-2 text-white/85 light:text-black/85">
                                            <Iconify.Icon
                                                icon={entryIsDir(entry) ? 'heroicons:folder' : 'heroicons:document'}
                                                className={`size-4 ${entryIsDir(entry) ? 'text-brand-secondary-400' : 'text-white/40 light:text-black/40'}`}
                                            />
                                            {entry.name}
                                        </span>
                                    </td>
                                    <td className="px-3 py-2 text-white/45 light:text-black/45 text-xs">
                                        {entryIsDir(entry) ? '' : formatSize(entry.size)}
                                    </td>
                                    <td className="px-3 py-2">
                                        <div className="flex items-center justify-end gap-1">
                                            {!entryIsDir(entry) && (
                                                <button
                                                    onClick={() => handleDownload(entry)}
                                                    className="p-1 rounded text-white/45 light:text-black/45 hover:text-white light:hover:text-brand-main-50 hover:bg-brand-main-700/60"
                                                    title="Download"
                                                    aria-label={`Download ${entry.name}`}
                                                >
                                                    <Iconify.Icon icon="heroicons:arrow-down-tray" className="size-4" />
                                                </button>
                                            )}
                                            <button
                                                onClick={() => handleDelete(entry)}
                                                className="p-1 rounded text-white/45 light:text-black/45 hover:text-red-400 light:hover:text-red-600 hover:bg-red-500/10"
                                                title="Delete"
                                                aria-label={`Delete ${entry.name}`}
                                            >
                                                <Iconify.Icon icon="heroicons:trash" className="size-4" />
                                            </button>
                                        </div>
                                    </td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                )}
            </div>
        </div>
    )
}

function EmptyState({ message }: { message: string }) {
    return (
        <div className="flex flex-col items-center justify-center h-full pb-16">
            <Iconify.Icon icon="heroicons:folder-open" className="size-8 text-white/25 light:text-black/25 mb-3" />
            <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">{message}</p>
        </div>
    )
}
