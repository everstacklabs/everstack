import { createContext, useContext, useMemo, type ReactNode } from 'react'
import { useSandboxInstances } from '@/hooks/deployments/use-sandbox'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@everstack/ui/components'
import type { SandboxInstance } from '@/server/sandbox'
import { useNavigate } from '@tanstack/react-router'
import { isSandboxRunning, sandboxStatusLabel } from './lifecycle'

// ─── Context ─────────────────────────────────────────────────────────

interface SandboxContextValue {
    instances: SandboxInstance[]
    isLoading: boolean
    selectedSessionId: string
    selectedSandboxId: string
    setSelectedSessionId: (id: string) => void
    setSelectedSandboxId: (id: string) => void
    activeSessionId: string | undefined
    activeSandboxId: string | undefined
    // pinned: the provider is scoped to exactly one sandbox (the
    // detail page). Pickers render a static label and selection
    // setters are no-ops.
    pinned: boolean
}

const SandboxContext = createContext<SandboxContextValue | null>(null)

export function useSandboxContext(): SandboxContextValue {
    const ctx = useContext(SandboxContext)
    if (!ctx) {
        throw new Error('useSandboxContext must be used within a SandboxProvider')
    }
    return ctx
}

// ─── Provider ────────────────────────────────────────────────────────

interface SandboxProviderProps {
    children: ReactNode
    initialSessionId?: string
    initialSandboxId?: string
    // pinned scopes the provider to exactly one sandbox (the detail
    // page): no fallback-to-first-running, no running requirement
    // (stopped/error sandboxes have detail pages too), and selection
    // setters become no-ops so embedded tab components cannot navigate
    // the user away.
    pinned?: boolean
}

export function SandboxProvider({ children, initialSessionId, initialSandboxId, pinned = false }: SandboxProviderProps) {
    const { data, isLoading } = useSandboxInstances()
    const instances = data?.instances ?? []
    const navigate = useNavigate({ from: '/deployments/sandboxes/' })

    const selectedSessionId = initialSessionId ?? ''
    const selectedSandboxId = initialSandboxId ?? ''

    const setSelectedSessionId = (id: string) => {
        if (pinned) return
        const inst = instances.find((i) => i.sessionId === id)
        navigate({
            search: (prev) => ({
                ...prev,
                sessionId: id || undefined,
                sandboxId: inst?.id || undefined,
            }),
        })
    }

    const setSelectedSandboxId = (id: string) => {
        if (pinned) return
        const inst = instances.find((i) => i.id === id)
        navigate({
            search: (prev) => ({
                ...prev,
                sandboxId: id || undefined,
                sessionId: inst?.sessionId || undefined,
            }),
        })
    }

    const { activeSessionId, activeSandboxId } = useMemo(() => {
        // Pinned: the sandbox id is authoritative regardless of state.
        if (pinned && selectedSandboxId) {
            const inst = instances.find((i) => i.id === selectedSandboxId)
            return {
                activeSessionId: inst?.sessionId,
                activeSandboxId: selectedSandboxId,
            }
        }

        const runningInstances = instances.filter(isSandboxRunning)
        const runningSessionIds = new Set(runningInstances.map((i) => i.sessionId))

        // Check if the selected session is still running
        const validSession = selectedSessionId && runningSessionIds.has(selectedSessionId)
            ? selectedSessionId
            : undefined

        // Check if the selected sandbox is still running
        const validSandbox = selectedSandboxId && instances.find((i) => i.id === selectedSandboxId)
            ? selectedSandboxId
            : undefined

        // Derive from valid session
        if (validSession) {
            const inst = instances.find((i) => i.sessionId === validSession)
            return { activeSessionId: validSession, activeSandboxId: inst?.id }
        }

        // Derive from valid sandbox
        if (validSandbox) {
            const inst = instances.find((i) => i.id === validSandbox)
            return {
                activeSessionId: inst?.sessionId,
                activeSandboxId: validSandbox,
            }
        }

        // Fallback to first running instance
        const first = runningInstances[0]
        return {
            activeSessionId: first?.sessionId,
            activeSandboxId: first?.id,
        }
    }, [instances, selectedSessionId, selectedSandboxId, pinned])

    const value = useMemo<SandboxContextValue>(
        () => ({
            instances,
            isLoading,
            selectedSessionId,
            selectedSandboxId,
            setSelectedSessionId,
            setSelectedSandboxId,
            activeSessionId,
            activeSandboxId,
            pinned,
        }),
        // eslint-disable-next-line react-hooks/exhaustive-deps
        [instances, isLoading, selectedSessionId, selectedSandboxId, activeSessionId, activeSandboxId, pinned],
    )

    return <SandboxContext.Provider value={value}>{children}</SandboxContext.Provider>
}

