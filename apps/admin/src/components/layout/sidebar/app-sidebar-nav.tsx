import { useLocation } from '@tanstack/react-router'
import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import {
  SidebarNav,
  type SidebarNavAreas,
  type SidebarNavGroups,
} from './sidebar-nav'
import {
  useLicenseInfo,
  useLicenseStatus,
  useIsCommunityEdition,
  licenseKeys,
} from '@/hooks/license/use-license-status'
import { useTrialStatus } from '@/hooks/license/use-trial-status'
import { GatewayLockedBanner } from './gateway-locked-banner'
import { SpendBlockedBanner } from './spend-blocked-banner'
import { TrialModeBanner } from './trial-mode-banner'
import { FreePlanBanner } from './free-plan-banner'
import { CommunityEditionBanner } from './community-edition-banner'
import { VersionUpdateBanner } from './version-update-banner'
import { OnboardingChecklist } from '@/components/onboarding/onboarding-checklist'
import { useOnboarding } from '@/hooks/use-onboarding'
import { useVersionCheck } from '@/hooks/use-version-check'
import { UserMenu } from './user-menu'
import { useFeatureSet, type FeatureSet } from '@/hooks/use-features'
import { FeatureKey } from '@/config/features'
import { usePermissions } from '@/hooks/auth'
import { SidebarModeToggle, type SidebarMode } from './sidebar-mode-toggle'
import { ChatSessionsList } from './chat-sessions-list'
import { HelpMenu } from './help-menu'
type SidebarNavData = {
  pathname: string
  queryString: string
  showNews?: boolean
  pendingPayoutsCount?: number
  applicationsCount?: number
  submittedBountiesCount?: number
  unreadMessagesCount?: number
  showConversionGuides?: boolean
  featureSet?: FeatureSet
  auditLogsBadge?: ReactNode
  voiceBadge?: ReactNode
  evaluationsBadge?: ReactNode
  alertsBadge?: ReactNode
  teamBadge?: ReactNode
  metricsBadge?: ReactNode
  sshKeysBadge?: ReactNode
  canManageBilling?: boolean
}

const NAV_GROUPS_TOP: SidebarNavGroups<SidebarNavData> = ({ pathname }) => [
  {
    name: 'Observability',
    icon: 'hugeicons:telescope-02',
    description: 'Observability',
    learnMoreHref: 'https://everstack.ai/docs/observability',
    href: '/observability/logs',
    active: pathname.startsWith('/observability'),
  },
  {
    name: 'Gateway',
    icon: 'mingcute:route-line',
    description: 'Gateway',
    learnMoreHref: 'https://everstack.ai/docs/gateway',
    href: '/gateway/config',
    active: pathname.startsWith('/gateway'),
  },
  {
    name: 'Evaluations',
    icon: 'lucide:flask-conical',
    description: 'Evaluations',
    learnMoreHref: 'https://everstack.ai/docs/evaluations-and-experiments',
    href: '/evaluations',
    active: pathname.startsWith('/evaluations'),
  },
  {
    name: 'Deployments',
    icon: 'ant-design:deployment-unit-outlined',
    description: 'Deployments',
    learnMoreHref: 'https://everstack.ai/docs/Deployments',
    href: '/deployments/studio',
    active:
      pathname.startsWith('/deployments') || pathname.startsWith('/sites'),
  },
  {
    name: 'Vault',
    icon: 'stash:vault',
    description: 'Vault',
    learnMoreHref: 'https://everstack.ai/docs/vault',
    href: '/vault/api-keys',
    active: pathname.startsWith('/vault'),
  },
  {
    name: 'Storage',
    icon: 'lucide:hard-drive',
    description: 'Storage',
    learnMoreHref: 'https://everstack.ai/docs/storage',
    href: '/storage/overview',
    active: pathname.startsWith('/storage'),
  },
]

const NAV_GROUPS_BOTTOM: SidebarNavGroups<SidebarNavData> = ({ pathname }) => [
  {
    name: 'Help',
    icon: 'hugeicons:help-circle',
    href: '#help',
    active: false,
    showTooltip: false,
    render: () => <HelpMenu />,
  },
  {
    name: 'Settings',
    icon: 'ph:gear-bold',
    href: '/settings/general',
    active: pathname.startsWith('/settings'),
  },
]

