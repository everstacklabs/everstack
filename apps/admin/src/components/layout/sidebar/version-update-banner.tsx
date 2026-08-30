import { Sparkles } from '@everstack/ui/icons'
import { useState } from 'react'
import { useVersionCheck } from '@/hooks/use-version-check'

const DISMISS_KEY_PREFIX = 'evs-update-dismissed:'

export const VersionUpdateBanner = () => {
    const { serverVersion, bundleVersion, updateAvailable } = useVersionCheck()
    const dismissKey = serverVersion ? `${DISMISS_KEY_PREFIX}${serverVersion}` : ''
    const [dismissed, setDismissed] = useState<boolean>(() => {
        if (!dismissKey) return false
        try { return sessionStorage.getItem(dismissKey) === '1' } catch { return false }
    })

    if (!updateAvailable || dismissed) return null

    const handleUpdate = () => {
        window.location.reload()
    }

    const handleDismiss = () => {
        if (dismissKey) {
            try { sessionStorage.setItem(dismissKey, '1') } catch { /* sessionStorage may be unavailable */ }
        }
        setDismissed(true)
    }

    return (
        <div className="mx-2 mb-2 rounded-sm bg-brand-secondary-700/10 border border-brand-secondary-600/20 p-3">
            <div className="flex items-center gap-2 mb-2">
                <div className="flex size-6 items-center justify-center rounded-md bg-brand-secondary-500/20 text-brand-secondary-500">
                    <Sparkles className="size-3.5" />
                </div>
                <span className="text-sm font-medium text-brand-secondary-400">
                    Update available
                </span>
            </div>
            <p className="text-xs text-brand-secondary-400/90 mb-3 leading-relaxed">
                A newer version of Everstack is ready. Reload to upgrade — your unsaved work in this tab will be lost.
            </p>
            <p className="text-[11px] text-brand-secondary-400/60 mb-3 font-mono">
                {bundleVersion} → {serverVersion}
            </p>
            <div className="flex gap-2">
                <button
                    type="button"
                    onClick={handleUpdate}
                    className="flex-1 flex items-center justify-center rounded bg-brand-secondary-500 px-3 py-1.5 text-xs font-medium text-white hover:bg-brand-secondary-600 transition-colors"
                >
                    Update now
                </button>
                <button
                    type="button"
                    onClick={handleDismiss}
                    className="flex items-center justify-center rounded border border-brand-secondary-600/30 px-3 py-1.5 text-xs font-medium text-brand-secondary-400/80 hover:bg-brand-secondary-500/10 transition-colors"
                >
                    Later
                </button>
            </div>
        </div>
    )
}
