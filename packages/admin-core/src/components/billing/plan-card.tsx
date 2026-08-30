import { motion } from 'framer-motion'
import { Icon } from '@iconify/react'
import { cn } from '@everstack/utils/functions/cn'
import { ui } from '@everstack/ui'
import type { Plan, BillingPeriod } from '../../lib/plans'
import {
    getFeatureDescription,
    formatUsageLimit,
    formatSeatsLabel,
    PLAN_META,
    PLAN_RANK,
} from '../../lib/plans'

const { Tooltip } = ui

export interface PlanCardProps {
    plan: Plan
    currentPlanId?: string
    billingPeriod: BillingPeriod
    onUpgrade?: (planId: string) => void
    loading?: boolean
    upgradesDisabled?: boolean
    /** Stagger animation index — passed by the parent grid. */
    index?: number
}

const ease = [0.22, 1, 0.36, 1] as const
const DEFAULT_META = { accent: '#06b6d4', cta: 'Get started' }

// Direct port of apps/landing/src/components/landing/pricing-preview.tsx
// — same markup, same min-h slots, same accent treatment, same
// Tooltip-driven "+N more" pattern, same motion stagger entry. The
// only differences are click handler (button + onUpgrade rather
// than <Link to="/pricing">) and the addition of current-plan
// detection so the in-product Plans tab can mark "you are here".
export function PlanCard({
    plan,
    currentPlanId,
    billingPeriod,
    onUpgrade,
    loading,
    upgradesDisabled = false,
    index = 0,
}: PlanCardProps) {
    const isCurrent = currentPlanId === plan.tier
    const isEnterprise = plan.tier === 'enterprise'
    const meta = PLAN_META[plan.tier] ?? DEFAULT_META
    const isHighlighted = plan.highlight && !isCurrent

    const currentRank = PLAN_RANK[currentPlanId || ''] ?? -1
    const thisRank = PLAN_RANK[plan.tier] ?? -1
    const isDowngrade = thisRank < currentRank && currentRank !== -1
    const showComingSoon = upgradesDisabled && !isCurrent && !isEnterprise

    const yearly = billingPeriod === 'yearly'
    const displayPrice = plan.pricing[billingPeriod]
    const isCustom = displayPrice === 'Custom'

    const enabledFeatures = plan.features.filter((f) => f.enabled)
    const keyUsageLimits = plan.usage_limits.slice(0, 2)
    const keyFeatures = enabledFeatures.slice(0, 3)
    const hiddenUsageLimits = plan.usage_limits.slice(keyUsageLimits.length)
    const hiddenFeatures = enabledFeatures.slice(keyFeatures.length)
    const hiddenCount =
        plan.usage_limits.length +
        enabledFeatures.length -
        (keyUsageLimits.length + keyFeatures.length)

    const ctaLabel = isCurrent
        ? 'Current plan'
        : isEnterprise
        ? meta.cta
        : showComingSoon
        ? 'Coming soon'
        : isDowngrade
        ? 'Switch plan'
        : meta.cta

    const ctaDisabled = isCurrent || loading || showComingSoon

    return (
        <motion.div
            initial={{ opacity: 0, y: 12 }}
            whileInView={{ opacity: 1, y: 0 }}
            viewport={{ once: true }}
            transition={{ duration: 0.5, delay: index * 0.06, ease }}
            className={cn(
                'group relative flex flex-col rounded-sm border p-6 transition-all duration-300 hover:border-white/[0.12]',
                isHighlighted
                    ? 'border-brand-secondary-500/25 bg-brand-secondary-500/[0.04]'
                    : 'border-white/[0.06] bg-white/[0.015]',
            )}
        >
            {isHighlighted && (
                <div
                    className="absolute -top-3 right-5 rounded-full px-3 py-1 font-mono text-[11px] font-medium tracking-wider uppercase text-brand-secondary-900"
                    style={{ backgroundColor: meta.accent }}
                >
                    Popular
                </div>
            )}

            {isCurrent && (
                <div className="absolute -top-3 right-5 rounded-full bg-brand-secondary-500 px-3 py-1 font-mono text-[11px] font-medium uppercase tracking-wider text-brand-secondary-900">
                    Current
                </div>
            )}

            {/* Tier name */}
            <div className="flex items-center gap-2.5">
                <span
                    className="h-1.5 w-1.5 rounded-full"
                    style={{
                        backgroundColor: meta.accent,
                        boxShadow: `0 0 6px ${meta.accent}50`,
                    }}
                />
                <h3 className="text-base font-semibold text-brand-main-50">{plan.name}</h3>
            </div>
            <p className="mt-1.5 min-h-[40px] text-sm leading-relaxed text-brand-main-200">
                {plan.description ?? ''}
            </p>

            {/* Price */}
            <div className="mt-2 min-h-[100px]">
                <div className="flex items-baseline gap-1">
                    <span className="font-mono text-3xl font-bold tracking-tight text-brand-main-50">
                        {isCustom ? 'Custom' : displayPrice}
                    </span>
                    {!isCustom && displayPrice !== '$0' && (
                        <span className="font-mono text-xs text-brand-main-300">
                            /{yearly ? 'yr' : 'mo'}
                        </span>
                    )}
                </div>
                <div className="mt-2 min-h-[16px]">
                    {yearly && plan.pricing.suggested ? (
                        <p className="text-[10.5px] text-emerald-400/80">
                            {plan.pricing.suggested}
                        </p>
                    ) : (
                        <p className="text-xs opacity-0">placeholder</p>
                    )}
                </div>
                <div className="mt-2 min-h-[16px]">
                    {plan.pricing.per_seat ? (
                        <p className="font-mono text-[11px] text-brand-main-300">
                            +
                            {yearly
                                ? plan.pricing.per_seat.yearly
                                : plan.pricing.per_seat.monthly}
                            /{yearly ? 'yr' : 'mo'} per extra seat
                        </p>
                    ) : (
                        <p className="font-mono text-[11px] opacity-0">placeholder</p>
                    )}
                </div>
            </div>

            {/* Divider */}
            <div
                className="mt-0.5 mb-3 h-px w-full"
                style={{ backgroundColor: `${meta.accent}15` }}
            />

            {/* All features */}
            <ul className="flex-1 space-y-2">
                <li className="flex items-center gap-2">
                    <Icon icon="lucide:check" className="h-3.5 w-3.5 shrink-0" style={{ color: meta.accent }} />
                    <span className="text-sm text-brand-main-100">
                        {formatSeatsLabel(plan.seat_limit)}
                    </span>
                </li>
                {keyUsageLimits.map((limit) => (
                    <li key={limit.type} className="flex items-start gap-2">
                        <Icon
                            icon="lucide:check"
                            className="mt-0.5 h-3.5 w-3.5 shrink-0"
                            style={{ color: meta.accent }}
                        />
                        <span className="text-sm leading-relaxed text-brand-main-100">
                            {formatUsageLimit(limit)}
                        </span>
                    </li>
                ))}
                {keyFeatures.map((feature) => (
                    <li key={feature.name} className="flex items-start gap-2">
                        <Icon
                            icon="lucide:check"
                            className="mt-0.5 h-3.5 w-3.5 shrink-0"
                            style={{ color: meta.accent }}
                        />
                        <span className="text-sm leading-relaxed text-brand-main-100">
                            {getFeatureDescription(feature)}
                        </span>
                    </li>
                ))}
                {hiddenCount > 0 && (
                    <li className="pl-5 text-xs text-brand-main-300">
                        <Tooltip
                            side="top"
                            content={
                                <div className="max-w-[260px] px-3 py-2">
                                    <p className="text-xs font-semibold text-white">
                                        Included in full pricing
                                    </p>
                                    <ul className="mt-1.5 space-y-1">
                                        {hiddenUsageLimits.map((limit) => (
                                            <li
                                                key={`hidden-limit-${plan.tier}-${limit.type}`}
                                                className="text-xs text-brand-main-200"
                                            >
                                                {formatUsageLimit(limit)}
                                            </li>
                                        ))}
                                        {hiddenFeatures.map((feature) => (
                                            <li
                                                key={`hidden-feature-${plan.tier}-${feature.name}`}
                                                className="text-xs text-brand-main-200"
                                            >
                                                {getFeatureDescription(feature)}
                                            </li>
                                        ))}
                                    </ul>
                                </div>
                            }
                        >
                            <button
                                type="button"
                                className="cursor-help text-brand-main-300 underline decoration-dotted underline-offset-2 hover:text-brand-main-100"
                            >
                                +{hiddenCount} more in full pricing
                            </button>
                        </Tooltip>
                    </li>
                )}
            </ul>

            {/* CTA — pushed to bottom. Was a <Link to="/pricing"> on
                the landing page; here it's a button that triggers the
                actual upgrade flow (Stripe checkout) or opens the
                sales mailto for Enterprise. */}
            <div className="mt-auto pt-6">
                <button
                    type="button"
                    onClick={() => {
                        if (ctaDisabled) return
                        if (isEnterprise) {
                            window.open('mailto:sales@everstack.ai', '_blank')
                        } else {
                            onUpgrade?.(plan.tier)
                        }
                    }}
                    disabled={ctaDisabled}
                    className={cn(
                        'flex w-full items-center justify-center gap-2 rounded-sm px-4 py-2.5 text-sm font-medium transition-all duration-300',
                        isCurrent
                            ? 'cursor-default border border-brand-secondary-500/20 text-brand-secondary-400 opacity-60'
                            : isHighlighted
                            ? 'text-brand-secondary-900 hover:brightness-110'
                            : showComingSoon
                            ? 'cursor-not-allowed border border-white/[0.06] text-brand-main-400 opacity-50'
                            : 'border border-white/[0.08] text-brand-main-100 hover:border-white/[0.16] hover:text-brand-main-50',
                    )}
                    style={isHighlighted && !isCurrent ? { backgroundColor: meta.accent } : undefined}
                >
                    {loading && !isCurrent ? (
                        <Icon icon="lucide:loader-2" className="h-3.5 w-3.5 animate-spin" />
                    ) : (
                        <>
                            {ctaLabel}
                            {!ctaDisabled && <Icon icon="lucide:arrow-right" className="h-3.5 w-3.5" />}
                        </>
                    )}
                </button>
            </div>
        </motion.div>
    )
}
