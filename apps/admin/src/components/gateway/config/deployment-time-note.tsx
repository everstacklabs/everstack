import { Iconify } from '@everstack/ui/icons'

// Top-of-form hint for sections where some fields can't be safely
// overridden per tenant on a shared gateway (backend selection,
// docker hosts, image registry, bloom-filter sizes, vector memory
// backend). The shared gateway pod has one connection / one engine
// instance for those — letting tenants override would either silently
// fail or break neighbours. Listed in docs/audits/runtime-config.md.
export function DeploymentTimeNote({ children }: { children: React.ReactNode }) {
  return (
    <div className="mb-4 flex items-start gap-2 rounded-md border border-brand-main-600/40 bg-brand-main-700/20 px-3 py-2">
      <Iconify.Icon
        icon="mdi:wrench-outline"
        className="mt-0.5 h-3.5 w-3.5 shrink-0 text-brand-main-300"
      />
      <p className="text-[11px] leading-relaxed text-brand-main-200">{children}</p>
    </div>
  )
}
