import { Iconify } from '@everstack/ui/icons'

interface ComingSoonPanelProps {
  title: string
  // What the user is trying to do that this panel does NOT yet do.
  // Plain language; no marketing voice.
  intent: string
  // Optional second sentence — usually clarifies what IS live so users
  // don't think the underlying feature is missing entirely.
  alreadyLive?: string
}

// Used in place of half-wired config panels. The runtime config
// surface has more dials than the gateway actually consumes today
// (see docs/audits/runtime-config.md). Rather than ship a form that
// silently ignores edits, render this. Honest UX, one component.
export function ComingSoonPanel({ title, intent, alreadyLive }: ComingSoonPanelProps) {
  return (
    <div className="rounded-md border border-brand-main-600/40 bg-brand-main-700/30 p-6">
      <div className="flex items-start gap-3">
        <Iconify.Icon
          icon="mdi:clock-outline"
          className="text-brand-main-200 mt-0.5 shrink-0"
          width={20}
          height={20}
        />
        <div className="flex flex-col gap-1.5">
          <h3 className="text-white light:text-brand-main-50 text-sm font-medium">
            {title} — coming soon
          </h3>
          <p className="text-xs text-brand-main-200 leading-relaxed">
            {intent}
          </p>
          {alreadyLive ? (
            <p className="text-xs text-brand-main-200 leading-relaxed">
              {alreadyLive}
            </p>
          ) : null}
        </div>
      </div>
    </div>
  )
}
