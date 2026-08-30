import { useCallback, useEffect, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Iconify } from '@everstack/ui/icons'
import { toast } from '@everstack/ui/components'
import { useSandboxContext } from './sandbox-context'
import { isSandboxRunning } from './lifecycle'
import {
    listExecSessions,
    createExecSession,
    deleteExecSession,
    executeExecSessionCommand,
    getExecCommandStatus,
    getExecCommandLogs,
} from '@/server/sandbox'

// ProcessesTab is the Daytona-style exec-session console: persistent
// sessions (cwd + env carry across commands) with a command input and
// an output log. Async commands poll their exit marker and stream the
// log file.

const sessionsQueryKey = (sandboxId: string) => ['sandbox', sandboxId, 'exec-sessions'] as const

interface CommandEntry {
    commandId: string
    command: string
    output: string
    exitCode?: number | string
    running: boolean
}

export function ProcessesTab() {
    const { instances, activeSandboxId } = useSandboxContext()
    const inst = instances.find((i) => i.id === activeSandboxId)
    const running = inst ? isSandboxRunning(inst) : false
    const queryClient = useQueryClient()

    const [activeSession, setActiveSession] = useState<string>('')
    const [command, setCommand] = useState('')
    const [runAsync, setRunAsync] = useState(false)
    const [history, setHistory] = useState<CommandEntry[]>([])
    const [executing, setExecuting] = useState(false)
    const outputEndRef = useRef<HTMLDivElement>(null)

    const { data: sessions = [], isLoading } = useQuery({
        queryKey: activeSandboxId ? sessionsQueryKey(activeSandboxId) : ['exec-sessions-idle'],
        queryFn: () => listExecSessions(activeSandboxId!),
        enabled: Boolean(activeSandboxId) && running,
        refetchInterval: 15_000,
    })

    // Reset console when the sandbox or session changes.
    useEffect(() => {
        setHistory([])
    }, [activeSandboxId, activeSession])

    useEffect(() => {
        outputEndRef.current?.scrollIntoView({ behavior: 'smooth' })
    }, [history])

    const invalidateSessions = useCallback(() => {
        if (activeSandboxId) {
            queryClient.invalidateQueries({ queryKey: sessionsQueryKey(activeSandboxId) })
        }
    }, [queryClient, activeSandboxId])

    const handleCreateSession = useCallback(async () => {
        if (!activeSandboxId) return
        try {
            const id = await createExecSession(activeSandboxId)
            invalidateSessions()
            setActiveSession(id)
            toast.success(`Session ${id} created`)
        } catch (e) {
            toast.error(`Create session failed: ${(e as Error).message}`)
        }
    }, [activeSandboxId, invalidateSessions])

    const handleDeleteSession = useCallback(
        async (sessionId: string) => {
            if (!activeSandboxId) return
            try {
                await deleteExecSession(activeSandboxId, sessionId)
                invalidateSessions()
                if (activeSession === sessionId) setActiveSession('')
            } catch (e) {
                toast.error(`Delete session failed: ${(e as Error).message}`)
            }
        },
        [activeSandboxId, activeSession, invalidateSessions],
    )

    // pollAsyncCommand follows an async command until its exit marker
    // appears, refreshing the captured log on each round.
    const pollAsyncCommand = useCallback(
        async (sessionId: string, commandId: string) => {
            if (!activeSandboxId) return
            for (let i = 0; i < 600; i++) {
                await new Promise((r) => setTimeout(r, 2000))
                try {
                    const [status, logs] = await Promise.all([
                        getExecCommandStatus(activeSandboxId, sessionId, commandId),
                        getExecCommandLogs(activeSandboxId, sessionId, commandId).catch(() => ''),
                    ])
                    setHistory((prev) =>
                        prev.map((entry) =>
                            entry.commandId === commandId
                                ? { ...entry, output: logs, running: status.running, exitCode: status.exit_code }
                                : entry,
                        ),
                    )
                    if (!status.running) return
                } catch {
                    // transient; keep polling
                }
            }
        },
        [activeSandboxId],
    )

    const handleRun = useCallback(async () => {
        const cmd = command.trim()
        if (!cmd || !activeSandboxId || !activeSession) return
        setCommand('')
        setExecuting(true)
        try {
            const res = await executeExecSessionCommand(activeSandboxId, activeSession, cmd, {
                runAsync,
                timeoutSeconds: 120,
            })
            setHistory((prev) => [
                ...prev,
                {
                    commandId: res.command_id,
                    command: cmd,
                    output: res.output ?? '',
                    exitCode: res.exit_code,
                    running: Boolean(res.running),
                },
            ])
            if (res.running) {
                void pollAsyncCommand(activeSession, res.command_id)
            }
        } catch (e) {
            toast.error(`Command failed: ${(e as Error).message}`)
        } finally {
            setExecuting(false)
        }
    }, [command, activeSandboxId, activeSession, runAsync, pollAsyncCommand])

    if (!activeSandboxId) {
        return <EmptyState message="Select a sandbox to run commands." />
    }
    if (!running) {
        return <EmptyState message="Commands can only run while the sandbox is started." />
    }

    return (
        <div className="flex h-full overflow-hidden">
            {/* Session list */}
            <aside className="w-60 shrink-0 border-r border-brand-main-700 p-3 flex flex-col gap-2 overflow-y-auto">
                <div className="flex items-center justify-between">
                    <h3 className="text-xs uppercase tracking-wider text-white/40 light:text-black/40">Sessions</h3>
                    <button
                        onClick={handleCreateSession}
                        className="flex items-center gap-1 px-2 py-1 text-xs text-brand-secondary-300 hover:text-brand-secondary-200 hover:bg-brand-secondary-500/10 rounded"
                    >
                        <Iconify.Icon icon="heroicons:plus" className="size-3" />
                        New
                    </button>
                </div>
                {isLoading ? (
                    <p className="text-xs text-white/40 light:text-black/40">Loading…</p>
                ) : sessions.length === 0 ? (
                    <p className="text-xs text-white/40 light:text-black/40 leading-relaxed">
                        No sessions yet. Create one to run commands with persistent shell state.
                    </p>
                ) : (
                    sessions.map((id) => (
                        <div
                            key={id}
                            className={`flex items-center justify-between gap-1 px-2 py-1.5 rounded text-xs cursor-pointer border ${
                                id === activeSession
                                    ? 'bg-brand-secondary-500/15 border-brand-secondary-500/40 text-white light:text-brand-main-50'
                                    : 'bg-brand-main-800/50 border-transparent text-white/60 light:text-black/60 hover:text-white/80 light:hover:text-black/80 hover:bg-brand-main-700/50'
                            } light:text-brand-main-50`}
                            onClick={() => setActiveSession(id)}
                        >
                            <span className="font-mono truncate">{id}</span>
                            <button
                                onClick={(e) => {
                                    e.stopPropagation()
                                    handleDeleteSession(id)
                                }}
                                className="p-0.5 rounded text-white/30 light:text-black/30 hover:text-red-400 light:hover:text-red-600 hover:bg-red-500/10"
                                title="Delete session"
                                aria-label={`Delete session ${id}`}
                            >
                                <Iconify.Icon icon="heroicons:x-mark" className="size-3" />
                            </button>
                        </div>
                    ))
                )}
            </aside>

            {/* Console */}
            <div className="flex-1 min-w-0 flex flex-col">
                {!activeSession ? (
                    <EmptyState message="Select or create a session to start running commands. Shell state (cwd, exported env) persists across commands within a session." />
                ) : (
                    <>
                        <div className="flex-1 min-h-0 overflow-y-auto p-4 space-y-4 font-mono text-xs">
                            {history.length === 0 && (
                                <p className="text-white/35 light:text-black/35 font-sans">
                                    Session <span className="font-mono">{activeSession}</span> ready. Commands share cwd and environment.
                                </p>
                            )}
                            {history.map((entry) => (
                                <div key={entry.commandId}>
                                    <div className="flex items-center gap-2 text-brand-secondary-300">
                                        <span className="text-white/30 light:text-black/30">$</span>
                                        <span className="break-all">{entry.command}</span>
                                        {entry.running ? (
                                            <span className="text-[10px] font-sans uppercase tracking-wider text-brand-secondary-400 animate-pulse">
                                                running
                                            </span>
                                        ) : entry.exitCode !== undefined && String(entry.exitCode) !== '0' ? (
                                            <span className="text-[10px] font-sans text-red-400 light:text-red-600">exit {entry.exitCode}</span>
                                        ) : null}
                                    </div>
                                    {entry.output && (
                                        <pre className="mt-1 whitespace-pre-wrap break-all text-white/75 light:text-black/75">{entry.output}</pre>
                                    )}
                                </div>
                            ))}
                            <div ref={outputEndRef} />
                        </div>
                        <div className="border-t border-brand-main-700 p-3 flex items-center gap-2">
                            <span className="text-white/30 light:text-black/30 font-mono text-sm">$</span>
                            <input
                                value={command}
                                onChange={(e) => setCommand(e.target.value)}
                                onKeyDown={(e) => {
                                    if (e.key === 'Enter' && !e.shiftKey) {
                                        e.preventDefault()
                                        handleRun()
                                    }
                                }}
                                placeholder="Run a command in this session…"
                                disabled={executing}
                                className="flex-1 bg-brand-main-800 border border-brand-main-600 rounded px-3 py-1.5 text-sm font-mono text-white light:text-brand-main-50 placeholder:text-white/30 light:placeholder:text-black/30 focus:outline-none focus:border-brand-secondary-500/50 disabled:opacity-50"
                            />
                            <label className="flex items-center gap-1.5 text-xs text-white/55 light:text-black/55 cursor-pointer select-none">
                                <input
                                    type="checkbox"
                                    checked={runAsync}
                                    onChange={(e) => setRunAsync(e.target.checked)}
                                    className="accent-brand-secondary-500"
                                />
                                async
                            </label>
                            <button
                                onClick={handleRun}
                                disabled={executing || !command.trim()}
                                className="flex items-center gap-1.5 px-3 py-1.5 text-xs rounded bg-brand-secondary-600/20 border border-brand-secondary-500/40 text-brand-secondary-200 hover:bg-brand-secondary-600/30 disabled:opacity-50"
                            >
                                <Iconify.Icon
                                    icon={executing ? 'heroicons:arrow-path' : 'heroicons:play'}
                                    className={`size-4 ${executing ? 'animate-spin' : ''}`}
                                />
                                Run
                            </button>
                        </div>
                    </>
                )}
            </div>
        </div>
    )
}

function EmptyState({ message }: { message: string }) {
    return (
        <div className="flex flex-col items-center justify-center h-full pb-16 px-6">
            <Iconify.Icon icon="heroicons:command-line" className="size-8 text-white/25 light:text-black/25 mb-3" />
            <p className="text-sm text-white/50 light:text-black/50 max-w-md text-center leading-relaxed">{message}</p>
        </div>
    )
}
