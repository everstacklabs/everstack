import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createConnectTransport, connectQuery } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { isAuthError, redirectToCloudLogin } from '@/lib/auth-redirect'

export function getContext() {
  const queryClient = new QueryClient()
  return {
    queryClient,
  }
}

export function Provider({
  children,
  queryClient,
}: {
  children: React.ReactNode
  queryClient: QueryClient
}) {
  const env = (
    (typeof import.meta !== 'undefined'
      ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
      : undefined) ?? {}
  ) as Record<string, string | undefined>

  const baseUrl = `${getApiBaseUrl()}${env.VITE_CONNECT_BASE_PATH ?? ''}`
  const apiKey = env.VITE_API_KEY

  const transport = createConnectTransport({
    baseUrl,
    useBinaryFormat: true,
    // Include credentials (cookies) for session-based auth
    fetch: (input, init) => fetch(input, { ...init, credentials: 'include' }),
    interceptors: [
      // Only add API key if explicitly provided (not needed for same-origin requests)
      (next: (req: any) => Promise<any>) => async (req: any) => {
        if (apiKey) {
          req.header.set('x-evs-api-key', apiKey)
          console.log('[Transport] Using explicit API key')
        } else {
          console.log('[Transport] No API key - relying on same-origin bypass')
        }
        return next(req)
      },
      // Catch auth-shaped errors (Unauthenticated, PermissionDenied) from
      // any RPC and bounce the browser to the cloud /login. The cloud
      // session may be valid for the IdP but invalid for this instance
      // (no org membership) — in that case AuthGuard's session poll
      // returns "authenticated" and won't redirect, so data queries
      // would silently fail and render broken UI. This catches that
      // earlier: the moment the first RPC denies, we navigate, and the
      // user never sees the half-loaded instance.
      (next: (req: any) => Promise<any>) => async (req: any) => {
        try {
          return await next(req)
        } catch (err) {
          if (isAuthError(err)) redirectToCloudLogin()
          throw err
        }
      },
    ],
  })

  return (
    <connectQuery.TransportProvider transport={transport}>
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    </connectQuery.TransportProvider>
  )
}