// ─── Session Picker ──────────────────────────────────────────────────

function formatInstanceLabel(inst: SandboxInstance): string {
    const name = inst.name?.trim()
    if (name) {
        return `${name} (${inst.image})`
    }
    const prefix = inst.sessionId.length > 12 ? inst.sessionId.slice(0, 12) + '...' : inst.sessionId
    return `${prefix} (${inst.image})`
}

export function SandboxSessionPicker() {
    const { instances, activeSandboxId, setSelectedSandboxId, pinned } = useSandboxContext()
    const runningInstances = instances.filter(isSandboxRunning)

    // Detail page: the sandbox is fixed; render a static label instead
    // of a selector so the tab cannot navigate the user elsewhere.
    if (pinned) {
        const inst = instances.find((i) => i.id === activeSandboxId)
        return (
            <span className="text-sm text-white/80 light:text-black/80">
                {inst ? formatInstanceLabel(inst) : activeSandboxId}
            </span>
        )
    }

    return (
        <>
            <label className="text-xs text-white/50 light:text-black/50">Sandbox:</label>
            <Select
                value={activeSandboxId ?? ''}
                onValueChange={(value) => setSelectedSandboxId(value)}
                disabled={runningInstances.length === 0}
            >
                <SelectTrigger className="bg-brand-main-800 border-brand-main-600 text-white light:text-brand-main-50 text-sm min-w-[200px] h-8">
                    <SelectValue placeholder={runningInstances.length === 0 ? 'No active sandboxes' : 'Select a sandbox...'} />
                </SelectTrigger>
                <SelectContent>
                    {runningInstances.map((inst) => (
                        <SelectItem key={inst.id} value={inst.id}>
                            {formatInstanceLabel(inst)}
                        </SelectItem>
                    ))}
                </SelectContent>
            </Select>
        </>
    )
}

// ─── Sandbox Picker (for events tab — selects by sandbox ID) ─────────

export function SandboxPicker() {
    const { instances, activeSandboxId, setSelectedSandboxId, pinned } = useSandboxContext()

    if (pinned) {
        const inst = instances.find((i) => i.id === activeSandboxId)
        return (
            <span className="text-sm text-white/80 light:text-black/80">
                {inst?.name?.trim() || activeSandboxId}
            </span>
        )
    }

    return (
        <>
            <label className="text-xs text-white/50 light:text-black/50">Sandbox:</label>
            <Select
                value={activeSandboxId ?? ''}
                onValueChange={(value) => setSelectedSandboxId(value)}
                disabled={instances.length === 0}
            >
                <SelectTrigger className="bg-brand-main-800 border-brand-main-600 text-white light:text-brand-main-50 text-sm min-w-[200px] h-8">
                    <SelectValue placeholder={instances.length === 0 ? 'No sandboxes' : 'Select a sandbox...'} />
                </SelectTrigger>
                <SelectContent>
                    {instances.map((inst) => (
                        <SelectItem key={inst.id} value={inst.id}>
                            {inst.name?.trim()
                                ? `${inst.name} (${sandboxStatusLabel(inst)})`
                                : `${inst.id.length > 16 ? inst.id.slice(0, 16) + '...' : inst.id} (${sandboxStatusLabel(inst)})`
                            }
                        </SelectItem>
                    ))}
                </SelectContent>
            </Select>
        </>
    )
}
