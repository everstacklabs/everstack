import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useSandboxContext, SandboxSessionPicker } from './sandbox-context'
import { Loader, toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { ui } from '@everstack/ui'
import { getApiBaseUrl } from '@/lib/api-url'
import { getSandboxPreviewUrl } from '@/server/sandbox'
import { useSession } from '@/hooks/auth/use-auth'

const { Button, Input, Label, Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } = ui

// ─── Types ──────────────────────────────────────────────────────────

interface SandboxPort {
    port: number
    protocol: string
    url: string
    subdomain: string
    status: string
    createdAt: string
}

interface DetectedPort {
    port: number
    protocol: string
    address: string
    pid: number
    process: string
    isExposed: boolean
}

// ─── API Functions ──────────────────────────────────────────────────

const baseUrl = getApiBaseUrl()

async function fetchPorts(sessionId: string): Promise<SandboxPort[]> {
    const res = await fetch(`${baseUrl}/v1/sandbox/${sessionId}/ports`, { credentials: 'include' })
    if (!res.ok) throw new Error(`Failed to fetch ports: ${res.status}`)
    const data = await res.json()
    return data.ports ?? []
}

async function fetchDetectedPorts(sessionId: string): Promise<DetectedPort[]> {
    const res = await fetch(`${baseUrl}/v1/sandbox/${sessionId}/ports/detect`, { credentials: 'include' })
    if (!res.ok) return [] // non-fatal — detection may not be supported
    const data = await res.json()
    return data.ports ?? []
}

async function exposePort(sessionId: string, port: number, protocol: string): Promise<SandboxPort> {
    const res = await fetch(`${baseUrl}/v1/sandbox/${sessionId}/ports`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ port, protocol }),
    })
    if (!res.ok) {
        const text = await res.text()
        throw new Error(`Failed to expose port: ${text}`)
    }
    return res.json()
}

// generateSignedPreviewURL uses the ConnectRPC client (same pattern as stopSandbox, reviveSandbox, etc.)
// so it goes through the correct /everstack.agents.v1.AgentsService/GetSandboxPreviewUrl path.
async function generateSignedPreviewURL(
    tenantId: string,
    sandboxId: string,
    port: number,
    expiresInSeconds = 3600,
): Promise<string> {
    const result = await getSandboxPreviewUrl(tenantId, sandboxId, port, expiresInSeconds)
    return result.url
}

async function unexposePort(sessionId: string, port: number, protocol: string): Promise<void> {
    const res = await fetch(`${baseUrl}/v1/sandbox/${sessionId}/ports/${port}?protocol=${protocol}`, {
        method: 'DELETE',
        credentials: 'include',
    })
    if (!res.ok) {
        const text = await res.text()
        throw new Error(`Failed to unexpose port: ${text}`)
    }
}

// ─── Hooks ──────────────────────────────────────────────────────────

const PORTS_KEY = ['sandbox-ports']
const DETECTED_PORTS_KEY = ['sandbox-detected-ports']

function useSandboxPorts(sessionId: string | undefined) {
    return useQuery({
        queryKey: [...PORTS_KEY, sessionId],
        queryFn: () => fetchPorts(sessionId!),
        enabled: !!sessionId,
        refetchInterval: 5000,
    })
}

function useDetectedPorts(sessionId: string | undefined) {
    return useQuery({
        queryKey: [...DETECTED_PORTS_KEY, sessionId],
        queryFn: () => fetchDetectedPorts(sessionId!),
        enabled: !!sessionId,
        refetchInterval: 5000,
    })
}

function useExposePort(sessionId: string | undefined) {
    const queryClient = useQueryClient()
    return useMutation({
        mutationFn: ({ port, protocol }: { port: number; protocol: string }) =>
            exposePort(sessionId!, port, protocol),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: [...PORTS_KEY, sessionId] })
            queryClient.invalidateQueries({ queryKey: [...DETECTED_PORTS_KEY, sessionId] })
        },
    })
}

