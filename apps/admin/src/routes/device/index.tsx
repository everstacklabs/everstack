import { createFileRoute } from '@tanstack/react-router'
import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import { createAuthClient } from '@everstack/admin-core'
import { ui } from '@everstack/ui'
import { Icon } from '@iconify/react'
import { AuthLayout } from '@/components/layout/auth-layout'
import { EverstackLogo } from '@/components/brand/everstack-logo'
import { useSession } from '@/hooks/auth'

const { Button, Input, Label } = ui

export const Route = createFileRoute('/device/')({
  component: DeviceAuthorizationPage,
  validateSearch: (search: Record<string, unknown>) => ({
    code: typeof search.code === 'string' ? search.code : undefined,
  }),
})

type DeviceState = {
  valid: boolean
  expired: boolean
  clientId: string
  scope: string
  status: string
}

function DeviceAuthorizationPage() {
  const { code: codeFromURL } = Route.useSearch()
  const { data: session, isLoading: sessionLoading } = useSession()
  const authClient = useMemo(() => createAuthClient(), [])
  const [userCode, setUserCode] = useState(formatUserCode(codeFromURL ?? ''))
  const [deviceState, setDeviceState] = useState<DeviceState | null>(null)
  const [hasValidated, setHasValidated] = useState(false)
  const [deviceLoading, setDeviceLoading] = useState(Boolean(codeFromURL))
  const [selectedOrgId, setSelectedOrgId] = useState('')
  const [isApproving, setIsApproving] = useState(false)
  const [approved, setApproved] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const authenticated = session?.authenticated === true
  const user = session?.user?.user
  const organizations = session?.user?.organizations ?? []

  const validateCode = useCallback(
    async (code: string) => {
      const formattedCode = formatUserCode(code)
      if (formattedCode.length !== 9) {
        setHasValidated(true)
        setDeviceState(null)
        setError('Enter the eight-character code from your terminal.')
        return
      }

      setDeviceLoading(true)
      setError(null)
      try {
        const response = await authClient.getDeviceAuthorizationStatus({
          userCode: formattedCode,
        })
        setDeviceState({
          valid: response.valid,
          expired: response.expired,
          clientId: response.clientId,
          scope: response.scope,
          status: response.status,
        })
      } catch (cause) {
        setDeviceState(null)
        setError(
          cause instanceof Error
            ? cause.message
            : 'Could not validate this code.',
        )
      } finally {
        setHasValidated(true)
        setDeviceLoading(false)
      }
    },
    [authClient],
  )

  useEffect(() => {
    if (codeFromURL) void validateCode(codeFromURL)
  }, [codeFromURL, validateCode])

  useEffect(() => {
    if (!sessionLoading && !authenticated) {
      const returnURL = `${window.location.pathname}${window.location.search}${window.location.hash}`
      window.location.replace(
        `/login?returnUrl=${encodeURIComponent(returnURL)}`,
      )
    }
  }, [authenticated, sessionLoading])

  useEffect(() => {
    if (!selectedOrgId && organizations.length > 0) {
      setSelectedOrgId(organizations[0].id)
    }
  }, [organizations, selectedOrgId])

  const approve = useCallback(async () => {
    if (!selectedOrgId) {
      setError('Select an organization to continue.')
      return
    }

    try {
      setIsApproving(true)
      setError(null)
      await authClient.approveDeviceAuthorization({
        userCode,
        organizationId: selectedOrgId,
      })
      setApproved(true)
    } catch (cause) {
      setError(
        cause instanceof Error ? cause.message : 'Could not authorize the CLI.',
      )
      setIsApproving(false)
    }
  }, [authClient, selectedOrgId, userCode])

  if (
    sessionLoading ||
    (!authenticated && !sessionLoading) ||
    (deviceLoading && !deviceState)
  ) {
    return (
      <DeviceShell>
        <div className="flex items-center justify-center py-16 text-zinc-400">
          <Icon icon="lucide:loader-circle" className="h-5 w-5 animate-spin" />
          <span className="ml-2 text-sm">Checking authorization…</span>
        </div>
      </DeviceShell>
    )
  }

  if (approved || deviceState?.status === 'authorized') {
    return (
      <DeviceShell>
        <ResultState
          icon="lucide:circle-check"
          title="CLI connected"
          description="Authorization is complete. Return to your terminal; this window can be closed."
          tone="success"
        />
      </DeviceShell>
    )
  }

  if (deviceState?.status === 'denied') {
    return (
      <DeviceShell>
        <ResultState
          icon="lucide:circle-x"
          title="Request denied"
          description="This request can no longer be approved. Run evs login again to start a new one."
          tone="danger"
        />
      </DeviceShell>
    )
  }

  if (!deviceState?.valid) {
    const expired = deviceState?.expired === true
    return (
      <DeviceShell>
        <div className="space-y-6">
          <div className="space-y-2 text-center">
            <p className="text-xs font-medium uppercase tracking-[0.18em] text-brand-secondary-400">
              Device authorization
            </p>
            <h1 className="text-xl font-semibold text-white light:text-zinc-950">
              {expired
                ? 'Authorization code expired'
                : hasValidated
                  ? 'Code not recognized'
                  : 'Connect the Everstack CLI'}
            </h1>
            <p className="text-sm leading-6 text-zinc-400 light:text-zinc-600">
              {expired
                ? 'Run evs login again in your terminal to generate a fresh code.'
                : 'Enter the code shown by evs login. Codes expire after 15 minutes.'}
            </p>
          </div>

          {!expired && (
            <form
              className="space-y-4"
              onSubmit={(event) => {
                event.preventDefault()
                void validateCode(userCode)
              }}
            >
              <div className="space-y-2">
                <Label
                  htmlFor="device-code"
                  className="text-xs text-zinc-300 light:text-zinc-700"
                >
                  Authorization code
                </Label>
                <Input
                  id="device-code"
                  value={userCode}
                  onChange={(event) =>
                    setUserCode(formatUserCode(event.target.value))
                  }
                  autoComplete="one-time-code"
                  autoCapitalize="characters"
                  spellCheck={false}
                  autoFocus
                  placeholder="XXXX-XXXX"
                  className="h-12 border-zinc-700 bg-zinc-900 text-center font-mono text-lg tracking-[0.22em] text-white placeholder:text-zinc-600 light:border-zinc-300 light:bg-white light:text-zinc-950"
                />
              </div>

              {error && <ErrorMessage>{error}</ErrorMessage>}

              <Button
                type="submit"
                className="w-full"
                disabled={deviceLoading || userCode.length !== 9}
              >
                {deviceLoading && (
                  <Icon
                    icon="lucide:loader-circle"
                    className="mr-2 h-4 w-4 animate-spin"
                  />
                )}
                Continue
              </Button>
            </form>
          )}
        </div>
      </DeviceShell>
    )
  }

  const clientLabel =
    deviceState.clientId === 'evs-desktop' ||
    deviceState.clientId === 'ewt-desktop'
      ? 'Everstack Desktop'
      : 'Everstack CLI'
  const selectedOrganization = organizations.find(
    (organization) => organization.id === selectedOrgId,
  )

  return (
    <DeviceShell>
      <div className="space-y-6">
        <div className="space-y-2 text-center">
          <p className="text-xs font-medium uppercase tracking-[0.18em] text-brand-secondary-400">
            Confirm access
          </p>
          <h1 className="text-xl font-semibold text-white light:text-zinc-950">
            Connect {clientLabel}
          </h1>
          <p className="text-sm leading-6 text-zinc-400 light:text-zinc-600">
            Only continue if you started this request from your own terminal.
          </p>
        </div>

        <div className="rounded-lg bg-zinc-900/70 px-4 py-3 light:bg-zinc-100">
          <div className="flex items-center justify-between gap-4">
            <span className="text-xs text-zinc-500">Code</span>
            <span className="font-mono text-sm tracking-[0.16em] text-zinc-100 light:text-zinc-900">
              {userCode}
            </span>
          </div>
        </div>

        <div className="space-y-3">
          <p className="text-xs font-medium text-zinc-300 light:text-zinc-700">
            This grants
          </p>
          <PermissionRow label="Full CLI access to the selected organization" />
          <PermissionRow label="Access to sandboxes, deployments, and operational data" />
          <PermissionRow label="A 90-day credential stored by the CLI on this device" />
        </div>

        {organizations.length > 1 ? (
          <div className="space-y-2">
            <Label className="text-xs text-zinc-300 light:text-zinc-700">
              Organization
            </Label>
            <div className="space-y-2">
              {organizations.map((organization) => {
                const selected = organization.id === selectedOrgId
                return (
                  <button
                    key={organization.id}
                    type="button"
                    onClick={() => setSelectedOrgId(organization.id)}
                    className={`flex w-full items-center justify-between rounded-lg px-3 py-2.5 text-left transition-colors ${
                      selected
                        ? 'bg-brand-secondary-500/15 text-white ring-1 ring-brand-secondary-500/60 light:text-zinc-950'
                        : 'bg-zinc-900/70 text-zinc-300 hover:bg-zinc-800 light:bg-zinc-100 light:text-zinc-700 light:hover:bg-zinc-200'
                    }`}
                  >
                    <span className="text-sm font-medium">
                      {organization.name}
                    </span>
                    <span className="text-xs text-zinc-500">
                      {organization.slug}
                    </span>
                  </button>
                )
              })}
            </div>
          </div>
        ) : (
          <div className="flex items-start gap-3 text-sm">
            <Icon
              icon="lucide:user-round"
              className="mt-0.5 h-4 w-4 shrink-0 text-zinc-500"
            />
            <div className="min-w-0">
              <p className="truncate text-zinc-200 light:text-zinc-800">
                {user?.email}
              </p>
              {selectedOrganization && (
                <p className="truncate text-xs text-zinc-500">
                  {selectedOrganization.name}
                </p>
              )}
            </div>
          </div>
        )}

        {error && <ErrorMessage>{error}</ErrorMessage>}

        <div className="space-y-2">
          <Button
            onClick={() => void approve()}
            disabled={!selectedOrgId || isApproving}
            className="w-full"
          >
            {isApproving && (
              <Icon
                icon="lucide:loader-circle"
                className="mr-2 h-4 w-4 animate-spin"
              />
            )}
            Authorize {clientLabel}
          </Button>
          <button
            type="button"
            onClick={() => window.close()}
            className="w-full py-2 text-sm text-zinc-500 transition-colors hover:text-zinc-300 light:hover:text-zinc-700"
          >
            Cancel
          </button>
        </div>
      </div>
    </DeviceShell>
  )
}

