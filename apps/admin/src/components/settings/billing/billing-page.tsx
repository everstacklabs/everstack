import {
  useLicenseStatus,
  useRefreshLicense,
  useLicenseInfo,
} from '@/hooks/license/use-license-status'
import { useGatewayPlans } from '@/hooks/license/use-plans'
import { UpgradeLicenseDialog } from '../usage/upgrade-license-dialog'
import { UpgradePlanDialog } from './upgrade-plan-dialog'
import { SpendLimitsSection } from './spend-limits-section'
import { useCallback, useState, useEffect, useRef } from 'react'
import { ui, useCopyToClipboard } from '@everstack/ui'
import { Icon } from '@iconify/react'
import dayjs from 'dayjs'
import relativeTime from 'dayjs/plugin/relativeTime'
import { useGatewayLicenseStatus } from '@/hooks/license/use-license-observer'
import {
  useWaitForActivation,
  useSubscriptionStatusEvents,
  type ActivationDetails,
  type SubscriptionStatusDetails,
} from '@/hooks/license/use-license-events'
import { cn } from '@everstack/utils/functions/cn'
import { MetricBar } from '../usage/metric-bar'
import { toast } from '@everstack/ui/components'
import {
  storeUpgradeCallbackSecret,
  buildSecureCloudUpgradeUrl,
} from '@/server/upgrade'
import { useSearch } from '@tanstack/react-router'
import { FeatureKey } from '@/config/features'
import { Feature } from '@/hooks/use-features'
dayjs.extend(relativeTime)

const {
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
  CardContent,
  Button,
  Badge,
  TooltipProvider,
  Tooltip,
  Tabs,
  TabsList,
  TabsTrigger,
  TabsContent,
} = ui

const TAB_TRIGGER_CLASS =
  'relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1'

/**
 * Format a number with appropriate suffix (K, M, B)
 */
function formatNumber(num: number): string {
  if (num >= 1_000_000_000) return `${(num / 1_000_000_000).toFixed(1)}B`
  if (num >= 1_000_000) return `${(num / 1_000_000).toFixed(1)}M`
  if (num >= 1_000) return `${(num / 1_000).toFixed(1)}K`
  return num.toLocaleString()
}

/**
 * Format USD currency
 */
// function formatCurrency(amount: number): string {
//     return new Intl.NumberFormat('en-US', {
//         style: 'currency',
//         currency: 'USD',
//         minimumFractionDigits: 2,
//         maximumFractionDigits: 4,
//     }).format(amount)
// }

