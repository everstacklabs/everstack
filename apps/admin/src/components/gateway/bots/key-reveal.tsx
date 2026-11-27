import { useState, type ReactNode } from 'react'
import { ui } from '@everstack/ui'
import { CopyField } from '@/components/gateway/interop/copy-field'
import { BotSetup } from './bot-setup'

const {
    AlertDialog,
    AlertDialogAction,
    AlertDialogCancel,
    AlertDialogContent,
    AlertDialogDescription,
    AlertDialogFooter,
    AlertDialogHeader,
    AlertDialogTitle,
} = ui

/** The new key, shown once, followed by the setup with the key filled in. */
export function KeyReveal({
    apiKey,
    apiKeyId,
    kind,
    surfaces,
    onCopied,
}: {
    apiKey: string
    apiKeyId?: string
    kind: string
    surfaces: string[]
    onCopied: () => void
}) {
    return (
        <div className="min-w-0 space-y-6">
            <div className="space-y-2 rounded border border-warning-border bg-warning-surface p-3">
                <p className="text-sm font-medium">Bot key</p>
                <CopyField value={apiKey} onCopy={onCopied} ariaLabel="Copy bot key" wrap />
                <p className="text-xs leading-5 text-warning">
                    You will not see this key again. Copy it or the config below before you close this panel.
                </p>
            </div>
            <BotSetup kind={kind} surfaces={surfaces} apiKey={apiKey} apiKeyId={apiKeyId} onCopy={onCopied} />
        </div>
    )
}

/** Asks before closing a panel that holds a key nobody copied yet. */
export function useGuardedClose(onClose: () => void) {
    const [copied, setCopied] = useState(false)
    const [asking, setAsking] = useState(false)
    const request = (hasKey: boolean) => {
        if (hasKey && !copied) setAsking(true)
        else onClose()
    }
    const reset = () => {
        setCopied(false)
        setAsking(false)
    }
    const confirm: ReactNode = (
        <AlertDialog open={asking} onOpenChange={setAsking}>
            <AlertDialogContent className="border border-brand-main-700 bg-brand-main-900 text-brand-main-100 sm:max-w-md">
                <AlertDialogHeader>
                    <AlertDialogTitle className="text-white light:text-brand-main-50">Close without copying the key?</AlertDialogTitle>
                    <AlertDialogDescription className="text-white/55 light:text-black/55">
                        You will not see this key again. If you lose it, make a new key from the bot list.
                    </AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                    <AlertDialogCancel>Keep open</AlertDialogCancel>
                    <AlertDialogAction
                        onClick={() => {
                            setAsking(false)
                            onClose()
                        }}
                    >
                        Close
                    </AlertDialogAction>
                </AlertDialogFooter>
            </AlertDialogContent>
        </AlertDialog>
    )
    return { copied, markCopied: () => setCopied(true), request, reset, confirm }
}
