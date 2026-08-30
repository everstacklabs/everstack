import { useLocation, useSearch } from '@tanstack/react-router'
import { useState, useCallback, useMemo } from 'react'
import { CreateApiKeyDialog } from '@/components/keys'
import { CreateFunctionDialog } from '@/components/deployments/functions/create-function-dialog'
import { AgentFormDialog } from '@/components/deployments/agents'
import { AddSSHKeyDialog } from '@/components/settings/profile/add-ssh-key-dialog'
import { RegisterMcpServerDialog } from '@/components/gateway/mcp/register-mcp-server-dialog'
import { InviteMemberDialog } from '@/components/settings/team/invite-member-dialog'
import { VoiceProfileSheet } from '@/components/deployments/voice/voice-profile-sheet'
import { TrooperFormSheet } from '@/components/deployments/troopers/trooper-form-sheet'
import { type TopbarAction, type ActionGroup } from './types'
import {
  SearchAction,
  FilterAction,
  ButtonAction,
  CustomAction,
} from './action-components'
import { routeActions } from './routes'
import { CatalogSyncSettings } from '@/components/catalog/catalog-sync-settings'
import { cn } from '@everstack/utils/functions/cn'
import { useFeatureSet } from '@/hooks/use-features'
import { FeatureKey } from '@/config/features'
import { usePermissions } from '@/hooks/auth'

