import { useEffect } from 'react'
import { Outlet, createRootRouteWithContext, useLocation } from '@tanstack/react-router'
import { TanStackRouterDevtoolsPanel } from '@tanstack/react-router-devtools'
import { TanstackDevtools } from '@tanstack/react-devtools'
import type { QueryClient } from '@tanstack/react-query'
import { MainLayout } from '@/components/layout/main-layout'
import { AuthGuard } from '@/components/auth'
import { Toaster } from '@everstack/ui/components'
import { ReactQueryDevtoolsPanel } from '@tanstack/react-query-devtools'
import { LayoutProvider, ApiProvider } from '@everstack/admin-core'
import { FeatureProvider } from '@/hooks/use-features'
import { useLicenseStatus } from '@/hooks/license/use-license-status'
import {
  POST_AUTH_RETURN_URL_KEY,
  safeLocalReturnURL,
} from '@/lib/local-return-url'

interface MyRouterContext {
  queryClient: QueryClient
}

export const Route = createRootRouteWithContext<MyRouterContext>()({
  component: RootComponent,
})

// Auth routes that should render outside the dashboard layout
const AUTH_ROUTES = ['/login', '/register', '/invite', '/device']

function RootComponent() {
  const location = useLocation()
  // For Community Edition, the API is served from the same origin
  const apiBaseUrl = typeof window !== 'undefined' ? window.location.origin : ''
  const { data } = useLicenseStatus()

  // Cloud relay and OIDC callbacks both mint an instance-local session before
  // returning to the SPA. Preserve the original local route across that
  // cross-origin round trip without accepting an external redirect target.
  useEffect(() => {
    const stored = window.sessionStorage.getItem(POST_AUTH_RETURN_URL_KEY)
    if (!stored) return

    window.sessionStorage.removeItem(POST_AUTH_RETURN_URL_KEY)
    const target = safeLocalReturnURL(stored)
    const current = `${window.location.pathname}${window.location.search}${window.location.hash}`
    if (target !== current) {
      window.location.replace(target)
    }
  }, [])

  // Check if current route is an auth route
  const isAuthRoute = AUTH_ROUTES.some(route => location.pathname.startsWith(route))

  // Auth routes render with minimal wrapper - no dashboard layout
  if (isAuthRoute) {
    return (
      <>
        <ApiProvider baseUrl={apiBaseUrl}>
          <Outlet />
          <Toaster richColors />
        </ApiProvider>

        {import.meta.env.DEV && (
          <TanstackDevtools
            config={{
              position: 'bottom-left',
            }}
            plugins={[
              {
                name: 'Tanstack Router',
                render: () => <TanStackRouterDevtoolsPanel />,
              },
              {
                name: 'Tanstack Query',
                render: () => <ReactQueryDevtoolsPanel />,
              },
            ]}
          />
        )}
      </>
    )
  }

  // Dashboard routes get the full layout with auth guard
  return (
    <>
      <ApiProvider baseUrl={apiBaseUrl}>
        <AuthGuard>
          <LayoutProvider
            config={{
              mode: 'community',
              apiBaseUrl,
              showActivationGuard: false,
              showTrialBanner: true,
              showGatewayLockedBanner: true,
            }}
          >
            <FeatureProvider availableFeatures={data?.availableFeatures}>
              <MainLayout>
                <Outlet />
                <Toaster richColors />
              </MainLayout>
            </FeatureProvider>
          </LayoutProvider>
        </AuthGuard>
      </ApiProvider>

      {import.meta.env.DEV && (
        <TanstackDevtools
          config={{
            position: 'bottom-left',
          }}
          plugins={[
            {
              name: 'Tanstack Router',
              render: () => <TanStackRouterDevtoolsPanel />,
            },
            {
              name: 'Tanstack Query',
              render: () => <ReactQueryDevtoolsPanel />,
            },
          ]}
        />
      )}
    </>
  )
}
