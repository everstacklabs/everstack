import { useEffect, useState } from 'react'
import { Iconify } from '@everstack/ui/icons'
import { Loader } from '@everstack/ui/components'
import {
    useGatewayStatus,
    useEgressEvents,
    useEgressPolicy,
    useSandboxStatsStream,
} from '@/hooks/deployments/use-sandbox'
import { useSandboxContext, SandboxPicker } from './sandbox-context'
import type { EgressEvent } from '@/server/sandbox'

// ─── Constants ──────────────────────────────────────────────────────

type EgressFilter = 'all' | 'allowed' | 'blocked'

const EMPTY_EVENTS: EgressEvent[] = []

function formatBytes(bytes: number): string {
    if (!bytes || bytes < 0) return '0 B'
    const k = 1024
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
    const i = Math.min(sizes.length - 1, Math.floor(Math.log(bytes) / Math.log(k)))
    return `${(bytes / Math.pow(k, i)).toFixed(i === 0 ? 0 : 1)} ${sizes[i]}`
}

const ACTION_STYLES: Record<string, string> = {
    allowed: 'bg-green-500/20 text-green-400 light:text-green-600 border-green-500/30',
    blocked: 'bg-red-500/20 text-red-400 light:text-red-600 border-red-500/30',
}

const ACTION_DOT_COLORS: Record<string, string> = {
    allowed: 'bg-green-400',
    blocked: 'bg-red-400',
}

const MODE_STYLES: Record<string, string> = {
    allow: 'bg-green-500/20 text-green-400 light:text-green-600 border-green-500/30',
    whitelist: 'bg-yellow-500/20 text-yellow-400 light:text-yellow-700 border-yellow-500/30',
    deny: 'bg-red-500/20 text-red-400 light:text-red-600 border-red-500/30',
}

// ─── Main Component ─────────────────────────────────────────────────

