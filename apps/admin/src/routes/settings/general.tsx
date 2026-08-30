import { createFileRoute } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useCurrentWorkspace, useSession } from '@/hooks/auth'
import {
  listOrganizations,
  updateOrganization,
  updateWorkspace,
} from '@/server/organizations'
import { isCloudManaged } from '@/lib/cloud-mode'
import { ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import { Icon } from '@iconify/react'
import { InterfaceDensityCards } from '@/components/common/interface-density-cards'
import {
  type InterfaceDensity,
  persistInterfaceDensity,
  readStoredInterfaceDensity,
} from '@/lib/interface-density'

const {
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Textarea,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Button,
  Switch,
} = ui

export const Route = createFileRoute('/settings/general')({
  component: GeneralSettingsPage,
})

type DateFormat = 'mdy' | 'dmy' | 'ymd'
type LandingPage =
  | '/observability/logs'
  | '/gateway/config'
  | '/evaluations/datasets'
  | '/deployments/studio'
  | '/settings/general'

type GeneralPrefs = {
  instanceDescription: string
  timezone: string
  dateFormat: DateFormat
  locale: string
  landingPage: LandingPage
  tablePageSize: string
  density: InterfaceDensity
  notifyProductUpdates: boolean
  notifySystemAlerts: boolean
  notifyWeeklySummary: boolean
  sessionTimeoutMinutes: string
  enforceMfa: boolean
}

const DEFAULT_PREFS: GeneralPrefs = {
  instanceDescription: '',
  timezone: 'UTC',
  dateFormat: 'mdy',
  locale: 'en-US',
  landingPage: '/observability/logs',
  tablePageSize: '25',
  density: 'compact',
  notifyProductUpdates: true,
  notifySystemAlerts: true,
  notifyWeeklySummary: false,
  sessionTimeoutMinutes: '60',
  enforceMfa: false,
}

const TIMEZONE_OPTIONS = [
  'UTC',
  'America/New_York',
  'America/Chicago',
  'America/Denver',
  'America/Los_Angeles',
  'Europe/London',
  'Asia/Kolkata',
  'Asia/Tokyo',
]

const TAB_TRIGGER_CLASS =
  'relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1'

function getStorageKey(orgId: string) {
  return `mf:general-settings:${orgId || 'default'}`
}

function loadPrefs(orgId: string): GeneralPrefs {
  if (typeof window === 'undefined') return DEFAULT_PREFS
  try {
    const raw = localStorage.getItem(getStorageKey(orgId))
    if (!raw) return DEFAULT_PREFS
    const parsed = JSON.parse(raw) as Partial<GeneralPrefs>
    const legacyParsed = parsed as Partial<GeneralPrefs> & {
      trooperDescription?: string
    }
    return {
      ...DEFAULT_PREFS,
      ...parsed,
      instanceDescription:
        parsed.instanceDescription ??
        legacyParsed.trooperDescription ??
        DEFAULT_PREFS.instanceDescription,
      density:
        readStoredInterfaceDensity() ?? parsed.density ?? DEFAULT_PREFS.density,
    }
  } catch {
    return DEFAULT_PREFS
  }
}

function savePrefs(orgId: string, prefs: GeneralPrefs) {
  if (typeof window === 'undefined') return
  persistInterfaceDensity(prefs.density)
  localStorage.setItem(getStorageKey(orgId), JSON.stringify(prefs))
}

function getDefaultInstanceName(workspaceName?: string) {
  const name = workspaceName?.trim()
  if (name) return name
  return isCloudManaged() ? 'Managed Instance' : 'Localhost'
}

/* ── Section wrapper ── */
function Section({
  icon,
  title,
  description,
  children,
  footer,
}: {
  icon: string
  title: string
  description: string
  children: React.ReactNode
  footer?: React.ReactNode
}) {
  return (
    <section className="overflow-hidden rounded-md border border-brand-main-600/85 bg-brand-main-900/55 shadow-sm light:border-brand-main-700 light:bg-white light:shadow-[0_1px_2px_rgba(0,0,0,0.04)]">
      <div className="flex items-start gap-2.5 border-b border-brand-main-600/70 px-4 py-3 light:border-brand-main-700">
        <div className="flex size-7 shrink-0 items-center justify-center rounded border border-brand-secondary-500/25 bg-brand-secondary-500/10 light:border-brand-secondary-300 light:bg-brand-secondary-100">
          <Icon
            icon={icon}
            className="size-3.5 text-brand-secondary-300 light:text-brand-secondary-800"
          />
        </div>
        <div className="min-w-0">
          <h3 className="text-[13px] font-medium text-white light:text-brand-main-50">
            {title}
          </h3>
          <p className="mt-0.5 text-[11px] leading-4 text-white/45 light:text-black/55">
            {description}
          </p>
        </div>
      </div>
      <div className="space-y-3 p-4">{children}</div>
      {footer && (
        <div className="flex justify-end border-t border-brand-main-600/70 bg-brand-main-900/35 px-4 py-3 light:border-brand-main-700 light:bg-brand-main-900">
          {footer}
        </div>
      )}
    </section>
  )
}

/* ── Toggle row ── */
function ToggleRow({
  label,
  description,
  checked,
  onCheckedChange,
}: {
  label: string
  description: string
  checked: boolean
  onCheckedChange: (v: boolean) => void
}) {
  return (
    <div className="group flex min-h-[3.75rem] items-center justify-between gap-4 rounded-md border border-brand-main-600/85 bg-brand-main-900/35 px-3 py-2.5 transition-colors hover:border-brand-main-500 hover:bg-brand-main-800/45 light:border-brand-main-700 light:bg-brand-main-900 light:hover:border-brand-main-600 light:hover:bg-brand-main-800">
      <div className="min-w-0">
        <p className="text-sm text-white light:text-brand-main-50">{label}</p>
        <p className="text-xs text-white/40 light:text-black/40">
          {description}
        </p>
      </div>
      <Switch checked={checked} onCheckedChange={onCheckedChange} />
    </div>
  )
}

function DangerActionRow({
  icon,
  title,
  description,
  tone = 'neutral',
  action,
}: {
  icon: string
  title: string
  description: string
  tone?: 'neutral' | 'secondary' | 'destructive'
  action: React.ReactNode
}) {
  const styles = {
    neutral: {
      row: 'border-brand-main-700/70 bg-transparent hover:bg-brand-main-900/35 light:border-brand-main-700 light:hover:bg-brand-main-900',
      icon: 'text-brand-main-200 light:text-brand-main-400',
      title: 'text-white light:text-brand-main-50',
      description: 'text-white/45 light:text-black/55',
    },
    secondary: {
      row: 'border-brand-main-700/70 bg-transparent hover:bg-brand-main-900/35 light:border-brand-main-700 light:hover:bg-brand-main-900',
      icon: 'text-brand-secondary-300 light:text-brand-secondary-700',
      title: 'text-white light:text-brand-main-50',
      description: 'text-white/45 light:text-black/55',
    },
    destructive: {
      row: 'border-red-500/20 bg-red-500/[0.06] light:border-red-200 light:bg-red-50/70',
      icon: 'text-red-300 light:text-red-700',
      title: 'text-red-100 light:text-red-700',
      description: 'text-red-200/70 light:text-red-700/75',
    },
  }[tone]

  return (
    <div
      className={`grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 border-b px-3 py-2.5 last:border-b-0 ${styles.row}`}
    >
      <div
        className={`flex size-6 shrink-0 items-center justify-center ${styles.icon}`}
      >
        <Icon icon={icon} className="size-3.5" />
      </div>
      <div className="min-w-0">
        <p className={`text-sm font-medium ${styles.title}`}>{title}</p>
        <p className={`mt-0.5 text-xs leading-4 ${styles.description}`}>
          {description}
        </p>
      </div>
      <div className="shrink-0">{action}</div>
    </div>
  )
}

function GeneralSettingsPage() {
  const queryClient = useQueryClient()
  const { data: session } = useSession()
  const { data: workspace } = useCurrentWorkspace()
  const currentOrg = session?.user?.organizations?.[0]
  const orgId = currentOrg?.id ?? ''

  const [instanceName, setInstanceName] = useState('')
  const [supportEmail, setSupportEmail] = useState('')
  const [prefs, setPrefs] = useState<GeneralPrefs>(DEFAULT_PREFS)

  const orgQuery = useQuery({
    queryKey: ['settings', 'general', 'organization', orgId],
    queryFn: async () => {
      const response = await listOrganizations()
      return (
        response.organizations.find(
          (item) => item.organization?.id === orgId,
        ) ?? null
      )
    },
    enabled: !!orgId,
  })

  const organization = orgQuery.data?.organization

  useEffect(() => {
    setPrefs(loadPrefs(orgId))
  }, [orgId])

  useEffect(() => {
    setInstanceName(getDefaultInstanceName(workspace?.name))
    setSupportEmail(organization?.billingEmail ?? '')
  }, [organization?.billingEmail, workspace?.name])

  const updateOrgMutation = useMutation({
    mutationFn: async () => {
      if (!orgId) throw new Error('Organization not found')

      const updates: Array<Promise<unknown>> = []
      const nextInstanceName = instanceName.trim()
      const currentInstanceName = getDefaultInstanceName(workspace?.name)
      const nextSupportEmail = supportEmail.trim()
      const currentSupportEmail = organization?.billingEmail ?? ''

      if (
        workspace?.id &&
        nextInstanceName &&
        nextInstanceName !== currentInstanceName
      ) {
        updates.push(
          updateWorkspace({
            workspaceId: workspace.id,
            name: nextInstanceName,
          }),
        )
      }

      if (nextSupportEmail !== currentSupportEmail) {
        updates.push(
          updateOrganization({
            organizationId: orgId,
            billingEmail: nextSupportEmail || undefined,
          }),
        )
      }

      await Promise.all(updates)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['auth', 'session'] })
      queryClient.invalidateQueries({
        queryKey: ['settings', 'general', 'organization', orgId],
      })
      queryClient.invalidateQueries({
        queryKey: ['workspace', 'current', orgId],
      })
      toast.success('General settings saved')
    },
    onError: (error) => {
      toast.error((error as Error).message || 'Failed to save general settings')
    },
  })

  const handleSaveLocalPrefs = (message: string) => {
    savePrefs(orgId, prefs)
    toast.success(message)
  }

  const handleDensityChange = (density: InterfaceDensity) => {
    const nextPrefs = { ...prefs, density }
    persistInterfaceDensity(density)
    setPrefs(nextPrefs)
    savePrefs(orgId, nextPrefs)
    toast.success('Interface density updated')
  }

  const handleExportMetadata = () => {
    const payload = {
      organization: {
        id: orgId,
        slug: currentOrg?.slug ?? '',
        name: instanceName,
      },
      supportContactEmail: supportEmail,
      preferences: prefs,
      exportedAt: new Date().toISOString(),
    }

    const blob = new Blob([JSON.stringify(payload, null, 2)], {
      type: 'application/json',
    })
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = `instance-metadata-${currentOrg?.slug || 'instance'}.json`
    document.body.appendChild(anchor)
    anchor.click()
    document.body.removeChild(anchor)
    URL.revokeObjectURL(url)

    toast.success('Instance metadata exported')
  }

  const handleDangerAction = (actionLabel: string) => {
    toast.info(`${actionLabel} is not enabled yet.`)
  }

  return (
    <div className="flex h-full w-full flex-col bg-brand-main-950">
      <div className="mx-auto flex w-full max-w-4xl flex-1 flex-col overflow-y-auto px-4 py-5 sm:px-6 lg:px-8">
        <Tabs defaultValue="profile" className="w-full">
          <div className="-mx-1 overflow-x-auto px-1">
            <TabsList className="h-auto w-fit gap-1 rounded border border-brand-main-600 bg-brand-main-800/50 p-1 light:border-brand-main-700 light:bg-white">
              <TabsTrigger className={TAB_TRIGGER_CLASS} value="profile">
                Profile
              </TabsTrigger>
              <TabsTrigger className={TAB_TRIGGER_CLASS} value="regional">
                Regional
              </TabsTrigger>
              <TabsTrigger className={TAB_TRIGGER_CLASS} value="interface">
                Interface
              </TabsTrigger>
              <TabsTrigger className={TAB_TRIGGER_CLASS} value="notifications">
                Notifications
              </TabsTrigger>
              <TabsTrigger className={TAB_TRIGGER_CLASS} value="security">
                Security
              </TabsTrigger>
              <TabsTrigger className={TAB_TRIGGER_CLASS} value="danger">
                Danger
              </TabsTrigger>
            </TabsList>
          </div>

          <TabsContent value="profile" className="mt-4">
            {/* ── Instance Profile ── */}
            <Section
              icon="lucide:building-2"
              title="Instance Profile"
              description="Update the visible identity of your instance."
              footer={
                <Button
                  onClick={() => {
                    savePrefs(orgId, prefs)
                    updateOrgMutation.mutate()
                  }}
                  disabled={updateOrgMutation.isPending}
                  variant="default"
                >
                  {updateOrgMutation.isPending ? 'Saving...' : 'Save Profile'}
                </Button>
              }
            >
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <Label className="text-xs text-white/50 light:text-black/50">
                    Instance Name
                  </Label>
                  <Input
                    value={instanceName}
                    onChange={(e) => setInstanceName(e.target.value)}
                    placeholder="Instance name"
                    className="border-brand-main-600 bg-brand-main-800 text-white light:text-brand-main-50 placeholder:text-white/25 light:placeholder:text-black/25"
                  />
                </div>
                <div className="space-y-1.5">
                  <Label className="text-xs text-white/50 light:text-black/50">
                    Support Contact Email
                  </Label>
                  <Input
                    type="email"
                    value={supportEmail}
                    onChange={(e) => setSupportEmail(e.target.value)}
                    placeholder="support@company.com"
                    className="border-brand-main-600 bg-brand-main-800 text-white light:text-brand-main-50 placeholder:text-white/25 light:placeholder:text-black/25"
                  />
                </div>
              </div>

              <div className="space-y-1.5">
                <Label className="text-xs text-white/50 light:text-black/50">
                  Instance Description
                </Label>
                <Textarea
                  value={prefs.instanceDescription}
                  onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) =>
                    setPrefs((prev) => ({
                      ...prev,
                      instanceDescription: e.target.value,
                    }))
                  }
                  rows={2}
                  placeholder="Short description of this instance"
                  className="border-brand-main-600 bg-brand-main-800 text-white light:text-brand-main-50 placeholder:text-white/25 light:placeholder:text-black/25"
                />
              </div>

              <div className="flex flex-wrap items-center gap-x-6 gap-y-1 rounded-md border border-brand-main-700/60 bg-brand-main-800/30 px-3 py-2 text-xs text-white/50 light:bg-brand-main-900 light:text-black/50">
                <span>
                  <span className="text-white/30 light:text-black/30">
                    Org ID
                  </span>{' '}
                  <span className="font-mono text-white/60 light:text-black/60">
                    {orgId || '—'}
                  </span>
                </span>
                <span>
                  <span className="text-white/30 light:text-black/30">
                    Slug
                  </span>{' '}
                  <span className="font-mono text-white/60 light:text-black/60">
                    {currentOrg?.slug || '—'}
                  </span>
                </span>
              </div>
            </Section>
          </TabsContent>

          <TabsContent value="regional" className="mt-4">
            {/* ── Regional Preferences ── */}
            <Section
              icon="lucide:globe"
              title="Regional Preferences"
              description="Default display preferences for time and date formatting."
              footer={
                <Button
                  onClick={() =>
                    handleSaveLocalPrefs('Regional preferences saved')
                  }
                  variant="default"
                >
                  Save Regional
                </Button>
              }
            >
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                <div className="space-y-1.5">
                  <Label className="text-xs text-white/50 light:text-black/50">
                    Timezone
                  </Label>
                  <Select
                    value={prefs.timezone}
                    onValueChange={(value) =>
                      setPrefs((prev) => ({ ...prev, timezone: value }))
                    }
                  >
                    <SelectTrigger className="w-full border-brand-main-600 bg-brand-main-800 text-white light:bg-white light:text-brand-main-50">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent className="border-brand-main-600 bg-brand-main-900">
                      {TIMEZONE_OPTIONS.map((tz) => (
                        <SelectItem key={tz} value={tz}>
                          {tz}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className="space-y-1.5">
                  <Label className="text-xs text-white/50 light:text-black/50">
                    Date Format
                  </Label>
                  <Select
                    value={prefs.dateFormat}
                    onValueChange={(value: DateFormat) =>
                      setPrefs((prev) => ({ ...prev, dateFormat: value }))
                    }
                  >
                    <SelectTrigger className="w-full border-brand-main-600 bg-brand-main-800 text-white light:bg-white light:text-brand-main-50">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent className="border-brand-main-600 bg-brand-main-900">
                      <SelectItem value="mdy">MM/DD/YYYY</SelectItem>
                      <SelectItem value="dmy">DD/MM/YYYY</SelectItem>
                      <SelectItem value="ymd">YYYY-MM-DD</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <div className="space-y-1.5">
                  <Label className="text-xs text-white/50 light:text-black/50">
                    Locale
                  </Label>
                  <Input
                    value={prefs.locale}
                    onChange={(e) =>
                      setPrefs((prev) => ({ ...prev, locale: e.target.value }))
                    }
                    className="border-brand-main-600 bg-brand-main-800 text-white light:text-brand-main-50"
                    placeholder="en-US"
                  />
                </div>
              </div>
            </Section>
          </TabsContent>

          <TabsContent value="interface" className="mt-4">
            {/* ── UI Defaults ── */}
            <Section
              icon="lucide:layout-dashboard"
              title="UI Defaults"
              description="Default navigation and interface density preferences."
              footer={
                <Button
                  onClick={() => handleSaveLocalPrefs('UI defaults saved')}
                  variant="default"
                >
                  Save UI Defaults
                </Button>
              }
            >
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <Label className="text-xs text-white/50 light:text-black/50">
                    Landing Page
                  </Label>
                  <Select
                    value={prefs.landingPage}
                    onValueChange={(value: LandingPage) =>
                      setPrefs((prev) => ({ ...prev, landingPage: value }))
                    }
                  >
                    <SelectTrigger className="w-full border-brand-main-600 bg-brand-main-800 text-white light:bg-white light:text-brand-main-50">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent className="border-brand-main-600 bg-brand-main-900">
                      <SelectItem value="/observability/logs">
                        Observability / Logs
                      </SelectItem>
                      <SelectItem value="/gateway/config">
                        Gateway / Config
                      </SelectItem>
                      <SelectItem value="/evaluations/datasets">
                        Evaluations / Datasets
                      </SelectItem>
                      <SelectItem value="/deployments/studio">
                        Deployments / Studio
                      </SelectItem>
                      <SelectItem value="/settings/general">
                        Settings / General
                      </SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <div className="space-y-1.5">
                  <Label className="text-xs text-white/50 light:text-black/50">
                    Table Page Size
                  </Label>
                  <Select
                    value={prefs.tablePageSize}
                    onValueChange={(value) =>
                      setPrefs((prev) => ({ ...prev, tablePageSize: value }))
                    }
                  >
                    <SelectTrigger className="w-full border-brand-main-600 bg-brand-main-800 text-white light:bg-white light:text-brand-main-50">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent className="border-brand-main-600 bg-brand-main-900">
                      <SelectItem value="10">10</SelectItem>
                      <SelectItem value="25">25</SelectItem>
                      <SelectItem value="50">50</SelectItem>
                      <SelectItem value="100">100</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <div className="space-y-2 sm:col-span-2">
                  <Label className="text-xs text-white/50 light:text-black/50">
                    Layout Density
                  </Label>
                  <InterfaceDensityCards
                    value={prefs.density}
                    onChange={handleDensityChange}
                  />
                </div>
              </div>
            </Section>
          </TabsContent>

          <TabsContent value="notifications" className="mt-4">
            {/* ── Notification Preferences ── */}
            <Section
              icon="lucide:bell"
              title="Notification Preferences"
              description="Control which operational updates are sent to your team."
              footer={
                <Button
                  onClick={() =>
                    handleSaveLocalPrefs('Notification preferences saved')
                  }
                  variant="default"
                >
                  Save Notifications
                </Button>
              }
            >
              <div className="space-y-1.5">
                <ToggleRow
                  label="Product Update Emails"
                  description="Release notes and feature announcements."
                  checked={prefs.notifyProductUpdates}
                  onCheckedChange={(checked) =>
                    setPrefs((prev) => ({
                      ...prev,
                      notifyProductUpdates: checked,
                    }))
                  }
                />
                <ToggleRow
                  label="System Alert Emails"
                  description="Critical service and platform notifications."
                  checked={prefs.notifySystemAlerts}
                  onCheckedChange={(checked) =>
                    setPrefs((prev) => ({
                      ...prev,
                      notifySystemAlerts: checked,
                    }))
                  }
                />
                <ToggleRow
                  label="Weekly Usage Summary"
                  description="Usage recap delivered once per week."
                  checked={prefs.notifyWeeklySummary}
                  onCheckedChange={(checked) =>
                    setPrefs((prev) => ({
                      ...prev,
                      notifyWeeklySummary: checked,
                    }))
                  }
                />
              </div>
            </Section>
          </TabsContent>

          <TabsContent value="security" className="mt-4">
            {/* ── Security Basics ── */}
            <Section
              icon="lucide:shield-check"
              title="Security Basics"
              description="Baseline authentication and session behavior."
              footer={
                <Button
                  onClick={() =>
                    handleSaveLocalPrefs('Security preferences saved')
                  }
                  variant="default"
                >
                  Save Security
                </Button>
              }
            >
              <div className="grid grid-cols-1 gap-3">
                <div className="space-y-1.5">
                  <Label className="text-xs text-white/50 light:text-black/50">
                    Session Timeout
                  </Label>
                  <Select
                    value={prefs.sessionTimeoutMinutes}
                    onValueChange={(value) =>
                      setPrefs((prev) => ({
                        ...prev,
                        sessionTimeoutMinutes: value,
                      }))
                    }
                  >
                    <SelectTrigger className="w-full border-brand-main-600 bg-brand-main-800 text-white light:bg-white light:text-brand-main-50">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent className="border-brand-main-600 bg-brand-main-900">
                      <SelectItem value="15">15 minutes</SelectItem>
                      <SelectItem value="30">30 minutes</SelectItem>
                      <SelectItem value="60">60 minutes</SelectItem>
                      <SelectItem value="120">120 minutes</SelectItem>
                      <SelectItem value="480">8 hours</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <ToggleRow
                  label="Enforce MFA for Members"
                  description="Require multi-factor authentication."
                  checked={prefs.enforceMfa}
                  onCheckedChange={(checked) =>
                    setPrefs((prev) => ({ ...prev, enforceMfa: checked }))
                  }
                />
              </div>
            </Section>
          </TabsContent>

          <TabsContent value="danger" className="mt-4">
            <div className="overflow-hidden rounded-md border border-brand-main-700 bg-brand-main-900/25 light:border-brand-main-700 light:bg-white">
              <DangerActionRow
                icon="lucide:download"
                title="Export metadata"
                description="Download a JSON snapshot of workspace identity, support contact, and local preferences."
                action={
                  <Button variant="outline" onClick={handleExportMetadata}>
                    <Icon icon="lucide:download" className="mr-1.5 size-3.5" />
                    Export
                  </Button>
                }
              />
              <DangerActionRow
                icon="lucide:archive"
                title="Archive instance"
                description="Freeze writes while keeping audit history and read access available."
                tone="secondary"
                action={
                  <Button
                    variant="outline"
                    className="h-8 border-brand-main-600 px-3 text-brand-main-100 hover:bg-brand-main-800 light:border-brand-main-700 light:text-brand-main-50 light:hover:bg-brand-main-900"
                    onClick={() => handleDangerAction('Archive instance')}
                  >
                    Archive
                  </Button>
                }
              />
              <DangerActionRow
                icon="lucide:trash-2"
                title="Delete instance"
                description="Permanently remove instance data and access. This action cannot be undone."
                tone="destructive"
                action={
                  <Button
                    variant="outline"
                    className="h-8 border-red-500/40 px-3 text-red-200 hover:bg-red-500/15 light:text-red-700"
                    onClick={() => handleDangerAction('Delete instance')}
                  >
                    Delete
                  </Button>
                }
              />
            </div>
          </TabsContent>
        </Tabs>
      </div>
    </div>
  )
}
