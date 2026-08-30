import { useState, useRef, useEffect, useCallback } from 'react'
import { useSandboxLogs } from '@/hooks/deployments/use-sandbox'
import { useSandboxContext, SandboxSessionPicker } from './sandbox-context'
import { Iconify } from '@everstack/ui/icons'

export function LogsTab() {
    const [filter, setFilter] = useState('')
    const [autoScroll, setAutoScroll] = useState(true)
    const logsEndRef = useRef<HTMLDivElement>(null)
    const containerRef = useRef<HTMLDivElement>(null)

    const { activeSessionId } = useSandboxContext()
    const { logs, isStreaming, startStreaming, stopStreaming, clearLogs } = useSandboxLogs(activeSessionId)

    const filteredLogs = filter
        ? logs.filter((line) => line.toLowerCase().includes(filter.toLowerCase()))
        : logs

    // Auto-scroll to bottom
    useEffect(() => {
        if (autoScroll && logsEndRef.current) {
            logsEndRef.current.scrollIntoView({ behavior: 'smooth' })
        }
    }, [filteredLogs.length, autoScroll])

    // Detect manual scroll up to disable auto-scroll
    const handleScroll = useCallback(() => {
        if (!containerRef.current) return
        const { scrollTop, scrollHeight, clientHeight } = containerRef.current
        const isAtBottom = scrollHeight - scrollTop - clientHeight < 50
        setAutoScroll(isAtBottom)
    }, [])

    return (
        <div className="flex flex-col h-full">
            {/* Controls */}
            <div className="flex items-center gap-3 px-4 py-2 border-b border-brand-main-600">
                <SandboxSessionPicker />

                <input
                    type="text"
                    placeholder="Filter logs..."
                    value={filter}
                    onChange={(e) => setFilter(e.target.value)}
                    className="bg-brand-main-800 border border-brand-main-600 text-white light:text-brand-main-50 text-sm rounded px-2 py-1 ml-2 w-48"
                />

                <div className="flex-1" />

                {isStreaming ? (
                    <button
                        onClick={stopStreaming}
                        className="flex items-center gap-1 text-xs bg-red-500/20 text-red-400 light:text-red-600 border border-red-500/30 rounded px-2 py-1 hover:bg-red-500/30"
                    >
                        <Iconify.Icon icon="heroicons:stop-solid" className="size-3" />
                        Stop
                    </button>
                ) : (
                    <button
                        onClick={startStreaming}
                        disabled={!activeSessionId}
                        className="flex items-center gap-1 text-xs bg-green-500/20 text-green-400 light:text-green-600 border border-green-500/30 rounded px-2 py-1 hover:bg-green-500/30 disabled:opacity-50"
                    >
                        <Iconify.Icon icon="heroicons:play-solid" className="size-3" />
                        Stream
                    </button>
                )}

                <button
                    onClick={clearLogs}
                    className="text-xs text-white/50 light:text-black/50 hover:text-white/70 light:hover:text-black/70"
                >
                    Clear
                </button>
            </div>

            {/* Log lines */}
            <div
                ref={containerRef}
                onScroll={handleScroll}
                className="flex-1 overflow-y-auto bg-brand-main-900 p-2 font-mono text-xs"
            >
                {filteredLogs.length === 0 ? (
                    <div className="flex flex-col items-center justify-center h-full pb-16">
                        <div className="relative mb-6">
                            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                                <Iconify.Icon icon="heroicons:document-text" className="size-8 text-brand-secondary-400" />
                            </div>
                        </div>
                        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">
                            {!activeSessionId
                                ? 'No session selected'
                                : isStreaming
                                    ? 'Streaming — waiting for activity'
                                    : 'Stream not connected'}
                        </h3>
                        <p className="text-sm text-white/50 light:text-black/50 max-w-md text-center leading-relaxed">
                            {!activeSessionId ? (
                                'Select a session to view logs.'
                            ) : isStreaming ? (
                                <>
                                    Logs capture commands run via the Exec API and the
                                    interactive Shell. Run a command in the
                                    {' '}<span className="text-white/75 light:text-black/75">Shell</span> tab,
                                    or trigger an agent tool, to see entries appear here.
                                </>
                            ) : (
                                <>
                                    Click <span className="text-white/75 light:text-black/75">Stream</span> to
                                    start collecting logs. The stream auto-attaches when a
                                    session is selected; if it failed silently, retrying
                                    here reopens the connection.
                                </>
                            )}
                        </p>
                    </div>
                ) : (
                    filteredLogs.map((line, i) => (
                        <div key={i} className="text-white/80 light:text-black/80 py-0.5 hover:bg-white/5 light:hover:bg-black/5 px-1 whitespace-pre-wrap break-all">
                            {line}
                        </div>
                    ))
                )}
                <div ref={logsEndRef} />
            </div>

            {/* Jump to bottom */}
            {!autoScroll && filteredLogs.length > 0 && (
                <button
                    onClick={() => {
                        setAutoScroll(true)
                        logsEndRef.current?.scrollIntoView({ behavior: 'smooth' })
                    }}
                    className="absolute bottom-4 right-4 bg-brand-secondary-600 text-white text-xs rounded-full px-3 py-1 shadow-lg"
                >
                    Jump to latest
                </button>
            )}
        </div>
    )
}