const CHAT_AREAS: SidebarNavAreas<SidebarNavData> = {
  chat: () => ({
    title: (
      <div className="h-full -mx-3 -my-2.5">
        <ChatSessionsList />
      </div>
    ),
    direction: 'left' as const,
    content: [],
  }),
}

const NAV_AREAS: SidebarNavAreas<SidebarNavData> = {
  gateway: ({ showNews, featureSet }) => ({
    title: 'Gateway',
    showNews,
    direction: 'left',
    // backHref: '/',
    content: [
      {
        name: 'Configs',
        items: [
          {
            name: 'Config',
            href: '/gateway/config',
            icon: 'solar:code-outline',
          },
        ],
      },
      {
        name: 'Registries',
        items: [
          {
            name: 'MCP Gateway',
            href: '/gateway/mcp',
            icon: 'octicon:mcp-16',
          },
          {
            name: 'A2A',
            href: '/gateway/a2a',
            icon: 'lucide:waypoints',
          },
          ...(featureSet?.has(FeatureKey.GUARDRAILS)
            ? [
                {
                  name: 'Guardrails',
                  href: '/gateway/guardrails' as const,
                  icon: 'hugeicons:shield-02',
                },
              ]
            : []),
        ],
      },
    ],
  }),

  evaluations: ({ showNews, featureSet, evaluationsBadge }) => ({
    title: 'Evaluations',
    showNews,
    direction: 'left',
    content: [
      {
        name: 'Evaluations',
        items: [
          {
            name: 'Overview',
            href: '/evaluations',
            // Exact-match (trailing-slash tolerant) so the Overview entry
            // doesn't light up for every /evaluations/* child route.
            isActive: (pathname: string) =>
              pathname === '/evaluations' || pathname === '/evaluations/',
            icon: 'lucide:layout-dashboard',
            badge: evaluationsBadge,
          },
          {
            name: 'Playgrounds',
            href: '/evaluations/playgrounds',
            icon: 'fluent:play-28-regular',
            badge: evaluationsBadge,
          },
          {
            name: 'Datasets',
            href: '/evaluations/datasets',
            icon: 'lucide:database',
            badge: evaluationsBadge,
          },
          {
            name: 'Runs',
            href: '/evaluations/runs',
            icon: 'lucide:play-circle',
            badge: evaluationsBadge,
          },
          {
            name: 'Annotation Queues',
            href: '/evaluations/annotation-queues',
            icon: 'hugeicons:queue-02',
            badge: evaluationsBadge,
          },
          {
            name: 'Score Configs',
            href: '/evaluations/score-configs',
            icon: 'mynaui:hash-hexagon',
            badge: evaluationsBadge,
          },
          {
            name: 'Online Evals',
            href: '/evaluations/online-evals',
            icon: 'lucide:activity',
            badge: evaluationsBadge,
          },
        ],
      },
      ...(featureSet?.has(FeatureKey.PROMPT_PLAYGROUND)
        ? [
            {
              name: 'Prompts Manager',
              items: [
                {
                  name: 'Prompts Library',
                  href: '/evaluations/prompts-library' as const,
                  icon: 'solar:library-line-duotone',
                },
                // Playgrounds now lives in the (ungated) Evaluations group above
                // so it always shows; it is core, not a gated preview.
                // Prompt Partials stays out of the nav until the feature
                // ships — a permanent "coming soon" entry reads as unfinished.
              ],
            },
          ]
        : []),
    ],
  }),

  vault: ({ showNews }) => ({
    title: 'Vault',
    showNews,
    direction: 'left',
    content: [
      {
        name: 'Keys & Providers',
        items: [
          {
            name: 'API Keys',
            href: '/vault/api-keys',
            icon: 'ph:key-bold',
          },
          {
            name: 'LLM Providers',
            href: '/vault/llm-providers',
            icon: 'hugeicons:ai-lock',
          },
        ],
      },
    ],
  }),

  deployments: ({ showNews, voiceBadge }) => {
    const items = [
      {
        name: 'Studio',
        href: '/deployments/studio' as const,
        icon: 'solar:atom-broken',
      },
      {
        name: 'Agents',
        href: '/deployments/agents' as const,
        icon: 'ri:apps-ai-line',
      },
      {
        name: 'Sandboxes',
        href: '/deployments/sandboxes' as const,
        icon: 'lucide:box',
      },
      {
        name: 'Functions',
        href: '/deployments/functions' as const,
        icon: 'hugeicons:function-square',
      },
      {
        name: 'Sites',
        href: '/sites' as const,
        icon: 'lucide:globe',
      },
      {
        name: 'Memory',
        href: '/deployments/memory' as const,
        icon: 'lucide:brain',
      },
      {
        name: 'Channels',
        href: '/deployments/channels' as const,
        icon: 'lucide:message-square',
      },
      {
        name: 'Voice',
        href: '/deployments/voice' as const,
        icon: 'lucide:mic',
        badge: voiceBadge,
      },
    ]

    return {
      title: 'Deployments',
      showNews,
      direction: 'left' as const,
      content: [{ items }],
    }
  },

  observability: ({ showNews, alertsBadge, metricsBadge }) => ({
    title: 'Observability',
    showNews,
    direction: 'left',
    // backHref: '/',
    content: [
      {
        name: 'Monitoring',
        items: [
          {
            name: 'Logs',
            href: '/observability/logs',
            icon: 'tabler:logs',
          },
          {
            name: 'Traces',
            href: '/observability/traces',
            icon: 'lucide:list-tree',
          },
          {
            name: 'Queries',
            href: '/observability/saved-queries',
            icon: 'lucide:bookmark',
          },
          {
            name: 'Issues',
            href: '/observability/issues',
            icon: 'lucide:bug',
          },
        ],
      },
      {
        name: 'Analytics',
        items: [
          {
            name: 'Metrics',
            href: '/observability/metrics',
            icon: 'solar:chart-square-broken',
            badge: metricsBadge,
          },
          {
            name: 'Outcomes',
            href: '/observability/outcomes' as const,
            icon: 'lucide:target',
            badge: metricsBadge,
          },
          {
            name: 'Alerts',
            href: '/observability/alerts',
            icon: 'solar:bell-linear',
            badge: alertsBadge,
          },
        ],
      },
      {
        name: 'Segments',
        items: [
          {
            name: 'Sessions',
            href: '/observability/sessions',
            icon: 'lucide:user-plus',
          },
          {
            name: 'Users',
            href: '/observability/users',
            icon: 'lucide:user-plus',
          },
        ],
      },
    ],
  }),

  license: ({ showNews }) => ({
    title: 'License',
    showNews,
    direction: 'left',
    content: [
      {
        name: 'Overview',
        items: [
          {
            name: 'Status & Usage',
            href: '/license/status-and-usage',
            icon: 'lucide:shield-check',
          },
        ],
      },
    ],
  }),

  account: ({ showNews }) => ({
    title: 'Account',
    showNews,
    direction: 'left',
    content: [
      {
        name: 'Account',
        items: [
          {
            name: 'Profile',
            href: '/account/profile',
            icon: 'lucide:user',
          },
        ],
      },
    ],
  }),

  storage: ({ showNews }) => ({
    title: 'Storage',
    showNews,
    direction: 'left',
    content: [
      {
        name: 'Storage',
        items: [
          {
            name: 'Overview',
            href: '/storage/overview',
            icon: 'lucide:hard-drive',
          },
        ],
      },
    ],
  }),

  settings: ({
    showNews,
    featureSet,
    auditLogsBadge,
    teamBadge,
    sshKeysBadge,
    canManageBilling,
  }) => {
    const trooperItems = [
      {
        name: 'General',
        href: '/settings/general' as const,
        icon: 'hugeicons:settings-03',
      },
      {
        name: 'SSH Keys',
        href: '/settings/ssh-keys' as const,
        icon: 'heroicons:key',
        badge: sshKeysBadge,
      },
      ...(canManageBilling
        ? [
            {
              name: 'Billing',
              href: '/settings/billing' as const,
              icon: 'stash:billing-info-duotone',
            },
          ]
        : []),
      ...(featureSet?.has(FeatureKey.CUSTOM_DOMAIN)
        ? [
            {
              name: 'Domain',
              href: '/settings/domain' as const,
              icon: 'ph:globe',
            },
          ]
        : []),
      {
        name: 'Members',
        href: '/settings/members' as const,
        icon: 'ph:users-bold',
        badge: teamBadge,
      },
      {
        name: 'Integrations',
        href: '/settings/integrations' as const,
        icon: 'ri:apps-line',
      },
    ]

    const sections = [
      {
        name: 'Instance',
        items: trooperItems,
      },
      ...(featureSet?.has(FeatureKey.AI_GATEWAY_SETTINGS)
        ? [
            {
              name: 'AI Gateway',
              items: [
                {
                  name: 'General',
                  href: '/settings/gateway' as const,
                  icon: 'carbon:gateway-security',
                },
                {
                  name: 'Catalog',
                  href: '/settings/catalog' as const,
                  icon: 'hugeicons:catalogue',
                },
              ],
            },
          ]
        : []),
      {
        name: 'System Events',
        items: [
          {
            name: 'Events',
            href: '/settings/events' as const,
            icon: 'solar:list-outline',
            badge: auditLogsBadge,
          },
        ],
      },
    ]

    return {
      title: 'Settings',
      showNews,
      direction: 'left' as const,
      content: sections,
    }
  },
}

