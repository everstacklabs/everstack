import { ui } from '@everstack/ui'
import { useState } from 'react'
import type { BillingPeriod, Plan } from '@/config/plans'
import { PlanCard } from './plan-card'
import { cn } from '@everstack/utils/functions/cn'

const { Dialog, DialogContent, DialogTitle, DialogDescription, TooltipProvider } = ui

interface UpgradePlanDialogProps {
    open: boolean
    onOpenChange: (open: boolean) => void
    plans: Plan[] | undefined
    currentPlanId: string | undefined
    onUpgrade?: (planTier: string, billingPeriod: BillingPeriod) => void
}

export function UpgradePlanDialog({ open, onOpenChange, plans, currentPlanId, onUpgrade }: UpgradePlanDialogProps) {
    const [billingPeriod, setBillingPeriod] = useState<BillingPeriod>('monthly')
    const [loading, setLoading] = useState(false)
    const yearly = billingPeriod === 'yearly'

    const handleUpgrade = async (planTier: string) => {
        if (!onUpgrade) return
        setLoading(true)
        try {
            await onUpgrade(planTier, billingPeriod)
        } finally {
            setLoading(false)
        }
    }

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="sm:max-w-[calc(100%-16rem)] max-h-[90vh] overflow-y-auto">
                <div className="text-center">
                    <DialogTitle className="text-xl font-semibold text-brand-main-50">
                        Choose your plan
                    </DialogTitle>
                    <DialogDescription className="mt-1.5 text-sm text-brand-main-200">
                        Start free. Scale as you grow. No hidden fees.
                    </DialogDescription>

                    {/* Billing Period Toggle */}
                    <div className="relative mt-6 flex items-center justify-center gap-3">
                        <span
                            className={cn(
                                'w-16 text-right text-sm transition-colors',
                                !yearly ? 'text-brand-main-50' : 'text-brand-main-300',
                            )}
                        >
                            Monthly
                        </span>
                        <button
                            onClick={() => setBillingPeriod(yearly ? 'monthly' : 'yearly')}
                            className={cn(
                                'relative h-6 w-11 shrink-0 rounded-full transition-colors duration-300',
                                yearly ? 'bg-brand-secondary-500' : 'bg-brand-main-700',
                            )}
                        >
                            <span
                                className={cn(
                                    'absolute top-0.5 left-0.5 h-5 w-5 rounded-full bg-white transition-transform duration-300',
                                    yearly && 'translate-x-5',
                                )}
                            />
                        </button>
                        <span
                            className={cn(
                                'w-16 text-left text-sm transition-colors',
                                yearly ? 'text-brand-main-50' : 'text-brand-main-300',
                            )}
                        >
                            Yearly
                        </span>
                        <span
                            className={cn(
                                'absolute left-1/2 top-full mt-2 -translate-x-1/2 rounded-full bg-emerald-500/10 px-2.5 py-0.5 font-mono text-[11px] font-medium text-emerald-400 light:text-emerald-600 transition-opacity duration-300',
                                yearly ? 'opacity-100' : 'opacity-0',
                            )}
                        >
                            Save 2 months
                        </span>
                    </div>
                </div>

                {/* Plans Grid */}
                <TooltipProvider>
                    <div className="grid gap-4 grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 mt-8">
                        {plans?.map((plan, index) => (
                            <PlanCard
                                key={plan.tier}
                                plan={plan}
                                currentPlanId={currentPlanId}
                                billingPeriod={billingPeriod}
                                onUpgrade={handleUpgrade}
                                loading={loading}
                                upgradesDisabled={!onUpgrade}
                                index={index}
                            />
                        ))}
                    </div>
                </TooltipProvider>

                {/* Sandbox compute — same for all plans */}
                <div className="mt-6 flex items-center justify-center gap-6 rounded border border-brand-main-600/50 bg-brand-main-800/30 px-5 py-3">
                    <span className="font-mono text-xs tracking-wider uppercase text-brand-main-300">
                        Sandbox compute
                    </span>
                    <span className="text-sm text-brand-main-200">
                        Billed separately by usage
                    </span>
                    <span className="font-mono text-xs text-brand-main-300">
                        mem $0.0414/GiB-hr · disk $0.0002/GiB-hr
                    </span>
                </div>
            </DialogContent>
        </Dialog>
    )
}
