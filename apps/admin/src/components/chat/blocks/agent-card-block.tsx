import { useNavigate } from '@tanstack/react-router'
import { Bot, Settings, MessageSquare, Trash2 } from 'lucide-react'
import { Button } from '@everstack/ui/components'
import { useContextPanelStore } from '@/stores/context-panel-store'

interface AgentCardBlockProps {
  data: Record<string, unknown>
}

function statusColor(status: string): string {
  switch (status) {
    case 'running':
      return 'bg-green-500'
    case 'idle':
      return 'bg-blue-500'
    case 'created':
      return 'bg-yellow-500'
    case 'deleted':
      return 'bg-red-500'
    case 'error':
      return 'bg-red-500'
    default:
      return 'bg-gray-500'
  }
}

function actionLabel(action: string): string {
  switch (action) {
    case 'created':
      return 'Created'
    case 'updated':
      return 'Updated'
    case 'deleted':
      return 'Deleted'
    case 'viewed':
      return ''
    default:
      return ''
  }
}

export function AgentCardBlock({ data }: AgentCardBlockProps) {
  const navigate = useNavigate()
  const openPanel = useContextPanelStore((s) => s.open)

  const id = data.id as string
  const name = data.name as string
  const description = data.description as string | undefined
  const model = data.model as string | undefined
  const tools = (data.tools as string[]) ?? []
  const status = (data.status as string) ?? 'created'
  const action = data.action as string | undefined
  const isDeleted = action === 'deleted'

  return (
    <div
      className={`my-2 rounded-lg border border-brand-main-600 bg-brand-main-800/60 p-3 transition-colors ${
        isDeleted ? 'opacity-50' : 'hover:border-brand-main-500 cursor-pointer'
      }`}
      onClick={() => {
        if (!isDeleted && id) {
          openPanel('agent-detail', { agentId: id })
        }
      }}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="flex items-center gap-2.5 min-w-0">
          <div className="shrink-0 rounded-md border border-brand-main-600 bg-brand-main-900 p-1.5">
            {isDeleted ? (
              <Trash2 className="size-4 text-red-400" />
            ) : (
              <Bot className="size-4 text-brand-secondary-400" />
            )}
          </div>
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <span className="text-sm font-medium text-white truncate light:text-brand-main-50">
                {name || id}
              </span>
              {action && actionLabel(action) && (
                <span className="shrink-0 rounded bg-brand-secondary-500/20 px-1.5 py-0.5 text-[10px] font-medium text-brand-secondary-300">
                  {actionLabel(action)}
                </span>
              )}
              {!isDeleted && (
                <span className="flex items-center gap-1 shrink-0">
                  <span
                    className={`size-1.5 rounded-full ${statusColor(status)}`}
                  />
                  <span className="text-[10px] text-white/40 capitalize light:text-black/40">
                    {status}
                  </span>
                </span>
              )}
            </div>
            {description && (
              <p className="text-xs text-white/50 truncate mt-0.5 light:text-black/50">
                {description}
              </p>
            )}
          </div>
        </div>

        {!isDeleted && (
          <div className="flex items-center gap-1 shrink-0">
            <Button
              size="xs"
              variant="ghost"
              title="Chat with agent"
              onClick={(e) => {
                e.stopPropagation()
                navigate({ to: `/deployments/agents/${id}/chat` })
              }}
              className="text-white/40 hover:text-white/70 light:text-black/40 light:hover:text-black/70"
            >
              <MessageSquare className="size-3.5" />
            </Button>
            <Button
              size="xs"
              variant="ghost"
              title="Agent settings"
              onClick={(e) => {
                e.stopPropagation()
                navigate({ to: `/deployments/agents/${id}/settings` })
              }}
              className="text-white/40 hover:text-white/70 light:text-black/40 light:hover:text-black/70"
            >
              <Settings className="size-3.5" />
            </Button>
          </div>
        )}
      </div>

      {!isDeleted && (model || tools.length > 0) && (
        <div className="mt-2 flex items-center gap-3 text-[11px] text-white/40 light:text-black/40">
          {model && <span className="font-mono">{model}</span>}
          {tools.length > 0 && (
            <span>
              {tools.length} tool{tools.length !== 1 ? 's' : ''}
            </span>
          )}
        </div>
      )}
    </div>
  )
}
