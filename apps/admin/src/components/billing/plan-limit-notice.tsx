import { Icon } from '@iconify/react'
import { Link } from '@tanstack/react-router'
import { planLimitFromError } from '@/lib/plan-limit-error'
import { cn } from '@/lib/utils'

interface PlanLimitNoticeProps {
  /** The create mutation's error; renders nothing unless it is a plan cap. */
  error: unknown
  className?: string
}

/**
 * Quiet inline notice for a create refused at the plan cap. It replaces the
 * red error toast for that case: the customer did nothing wrong, the plan is
 * full, and the one useful action is the Upgrade link. In cloud-managed mode
 * /settings/billing forwards to the Cloud billing page.
 */
export function PlanLimitNotice({ error, className }: PlanLimitNoticeProps) {
  const limit = planLimitFromError(error)
  if (!limit) return null
  return (
    <div
      role="status"
      className={cn(
        'flex items-start gap-2 rounded-md border border-warning-border bg-warning-surface px-3 py-2 text-sm text-warning',
        className,
      )}
    >
      <Icon
        icon="lucide:gauge"
        className="mt-0.5 size-4 shrink-0"
        aria-hidden
      />
      <p className="min-w-0 flex-1 leading-snug">{limit.message}</p>
      <Link
        to="/settings/billing"
        search={{ upgrade_success: false, plan: undefined }}
        className="shrink-0 font-medium underline underline-offset-2 hover:text-warning/80"
      >
        Upgrade
      </Link>
    </div>
  )
}
