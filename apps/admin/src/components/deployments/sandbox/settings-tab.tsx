import { useEffect, useState } from 'react'
import { toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { useSandboxContext } from './sandbox-context'
import { sandboxStatusLabel } from './lifecycle'
import { useUpdateSandboxAutoIntervals } from '@/hooks/deployments/use-sandbox'

// SettingsTab: per-sandbox configuration (Daytona-style auto-lifecycle
// intervals) plus the identity card. The legacy session id lives here
// under a troubleshooting block, deliberately out of the main surfaces.

const MINUTES_PER_DAY = 1440

function formatInterval(mins?: number): string {
    if (mins === undefined || mins === null) return 'default'
    if (mins < 0) return 'never'
    if (mins === 0) return 'disabled'
    if (mins % MINUTES_PER_DAY === 0) return `${mins / MINUTES_PER_DAY}d`
    if (mins % 60 === 0) return `${mins / 60}h`
    return `${mins}m`
}

export function SettingsTab() {
    const { instances, activeSandboxId } = useSandboxContext()
    const inst = instances.find((i) => i.id === activeSandboxId)
    const updateIntervals = useUpdateSandboxAutoIntervals()

    const [autoStop, setAutoStop] = useState('')
    const [autoArchive, setAutoArchive] = useState('')
    const [autoDelete, setAutoDelete] = useState('')

    // Seed the editors from the instance once it loads (and re-seed on
    // sandbox switch).
    useEffect(() => {
        setAutoStop(inst?.autoStopInterval !== undefined ? String(inst.autoStopInterval) : '')
        setAutoArchive(inst?.autoArchiveInterval !== undefined ? String(inst.autoArchiveInterval) : '')
        setAutoDelete(inst?.autoDeleteInterval !== undefined ? String(inst.autoDeleteInterval) : '')
    }, [inst?.id, inst?.autoStopInterval, inst?.autoArchiveInterval, inst?.autoDeleteInterval])

    if (!inst) {
        return (
            <div className="flex items-center justify-center h-full text-sm text-white/50 light:text-black/50">
                Sandbox not found.
            </div>
        )
    }

    const handleSave = () => {
        const parse = (v: string, min: number): number | undefined => {
            const trimmed = v.trim()
            if (trimmed === '') return undefined
            const n = Number(trimmed)
            if (!Number.isInteger(n) || n < min) return undefined
            return n
        }
        const payload = {
            sandboxId: inst.id,
            autoStopInterval: parse(autoStop, 0),
            autoArchiveInterval: parse(autoArchive, 0),
            autoDeleteInterval: parse(autoDelete, -1),
        }
        if (
            payload.autoStopInterval === undefined &&
            payload.autoArchiveInterval === undefined &&
            payload.autoDeleteInterval === undefined
        ) {
            toast.error('Nothing to save (values must be whole minutes)')
            return
        }
        updateIntervals.mutate(payload, {
            onSuccess: () => toast.success('Auto-lifecycle intervals updated'),
            onError: (e) => toast.error(`Update failed: ${e.message}`),
        })
    }

    return (
        <div className="h-full overflow-y-auto p-4 space-y-4 max-w-3xl">
            {/* Identity */}
            <section className="rounded-lg border border-brand-main-700 bg-brand-main-900/50 p-4">
                <h3 className="text-sm font-medium text-white/80 light:text-black/80 mb-3">Sandbox</h3>
                <dl className="grid grid-cols-[140px_1fr] gap-y-2 text-sm">
                    <dt className="text-white/45 light:text-black/45">Name</dt>
                    <dd className="text-white/85 light:text-black/85">{inst.name?.trim() || '(unnamed)'}</dd>
                    <dt className="text-white/45 light:text-black/45">State</dt>
                    <dd className="text-white/85 light:text-black/85">{sandboxStatusLabel(inst)}</dd>
                    <dt className="text-white/45 light:text-black/45">ID</dt>
                    <dd className="text-white/85 light:text-black/85 font-mono text-xs">{inst.id}</dd>
                    {inst.shortCode && (
                        <>
                            <dt className="text-white/45 light:text-black/45">Short code</dt>
                            <dd className="text-white/85 light:text-black/85 font-mono text-xs">{inst.shortCode}</dd>
                        </>
                    )}
                    <dt className="text-white/45 light:text-black/45">Image</dt>
                    <dd className="text-white/85 light:text-black/85 font-mono text-xs">{inst.image}</dd>
                    <dt className="text-white/45 light:text-black/45">Backend</dt>
                    <dd className="text-white/85 light:text-black/85">{inst.backend}</dd>
                    <dt className="text-white/45 light:text-black/45">Created</dt>
                    <dd className="text-white/85 light:text-black/85">{inst.createdAt ? new Date(inst.createdAt).toLocaleString() : ''}</dd>
                    {inst.errorReason && (
                        <>
                            <dt className="text-white/45 light:text-black/45">Error reason</dt>
                            <dd className="text-brand-secondary-200 font-mono text-xs">{inst.errorReason}</dd>
                        </>
                    )}
                </dl>
            </section>

            {/* Auto-lifecycle */}
            <section className="rounded-lg border border-brand-main-700 bg-brand-main-900/50 p-4">
                <h3 className="text-sm font-medium text-white/80 light:text-black/80 mb-1">Auto-lifecycle</h3>
                <p className="text-xs text-white/45 light:text-black/45 mb-4 leading-relaxed">
                    Intervals are in minutes. Auto-stop counts from the last activity (shell input,
                    commands, preview traffic); auto-archive and auto-delete count from when the
                    sandbox stopped.
                </p>
                <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
                    <IntervalField
                        label="Auto-stop"
                        hint="0 disables"
                        current={formatInterval(inst.autoStopInterval)}
                        value={autoStop}
                        onChange={setAutoStop}
                    />
                    <IntervalField
                        label="Auto-archive"
                        hint="0 disables"
                        current={formatInterval(inst.autoArchiveInterval)}
                        value={autoArchive}
                        onChange={setAutoArchive}
                    />
                    <IntervalField
                        label="Auto-delete"
                        hint="-1 never, 0 on stop"
                        current={formatInterval(inst.autoDeleteInterval)}
                        value={autoDelete}
                        onChange={setAutoDelete}
                    />
                </div>
                <div className="mt-4 flex justify-end">
                    <button
                        onClick={handleSave}
                        disabled={updateIntervals.isPending}
                        className="flex items-center gap-1.5 px-3 py-1.5 text-xs rounded bg-brand-secondary-600/20 border border-brand-secondary-500/40 text-brand-secondary-200 hover:bg-brand-secondary-600/30 disabled:opacity-50"
                    >
                        <Iconify.Icon icon="heroicons:check" className="size-4" />
                        {updateIntervals.isPending ? 'Saving…' : 'Save intervals'}
                    </button>
                </div>
            </section>

            {/* Troubleshooting: the one place the legacy session id is
                visible, for support correlation with gateway logs. */}
            <details className="rounded-lg border border-brand-main-700 bg-brand-main-900/30 p-4">
                <summary className="text-xs text-white/45 light:text-black/45 cursor-pointer select-none">
                    Troubleshooting details
                </summary>
                <dl className="grid grid-cols-[140px_1fr] gap-y-2 text-sm mt-3">
                    <dt className="text-white/45 light:text-black/45">Session ID</dt>
                    <dd className="text-white/70 light:text-black/70 font-mono text-xs break-all">{inst.sessionId}</dd>
                    <dt className="text-white/45 light:text-black/45">Container ID</dt>
                    <dd className="text-white/70 light:text-black/70 font-mono text-xs break-all">{inst.containerId || '(none)'}</dd>
                    <dt className="text-white/45 light:text-black/45">Lifecycle state</dt>
                    <dd className="text-white/70 light:text-black/70 font-mono text-xs">{inst.lifecycleState || inst.status}</dd>
                    <dt className="text-white/45 light:text-black/45">Desired state</dt>
                    <dd className="text-white/70 light:text-black/70 font-mono text-xs">{inst.desiredState || '(unknown)'}</dd>
                </dl>
            </details>
        </div>
    )
}

function IntervalField({
    label,
    hint,
    current,
    value,
    onChange,
}: {
    label: string
    hint: string
    current: string
    value: string
    onChange: (v: string) => void
}) {
    return (
        <label className="flex flex-col gap-1">
            <span className="text-xs text-white/55 light:text-black/55">
                {label} <span className="text-white/30 light:text-black/30">({hint})</span>
            </span>
            <input
                type="number"
                value={value}
                onChange={(e) => onChange(e.target.value)}
                placeholder={current}
                className="bg-brand-main-800 border border-brand-main-600 rounded px-2.5 py-1.5 text-sm text-white light:text-brand-main-50 placeholder:text-white/30 light:placeholder:text-black/30 focus:outline-none focus:border-brand-secondary-500/50"
            />
            <span className="text-[10px] text-white/35 light:text-black/35">current: {current}</span>
        </label>
    )
}