export const AppSidebarNav = ({
  toolContent,
  newsContent,
}: {
  toolContent?: ReactNode
  newsContent?: ReactNode
}) => {
  const { pathname } = useLocation()
  const queryClient = useQueryClient()
  const [sidebarMode, setSidebarMode] = useState<SidebarMode>(
    pathname.startsWith('/chat') ? 'chat' : 'browse',
  )
  // Auto-switch sidebar mode based on route
  useEffect(() => {
    if (pathname.startsWith('/chat')) {
      setSidebarMode('chat')
    } else {
      setSidebarMode('browse')
    }
  }, [pathname])
  const { isLocked, isSpendBlocked } = useLicenseInfo()
  const { data: licenseStatus } = useLicenseStatus()
  const { data: trialStatus } = useTrialStatus()
  const featureSet = useFeatureSet()
  const { can } = usePermissions()
  const isCE = useIsCommunityEdition()
  const { isVisible: onboardingVisible } = useOnboarding()
  const { updateAvailable } = useVersionCheck()

  // Invalidate license cache on tab focus and cross-tab storage events.
  // This handles the billing upgrade flow where Stripe opens in a new tab —
  // the original tab picks up the plan change when the user switches back.
  useEffect(() => {
    const invalidate = () => {
      queryClient.invalidateQueries({ queryKey: licenseKeys.status() })
    }
    const handleStorage = (e: StorageEvent) => {
      if (e.key === 'evs:license-changed') invalidate()
    }
    window.addEventListener('focus', invalidate)
    window.addEventListener('storage', handleStorage)
    window.addEventListener('evs:license-changed', invalidate)
    return () => {
      window.removeEventListener('focus', invalidate)
      window.removeEventListener('storage', handleStorage)
      window.removeEventListener('evs:license-changed', invalidate)
    }
  }, [queryClient])

  // Determine if we should show trial banner
  const isTrialMode =
    trialStatus?.mode === 'trial' && trialStatus?.active === true

  // Determine if user is on free plan (licensed but not paid)
  const isFreePlan =
    licenseStatus?.license?.active === true &&
    licenseStatus?.license?.is_paid === false

  // Tier badge helper: CE/free/basic → required tier badge, at-tier → "EE", enterprise → none
  // Dev mode (edition === "dev"): no badges — everything unlocked
  const tier = licenseStatus?.license?.tier?.toLowerCase() ?? 'free'
  const isDev = licenseStatus?.gateway?.edition === 'dev'
  const tierBadge = (featureKey: string, requiredTier: string) => {
    if (isDev) return undefined // dev mode: no badges
    if (isCE || !featureSet?.has(featureKey as any)) return requiredTier
    // Feature is enabled at current tier — upsell to Enterprise if not already there
    if (tier !== 'enterprise') return 'EE'
    return undefined // enterprise — no badge
  }

  const auditLogsBadge = useMemo(
    () => tierBadge(FeatureKey.AUDIT_LOGS, 'Pro'),
    [isCE, featureSet, tier],
  )
  const voiceBadge = useMemo(
    () => tierBadge(FeatureKey.VOICE, 'Pro'),
    [isCE, featureSet, tier],
  )
  const evaluationsBadge = useMemo(
    () => tierBadge(FeatureKey.EVALUATIONS, 'Pro'),
    [isCE, featureSet, tier],
  )
  const alertsBadge = useMemo(
    () => tierBadge(FeatureKey.ALERTS, 'Pro'),
    [isCE, featureSet, tier],
  )
  const teamBadge = useMemo(
    () => tierBadge(FeatureKey.TEAM_MANAGEMENT, 'Basic'),
    [isCE, featureSet, tier],
  )
  const metricsBadge = useMemo(
    () => tierBadge(FeatureKey.ADVANCED_ANALYTICS, 'Pro'),
    [isCE, featureSet, tier],
  )
  const sshKeysBadge = useMemo(
    () => tierBadge(FeatureKey.SANDBOX_SSH, 'Pro'),
    [isCE, featureSet, tier],
  )

  const data = {
    pathname,
    queryString: '',
    showNews:
      isLocked ||
      isSpendBlocked ||
      updateAvailable ||
      isTrialMode ||
      isFreePlan ||
      isCE ||
      onboardingVisible,
    featureSet,
    auditLogsBadge,
    voiceBadge,
    evaluationsBadge,
    alertsBadge,
    teamBadge,
    metricsBadge,
    sshKeysBadge,
    canManageBilling: can('org:manage_billing'),
  }

  const currentArea = useMemo(() => {
    if (pathname === '/') return null
    if (pathname.startsWith('/gateway')) return 'gateway'
    if (pathname.startsWith('/vault')) return 'vault'
    if (pathname.startsWith('/observability')) return 'observability'
    if (pathname.startsWith('/license')) return 'license'
    if (pathname.startsWith('/evaluations')) return 'evaluations'
    if (pathname.startsWith('/deployments')) return 'deployments'
    if (pathname.startsWith('/sites')) return 'deployments'
    if (pathname.startsWith('/storage')) return 'storage'
    if (pathname.startsWith('/account')) return 'account'
    if (pathname.startsWith('/settings')) return 'settings'
    return 'observability'
  }, [pathname])

  // Determine which banner to show (locked > spend-blocked > update-available > trial > free plan > CE > onboarding > news)
  const bannerContent = isLocked ? (
    <GatewayLockedBanner />
  ) : isSpendBlocked ? (
    <SpendBlockedBanner />
  ) : updateAvailable ? (
    <VersionUpdateBanner />
  ) : isTrialMode ? (
    <TrialModeBanner />
  ) : isFreePlan ? (
    <FreePlanBanner />
  ) : isCE ? (
    <CommunityEditionBanner />
  ) : onboardingVisible ? (
    <OnboardingChecklist />
  ) : (
    newsContent
  )

  const isChatMode = sidebarMode === 'chat'
  const effectiveAreas = isChatMode ? CHAT_AREAS : NAV_AREAS
  const effectiveCurrentArea = isChatMode ? 'chat' : currentArea

  return (
    <div>
      <SidebarNav
        groupsTop={NAV_GROUPS_TOP}
        groupsBottom={NAV_GROUPS_BOTTOM}
        areas={effectiveAreas}
        currentArea={effectiveCurrentArea}
        data={data}
        toolContent={toolContent}
        newsContent={isChatMode ? undefined : bannerContent}
        switcher={
          <SidebarModeToggle mode={sidebarMode} onModeChange={setSidebarMode} />
        }
        bottom={<UserMenu />}
      />
    </div>
  )
}
