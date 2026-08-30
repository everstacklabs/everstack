import { Bot, Repeat, Wrench, Coins, Flag, AlertTriangle } from 'lucide-react'
import type { Span } from '@everstack/proto/everstack/traces/v1/traces_pb'
import { getAttr } from '@/utils/traces-common'

/** Iterations at/above this are flagged as a possible runaway loop. */
const LOOP_WARN_TURNS = 10

function num(v: string | undefined): number | undefined {
  if (v == null || v === '') return undefined
  const n = Number(v)
  return Number.isFinite(n) ? n : undefined
}

/**
 * Compact summary of an agent run, surfaced from the agent span's attributes
 * (agent.total_turns / total_tool_calls / finish_reason / iteration, etc.).
 * Renders nothing for non-agent traces. Gives the agentic structure a home in
 * the trace detail instead of leaving it buried in raw attributes.
 */
export function AgentRunSummary({ spans }: { spans: Span[] }) {
  // The agent span is whichever carries agent-run totals / identity.
  const agentSpan = spans.find(
    (s) =>
      getAttr(s, 'agent.total_turns') !== undefined ||
      getAttr(s, 'agent.name') !== undefined ||
      (getAttr(s, 'observation.type') || '').toUpperCase() === 'AGENT',
  )
  if (!agentSpan) return null

  const name = getAttr(agentSpan, 'agent.name')
  const model = getAttr(agentSpan, 'agent.model')

  // Total turns: prefer the explicit total, else the max turn number seen.
  let turns = num(getAttr(agentSpan, 'agent.total_turns')) ?? num(getAttr(agentSpan, 'agent.iteration'))
  if (turns === undefined) {
    const maxTurn = spans.reduce((m, s) => Math.max(m, num(getAttr(s, 'agent.turn.number')) ?? 0), 0)
    if (maxTurn > 0) turns = maxTurn
  }

  const toolCallsTotal =
    num(getAttr(agentSpan, 'agent.total_tool_calls')) ??
    spans.reduce((m, s) => m + (num(getAttr(s, 'agent.turn.tool_calls')) ?? 0), 0)
  const toolCalls = toolCallsTotal > 0 ? toolCallsTotal : undefined
  const tokens = num(getAttr(agentSpan, 'agent.total_tokens')) ?? num(getAttr(agentSpan, 'agent.tokens.total'))
  const finishReason = getAttr(agentSpan, 'agent.finish_reason')
  const executionMode = getAttr(agentSpan, 'agent.execution_mode')
  const looping =
    (getAttr(agentSpan, 'loop_health.looping') || '').toLowerCase() === 'true' ||
    (turns !== undefined && turns >= LOOP_WARN_TURNS)

  const stats: { icon: typeof Repeat; label: string; value: string }[] = []
  if (turns !== undefined) stats.push({ icon: Repeat, label: 'Iterations', value: String(turns) })
  if (toolCalls !== undefined) stats.push({ icon: Wrench, label: 'Tool calls', value: String(toolCalls) })
  if (tokens !== undefined) stats.push({ icon: Coins, label: 'Tokens', value: tokens.toLocaleString() })
  if (finishReason) stats.push({ icon: Flag, label: 'Finish', value: finishReason })

  return (
    <div className="rounded-lg border border-brand-main-500 bg-brand-main-600/10 p-3 space-y-2">
      <div className="flex items-center gap-2">
        <Bot className="h-4 w-4 text-brand-secondary-300" />
        <span className="text-sm font-medium text-brand-main-50 light:text-black">{name || 'Agent run'}</span>
        {model && <span className="text-[11px] text-brand-main-50 light:text-black">{model}</span>}
        {executionMode && (
          <span className="ml-auto text-[10px] uppercase tracking-wide text-brand-main-50 light:text-black">{executionMode}</span>
        )}
      </div>

      {stats.length > 0 && (
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-2">
          {stats.map((s) => {
            const Icon = s.icon
            return (
              <div key={s.label} className="rounded bg-brand-main-700/30 px-2.5 py-1.5">
                <div className="flex items-center gap-1 text-[10px] text-brand-main-50 light:text-black">
                  <Icon className="h-3 w-3" />
                  {s.label}
                </div>
                <div className="text-sm text-brand-main-50 mt-0.5 truncate light:text-black" title={s.value}>
                  {s.value}
                </div>
              </div>
            )
          })}
        </div>
      )}

      {looping && (
        <div className="flex items-center gap-1.5 rounded border border-amber-500/30 bg-amber-500/10 px-2.5 py-1.5 text-[11px] text-amber-300">
          <AlertTriangle className="h-3.5 w-3.5 shrink-0" />
          High iteration count — check for a tool-call loop or a non-terminating plan.
        </div>
      )}
    </div>
  )
}