function DeviceShell({ children }: { children: ReactNode }) {
  return (
    <AuthLayout>
      <main className="w-full max-w-md">
        <div className="mb-6 flex justify-center">
          <EverstackLogo variant="wordmark" size="sm" />
        </div>
        <section className="rounded-xl border border-zinc-800 bg-zinc-950/90 p-6 shadow-2xl shadow-black/20 light:border-zinc-200 light:bg-white sm:p-7">
          {children}
        </section>
      </main>
    </AuthLayout>
  )
}

function PermissionRow({ label }: { label: string }) {
  return (
    <div className="flex items-start gap-2.5 text-sm leading-5 text-zinc-300 light:text-zinc-700">
      <Icon
        icon="lucide:check"
        className="mt-0.5 h-4 w-4 shrink-0 text-brand-secondary-400"
      />
      <span>{label}</span>
    </div>
  )
}

function ResultState({
  icon,
  title,
  description,
  tone,
}: {
  icon: string
  title: string
  description: string
  tone: 'success' | 'danger'
}) {
  return (
    <div className="space-y-4 py-5 text-center">
      <div
        className={`mx-auto flex h-11 w-11 items-center justify-center rounded-full ${
          tone === 'success'
            ? 'bg-emerald-500/10 text-emerald-400'
            : 'bg-red-500/10 text-red-400'
        }`}
      >
        <Icon icon={icon} className="h-6 w-6" />
      </div>
      <div className="space-y-2">
        <h1 className="text-xl font-semibold text-white light:text-zinc-950">
          {title}
        </h1>
        <p className="text-sm leading-6 text-zinc-400 light:text-zinc-600">
          {description}
        </p>
      </div>
    </div>
  )
}

function ErrorMessage({ children }: { children: ReactNode }) {
  return (
    <div className="flex items-start gap-2 rounded-lg bg-red-500/10 px-3 py-2.5 text-sm text-red-300 light:text-red-700">
      <Icon icon="lucide:triangle-alert" className="mt-0.5 h-4 w-4 shrink-0" />
      <span>{children}</span>
    </div>
  )
}

function formatUserCode(value: string): string {
  const compact = value
    .toUpperCase()
    .replace(/[^A-Z]/g, '')
    .slice(0, 8)
  return compact.length > 4
    ? `${compact.slice(0, 4)}-${compact.slice(4)}`
    : compact
}