function useUnexposePort(sessionId: string | undefined) {
    const queryClient = useQueryClient()
    return useMutation({
        mutationFn: ({ port, protocol }: { port: number; protocol: string }) =>
            unexposePort(sessionId!, port, protocol),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: [...PORTS_KEY, sessionId] })
            queryClient.invalidateQueries({ queryKey: [...DETECTED_PORTS_KEY, sessionId] })
        },
    })
}

// ─── Status Helpers ─────────────────────────────────────────────────

const STATUS_STYLES: Record<string, string> = {
    active: 'bg-green-500/20 text-green-400 light:text-green-600 border-green-500/30',
    listening: 'bg-green-500/20 text-green-400 light:text-green-600 border-green-500/30',
    pending: 'bg-yellow-500/20 text-yellow-400 light:text-yellow-700 border-yellow-500/30',
    closed: 'bg-gray-500/20 text-gray-400 light:text-gray-600 border-gray-500/30',
    error: 'bg-red-500/20 text-red-400 light:text-red-600 border-red-500/30',
}

const STATUS_DOT_COLORS: Record<string, string> = {
    active: 'bg-green-400',
    listening: 'bg-green-400',
    pending: 'bg-yellow-400',
    closed: 'bg-gray-500',
    error: 'bg-red-400',
}

function copyToClipboard(text: string) {
    navigator.clipboard.writeText(text).then(
        () => toast.success('URL copied to clipboard'),
        () => toast.error('Failed to copy URL')
    )
}

// ─── Main Component ─────────────────────────────────────────────────

