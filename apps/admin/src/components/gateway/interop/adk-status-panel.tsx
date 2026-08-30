import type { ReactNode } from 'react'
import { ui } from '@everstack/ui'
import { useAdkStatus } from '@/hooks/gateway/use-interop'

const { Badge } = ui

/** A borderless guidance note inside the ADK card. We deliberately avoid the
 *  Alert primitive here: its heavy outline nested inside the card's subtle
 *  border read as a clashing double box. */
function Note({ title, children }: { title: string; children: ReactNode }) {
    return (
        <div className="space-y-1">
            <div className="text-sm font-medium text-brand-main-100">{title}</div>
            <p className="text-xs leading-5 text-brand-main-300">{children}</p>
        </div>
    )
}

/** ADK runtime status. ADK is a universal capability - on by default wherever a
 *  sandbox backend exists, with no plan gate and no off-switch. The only "off"
 *  state is an instance with no sandbox backend at all. This panel just reports
 *  state + the egress policy (the safety control). */
export function AdkStatusPanel() {
    const { data } = useAdkStatus()

    const enabled = data?.enabled ?? false
    const networkMode = data?.network_mode

    return (
        <div className="space-y-2">
            <div className="flex items-center justify-between">
                <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">ADK runtime</div>
                <Badge variant={enabled ? 'success' : 'secondary'}>{enabled ? 'Enabled' : 'Unavailable'}</Badge>
            </div>

            <div className="rounded-md border border-brand-main-600/40 bg-brand-main-800/20 p-3 space-y-3">
                <p className="text-xs leading-5 text-brand-main-300">
                    The <code className="text-brand-main-100">run_adk_agent</code> tool runs Google ADK agents in an
                    isolated sandbox. It is available on every instance.
                </p>

                {enabled ? (
                    <Note title="Enabled for this workspace">
                        ADK agents run in an isolated sandbox
                        {networkMode ? (
                            <>
                                {' '}with{' '}
                                <code className="text-brand-main-100">{networkMode}</code> egress
                            </>
                        ) : null}
                        , and reach models through the Everstack gateway.
                    </Note>
                ) : (
                    <Note title="No sandbox backend on this instance">
                        The ADK runtime needs a sandbox backend. It turns on automatically once one is configured - there
                        is nothing to enable.
                    </Note>
                )}
            </div>
        </div>
    )
}
