import { Link, useLocation } from '@tanstack/react-router'
import { type ActionGroup } from '@/components/layout/topbar/types'
import { useTrooper } from '@/hooks/deployments/use-troopers'

function TrooperBreadcrumb() {
  const { pathname } = useLocation()
  const segments = pathname.split('/').filter(Boolean)
  // pathname: /deployments/troopers/{trooperId}
  const trooperId = segments.length > 2 ? segments[2] : ''

  const { data: trooper, isLoading } = useTrooper(trooperId)

  return (
    <div className="flex items-center gap-1.5">
      <Link
        to="/deployments/troopers"
        className="text-sm font-normal text-brand-main-300 hover:text-white/80 light:hover:text-black/80 transition-colors"
      >
        Instances
      </Link>
      {trooperId && (
        <>
          <span className="text-brand-main-400 text-sm">/</span>
          {isLoading ? (
            <span className="inline-block h-4 w-24 rounded bg-white/10 light:bg-black/10 animate-pulse" />
          ) : (
            <span className="text-sm text-white light:text-brand-main-50 font-normal">
              {trooper?.name ?? trooperId}
            </span>
          )}
        </>
      )}
    </div>
  )
}

export const DeploymentsTroopersActions: ActionGroup[] = [
  {
    title: 'Instances',
    actions: [
      {
        type: 'button',
        key: 'create-trooper',
        label: 'Create Instance',
        variant: 'default',
        onClick: (setDialogOpen: (open: boolean) => void) => () =>
          setDialogOpen(true),
      },
    ],
  },
]

export const DeploymentsTroopersDetailActions: ActionGroup[] = [
  {
    title: <TrooperBreadcrumb />,
  },
]
