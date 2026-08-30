import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useNavigate, useSearch } from '@tanstack/react-router'
import { Plus, Loader2, MoreHorizontal, ExternalLink, Link2, Trash2 } from 'lucide-react'
import { listSessions } from '@/server/agents'
import { getOrCreatePlatformAgent } from '@/server/platform-agent'
import { useSession } from '@/hooks/auth/use-auth'
import { cn } from '@/lib/utils'
import { Button } from '@everstack/ui/components'
import { ui } from '@everstack/ui'

const { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator } = ui

const PLATFORM_SESSIONS_KEY = ['platform-sessions']

export function ChatSessionsList() {
  const navigate = useNavigate()
  const search = useSearch({ strict: false }) as { session?: string, model?: string }
  const activeSessionId = search?.session
  const activeModel = search?.model

  // Gate on orgId so the request never fires before `setActiveOrgId`
  // primes the `x-org-id` interceptor. Key per-org so a multi-org switch
  // doesn't serve another tenant's cached row.
  const { data: authSession } = useSession()
  const orgId = authSession?.user?.organizations?.[0]?.id ?? ''

  const { data: platformAgent } = useQuery({
    queryKey: ['platform-agent', orgId],
    queryFn: getOrCreatePlatformAgent,
    enabled: !!orgId,
    staleTime: Infinity,
  })

  const { data: sessionsResp, isLoading } = useQuery({
    queryKey: [...PLATFORM_SESSIONS_KEY, platformAgent?.id],
    queryFn: async () => {
      if (!platformAgent?.id) throw new Error('No platform agent')
      return listSessions({
        tenantId: '',
        agentId: platformAgent.id,
        limit: 50,
      })
    },
    enabled: !!platformAgent?.id,
    refetchInterval: 30_000,
  })

  const sessions = sessionsResp?.sessions ?? []

  const copyLink = (sessionId: string) => {
    const url = `${window.location.origin}/chat?session=${sessionId}`
    navigator.clipboard.writeText(url)
  }

  const openInNewTab = (sessionId: string) => {
    window.open(`/chat?session=${sessionId}`, '_blank')
  }

  return (
    <div className="flex flex-col h-full px-3 py-2.5">
      <div className="flex items-center justify-between mb-3">
        <span className="text-xs font-medium text-white/50 light:text-black/50 uppercase tracking-wider">
          Chat history
        </span>
        <Button
          onClick={() => {
            localStorage.removeItem('platform-chat:last-session')
            navigate({
              to: '/chat', search: {
                model: undefined,
                session: undefined
              }
            })
          }}
          variant="outline"
          size="icon"
          title="New chat"
          className="h-4"
        >
          <Plus className="h-2.5 w-2.5" />
        </Button>
      </div>

      <div className="flex-1 overflow-y-auto -mx-1.5 scrollbar-macos">
        {isLoading && (
          <div className="flex justify-center py-8">
            <Loader2 className="size-4 text-brand-main-100 animate-spin" />
          </div>
        )}

        {!isLoading && sessions.length === 0 && (
          <p className="py-8 text-center text-xs text-white/25 light:text-black/25">
            No chats found
          </p>
        )}

        {sessions.map((session) => {
          const preview =
            session.summary ||
            session.turns?.[0]?.userInput?.slice(0, 60) ||
            'New conversation'
          const isActive = activeSessionId === session.id

          return (
            <SessionItem
              key={session.id}
              sessionId={session.id}
              preview={preview}
              turnCount={session.turnCount}
              isActive={isActive}
              onSelect={() => navigate({ to: '/chat', search: { session: session.id, model: activeModel } })}
              onOpenInNewTab={() => openInNewTab(session.id)}
              onCopyLink={() => copyLink(session.id)}
            />
          )
        })}
      </div>
    </div >
  )
}

function SessionItem({
  preview,
  turnCount,
  isActive,
  onSelect,
  onOpenInNewTab,
  onCopyLink,
}: {
  sessionId: string
  preview: string
  turnCount: number
  isActive: boolean
  onSelect: () => void
  onOpenInNewTab: () => void
  onCopyLink: () => void
}) {
  const [menuOpen, setMenuOpen] = useState(false)

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onSelect}
      onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') onSelect() }}
      className={cn(
        'group flex items-center rounded px-2.5 py-2 mb-0.5 transition-colors cursor-pointer',
        isActive
          ? 'bg-brand-secondary-600/15 border border-brand-secondary-500/20'
          : 'hover:bg-brand-main-600/30 border border-transparent',
      )}
    >
      <div className="min-w-0 flex-1 text-left">
        <div className="flex items-center justify-between gap-2">
          <p className={cn(
            'text-xs truncate leading-relaxed',
            isActive ? 'text-brand-secondary-300' : 'text-white/55 light:text-black/55',
            'group-hover:text-white/75 light:group-hover:text-black/75',
            isActive && 'group-hover:text-brand-secondary-300',
          )}>
            {preview}
          </p>

          <div className="shrink-0 flex items-center">
            {/* Turn count — hidden when menu trigger is visible */}
            <span className={cn(
              'text-[10px] text-nowrap',
              isActive ? 'text-brand-secondary-400/50' : 'text-brand-main-100',
              'group-hover:hidden',
              menuOpen && 'hidden',
            )}>
              {turnCount} turn{turnCount !== 1 ? 's' : ''}
            </span>

            {/* Menu trigger — visible on hover or when menu is open */}
            <div className={cn(
              'hidden',
              'group-hover:flex',
              menuOpen && '!flex',
            )}>
              <DropdownMenu onOpenChange={setMenuOpen}>
                <DropdownMenuTrigger asChild>
                  <button
                    className="flex items-center justify-center text-white/40 hover:text-white/70 light:text-black/40 light:hover:text-black/70"
                    onClick={(e) => e.stopPropagation()}
                  >
                    <MoreHorizontal className="size-3.5" />
                  </button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" side="bottom" className="w-48 bg-brand-main-800 border-brand-main-600 text-brand-main-100 p-1">
                  <DropdownMenuItem
                    onClick={onOpenInNewTab}
                    className="rounded text-white/70 hover:bg-brand-main-700 hover:text-white focus:bg-brand-main-700 focus:text-white light:text-black/70 light:hover:text-brand-main-50 light:focus:text-brand-main-50 cursor-pointer"
                  >
                    <ExternalLink className="size-3.5 mr-2" />
                    Open in new tab
                  </DropdownMenuItem>
                  <DropdownMenuItem
                    onClick={onCopyLink}
                    className="rounded text-white/70 hover:bg-brand-main-700 hover:text-white focus:bg-brand-main-700 focus:text-white light:text-black/70 light:hover:text-brand-main-50 light:focus:text-brand-main-50 cursor-pointer"
                  >
                    <Link2 className="size-3.5 mr-2" />
                    Copy link
                  </DropdownMenuItem>
                  <DropdownMenuSeparator className="bg-brand-main-600" />
                  <DropdownMenuItem
                    className="rounded text-red-400 hover:bg-red-500/10 hover:text-red-300 focus:bg-red-500/10 focus:text-red-300 light:text-red-600 light:hover:text-red-700 light:focus:text-red-700 cursor-pointer"
                    onClick={() => {
                      // TODO: wire up delete session API
                    }}
                  >
                    <Trash2 className="size-3.5 mr-2" />
                    Delete
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