export function PortsTab() {
    const [dialogOpen, setDialogOpen] = useState(false)
    const [newPort, setNewPort] = useState('')
    // Protocol is fixed to TCP at the moment — the Firecracker backend
    // explicitly rejects non-TCP in ExposePort (see backend.go around
    // L820). Earlier this tab let users pick UDP from a Select, the
    // request would round-trip and fail, and the error toast looked like
    // "we tried, server said no" instead of "this isn't supported."
    // Removing the choice eliminates the confusing failure mode.
    const newProtocol = 'tcp'

    const queryClient = useQueryClient()
    const { activeSessionId: sessionId, activeSandboxId: sandboxId } = useSandboxContext()
    const [sharingPort, setSharingPort] = useState<number | null>(null)
    const { data: session } = useSession()
    const orgId = session?.user?.organizations?.[0]?.id ?? ''

    const { data: ports, isLoading, error } = useSandboxPorts(sessionId)
    const { data: detectedPorts, isLoading: detectedLoading } = useDetectedPorts(sessionId)
    const exposeMutation = useExposePort(sessionId)
    const unexposeMutation = useUnexposePort(sessionId)

    const refreshPorts = () => {
        if (!sessionId) return
        queryClient.invalidateQueries({ queryKey: [...PORTS_KEY, sessionId] })
        queryClient.invalidateQueries({ queryKey: [...DETECTED_PORTS_KEY, sessionId] })
    }

    // Filter detected ports to only show unexposed ones
    const unexposedDetected = (detectedPorts ?? []).filter((dp) => !dp.isExposed)

    const handleExpose = () => {
        const portNum = parseInt(newPort, 10)
        if (isNaN(portNum) || portNum < 1 || portNum > 65535) {
            toast.error('Port must be between 1 and 65535')
            return
        }
        exposeMutation.mutate(
            { port: portNum, protocol: newProtocol },
            {
                onSuccess: () => {
                    toast.success(`Port ${portNum} exposed`)
                    setDialogOpen(false)
                    setNewPort('')
                },
                onError: (err) => toast.error(err.message),
            }
        )
    }

    const handleQuickExpose = (port: number) => {
        exposeMutation.mutate(
            { port, protocol: 'tcp' },
            {
                onSuccess: () => toast.success(`Port ${port} exposed`),
                onError: (err) => toast.error(err.message),
            }
        )
    }

    const handleUnexpose = (port: number, protocol: string) => {
        unexposeMutation.mutate(
            { port, protocol },
            {
                onSuccess: () => toast.success(`Port ${port} closed`),
                onError: (err) => toast.error(err.message),
            }
        )
    }

    const isLocalhost = (addr: string) => addr === '127.0.0.1' || addr === '::1'

    return (
        <div className="flex flex-col h-full">
            {/* Controls */}
            <div className="flex items-center gap-3 px-4 py-2 border-b border-brand-main-600">
                <SandboxSessionPicker />

                <div className="flex-1" />

                <button
                    onClick={refreshPorts}
                    disabled={!sessionId}
                    className="flex items-center gap-1 text-xs text-white/50 light:text-black/50 hover:text-white/80 light:hover:text-black/80 border border-brand-main-600 rounded px-2.5 py-1 hover:border-brand-main-500 disabled:opacity-50"
                    title="Refresh"
                >
                    <Iconify.Icon icon="heroicons:arrow-path" className="size-3" />
                    Refresh
                </button>

                <button
                    onClick={() => setDialogOpen(true)}
                    disabled={!sessionId}
                    className="flex items-center gap-1 text-xs bg-brand-secondary-600/20 text-brand-secondary-300 border border-brand-secondary-500/30 rounded px-2.5 py-1 hover:bg-brand-secondary-600/30 disabled:opacity-50"
                >
                    <Iconify.Icon icon="heroicons:plus" className="size-3" />
                    Expose Port
                </button>
            </div>

            {/* Content */}
            <div className="flex-1 overflow-y-auto">
                {!sessionId ? (
                    <div className="flex flex-col items-center justify-center h-full pb-16">
                        <div className="relative mb-6">
                            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                                <Iconify.Icon icon="heroicons:globe-alt" className="size-8 text-brand-secondary-400" />
                            </div>
                        </div>
                        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No session selected</h3>
                        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                            Select a running session to manage ports.
                        </p>
                    </div>
                ) : isLoading ? (
                    <div className="flex-1 flex items-center justify-center h-full">
                        <Loader loaderText="Loading ports..." />
                    </div>
                ) : error ? (
                    <div className="flex items-center justify-center h-full text-red-400 light:text-red-600 text-sm">
                        Error loading ports: {error.message}
                    </div>
                ) : (
                    <div className="p-4 space-y-4">
                        {/* Detected Ports Section */}
                        {unexposedDetected.length > 0 && (
                            <div>
                                <div className="flex items-center gap-2 mb-2">
                                    <Iconify.Icon icon="heroicons:signal" className="size-3.5 text-blue-400 light:text-blue-600" />
                                    <span className="text-xs text-white/50 light:text-black/50 uppercase font-medium">Detected Ports</span>
                                </div>
                                <div className="flex flex-wrap gap-2">
                                    {unexposedDetected.map((dp) => (
                                        <button
                                            key={`${dp.port}-${dp.protocol}`}
                                            onClick={() => handleQuickExpose(dp.port)}
                                            disabled={exposeMutation.isPending}
                                            className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded border border-brand-main-600 bg-brand-main-800/50 hover:bg-brand-main-700/50 hover:border-brand-secondary-500/40 transition-colors text-xs disabled:opacity-50"
                                            title={`Expose port ${dp.port}${dp.process ? ` (${dp.process})` : ''}`}
                                        >
                                            <span className="font-mono text-white/80 light:text-black/80">{dp.port}</span>
                                            {dp.process && (
                                                <span className="text-white/40 light:text-black/40">({dp.process})</span>
                                            )}
                                            {isLocalhost(dp.address) && (
                                                <span className="px-1 py-0 rounded text-[10px] bg-yellow-500/20 text-yellow-400 light:text-yellow-700 border border-yellow-500/30">
                                                    localhost
                                                </span>
                                            )}
                                            <Iconify.Icon icon="heroicons:plus" className="size-3 text-brand-secondary-400" />
                                        </button>
                                    ))}
                                </div>
                            </div>
                        )}

                        {/* Exposed Ports Table */}
                        {!ports || ports.length === 0 ? (
                            unexposedDetected.length === 0 && (
                                <div className="flex flex-col items-center justify-center py-12">
                                    <div className="relative mb-6">
                                        <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                                        <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                                            <Iconify.Icon icon="heroicons:globe-alt" className="size-8 text-brand-secondary-400" />
                                        </div>
                                    </div>
                                    <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No exposed ports</h3>
                                    <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                                        {detectedLoading
                                            ? 'Scanning for listening processes inside the sandbox…'
                                            : 'Nothing is listening inside the sandbox yet. Start your server (e.g. npm run dev) and detected ports will appear here for one-click expose. You can also expose a port manually with the button above.'}
                                    </p>
                                </div>
                            )
                        ) : (
                            <div>
                                {unexposedDetected.length > 0 && (
                                    <div className="flex items-center gap-2 mb-2">
                                        <Iconify.Icon icon="heroicons:globe-alt" className="size-3.5 text-green-400 light:text-green-600" />
                                        <span className="text-xs text-white/50 light:text-black/50 uppercase font-medium">Exposed Ports</span>
                                    </div>
                                )}
                                <table className="w-full text-sm">
                                    <thead>
                                        <tr className="border-b border-brand-main-600 text-white/40 light:text-black/40 text-xs uppercase">
                                            <th className="text-left py-2 px-3 font-medium">Port</th>
                                            <th className="text-left py-2 px-3 font-medium">Protocol</th>
                                            <th className="text-left py-2 px-3 font-medium">URL</th>
                                            <th className="text-left py-2 px-3 font-medium">Status</th>
                                            <th className="text-left py-2 px-3 font-medium">Created</th>
                                            <th className="text-right py-2 px-3 font-medium">Actions</th>
                                        </tr>
                                    </thead>
                                    <tbody>
                                        {ports.map((p) => (
                                            <tr
                                                key={`${p.port}-${p.protocol}`}
                                                className="border-b border-brand-main-700/50 hover:bg-brand-main-800/50"
                                            >
                                                <td className="py-2.5 px-3 font-mono text-white/80 light:text-black/80">{p.port}</td>
                                                <td className="py-2.5 px-3 text-white/60 light:text-black/60 uppercase text-xs">{p.protocol}</td>
                                                <td className="py-2.5 px-3">
                                                    {p.url ? (
                                                        <div className="flex items-center gap-2 min-w-0">
                                                            <span
                                                                className="font-mono text-xs text-brand-secondary-400 truncate max-w-[280px]"
                                                                title={p.url}
                                                            >
                                                                {p.url}
                                                            </span>
                                                            <button
                                                                onClick={() => copyToClipboard(p.url)}
                                                                className="p-0.5 rounded text-white/40 light:text-black/40 hover:text-white/70 light:hover:text-black/70 hover:bg-white/5 light:hover:bg-black/5 transition-colors shrink-0"
                                                                title="Copy URL"
                                                            >
                                                                <Iconify.Icon icon="heroicons:clipboard-document" className="size-3.5" />
                                                            </button>
                                                            <a
                                                                href={p.url}
                                                                target="_blank"
                                                                rel="noopener noreferrer"
                                                                className="p-0.5 rounded text-white/40 light:text-black/40 hover:text-white/70 light:hover:text-black/70 hover:bg-white/5 light:hover:bg-black/5 transition-colors shrink-0"
                                                                title="Open in new tab"
                                                            >
                                                                <Iconify.Icon icon="heroicons:arrow-top-right-on-square" className="size-3.5" />
                                                            </a>
                                                            {sandboxId && (
                                                                <button
                                                                    disabled={sharingPort === p.port}
                                                                    onClick={async () => {
                                                                        setSharingPort(p.port)
                                                                        try {
                                                                            const url = await generateSignedPreviewURL(orgId, sandboxId, p.port)
                                                                            await navigator.clipboard.writeText(url)
                                                                            toast.success('Signed preview URL copied (1h)')
                                                                        } catch (e) {
                                                                            toast.error('Failed to generate signed URL')
                                                                        } finally {
                                                                            setSharingPort(null)
                                                                        }
                                                                    }}
                                                                    className="p-0.5 rounded text-white/40 light:text-black/40 hover:text-brand-secondary-400 hover:bg-white/5 light:hover:bg-black/5 transition-colors shrink-0 disabled:opacity-40"
                                                                    title="Copy signed share URL (1h expiry)"
                                                                >
                                                                    <Iconify.Icon icon={sharingPort === p.port ? 'heroicons:arrow-path' : 'heroicons:share'} className="size-3.5" />
                                                                </button>
                                                            )}
                                                        </div>
                                                    ) : (
                                                        <span className="text-white/30 light:text-black/30 text-xs">--</span>
                                                    )}
                                                </td>
                                                <td className="py-2.5 px-3">
                                                    <span className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded text-xs font-medium border ${STATUS_STYLES[p.status] ?? STATUS_STYLES.closed}`}>
                                                        <span className={`size-1.5 rounded-full ${STATUS_DOT_COLORS[p.status] ?? STATUS_DOT_COLORS.closed}`} />
                                                        {p.status}
                                                    </span>
                                                </td>
                                                <td className="py-2.5 px-3 text-white/50 light:text-black/50 text-xs">
                                                    {p.createdAt ? new Date(p.createdAt).toLocaleString() : '--'}
                                                </td>
                                                <td className="py-2.5 px-3 text-right">
                                                    <button
                                                        onClick={() => handleUnexpose(p.port, p.protocol)}
                                                        disabled={unexposeMutation.isPending}
                                                        className="p-1 rounded text-red-400 light:text-red-600 hover:text-red-300 light:hover:text-red-600 hover:bg-red-500/10 transition-colors disabled:opacity-50"
                                                        title="Close port"
                                                    >
                                                        <Iconify.Icon icon="heroicons:x-mark" className="size-4" />
                                                    </button>
                                                </td>
                                            </tr>
                                        ))}
                                    </tbody>
                                </table>
                            </div>
                        )}
                    </div>
                )}
            </div>

            {/* Expose Port Dialog */}
            <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
                <DialogContent className="bg-brand-main-900 border-brand-main-600">
                    <DialogHeader>
                        <DialogTitle className="text-white light:text-brand-main-50">Expose Port</DialogTitle>
                    </DialogHeader>
                    <div className="space-y-4 py-2">
                        <div className="space-y-1.5">
                            <Label htmlFor="portNumber" className="text-xs text-white/60 light:text-black/60">Port Number</Label>
                            <Input
                                id="portNumber"
                                type="number"
                                value={newPort}
                                onChange={(e) => setNewPort(e.target.value)}
                                placeholder="e.g. 3000"
                                min={1}
                                max={65535}
                                className="w-full"
                                onKeyDown={(e) => {
                                    if (e.key === 'Enter') handleExpose()
                                }}
                            />
                            <p className="text-[11px] text-white/40 light:text-black/40 leading-relaxed">
                                Protocol: TCP. UDP forwarding is not currently supported by
                                the sandbox backend.
                            </p>
                        </div>
                    </div>
                    <DialogFooter>
                        <Button
                            variant="outline"
                            onClick={() => setDialogOpen(false)}
                        >
                            Cancel
                        </Button>
                        <Button
                            onClick={handleExpose}
                            disabled={exposeMutation.isPending || !newPort}
                        >
                            {exposeMutation.isPending ? 'Exposing...' : 'Expose'}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    )
}
