import { Iconify } from '@everstack/ui/icons'

// Marks form fields that cannot be safely overridden per tenant on a
// shared gateway: backend choice (firecracker/k8s/docker), docker host
// or socket, container image registry prefix, fastpath bloom-filter /
// cache sizes, vector memory backend (pgvector/qdrant/...). These are
// cluster-wide and only the operator can change them. The badge sits
// next to the field label as a hint so users don't waste time saving
// values that won't take effect.
//
// See docs/audits/runtime-config.md for the full list and the reasons.
export function DeploymentTimeBadge({
  reason = 'Set at deployment time — saved value will not take effect on shared gateways.',
}: {
  reason?: string
}) {
  return (
    <span
      title={reason}
      className="inline-flex items-center gap-1 rounded-sm border border-brand-main-600/50 bg-brand-main-700/30 px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-brand-main-300"
    >
      <Iconify.Icon icon="mdi:wrench-outline" className="h-3 w-3" />
      Deployment-time
    </span>
  )
}
