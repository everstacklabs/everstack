import { useGatewayLicenseStatus, useActivateGatewayLicense } from '@/hooks/license/use-license-observer'
import { useTrialStatus } from '@/hooks/license/use-trial-status'
import { useState, type PropsWithChildren } from 'react'
import { ui } from '@everstack/ui'

const { Dialog, DialogContent, DialogTitle, DialogDescription, Button, Input, Label, Loader } = ui

/**
 * ActivateLicense - Global activation guard component
 *
 * Allows access if:
 * 1. Gateway is activated with a valid license, OR
 * 2. Trial mode is active (no license required for trial)
 *
 * Shows an error screen with retry when status queries fail (transient errors
 * should not be confused with "not activated").
 * Shows a non-dismissible modal with activation form only when the backend
 * explicitly confirms activated=false.
 */
export function ActivateLicense({ children }: PropsWithChildren) {
    const { data: status, isLoading: isLoadingStatus, isError: isStatusError, error: statusError, refetch: refetchStatus } = useGatewayLicenseStatus()
    const { data: trialStatus, isLoading: isLoadingTrial, isError: isTrialError, refetch: refetchTrial } = useTrialStatus()
    const activate = useActivateGatewayLicense()
    const [token, setToken] = useState('')

    const handleSubmit = (e: React.FormEvent) => {
        e.preventDefault()
        if (!token.trim()) return

        activate.mutate(
            { activationToken: token.trim() },
            {
                onSuccess: () => {
                    setToken('') // Clear input on success
                },
            }
        )
    }

    // Show loading state while checking activation status
    const isLoading = isLoadingStatus || isLoadingTrial
    if (isLoading) {
        return (
            <div className="flex items-center justify-center h-screen">
                <Loader loaderText="Checking license status..." />
            </div>
        )
    }

    // If activated with license, render the app
    if (status?.activated) {
        return <>{children}</>
    }

    // If trial mode is active (no license required), render the app
    // Trial mode allows users to use the gateway without activation
    if (trialStatus?.mode === 'trial' && trialStatus?.active && !trialStatus?.expired) {
        return <>{children}</>
    }

    // If both queries failed, show an error screen with retry — not the activation modal.
    // A transient 401, network error, or HTML parse failure is NOT "not activated".
    if (isStatusError && isTrialError) {
        return (
            <div className="flex flex-col items-center justify-center h-screen gap-4 px-4 text-center">
                <p className="text-lg font-medium">Unable to check license status</p>
                <p className="text-sm text-muted-foreground max-w-md">
                    {statusError?.message || 'Could not reach the gateway API. The server may be starting up or temporarily unavailable.'}
                </p>
                <Button
                    variant="outline"
                    onClick={() => { refetchStatus(); refetchTrial() }}
                >
                    Retry
                </Button>
            </div>
        )
    }

    // If the status query errored but trial succeeded (or vice versa), and neither
    // confirmed activation, show a softer error — one endpoint being down shouldn't
    // lock the user out.
    if (isStatusError && !trialStatus) {
        return (
            <div className="flex flex-col items-center justify-center h-screen gap-4 px-4 text-center">
                <p className="text-lg font-medium">Unable to check license status</p>
                <p className="text-sm text-muted-foreground max-w-md">
                    {statusError?.message || 'Could not verify gateway activation. Please try again.'}
                </p>
                <Button
                    variant="outline"
                    onClick={() => { refetchStatus(); refetchTrial() }}
                >
                    Retry
                </Button>
            </div>
        )
    }

    // Only show activation modal when the backend explicitly confirmed activated=false
    // (i.e., status query succeeded and returned activated=false, and trial is not active)
    return (
        <>
            <Dialog open={true} onOpenChange={() => { }} modal>
                <DialogContent
                    showCloseButton={false}
                    className="sm:max-w-md"
                    onPointerDownOutside={(e) => e.preventDefault()}
                    onEscapeKeyDown={(e) => e.preventDefault()}
                >
                    <DialogTitle>Activate Gateway</DialogTitle>
                    <DialogDescription>
                        Enter your activation token to unlock the gateway admin panel.
                    </DialogDescription>

                    <form onSubmit={handleSubmit} className="space-y-4 mt-4">
                        <div className="space-y-2">
                            <Label htmlFor="activation-token">Activation Token</Label>
                            <Input
                                id="activation-token"
                                type="text"
                                placeholder="mf_act_..."
                                value={token}
                                onChange={(e) => setToken(e.target.value)}
                                disabled={activate.isPending}
                                autoFocus
                                className="font-mono"
                            />
                            {activate.isError && (
                                <p className="text-sm text-destructive">
                                    {activate.error?.message || 'Failed to activate. Please check your token.'}
                                </p>
                            )}
                        </div>

                        <div className="flex justify-end gap-2">
                            <Button
                                type="submit"
                                disabled={!token.trim() || activate.isPending}
                            >
                                {activate.isPending ? 'Activating...' : 'Activate'}
                            </Button>
                        </div>
                    </form>

                    <div className="mt-4 pt-4 border-t border-brand-main-400 text-xs">
                        <p>Don't have a token?</p>
                        <p className="mt-1">
                            Visit your cloud portal to generate an activation token.
                        </p>
                    </div>
                </DialogContent>
            </Dialog>
        </>
    )
}