export function TopbarActions() {
  const { pathname } = useLocation()
  const search = useSearch({
    strict: false,
    structuralSharing: false,
  }) as Record<string, unknown>
  const isAgentsRoute = pathname === '/deployments/agents'
  const hasCreateTab = isAgentsRoute && typeof search.createTab === 'string'
  const [isCreateDialogOpen, setIsCreateDialogOpen] = useState(false)
  const featureSet = useFeatureSet()
  const { can } = usePermissions()

  const handleCreateDialogOpenChange = useCallback(
    (open: boolean) => {
      if (isAgentsRoute) {
        const params = new URLSearchParams(window.location.search)
        if (open) {
          if (!params.has('createTab')) params.set('createTab', 'basics')
        } else {
          params.delete('createTab')
        }
        window.history.replaceState(
          null,
          '',
          `${pathname}${params.size > 0 ? `?${params}` : ''}`,
        )
      }
      setIsCreateDialogOpen(open)
    },
    [isAgentsRoute, pathname],
  )

  const handleCreateTabChange = useCallback(
    (tab: string) => {
      const params = new URLSearchParams(window.location.search)
      params.set('createTab', tab)
      window.history.replaceState(null, '', `${pathname}?${params}`)
    },
    [pathname],
  )

  const agentCreateOpen = isAgentsRoute
    ? hasCreateTab || isCreateDialogOpen
    : isCreateDialogOpen

  // Get action groups for current route
  const currentGroups: ActionGroup[] = useMemo(() => {
    // Split pathname into parts (e.g., '/vault/api-keys' => ['/vault', '/api-keys'])
    const pathParts = pathname.split('/')?.filter(Boolean)

    if (pathParts.length === 0) return []

    // Get base route (e.g., '/vault')
    const baseRoute = `/${pathParts[0]}`
    const subRoute =
      pathParts.length > 1 ? `/${pathParts.slice(1).join('/')}` : ''

    // Look up nested route structure
    const baseRouteConfig = routeActions[baseRoute]
    if (!baseRouteConfig) return []

    const normalize = (
      v: ActionGroup | ActionGroup[] | undefined,
    ): ActionGroup[] => (!v ? [] : Array.isArray(v) ? v : [v])

    if (!subRoute) return normalize(baseRouteConfig[''])

    // Exact match first
    if (baseRouteConfig[subRoute]) return normalize(baseRouteConfig[subRoute])

    // Fallback: for dynamic segments like /studio/$workflowId,
    // try matching parent prefixes with wildcard (e.g., /agents/abc123/chat -> /agents/abc123/* -> /agents/*)
    const subParts = subRoute.split('/').filter(Boolean)
    for (let depth = subParts.length - 1; depth >= 1; depth--) {
      const prefix = `/${subParts.slice(0, depth).join('/')}`
      const wildcardKey = `${prefix}/*`
      if (baseRouteConfig[wildcardKey])
        return normalize(baseRouteConfig[wildcardKey])
    }

    if (baseRouteConfig['/*']) return normalize(baseRouteConfig['/*'])

    return []
  }, [pathname])

  const visibleGroups: ActionGroup[] = useMemo(
    () =>
      currentGroups.map((group) => ({
        ...group,
        actions:
          group.actions?.filter(
            (action) =>
              !action.requiredPermission || can(action.requiredPermission),
          ) ?? group.actions,
      })),
    [currentGroups, can],
  )

  // Memoized render function for individual actions
  const renderAction = useCallback(
    (action: TopbarAction) => {
      switch (action.type) {
        case 'search':
          return <SearchAction key={action.key} action={action} />
        case 'filter':
          return <FilterAction key={action.key} action={action} />
        case 'button':
          return (
            <ButtonAction
              key={action.key}
              action={action}
              onDialogOpen={setIsCreateDialogOpen}
            />
          )
        case 'custom':
          return <CustomAction key={action.key} action={action} />
        default:
          return null
      }
    },
    [setIsCreateDialogOpen],
  )

  // Memoized render function for action groups
  const renderActionGroups = useMemo(() => {
    if (visibleGroups.length === 0) return null

    return (
      <div className={cn('flex items-center gap-4 pr-2 w-full')}>
        {/* Title and Search/Filters section */}
        <div className="flex items-center justify-between gap-4 flex-1">
          {/* Page title */}
          {visibleGroups[0]?.title && (
            <div className="flex items-center ml-2 pt-0.5 text-lg font-semibold text-white light:text-brand-main-50">
              {visibleGroups[0].title}
            </div>
          )}

          {/* Search and Filter actions */}
          <div className="flex items-center gap-3">
            {visibleGroups
              .flatMap(
                (group) =>
                  group &&
                  group.actions &&
                  group?.actions?.filter(
                    (action) =>
                      action.type === 'search' || action.type === 'filter',
                  ),
              )
              .map((action) => action && renderAction(action))}
          </div>
        </div>

        {/* Button actions */}
        <div className="flex items-center gap-2">
          {visibleGroups
            .flatMap(
              (group) =>
                group &&
                group.actions &&
                group?.actions?.filter(
                  (action) =>
                    action.type === 'button' || action.type === 'custom',
                ),
            )
            .map((action) => action && renderAction(action))}
        </div>
      </div>
    )
  }, [visibleGroups, renderAction])

  return (
    <div className={cn('flex items-center justify-end w-full')}>
      {renderActionGroups}

      {/* Global dialogs */}
      {pathname === '/vault/api-keys' && (
        <CreateApiKeyDialog
          open={isCreateDialogOpen}
          onOpenChange={setIsCreateDialogOpen}
        />
      )}

      {pathname === '/vault/llm-providers' && <CatalogSyncSettings />}

      {pathname === '/deployments/functions' && (
        <CreateFunctionDialog
          open={isCreateDialogOpen}
          onOpenChange={setIsCreateDialogOpen}
        />
      )}

      {pathname === '/deployments/agents' && (
        <AgentFormDialog
          open={agentCreateOpen}
          onOpenChange={handleCreateDialogOpenChange}
          activeTab={search.createTab as string | undefined}
          onActiveTabChange={handleCreateTabChange}
        />
      )}

      {pathname === '/settings/ssh-keys' && (
        <AddSSHKeyDialog
          open={isCreateDialogOpen}
          onOpenChange={setIsCreateDialogOpen}
        />
      )}

      {pathname === '/gateway/mcp' && (
        <RegisterMcpServerDialog
          open={isCreateDialogOpen}
          onOpenChange={setIsCreateDialogOpen}
        />
      )}

      {(pathname === '/settings/team' || pathname === '/settings/members') && (
        <InviteMemberDialog
          open={isCreateDialogOpen}
          onOpenChange={setIsCreateDialogOpen}
        />
      )}

      {pathname === '/deployments/voice' &&
        featureSet?.has(FeatureKey.VOICE) && (
          <VoiceProfileSheet
            open={isCreateDialogOpen}
            onOpenChange={setIsCreateDialogOpen}
            profile={null}
          />
        )}

      {pathname === '/deployments/troopers' && (
        <TrooperFormSheet
          open={isCreateDialogOpen}
          onOpenChange={setIsCreateDialogOpen}
        />
      )}
    </div>
  )
}