export function BillingPage() {
  const { data, isLoading, isError, error, refetch } = useLicenseStatus({
    enablePolling: true,
    pollingInterval: 30000,
  })
  const {
     data: plans,
     isLoading: plansLoading,
     isError: plansError,
   } = useGatewayPlans()
  const [selectedPlanId, setSelectedPlanId] = useState<string | null>(null)
  const [upgradePlanOpen, setUpgradePlanOpen] = useState(false)
  const billingPeriod = 'monthly' as const
  const { data: gatewayStatus, refetch: refetchGatewayStatus } =
    useGatewayLicenseStatus()
  const [copy, copied] = useCopyToClipboard()
  const refreshMutation = useRefreshLicense()
  const { isLocked, isExpiringSoon, isTrialExpiringSoon } = useLicenseInfo()

  // Check for upgrade success or downgrade pending from URL params
  const searchParams = useSearch({ strict: false }) as {
    upgrade_success?: boolean
    plan?: string
    activation_token?: string // Fallback token if automatic callback failed
    activation_fallback?: boolean // Flag indicating callback failed
    downgrade?: string // 'pending' when user has confirmed downgrade to free
  }
  const [upgradeSuccessShown, setUpgradeSuccessShown] = useState(false)
  const [isActivating, setIsActivating] = useState(false)
  const [showManualActivation, setShowManualActivation] = useState(false)
  const [activationPlan, setActivationPlan] = useState<string>('')
  const [prefillToken, setPrefillToken] = useState<string>('')
  const activationStartTimeRef = useRef<number | null>(null)

  // Pending cancellation state (persisted in localStorage)
  const [pendingCancellation, setPendingCancellation] = useState<{
    active: boolean
    cancelAt?: string
  }>({ active: false })

  // Listen for topbar upgrade button event
  useEffect(() => {
    const handler = () => setUpgradePlanOpen(true)
    window.addEventListener('evs:open-upgrade-dialog', handler)
    return () => window.removeEventListener('evs:open-upgrade-dialog', handler)
  }, [])

  // Load pending cancellation from localStorage on mount
  useEffect(() => {
    const stored = localStorage.getItem('everstack_pending_cancellation')
    if (stored) {
      try {
        const parsed = JSON.parse(stored)
        // Check if the cancellation date has passed
        if (parsed.cancelAt && new Date(parsed.cancelAt) < new Date()) {
          localStorage.removeItem('everstack_pending_cancellation')
        } else {
          setPendingCancellation(parsed)
        }
      } catch {
        localStorage.removeItem('everstack_pending_cancellation')
      }
    }
  }, [])

  // Handle downgrade=pending from URL params
  useEffect(() => {
    if (searchParams?.downgrade === 'pending') {
      // Calculate end of current billing period (approximate: 30 days from now if we don't have exact date)
      const cancelAt = new Date()
      cancelAt.setDate(cancelAt.getDate() + 30) // Approximate end of billing period

      const pendingState = {
        active: true,
        cancelAt: cancelAt.toISOString(),
      }
      setPendingCancellation(pendingState)
      localStorage.setItem(
        'everstack_pending_cancellation',
        JSON.stringify(pendingState),
      )

      toast.info(
        'Your subscription will be cancelled at the end of your billing period.',
        { duration: 8000 },
      )

      // Clean up URL
      window.history.replaceState({}, '', '/settings/billing')
    }
  }, [searchParams?.downgrade])

  // Subscribe to license events via SSE - receives real-time activation updates
  useWaitForActivation({
    enabled: isActivating,
    onSuccess: (details: ActivationDetails) => {
      setIsActivating(false)
      activationStartTimeRef.current = null

      // Update the activation plan state with the actual plan from the event
      if (details.planTier) {
        setActivationPlan(details.planTier)
      }

      // Show success message with plan details
      const planName = details.planTier
        ? details.planTier.charAt(0).toUpperCase() + details.planTier.slice(1)
        : activationPlan || 'new'

      toast.success(`Gateway activated with ${planName} plan!`, {
        duration: 5000,
      })

      // Force refetch to update the dashboard with new plan data
      refetch()
      refetchGatewayStatus()

      // Notify other tabs that license changed (sidebar banner etc.)
      localStorage.setItem('evs:license-changed', Date.now().toString())
      window.dispatchEvent(new CustomEvent('evs:license-changed'))
    },
    onError: (reason) => {
      setIsActivating(false)
      activationStartTimeRef.current = null
      setShowManualActivation(true)
      toast.error(
        reason ||
          'Automatic activation failed. Please enter your activation token manually.',
        { duration: 8000 },
      )
    },
  })

  // Subscribe to subscription status events via SSE - receives real-time cancellation/resume/plan change updates
  useSubscriptionStatusEvents({
    enabled: true,
    onCanceled: (details: SubscriptionStatusDetails) => {
      // Update pending cancellation state with actual data from the event
      const pendingState = {
        active: true,
        cancelAt: details.currentPeriodEnd,
      }
      setPendingCancellation(pendingState)
      localStorage.setItem(
        'everstack_pending_cancellation',
        JSON.stringify(pendingState),
      )

      toast.info(
        'Your subscription has been scheduled for cancellation at the end of your billing period.',
        { duration: 8000 },
      )

      // Force refetch to update the dashboard
      refetch()
      refetchGatewayStatus()
      localStorage.setItem('evs:license-changed', Date.now().toString())
      window.dispatchEvent(new CustomEvent('evs:license-changed'))
    },
    onResumed: (_details: SubscriptionStatusDetails) => {
      // Clear pending cancellation state
      setPendingCancellation({ active: false })
      localStorage.removeItem('everstack_pending_cancellation')

      toast.success('Your subscription has been resumed!', { duration: 5000 })

      // Force refetch to update the dashboard
      refetch()
      refetchGatewayStatus()
      localStorage.setItem('evs:license-changed', Date.now().toString())
      window.dispatchEvent(new CustomEvent('evs:license-changed'))
    },
    onPlanChanged: (details: SubscriptionStatusDetails) => {
      const planName = details.planTier
        ? details.planTier.charAt(0).toUpperCase() + details.planTier.slice(1)
        : 'new'

      toast.success(`Your plan has been updated to ${planName}!`, {
        duration: 5000,
      })

      // Force refetch to update the dashboard with new plan
      refetch()
      refetchGatewayStatus()
      localStorage.setItem('evs:license-changed', Date.now().toString())
      window.dispatchEvent(new CustomEvent('evs:license-changed'))
    },
  })

  // Poll license status while waiting for activation
  // This is a fallback in case SSE events are missed
  useEffect(() => {
    if (!isActivating) return

    // Set activation start time if not already set
    if (activationStartTimeRef.current === null) {
      activationStartTimeRef.current = Date.now()
    }

    const checkActivation = async () => {
      // Timeout after 30 seconds
      const elapsed =
        Date.now() - (activationStartTimeRef.current || Date.now())
      if (elapsed > 30000) {
        setIsActivating(false)
        activationStartTimeRef.current = null
        setShowManualActivation(true)
        toast.error(
          'Activation is taking longer than expected. Please enter your activation token manually.',
          { duration: 8000 },
        )
        return
      }

      // Refetch license status to check if activation completed
      try {
        const result = await refetch()
        const currentTier = result.data?.license?.tier?.toLowerCase()
        const expectedTier = activationPlan?.toLowerCase()

        // Check if the tier matches what we're activating to
        // This prevents false positives when already on a paid plan (e.g., Pro -> Basic downgrade)
        if (expectedTier && currentTier === expectedTier) {
          // Activation completed with correct tier!
          setIsActivating(false)
          activationStartTimeRef.current = null
          const planName = currentTier
            ? currentTier.charAt(0).toUpperCase() + currentTier.slice(1)
            : activationPlan || 'new'
          toast.success(`Gateway activated with ${planName} plan!`, {
            duration: 5000,
          })
          refetchGatewayStatus()
          localStorage.setItem('evs:license-changed', Date.now().toString())
          window.dispatchEvent(new CustomEvent('evs:license-changed'))
        } else if (!expectedTier && result.data?.license?.is_paid) {
          // Fallback: if we don't know the expected tier, just check if paid (legacy behavior)
          setIsActivating(false)
          activationStartTimeRef.current = null
          const tier = result.data?.license?.tier
          const planName = tier
            ? tier.charAt(0).toUpperCase() + tier.slice(1)
            : 'new'
          toast.success(`Gateway activated with ${planName} plan!`, {
            duration: 5000,
          })
          refetchGatewayStatus()
          localStorage.setItem('evs:license-changed', Date.now().toString())
          window.dispatchEvent(new CustomEvent('evs:license-changed'))
        }
      } catch {
        // Ignore errors, keep waiting
      }
    }

    // Check immediately and then every 3 seconds
    checkActivation()
    const intervalId = setInterval(checkActivation, 3000)

    return () => clearInterval(intervalId)
  }, [isActivating, refetch, refetchGatewayStatus, activationPlan])

  // Handle upgrade success from URL params (returned from Stripe checkout)
  useEffect(() => {
    if (searchParams?.upgrade_success && !upgradeSuccessShown) {
      setUpgradeSuccessShown(true)
      if (searchParams.plan) {
        setActivationPlan(searchParams.plan)
      }

      // Check if we have an activation token from fallback (callback failed)
      if (searchParams.activation_fallback && searchParams.activation_token) {
        // Skip SSE wait - we already know callback failed, show manual activation with prefilled token
        setPrefillToken(searchParams.activation_token)
        setShowManualActivation(true)
        toast.info('Payment successful! Please confirm activation below.', {
          duration: 5000,
        })
      } else {
        // Start listening for activation events via SSE
        setIsActivating(true)
        toast.info(
          `Payment successful! Activating your ${searchParams.plan || 'new'} plan...`,
          { duration: 5000 },
        )
      }

      // Clean up URL immediately
      window.history.replaceState({}, '', '/settings/billing')
    }
  }, [
    searchParams?.upgrade_success,
    searchParams?.plan,
    searchParams?.activation_token,
    searchParams?.activation_fallback,
    upgradeSuccessShown,
  ])

  const handleRefresh = () => {
    refreshMutation.mutate()
  }

  // Handle resuming a cancelled subscription
  const [isResuming, setIsResuming] = useState(false)
  const handleResumeSubscription = async () => {
    setIsResuming(true)
    try {
      // This would call the cloud's resume endpoint
      // For now, we just clear the local state since we don't have org context here
      // In a full implementation, this would make an API call
      localStorage.removeItem('everstack_pending_cancellation')
      setPendingCancellation({ active: false })
      toast.success('Subscription resumed successfully!', { duration: 5000 })
      refetch() // Refresh license status
      localStorage.setItem('evs:license-changed', Date.now().toString())
      window.dispatchEvent(new CustomEvent('evs:license-changed'))
    } catch (err) {
      console.error('Failed to resume subscription:', err)
      toast.error('Failed to resume subscription. Please try again.')
    } finally {
      setIsResuming(false)
    }
  }

  const handleCopy = useCallback(() => {
    copy(gatewayStatus?.instanceId || 'Unknown')
  }, [copy, gatewayStatus?.instanceId])

  if (isLoading || plansLoading) {
    return (
      <div className="flex h-[50vh] items-center justify-center">
        <div className="flex flex-col items-center gap-4">
          <Icon
            icon="lucide:loader-2"
            className="h-8 w-8 animate-spin text-white/20 light:text-black/20"
          />
          <p className="text-sm text-white/40 light:text-black/40">
            Loading billing information...
          </p>
        </div>
      </div>
    )
  }

  if (isError || plansError) {
    return (
      <div className="flex h-[50vh] items-center justify-center p-4">
        <Card className="w-full max-w-md border-red-500/20 bg-red-500/5">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-red-400 light:text-red-600">
              <Icon icon="lucide:alert-circle" className="h-5 w-5" />
              Error Loading Billing Info
            </CardTitle>
            <CardDescription className="text-red-200/60 light:text-red-700/60">
              {error?.message || 'Failed to load billing information'}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Button
              onClick={() => refetch()}
              variant="outline"
              className="w-full border-red-500/20 hover:bg-red-500/10 hover:text-red-400 light:hover:text-red-600"
            >
              <Icon icon="lucide:refresh-cw" className="mr-2 h-4 w-4" />
              Try Again
            </Button>
          </CardContent>
        </Card>
      </div>
    )
  }

  if (!data) return null

  const { license, usage, gateway } = data
  const currentPlanId = license.tier?.toLowerCase()

  /**
   * Handle upgrade to cloud - generates callback secret, stores it, and redirects to cloud
   * The instance ID is now always available (generated at first startup), even before activation
   */
  const handleCloudUpgrade = async (
    planTier: string,
    selectedBillingPeriod: 'monthly' | 'yearly' = 'monthly',
  ) => {
    const instanceId = gatewayStatus?.instanceId
    if (!instanceId) {
      // This should not happen after the migration, but handle gracefully
      toast.error(
        'Gateway instance ID not available. Please restart the gateway and try again.',
      )
      return
    }

    try {
      // Generate a unique callback secret
      // Use crypto.randomUUID if available, otherwise fallback for older browsers/non-secure contexts
      const callbackSecret =
        typeof crypto?.randomUUID === 'function'
          ? crypto.randomUUID()
          : 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c) => {
              const r = (Math.random() * 16) | 0
              const v = c === 'x' ? r : (r & 0x3) | 0x8
              return v.toString(16)
            })

      // Store the callback secret in the gateway (for verification when cloud calls back)
      await storeUpgradeCallbackSecret(
        callbackSecret,
        planTier,
        selectedBillingPeriod,
      )

      // Get the gateway's public URL (current origin)
      const gatewayUrl = window.location.origin

      // Build a secure cloud upgrade URL with a time-limited session
      // This creates a session on the billing service that validates the callback secret
      // and returns a short-lived session ID instead of exposing raw params
      const cloudUrl = await buildSecureCloudUpgradeUrl({
        instanceId,
        gatewayUrl,
        callbackSecret,
        planTier,
        billingPeriod: selectedBillingPeriod,
        currentTier: currentPlanId,
      })

      window.open(cloudUrl, '_blank')
    } catch (err) {
      console.error('Failed to initiate upgrade:', err)
      toast.error('Failed to start upgrade process. Please try again.')
    }
  }

  const cacheHitRate =
    usage.cache_hits + usage.cache_misses > 0
      ? (
          (usage.cache_hits / (usage.cache_hits + usage.cache_misses)) *
          100
        ).toFixed(1)
      : '0'

  return (
    <TooltipProvider>
      <div className="space-y-4 p-8 w-full px-60 mx-auto h-full overflow-y-auto">
        <UpgradeLicenseDialog
          open={!!selectedPlanId || showManualActivation}
          onOpenChange={(open) => {
            if (!open) {
              setSelectedPlanId(null)
              setShowManualActivation(false)
              setPrefillToken('')
            }
          }}
          targetPlanId={selectedPlanId || activationPlan || ''}
          billingPeriod={billingPeriod}
          isManualFallback={showManualActivation}
          prefillToken={prefillToken}
        />

        <UpgradePlanDialog
          open={upgradePlanOpen}
          onOpenChange={setUpgradePlanOpen}
          plans={plans}
          currentPlanId={currentPlanId}
          onUpgrade={handleCloudUpgrade}
        />

        {/* Activation in Progress Banner */}
        {isActivating && (
          <div className="rounded-lg border border-blue-500/20 bg-blue-500/10 p-4 flex items-start gap-4">
            <Icon
              icon="lucide:loader-2"
              className="h-5 w-5 text-blue-400 light:text-blue-600 mt-0.5 animate-spin"
            />
            <div>
              <h3 className="font-medium text-blue-400 light:text-blue-600">
                Activating Your Plan
              </h3>
              <p className="text-sm text-blue-300/80 light:text-blue-600/80 mt-1">
                Your payment was successful. The gateway is being activated with
                your new plan. This usually takes just a few seconds...
              </p>
            </div>
          </div>
        )}

        {/* Pending Cancellation Banner */}
        {pendingCancellation.active && (
          <Card className="rounded-sm border-brand-secondary-500/30 bg-gradient-to-r from-brand-secondary-500/5 to-brand-secondary-600/5 overflow-hidden">
            <CardContent className="py-2 px-4 flex items-center justify-between gap-4">
              <div className="flex items-center gap-4">
                <div className="p-2 rounded-lg bg-brand-secondary-500/10">
                  <Icon
                    icon="lucide:calendar-clock"
                    className="h-5 w-5 text-brand-secondary-400"
                  />
                </div>
                <div>
                  <div className="flex items-center gap-2">
                    <h3 className="font-medium text-white light:text-brand-main-50">
                      Downgrade Scheduled
                    </h3>
                    <Badge
                      variant="secondary"
                      className="bg-brand-secondary-500/10 border-brand-secondary-500/20 text-brand-secondary-400 text-xs"
                    >
                      {pendingCancellation.cancelAt &&
                        dayjs(pendingCancellation.cancelAt).format(
                          'MMM D, YYYY',
                        )}
                    </Badge>
                  </div>
                  <p className="text-sm text-white/60 light:text-black/60 mt-0.5">
                    Your plan will change to Free at the end of your billing
                    period. You have full access until then.
                  </p>
                </div>
              </div>
              <Button
                variant="outline"
                size="sm"
                onClick={handleResumeSubscription}
                disabled={isResuming}
                className="shrink-0 border-brand-secondary-500/30 text-brand-secondary-400 hover:bg-brand-secondary-500/10 hover:text-brand-secondary-300"
              >
                {isResuming ? (
                  <>
                    <Icon
                      icon="lucide:loader-2"
                      className="h-4 w-4 animate-spin mr-1"
                    />
                    Resuming...
                  </>
                ) : (
                  <>
                    <Icon icon="lucide:rotate-ccw" className="h-4 w-4 mr-1" />
                    Resume Subscription
                  </>
                )}
              </Button>
            </CardContent>
          </Card>
        )}

        {/* Alerts Section */}
        {isLocked && !isActivating && (
          <div className="rounded-lg border border-red-500/20 bg-red-500/10 p-4 flex items-start gap-4">
            <Icon
              icon="lucide:lock"
              className="h-5 w-5 text-red-400 light:text-red-600 mt-0.5"
            />
            <div>
              <h3 className="font-medium text-red-400 light:text-red-600">
                Gateway Locked
              </h3>
              <p className="text-sm text-red-300/80 light:text-red-600/80 mt-1">
                {gateway.lock_reason}
              </p>
            </div>
          </div>
        )}

        {(isExpiringSoon || isTrialExpiringSoon) && !isLocked && (
          <div className="rounded-lg border border-yellow-500/20 bg-yellow-500/10 p-4 flex items-start gap-4">
            <Icon
              icon="lucide:alert-triangle"
              className="h-5 w-5 text-yellow-400 light:text-yellow-700 mt-0.5"
            />
            <div>
              <h3 className="font-medium text-yellow-400 light:text-yellow-700">
                {license.is_paid
                  ? 'License Expiring Soon'
                  : 'Free Plan Ending Soon'}
              </h3>
              <p className="text-sm text-yellow-300/80 light:text-yellow-700/80 mt-1">
                {license.is_paid && license.expires_at
                  ? `Your license expires ${dayjs(license.expires_at).fromNow()}. Please renew to avoid service interruption.`
                  : license.trial_expires
                    ? `Your free plan ends ${dayjs(license.trial_expires).fromNow()}. Upgrade to continue using all features.`
                    : 'Please upgrade to avoid service interruption.'}
              </p>
            </div>
          </div>
        )}

        {/* Tabs with badges and actions */}
        <Tabs defaultValue="overview" className="w-full">
          <div className="flex items-center justify-between">
            <TabsList className="w-fit bg-brand-main-800/50 border border-brand-main-600 rounded p-1 h-auto gap-1">
              <TabsTrigger className={TAB_TRIGGER_CLASS} value="overview">
                Overview
              </TabsTrigger>
              <TabsTrigger className={TAB_TRIGGER_CLASS} value="usage">
                Usage
              </TabsTrigger>
              <TabsTrigger className={TAB_TRIGGER_CLASS} value="spend">
                Spend Limits
              </TabsTrigger>
            </TabsList>
            <div className="flex items-center gap-3">
              <Badge
                variant="primary"
                className="capitalize border-brand-secondary-500/50 text-brand-secondary-400 bg-brand-secondary-500/10"
              >
                {license.tier || 'Free'}
              </Badge>
              {gatewayStatus?.instanceId && (
                <Tooltip content="Copy Instance ID" side="bottom">
                  <Badge
                    variant="secondary"
                    className="hover:text-white light:hover:text-brand-main-50 px-2 py-1 cursor-pointer text-xs"
                    onClick={handleCopy}
                  >
                    {copied ? (
                      <Icon
                        icon="lucide:check"
                        className="h-3 w-3 text-green-500 mr-1"
                      />
                    ) : null}
                    {gatewayStatus.instanceId}
                  </Badge>
                </Tooltip>
              )}
            </div>
          </div>

          {/* ─── Overview ─── */}
          <TabsContent value="overview" className="space-y-6 mt-4">
            {/* Metric cards */}
            <div>
              <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider mb-3">
                Current Usage
              </div>
              <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
                <div className="rounded border border-brand-main-600 bg-brand-main-800/50 p-3">
                  <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
                    Requests / Min
                  </div>
                  <div className="text-lg font-semibold font-mono text-white light:text-brand-main-50 mt-1">
                    {formatNumber(usage.requests_in_min)}
                  </div>
                  <div className="text-xs text-white/40 light:text-black/40 mt-0.5">
                    Current load
                  </div>
                </div>
                <div className="rounded border border-brand-main-600 bg-brand-main-800/50 p-3">
                  <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
                    Total Requests
                  </div>
                  <div className="text-lg font-semibold font-mono text-white light:text-brand-main-50 mt-1">
                    {formatNumber(usage.total_requests)}
                  </div>
                  <div className="text-xs text-white/40 light:text-black/40 mt-0.5">
                    Since {dayjs(usage.last_reset).format('MMM D')}
                  </div>
                </div>
                <div className="rounded border border-brand-main-600 bg-brand-main-800/50 p-3">
                  <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
                    Total Tokens
                  </div>
                  <div className="text-lg font-semibold font-mono text-white light:text-brand-main-50 mt-1">
                    {formatNumber(usage.total_tokens)}
                  </div>
                  <div className="text-xs text-white/40 light:text-black/40 mt-0.5">
                    Since {dayjs(usage.last_reset).format('MMM D')}
                  </div>
                </div>
                <div className="rounded border border-brand-main-600 bg-brand-main-800/50 p-3">
                  <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
                    Cache Hit Rate
                  </div>
                  <div className="text-lg font-semibold font-mono text-white light:text-brand-main-50 mt-1">
                    {cacheHitRate}%
                  </div>
                  <div className="text-xs text-white/40 light:text-black/40 mt-0.5">
                    {formatNumber(usage.cache_hits + usage.cache_misses)} total
                  </div>
                </div>
              </div>
            </div>

            {/* Plan details */}
            <div className="grid grid-cols-2 gap-4">
              <div className="rounded border border-brand-main-600 bg-brand-main-800/50 p-3">
                <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
                  Active Plan
                </div>
                <div className="text-sm font-medium text-white light:text-brand-main-50 mt-1 capitalize">
                  {license.tier || 'Free'}
                </div>
              </div>
              <div className="rounded border border-brand-main-600 bg-brand-main-800/50 p-3">
                <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
                  Status
                </div>
                <div className="flex items-center gap-2 mt-1">
                  <div
                    className={`h-2 w-2 rounded-full ${license.active ? 'bg-green-500' : 'bg-red-500'}`}
                  />
                  <span className="capitalize text-sm font-medium text-white light:text-brand-main-50">
                    {license.status}
                  </span>
                </div>
              </div>
              <div className="rounded border border-brand-main-600 bg-brand-main-800/50 p-3">
                <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
                  {pendingCancellation.active
                    ? 'Downgrades'
                    : license.is_paid
                      ? 'Renews'
                      : 'Free Plan Ends'}
                </div>
                <div className="text-sm font-medium text-white light:text-brand-main-50 mt-1">
                  {pendingCancellation.active && pendingCancellation.cancelAt
                    ? dayjs(pendingCancellation.cancelAt).format('MMM D, YYYY')
                    : license.is_paid
                      ? license.expires_at
                        ? dayjs(license.expires_at).format('MMM D, YYYY')
                        : 'Never'
                      : license.trial_expires
                        ? dayjs(license.trial_expires).format('MMM D, YYYY')
                        : 'Never'}
                </div>
              </div>
              <div className="rounded border border-brand-main-600 bg-brand-main-800/50 p-3">
                <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
                  Estimated Cost
                </div>
                <div className="text-sm font-medium text-white light:text-brand-main-50 mt-1">
                  ${usage.estimated_cost_usd.toFixed(2)}
                </div>
              </div>
            </div>

            {/* Warning for expiring soon */}
            {license.expires_at &&
              dayjs(license.expires_at).diff(dayjs(), 'days') <= 7 && (
                <div className="p-4 rounded-lg border border-yellow-500/20 bg-yellow-500/10 flex items-start gap-3">
                  <Icon
                    icon="lucide:alert-triangle"
                    className="h-5 w-5 text-yellow-400 light:text-yellow-700 mt-0.5 shrink-0"
                  />
                  <div>
                    <p className="font-medium text-yellow-400 light:text-yellow-700">
                      Plan Expiring Soon
                    </p>
                    <p className="text-sm text-yellow-300/80 light:text-yellow-700/80 mt-1">
                      Your plan expires {dayjs(license.expires_at).fromNow()}.
                      Renew now to avoid service interruption.
                    </p>
                  </div>
                </div>
              )}

            {/* Activation & Next Plan row */}
            <div className="flex gap-4 w-full">
              <Card className="border-brand-main-600 rounded-md bg-brand-main-950 w-1/2">
                <CardHeader>
                  <CardTitle className="flex items-center gap-2">
                    <Icon
                      icon="lucide:key"
                      className="h-5 w-5 text-brand-main-400"
                    />
                    {!license.is_paid
                      ? 'Activate or Upgrade'
                      : 'Upgrade with Activation Token'}
                  </CardTitle>
                  <CardDescription>
                    {!license.is_paid
                      ? 'Use an activation token to upgrade your plan or reactivate on a new instance.'
                      : 'Have an activation token for a different plan already? Use it to upgrade your plan.'}
                  </CardDescription>
                </CardHeader>
                <CardContent className="-mt-2">
                  <Button onClick={() => setSelectedPlanId('upgrade')}>
                    <Icon icon="lucide:arrow-up-circle" className="h-4 w-4" />
                    Enter Activation Token
                  </Button>
                </CardContent>
              </Card>

              {(() => {
                const PLAN_ORDER = ['free', 'basic', 'pro', 'enterprise']
                const currentIndex = PLAN_ORDER.indexOf(currentPlanId || 'free')
                const nextPlanId =
                  currentIndex < PLAN_ORDER.length - 1
                    ? PLAN_ORDER[currentIndex + 1]
                    : null
                const nextPlan = plans?.find((p) => p.tier === nextPlanId)

                if (!nextPlan) return null

                return (
                  <Card className="relative rounded-md border-brand-secondary-600/50 overflow-hidden flex flex-col items-center justify-center w-1/2">
                    <div className="relative flex justify-between w-full">
                      <CardHeader className="relative z-10 w-full">
                        <div className="flex items-center gap-2">
                          <Icon
                            icon="lucide:sparkles"
                            className="h-5 w-5 text-brand-secondary-400"
                          />
                          <Badge
                            variant="secondary"
                            className="bg-brand-secondary-500/20 text-brand-secondary-300 border-brand-secondary-500/30"
                          >
                            Recommended
                          </Badge>
                        </div>
                        <CardTitle className="text-lg">
                          Enjoy the best of {nextPlan.name} features
                        </CardTitle>
                        <CardDescription className="text-white/60 light:text-black/60">
                          {nextPlan.description ||
                            'Unlock more features and higher usage limits'}
                        </CardDescription>
                      </CardHeader>
                      <CardContent className="relative z-10 w-1/2 mt-2">
                        <div className="flex flex-col gap-2 justify-start">
                          <div className="flex items-baseline gap-1">
                            <span className="text-2xl font-bold text-white light:text-brand-main-50">
                              {nextPlan.pricing[billingPeriod] || 'Contact us'}
                            </span>
                            {nextPlan.pricing[billingPeriod] && (
                              <span className="text-sm text-white/50 light:text-black/50">
                                /{billingPeriod === 'monthly' ? 'mo' : 'yr'}
                              </span>
                            )}
                          </div>
                          <Button
                            className="bg-white text-brand-secondary-800 border border-transparent hover:bg-brand-secondary-50/90"
                            onClick={() => {
                              if (nextPlan.tier === 'enterprise') {
                                window.open(
                                  'https://everstack.ai/contact',
                                  '_blank',
                                )
                              } else {
                                handleCloudUpgrade(nextPlan.tier, billingPeriod)
                              }
                            }}
                          >
                            {nextPlan.tier === 'enterprise'
                              ? 'Contact Sales'
                              : 'Upgrade to ' + nextPlan.name}
                          </Button>
                        </div>
                      </CardContent>
                    </div>

                    <div className="absolute inset-0 bg-gradient-to-br from-brand-secondary-900 via-brand-secondary-950 to-brand-secondary-900" />
                    <div
                      className="absolute inset-0 opacity-40 mix-blend-overlay"
                      style={{
                        backgroundImage: `url("data:image/svg+xml,%3Csvg viewBox='0 0 400 400' xmlns='http://www.w3.org/2000/svg'%3E%3Cfilter id='noiseFilter'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='0.9' numOctaves='4' stitchTiles='stitch'/%3E%3C/filter%3E%3Crect width='100%25' height='100%25' filter='url(%23noiseFilter)'/%3E%3C/svg%3E")`,
                      }}
                    />
                  </Card>
                )
              })()}
            </div>
          </TabsContent>

          {/* ─── Usage ─── */}
          <TabsContent value="usage" className="space-y-6 mt-4">
            <div className="flex items-center justify-between">
              <div>
                <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
                  Usage Statistics
                </div>
                <p className="text-sm text-brand-main-200 mt-1">
                  Real-time metrics against your plan limits
                </p>
              </div>
              <Button
                variant="ghost"
                size="sm"
                onClick={handleRefresh}
                className="h-8 w-8 p-0 hover:bg-white/5 light:hover:bg-black/5"
              >
                <Icon
                  icon="lucide:refresh-cw"
                  className={cn(
                    'h-4 w-4 text-white/60 light:text-black/60',
                    refreshMutation.isPending && 'animate-spin',
                  )}
                />
              </Button>
            </div>

            {/* Plan Limits */}
            <div className="space-y-5">
              <h3 className="text-xs font-semibold text-white/50 light:text-black/50 uppercase tracking-wider">
                Plan Limits
              </h3>
              {license.usage_limits?.map((limit) => {
                let currentValue = 0
                let label = ''
                let description = ''

                switch (limit.type) {
                  case 'RPM':
                    currentValue = usage.requests_in_min
                    label = 'Requests / Minute'
                    description = 'Current load'
                    break
                  case 'RPS':
                    currentValue = usage.requests_in_sec
                    label = 'Requests / Second'
                    description = 'Current load'
                    break
                  case 'RPH':
                    currentValue = usage.requests_in_hour
                    label = 'Requests / Hour'
                    description = 'Current load'
                    break
                  case 'TOKENS':
                    currentValue = usage.total_tokens
                    label = 'Total Tokens'
                    description = `Since ${dayjs(usage.last_reset).format('MMM D')}`
                    break
                  case 'REQUESTS':
                    currentValue = usage.total_requests
                    label = 'Total Requests'
                    description = `Since ${dayjs(usage.last_reset).format('MMM D')}`
                    break
                  case 'STORAGE_BYTES':
                    label = 'Storage'
                    break
                  case 'DATASET_ITEMS':
                    label = 'Dataset Items'
                    break
                  case 'EVAL_RUNS_MONTHLY':
                    label = 'Eval Runs / Month'
                    break
                  case 'ANNOTATION_QUEUES':
                    label = 'Annotation Queues'
                    break
                  case 'PERSISTENT_TROOPERS':
                    label = 'Persistent Instances'
                    break
                  case 'MAX_KEYS':
                    label = 'API Keys'
                    break
                  default:
                    // Fallback: humanize the type string (e.g. "SOME_TYPE" -> "Some Type")
                    label = limit.type
                      .split('_')
                      .map((w) => w.charAt(0) + w.slice(1).toLowerCase())
                      .join(' ')
                    break
                }

                if (!label) return null

                return (
                  <MetricBar
                    key={limit.type}
                    label={label}
                    value={currentValue}
                    limit={limit.limit}
                    description={description}
                  />
                )
              })}
            </div>

            {/* Token Usage */}
            <div className="border-t border-white/10 light:border-black/10 pt-6 space-y-5">
              <h3 className="text-xs font-semibold text-white/50 light:text-black/50 uppercase tracking-wider">
                Token Usage
              </h3>
              <MetricBar
                label="Input Tokens"
                value={usage.total_input_tokens}
                limit={usage.total_tokens > 0 ? usage.total_tokens : -1}
                description={`Since ${dayjs(usage.last_reset).format('MMM D')}`}
              />
              <MetricBar
                label="Output Tokens"
                value={usage.total_output_tokens}
                limit={usage.total_tokens > 0 ? usage.total_tokens : -1}
                description={`Since ${dayjs(usage.last_reset).format('MMM D')}`}
              />
            </div>

            {/* Cost & Savings */}
            <div className="border-t border-white/10 light:border-black/10 pt-6 space-y-5">
              <h3 className="text-xs font-semibold text-white/50 light:text-black/50 uppercase tracking-wider">
                Cost & Savings
              </h3>
              <MetricBar
                label="Estimated Cost"
                value={usage.estimated_cost_usd}
                limit={-1}
                unit="$"
                description={`Since ${dayjs(usage.last_reset).format('MMM D')}`}
              />
              <MetricBar
                label="Cache Savings"
                value={usage.cache_savings_usd}
                limit={usage.estimated_cost_usd}
                unit="$"
                description="Total saved through caching"
              />
            </div>

            {/* Cache Performance */}
            <div className="border-t border-white/10 light:border-black/10 pt-6 space-y-5">
              <h3 className="text-xs font-semibold text-white/50 light:text-black/50 uppercase tracking-wider">
                Cache Performance
              </h3>
              <MetricBar
                label="Cache Hit Rate"
                value={usage.cache_hits}
                limit={usage.cache_hits + usage.cache_misses}
                description={`${formatNumber(usage.cache_hits + usage.cache_misses)} total requests`}
                formatter={(val) =>
                  `${usage.cache_hits + usage.cache_misses > 0 ? ((val / (usage.cache_hits + usage.cache_misses)) * 100).toFixed(1) : 0}%`
                }
              />
            </div>
          </TabsContent>

          {/* ─── Spend Limits ─── */}
          <TabsContent value="spend" className="space-y-6 mt-4">
            <Feature
              flag={FeatureKey.SPEND_LIMITS}
              fallback={
                <div className="text-center py-12 text-white/50 light:text-black/50">
                  <Icon
                    icon="lucide:wallet"
                    className="h-10 w-10 mx-auto mb-4 opacity-50"
                  />
                  <p className="text-sm font-medium">Spend Limits</p>
                  <p className="text-xs mt-1">
                    This feature is not available on your current plan.
                  </p>
                </div>
              }
            >
              <div>
                <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
                  Spend Limits
                </div>
                <p className="text-sm text-brand-main-200 mt-1">
                  Control your API costs
                </p>
              </div>
              <SpendLimitsSection
                organizationId={license?.tenant_id}
                instanceId={license?.instance_id}
              />
            </Feature>
          </TabsContent>
        </Tabs>
      </div>
    </TooltipProvider>
  )
}