export function NetworkTab() {
    const { data: gateway, isLoading: gwLoading, error: gwError } = useGatewayStatus()
    const { activeSandboxId, activeSessionId } = useSandboxContext()
    const [egressFilter, setEgressFilter] = useState<EgressFilter>('all')

    const { data: egressData, isLoading: egressLoading } = useEgressEvents(
        activeSandboxId,
        egressFilter !== 'all' ? { action: egressFilter, limit: 100 } : { limit: 100 }
    )
    const { data: egressPolicy } = useEgressPolicy(activeSandboxId)

    // Live data transfer (RX/TX bytes) for the selected sandbox session.
    // Unlike egress events (DNS allow/block decisions, only logged in
    // whitelist mode), byte counters populate in every network mode.
    const { latestStats, isStreaming, start, stop } = useSandboxStatsStream(activeSessionId)
    useEffect(() => {
        if (activeSessionId) start()
        return () => stop()
    }, [activeSessionId]) // eslint-disable-line react-hooks/exhaustive-deps

    const egressEvents = egressData?.events ?? EMPTY_EVENTS

    return (
        <div className="flex flex-col h-full">
            {/* Controls */}
            <div className="flex items-center gap-3 px-4 py-2 border-b border-brand-main-600">
                <SandboxPicker />

                {/* Filter chips */}
                <div className="flex items-center gap-1.5">
                    {(['all', 'allowed', 'blocked'] as const).map((f) => (
                        <button
                            key={f}
                            onClick={() => setEgressFilter(f)}
                            className={`px-2 py-0.5 text-[10px] rounded border transition-colors capitalize select-none cursor-pointer ${
                                egressFilter === f
                                    ? 'border-brand-secondary-500/30 bg-brand-secondary-600/20 text-brand-secondary-300'
                                    : 'border-brand-main-600 text-white/40 light:text-black/40 hover:text-white/60 light:hover:text-black/60'
                            } light:hover:text-black/60`}
                        >
                            {f}
                        </button>
                    ))}
                </div>

                {/* Gateway health */}
                {!gwLoading && !gwError && gateway && (
                    <span className="flex items-center gap-1.5 text-xs text-white/50 light:text-black/50">
                        <span className={`size-1.5 rounded-full ${gateway.healthy ? 'bg-green-400' : 'bg-red-400'}`} />
                        Gateway {gateway.healthy ? 'up' : 'down'}
                    </span>
                )}

                <div className="flex-1" />

                {/* Gateway summary */}
                {!gwLoading && gateway && (
                    <span className="text-[10px] text-white/30 light:text-black/30 font-mono">
                        {gateway.activeRoutes} routes &middot; {gateway.baseDomain || 'no domain'} &middot; TLS {gateway.tlsEnabled ? 'on' : 'off'}
                    </span>
                )}
            </div>

            {/* Content */}
            <div className="flex-1 overflow-y-auto">
                {gwLoading ? (
                    <div className="flex-1 flex items-center justify-center h-full">
                        <Loader loaderText="Loading network status..." />
                    </div>
                ) : gwError ? (
                    <div className="flex items-center justify-center h-full text-red-400 light:text-red-600 text-sm">
                        Error loading gateway: {gwError.message}
                    </div>
                ) : !activeSandboxId ? (
                    <div className="flex flex-col items-center justify-center h-full pb-16">
                        <div className="relative mb-6">
                            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                                <Iconify.Icon icon="heroicons:signal" className="size-8 text-brand-secondary-400" />
                            </div>
                        </div>
                        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No sandbox selected</h3>
                        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                            Select a sandbox to view network activity.
                        </p>
                    </div>
                ) : egressLoading ? (
                    <div className="flex-1 flex items-center justify-center h-full">
                        <Loader loaderText="Loading egress events..." />
                    </div>
                ) : (
                    <div className="p-4 space-y-4">
                        {/* Data transfer (live RX/TX) */}
                        <div>
                            <div className="flex items-center gap-2 mb-2">
                                <Iconify.Icon icon="heroicons:arrows-up-down" className="size-3.5 text-brand-secondary-400" />
                                <span className="text-xs text-white/50 light:text-black/50 uppercase font-medium">Data Transfer</span>
                                {isStreaming && (
                                    <span className="flex items-center gap-1 text-[10px] text-green-400 light:text-green-600">
                                        <span className="size-1.5 rounded-full bg-green-500 animate-pulse" />
                                        Live
                                    </span>
                                )}
                            </div>
                            <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
                                <DataTransferCard
                                    label="Ingress"
                                    sublabel="received (RX)"
                                    icon="heroicons:arrow-down-tray"
                                    value={latestStats?.networkRxBytes ?? 0}
                                />
                                <DataTransferCard
                                    label="Egress"
                                    sublabel="transmitted (TX)"
                                    icon="heroicons:arrow-up-tray"
                                    value={latestStats?.networkTxBytes ?? 0}
                                />
                                <DataTransferCard
                                    label="Total"
                                    sublabel="in + out"
                                    icon="heroicons:arrows-up-down"
                                    value={(latestStats?.networkRxBytes ?? 0) + (latestStats?.networkTxBytes ?? 0)}
                                />
                            </div>
                            {!latestStats && (
                                <p className="mt-2 text-xs text-white/30 light:text-black/30">
                                    Waiting for live stats from the sandbox agent…
                                </p>
                            )}
                        </div>

                        {/* Egress policy */}
                        {egressPolicy && (
                            <div>
                                <div className="flex items-center gap-2 mb-2">
                                    <Iconify.Icon icon="heroicons:shield-check" className="size-3.5 text-brand-secondary-400" />
                                    <span className="text-xs text-white/50 light:text-black/50 uppercase font-medium">Egress Policy</span>
                                </div>
                                <div className="flex flex-wrap items-center gap-2">
                                    <span className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded text-xs font-medium border ${MODE_STYLES[egressPolicy.mode] ?? 'bg-gray-500/20 text-gray-400 light:text-gray-600 border-gray-500/30'}`}>
                                        {egressPolicy.mode}
                                    </span>
                                    {egressPolicy.allowedHosts.map((host) => (
                                        <span
                                            key={host}
                                            className="inline-flex items-center px-2 py-0.5 rounded text-xs font-mono text-white/50 light:text-black/50 bg-brand-main-800/50 border border-brand-main-600"
                                        >
                                            {host}
                                        </span>
                                    ))}
                                </div>
                            </div>
                        )}

                        {/* Egress events table */}
                        {egressEvents.length === 0 ? (
                            <div className="flex flex-col items-center justify-center py-12">
                                <div className="relative mb-6">
                                    <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                                    <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                                        <Iconify.Icon icon="heroicons:signal" className="size-8 text-brand-secondary-400" />
                                    </div>
                                </div>
                                <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No egress events</h3>
                                <p className="text-sm text-white/50 light:text-black/50 max-w-md text-center leading-relaxed">
                                    {egressPolicy?.mode === 'allow' ? (
                                        <>
                                            Network mode is <span className="text-white/80 light:text-black/80">allow</span> &mdash;
                                            all outbound traffic is unrestricted, so no per-domain decisions are
                                            logged. Audit events appear here when the sandbox runs in
                                            <span className="text-white/80 light:text-black/80"> whitelist</span> mode and the
                                            per-VM DNS proxy gates each lookup.
                                        </>
                                    ) : egressPolicy?.mode === 'deny' ? (
                                        <>
                                            Network mode is <span className="text-white/80 light:text-black/80">deny</span> &mdash;
                                            the sandbox has no outbound network and the egress controller is not
                                            engaged.
                                        </>
                                    ) : (
                                        <>
                                            DNS queries will appear here as the sandbox makes outbound requests.
                                            If you expected events but see none, check the sandbox events tab
                                            for <span className="font-mono text-white/70 light:text-black/70">network.config</span>
                                            entries from the sandbox agent &mdash; they show whether the
                                            per-sandbox iptables and DNS proxy actually came up.
                                        </>
                                    )}
                                </p>
                            </div>
                        ) : (
                            <div>
                                <div className="flex items-center gap-2 mb-2">
                                    <Iconify.Icon icon="heroicons:arrow-trending-up" className="size-3.5 text-brand-secondary-400" />
                                    <span className="text-xs text-white/50 light:text-black/50 uppercase font-medium">Egress Events</span>
                                </div>
                                <table className="w-full text-sm">
                                    <thead>
                                        <tr className="border-b border-brand-main-600 text-white/40 light:text-black/40 text-xs uppercase">
                                            <th className="text-left py-2 px-3 font-medium">Domain</th>
                                            <th className="text-left py-2 px-3 font-medium">Action</th>
                                            <th className="text-left py-2 px-3 font-medium">Query Type</th>
                                            <th className="text-left py-2 px-3 font-medium">Timestamp</th>
                                        </tr>
                                    </thead>
                                    <tbody>
                                        {egressEvents.map((evt) => (
                                            <tr
                                                key={evt.id}
                                                className="border-b border-brand-main-700/50 hover:bg-brand-main-800/50"
                                            >
                                                <td className="py-2.5 px-3 font-mono text-white/80 light:text-black/80">{evt.domain}</td>
                                                <td className="py-2.5 px-3">
                                                    <span className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded text-xs font-medium border ${ACTION_STYLES[evt.action] ?? ACTION_STYLES.allowed}`}>
                                                        <span className={`size-1.5 rounded-full ${ACTION_DOT_COLORS[evt.action] ?? ACTION_DOT_COLORS.allowed}`} />
                                                        {evt.action}
                                                    </span>
                                                </td>
                                                <td className="py-2.5 px-3 text-white/60 light:text-black/60 uppercase text-xs">{evt.queryType}</td>
                                                <td className="py-2.5 px-3 text-white/50 light:text-black/50 text-xs">
                                                    {evt.createdAt ? new Date(evt.createdAt).toLocaleString() : '--'}
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
        </div>
    )
}

// ─── Data Transfer Card ─────────────────────────────────────────────

function DataTransferCard({ label, sublabel, icon, value }: {
    label: string
    sublabel: string
    icon: string
    value: number
}) {
    return (
        <div className="rounded-lg border border-brand-main-600 bg-brand-main-800/50 p-3">
            <div className="flex items-center gap-1.5 mb-1">
                <Iconify.Icon icon={icon} className="size-3.5 text-white/40 light:text-black/40" />
                <span className="text-xs text-white/50 light:text-black/50">{label}</span>
            </div>
            <p className="text-xl font-bold text-white light:text-brand-main-50 font-mono">{formatBytes(value)}</p>
            <p className="text-[10px] text-white/30 light:text-black/30">{sublabel}</p>
        </div>
    )
}
