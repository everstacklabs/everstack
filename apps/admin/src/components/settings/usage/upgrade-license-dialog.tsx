import { useState, useEffect } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ui } from '@everstack/ui'
import { Icon } from '@iconify/react'
import { refreshLicenseStatus } from '@/server/license'
import { useActivateGatewayLicense, useGatewayLicenseStatus } from '@/hooks/license/use-license-observer'
import { toast } from '@everstack/ui/components'
import { useLicenseInfo, licenseKeys } from '@/hooks/license/use-license-status'
import { useGatewayPlans } from '@/hooks/license/use-plans'
import type { BillingPeriod } from '@/config/plans'

const { Dialog, DialogContent, DialogTitle, DialogDescription, Button, Input, Label } = ui

interface UpgradeLicenseDialogProps {
    open: boolean
    onOpenChange: (open: boolean) => void
    targetPlanId: string
    billingPeriod: BillingPeriod
    /** When true, shows messaging that automatic activation failed */
    isManualFallback?: boolean
    /** Pre-filled activation token (when passed from cloud redirect) */
    prefillToken?: string
}

const PLAN_ORDER = ['free', 'basic', 'pro', 'enterprise']

type UpgradeStep = 'idle' | 'activating' | 'success' | 'error'

export function UpgradeLicenseDialog({ open, onOpenChange, targetPlanId, billingPeriod, isManualFallback = false, prefillToken = '' }: UpgradeLicenseDialogProps) {
    const [step, setStep] = useState<UpgradeStep>('idle')
    const [activationToken, setActivationToken] = useState(prefillToken)
    const [errorMessage, setErrorMessage] = useState<string | null>(null)

    // Update token when prefillToken changes
    useEffect(() => {
        if (prefillToken) {
            setActivationToken(prefillToken)
        }
    }, [prefillToken])
    const queryClient = useQueryClient()
    const activate = useActivateGatewayLicense()
    const { data: gatewayStatus } = useGatewayLicenseStatus()
    const { data: currentTier } = useLicenseInfo()
    const { data: plans } = useGatewayPlans()

    const isGenericUpgrade = targetPlanId === 'upgrade'
    const targetPlan = isGenericUpgrade ? null : plans?.find(p => p.tier === targetPlanId)
    const currentPlanIndex = PLAN_ORDER.indexOf(currentTier?.license?.tier?.toLowerCase() || '')
    const targetPlanIndex = PLAN_ORDER.indexOf(targetPlanId)
    const isDowngrade = !isGenericUpgrade && targetPlanIndex < currentPlanIndex && currentPlanIndex !== -1
    const isProcessing = step === 'activating'

    const handleActivate = async () => {
        if (!activationToken.trim()) {
            setErrorMessage('Please enter an activation token')
            return
        }

        setStep('activating')
        setErrorMessage(null)

        try {
            // Activate gateway with the provided token (with instance ID for upgrade)
            const instanceId = gatewayStatus?.instanceId || ''
            await activate.mutateAsync({ activationToken: activationToken.trim(), instanceId })

            // Force the backend to refresh its license state and then refetch
            await refreshLicenseStatus()
            await queryClient.refetchQueries({ queryKey: licenseKeys.status() })

            // Notify sidebar and other tabs of the plan change
            localStorage.setItem('evs:license-changed', Date.now().toString())
            window.dispatchEvent(new CustomEvent('evs:license-changed'))

            setStep('success')
            toast.success(isGenericUpgrade
                ? 'License activated successfully!'
                : `Successfully ${isDowngrade ? 'downgraded' : 'upgraded'} to ${targetPlan?.name || 'new'} plan!`)

            // Close dialog after short delay to show success state
            setTimeout(() => {
                onOpenChange(false)
                setStep('idle')
                setActivationToken('')
            }, 1500)
        } catch (error) {
            console.error('Activation failed:', error)
            setStep('error')
            const message = error instanceof Error ? error.message : 'Failed to activate. Please check your token.'
            setErrorMessage(message)
            toast.error(message)
        }
    }

    const handleClose = (open: boolean) => {
        if (!open && !isProcessing) {
            setStep('idle')
            setErrorMessage(null)
            setActivationToken('')
            onOpenChange(false)
        }
    }

    const handleSubmit = (e: React.FormEvent) => {
        e.preventDefault()
        handleActivate()
    }

    return (
        <Dialog open={open} onOpenChange={handleClose}>
            <DialogContent className="sm:max-w-[500px]">
                <DialogTitle>{isGenericUpgrade ? 'Activate License' : 'Upgrade Plan'}</DialogTitle>
                <DialogDescription>
                    {isManualFallback
                        ? 'Automatic activation timed out. An activation token has been sent to your email. Please enter it below to complete the upgrade.'
                        : isGenericUpgrade
                            ? 'Enter your activation token from the Everstack Cloud portal to activate or upgrade your license.'
                            : `Enter your activation token from the Everstack Cloud portal to ${isDowngrade ? 'downgrade' : 'upgrade'} your plan.`}
                </DialogDescription>

                <form onSubmit={handleSubmit} className="py-4 space-y-4">
                    {/* Manual fallback notice */}
                    {isManualFallback && (
                        <div className="rounded-lg border border-amber-500/20 bg-amber-500/10 p-4">
                            <div className="flex items-start gap-3">
                                <Icon icon="lucide:mail" className="h-5 w-5 text-amber-400 light:text-amber-700 mt-0.5 shrink-0" />
                                <div>
                                    <p className="text-sm text-amber-200 light:text-amber-800 font-medium mb-1">
                                        Check your email for the activation token
                                    </p>
                                    <p className="text-xs text-amber-200/70 light:text-amber-800/70">
                                        Your payment was successful! The automatic activation couldn't reach your gateway.
                                        We've sent the activation token to your email address.
                                    </p>
                                </div>
                            </div>
                        </div>
                    )}

                    {/* Plan info - only show for specific plan upgrades */}
                    {targetPlan && !isGenericUpgrade && (
                        <div className="rounded-lg border border-white/10 light:border-black/10 bg-white/5 light:bg-black/5 p-4">
                            <div className="flex justify-between items-center mb-2">
                                <span className="text-sm text-white/60 light:text-black/60">Target Plan</span>
                                <span className="font-semibold text-white light:text-brand-main-50">{targetPlan.name}</span>
                            </div>
                            <div className="flex justify-between items-center mb-2">
                                <span className="text-sm text-white/60 light:text-black/60">Billing</span>
                                <span className="font-semibold text-white light:text-brand-main-50 capitalize">{billingPeriod}</span>
                            </div>
                            <div className="flex justify-between items-center">
                                <span className="text-sm text-white/60 light:text-black/60">Price</span>
                                <span className="font-semibold text-white light:text-brand-main-50">
                                    {targetPlan.pricing[billingPeriod]}{billingPeriod === 'monthly' ? '/mo' : '/yr'}
                                </span>
                            </div>
                        </div>
                    )}

                    {/* Info for generic upgrade */}
                    {isGenericUpgrade && (
                        <div className="rounded-lg border border-blue-500/20 bg-blue-500/10 p-4">
                            <div className="flex items-start gap-3">
                                <Icon icon="lucide:info" className="h-5 w-5 text-blue-400 light:text-blue-600 mt-0.5 shrink-0" />
                                <p className="text-sm text-blue-200 light:text-blue-700">
                                    The plan and billing period are determined by your activation token.
                                    Get your token from <a href={import.meta.env.VITE_CLOUD_URL || 'https://app.everstack.ai'} target="_blank" rel="noopener noreferrer" className="underline hover:text-blue-100 light:hover:text-blue-800">Everstack Cloud</a>.
                                </p>
                            </div>
                        </div>
                    )}

                    {/* Token input */}
                    <div className="space-y-2">
                        <Label htmlFor="activation-token">Activation Token</Label>
                        <Input
                            id="activation-token"
                            type="text"
                            placeholder="mf_act_..."
                            value={activationToken}
                            onChange={(e) => setActivationToken(e.target.value)}
                            disabled={isProcessing || step === 'success'}
                            className="font-mono"
                            autoFocus
                        />
                        <p className="text-xs text-white/40 light:text-black/40">
                            Get your activation token from{' '}
                            <a
                                href={import.meta.env.VITE_CLOUD_URL || 'https://app.everstack.ai'}
                                target="_blank"
                                rel="noopener noreferrer"
                                className="text-brand-main-400 hover:text-brand-main-300 underline"
                            >
                                Everstack Cloud
                            </a>
                        </p>
                    </div>

                    {isDowngrade && step === 'idle' && (
                        <div className="flex items-start gap-3 p-3 rounded-lg border border-yellow-500/20 bg-yellow-500/10">
                            <Icon icon="lucide:alert-triangle" className="h-5 w-5 text-yellow-400 light:text-yellow-700 mt-0.5 shrink-0" />
                            <p className="text-sm text-yellow-200/80 light:text-yellow-800/80">
                                Downgrading may result in loss of access to certain features and lower usage limits.
                            </p>
                        </div>
                    )}

                    {/* Progress indicator */}
                    {isProcessing && (
                        <div className="flex items-center gap-3 p-3 rounded-lg border border-blue-500/20 bg-blue-500/10">
                            <Icon icon="lucide:loader-2" className="h-5 w-5 text-blue-400 light:text-blue-600 animate-spin shrink-0" />
                            <p className="text-sm text-blue-200 light:text-blue-700">Activating license...</p>
                        </div>
                    )}

                    {/* Success indicator */}
                    {step === 'success' && (
                        <div className="flex items-center gap-3 p-3 rounded-lg border border-green-500/20 bg-green-500/10">
                            <Icon icon="lucide:check-circle" className="h-5 w-5 text-green-400 light:text-green-600 shrink-0" />
                            <p className="text-sm text-green-200 light:text-green-700">License activated successfully!</p>
                        </div>
                    )}

                    {/* Error indicator */}
                    {step === 'error' && errorMessage && (
                        <div className="flex items-start gap-3 p-3 rounded-lg border border-red-500/20 bg-red-500/10">
                            <Icon icon="lucide:alert-circle" className="h-5 w-5 text-red-400 light:text-red-600 mt-0.5 shrink-0" />
                            <div className="flex-1">
                                <p className="text-sm text-red-200 light:text-red-700">{errorMessage}</p>
                                <button
                                    type="button"
                                    onClick={() => { setStep('idle'); setErrorMessage(null); }}
                                    className="mt-2 text-xs text-red-300 light:text-red-600 hover:text-red-200 light:hover:text-red-700 underline"
                                >
                                    Try again
                                </button>
                            </div>
                        </div>
                    )}

                    <div className="flex justify-end gap-2 pt-2">
                        <Button
                            type="button"
                            variant="ghost"
                            onClick={() => handleClose(false)}
                            disabled={isProcessing}
                        >
                            Cancel
                        </Button>
                        <Button
                            type="submit"
                            disabled={isProcessing || step === 'success' || !activationToken.trim()}
                            className="bg-brand-main-500 hover:bg-brand-main-600 text-white min-w-[120px] light:text-brand-main-50"
                        >
                            {isProcessing ? (
                                <>
                                    <Icon icon="lucide:loader-2" className="mr-2 h-4 w-4 animate-spin" />
                                    Activating...
                                </>
                            ) : step === 'success' ? (
                                <>
                                    <Icon icon="lucide:check" className="mr-2 h-4 w-4" />
                                    Done
                                </>
                            ) : step === 'error' ? (
                                'Retry'
                            ) : (
                                'Activate'
                            )}
                        </Button>
                    </div>
                </form>
            </DialogContent>
        </Dialog>
    )
}
