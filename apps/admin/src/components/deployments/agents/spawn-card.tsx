import { useState } from 'react'
import { ChevronRight, ChevronDown, GitBranch } from 'lucide-react'
import { ui } from '@everstack/ui'
import type { AgentStreamEvent } from '@/hooks/deployments/use-agents'
import { ToolCallCard } from './tool-call-card'

const { Collapsible, CollapsibleContent, CollapsibleTrigger } = ui

interface SpawnCardProps {
    task: string
    agentId?: string
    events: AgentStreamEvent[]
    status: 'running' | 'done' | 'failed'
    tokensUsed?: number
    durationMs?: number
}

export function SpawnCard({
    task,
    agentId,
    events,
    status,
    tokensUsed,
    durationMs,
}: SpawnCardProps) {
    const [open, setOpen] = useState(status === 'running')

    const statusIndicator =
        status === 'running' ? (
            <span className="inline-block w-1.5 h-1.5 rounded-full bg-blue-400 animate-pulse" />
        ) : status === 'failed' ? (
            <span className="inline-block w-1.5 h-1.5 rounded-full bg-red-400" />
        ) : (
            <span className="inline-block w-1.5 h-1.5 rounded-full bg-green-400/70" />
        )

    // Collect child text chunks
    const childText = events
        .filter((e) => e.type === 'llm.chunk')
        .map((e) => e.textDelta)
        .join('')

    // Collect child tool calls
    const childToolStarts = events.filter((e) => e.type === 'tool_call.start')
    const childToolEnds = events.filter((e) => e.type === 'tool_call.end')

    return (
        <Collapsible open={open} onOpenChange={setOpen}>
            <CollapsibleTrigger asChild>
                <button
                    type="button"
                    className="flex items-center gap-2 w-full px-3 py-1.5 rounded-md bg-brand-main-800/30 hover:bg-brand-main-800/50 transition-colors text-left"
                >
                    {statusIndicator}
                    <GitBranch className="w-3 h-3 text-white/25 light:text-black/25" />
                    <span className="text-[11px] font-mono text-white/40 light:text-black/40">spawn</span>
                    {agentId && (
                        <span className="text-[10px] text-white/20 light:text-black/20 font-mono">{agentId.slice(0, 8)}</span>
                    )}
                    <span className="text-[10px] text-white/30 light:text-black/30 truncate max-w-[200px]">{task}</span>
                    <span className="ml-auto flex items-center gap-2">
                        {tokensUsed != null && tokensUsed > 0 && (
                            <span className="text-[10px] text-white/20 light:text-black/20">{tokensUsed} tok</span>
                        )}
                        {durationMs != null && durationMs > 0 && (
                            <span className="text-[10px] text-white/20 light:text-black/20">{durationMs}ms</span>
                        )}
                        {open ? (
                            <ChevronDown className="w-3 h-3 text-white/25 light:text-black/25" />
                        ) : (
                            <ChevronRight className="w-3 h-3 text-white/25 light:text-black/25" />
                        )}
                    </span>
                </button>
            </CollapsibleTrigger>
            <CollapsibleContent>
                <div className="ml-4 mt-1 space-y-1.5">
                    {/* Child tool calls */}
                    {childToolStarts.map((tc, i) => {
                        const ended = childToolEnds.find((e) => e.toolCallId === tc.toolCallId)
                        return (
                            <ToolCallCard
                                key={`${tc.toolCallId}-${i}`}
                                toolCallId={tc.toolCallId}
                                toolName={tc.toolName}
                                toolArgs={tc.toolArgs}
                                toolResult={ended?.toolResult}
                                toolSuccess={ended?.toolSuccess}
                                toolDurationMs={ended?.toolDurationMs}
                                status={ended ? (ended.toolSuccess ? 'done' : 'failed') : 'running'}
                            />
                        )
                    })}

                    {/* Child text output */}
                    {childText && (
                        <div className="rounded-md bg-brand-main-900/40 p-2 text-[12px] text-white/50 light:text-black/50 whitespace-pre-wrap">
                            {childText}
                        </div>
                    )}

                    {events.length === 0 && status === 'running' && (
                        <div className="text-[10px] text-white/25 light:text-black/25 py-1">Starting...</div>
                    )}
                </div>
            </CollapsibleContent>
        </Collapsible>
    )
}
