import { useEffect, useRef } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from '@everstack/ui/components'
import {
    getAuthMode as getAuthModeApi,
    getSession as getSessionApi,
    refreshSession as refreshSessionApi,
    login as loginApi,
    register as registerApi,
    requestMagicLink as requestMagicLinkApi,
    signOut as signOutApi,
} from '@/server/auth'
import { isCloudManaged } from '@/lib/cloud-mode'
import { identifyUser, resetPostHog } from '@/lib/posthog'
import { setActiveOrgId } from '@/lib/active-org'
import { useAgentSessionStore } from '@/stores/agent-session-store'

// Refresh interval: 4 minutes (tokens typically expire in 5-15 minutes)
const TOKEN_REFRESH_INTERVAL = 4 * 60 * 1000

export type AuthMode = 'cloud' | 'self_hosted' | 'unspecified'

export interface User {
    id: string
    email: string
    name?: string
    avatarUrl?: string
    createdAt: string
    updatedAt: string
}

export interface Organization {
    id: string
    slug: string
    name: string
    role: number // 1 = owner, 2 = admin, 3 = member
}

export interface UserWithOrganizations {
    user: User
    organizations?: Organization[]
}

export interface AuthModeResponse {
    mode: AuthMode
    hasUsers: boolean
}

export interface LoginResponse {
    success: boolean
    user: UserWithOrganizations
    sessionToken: string
}

export interface RegisterResponse {
    success: boolean
    user: UserWithOrganizations
    sessionToken: string
}

export interface SessionResponse {
    authenticated: boolean
    user?: UserWithOrganizations
}

export function useAuthMode() {
    return useQuery({
        queryKey: ['auth', 'mode'],
        queryFn: async (): Promise<AuthModeResponse> => {
            const response = await getAuthModeApi()
            return {
                mode: response.mode === 1 ? 'cloud' : response.mode === 2 ? 'self_hosted' : 'unspecified',
                hasUsers: response.hasUsers ?? false,
            }
        },
        staleTime: Infinity,
    })
}

export function useSession() {
    const queryClient = useQueryClient()
    const refreshIntervalRef = useRef<NodeJS.Timeout | null>(null)
    // Track the previously-seen org id so we can detect when the active
    // organisation changes (e.g. user is added to a new org and the session
    // refresh resorts the array, or a future UI exposes an org switcher).
    // When it changes we have to wipe React Query and the agent session
    // store — otherwise data fetched for org A would still be served to the
    // UI viewing org B. This is the frontend half of the tenant-isolation
    // fix; the SQL-level fix lives in the backend.
    const previousOrgIdRef = useRef<string | null>(null)

    const query = useQuery({
        queryKey: ['auth', 'session'],
        queryFn: async (): Promise<SessionResponse> => {
            const response = await getSessionApi()
            const sessionResponse = {
                authenticated: response.authenticated ?? false,
                user: response.user as SessionResponse['user'],
            }
            return sessionResponse
        },
        retry: false,
        staleTime: 1000 * 60 * 5, // 5 minutes
        // Re-poll every 60s so the SPA notices a server-side session
        // deletion (cloud logout, admin-initiated revoke, expiry) within
        // a minute instead of letting AuthGuard serve cached data for
        // up to staleTime. Without this, logging out of app.everstack.ai
        // in one tab leaves an instance UI in another tab fully rendered
        // until the user makes an API call.
        refetchInterval: 1000 * 60,
        refetchIntervalInBackground: false,
    })

    useEffect(() => {
        const currentOrgId = query.data?.user?.organizations?.[0]?.id ?? null
        const previousOrgId = previousOrgIdRef.current
        previousOrgIdRef.current = currentOrgId
        // Push the active org into the module-level holder consumed by
        // the transport's `x-org-id` interceptor. Done on every effect
        // pass (not just on transitions) so a fresh login also primes
        // it before the first RPC fires.
        setActiveOrgId(currentOrgId)
        // Only act on a real transition between two non-null values; the
        // initial null → orgId hop is a fresh login, not a switch, and
        // clearing state then would just churn the cache.
        if (previousOrgId && currentOrgId && previousOrgId !== currentOrgId) {
            queryClient.clear()
            useAgentSessionStore.getState().clearAll()
        }
    }, [query.data?.user?.organizations, queryClient])

    useEffect(() => {
        const user = query.data?.user?.user
        const org = query.data?.user?.organizations?.[0]
        if (query.data?.authenticated && user) {
            identifyUser({
                userId: user.id,
                email: user.email,
                name: user.name,
                organizationId: org?.id,
                organizationSlug: org?.slug,
                organizationName: org?.name,
            })
        }
    }, [query.data?.authenticated, query.data?.user?.user?.id, query.data?.user?.organizations])

    // Set up automatic token refresh when authenticated
    useEffect(() => {
        const isAuthenticated = query.data?.authenticated

        if (isAuthenticated) {
            // Start refresh interval
            refreshIntervalRef.current = setInterval(async () => {
                try {
                    await refreshSessionApi()
                    console.debug('Session refreshed successfully')
                } catch (err) {
                    console.warn('Failed to refresh session:', err)
                    // Re-check session state on failure
                    queryClient.invalidateQueries({ queryKey: ['auth', 'session'] })
                }
            }, TOKEN_REFRESH_INTERVAL)
        } else {
            // Stop refresh interval when not authenticated
            if (refreshIntervalRef.current) {
                clearInterval(refreshIntervalRef.current)
                refreshIntervalRef.current = null
            }
        }

        // Cleanup on unmount
        return () => {
            if (refreshIntervalRef.current) {
                clearInterval(refreshIntervalRef.current)
                refreshIntervalRef.current = null
            }
        }
    }, [query.data?.authenticated, queryClient])

    return query
}

/**
 * Hook to manually refresh the session
 */
export function useRefreshSession() {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: async () => {
            const response = await refreshSessionApi()
            return response
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['auth', 'session'] })
        },
    })
}

export function useLogin() {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: async ({ email, password }: { email: string; password: string }): Promise<LoginResponse> => {
            const response = await loginApi(email, password)
            return response as unknown as LoginResponse
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['auth', 'session'] })
        },
    })
}

export function useRegister() {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: async ({ email, password, name }: { email: string; password: string; name?: string }): Promise<RegisterResponse> => {
            const response = await registerApi(email, password, name)
            return response as unknown as RegisterResponse
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['auth', 'session'] })
            queryClient.invalidateQueries({ queryKey: ['auth', 'mode'] })
        },
    })
}

export function useRequestMagicLink() {
    return useMutation({
        mutationFn: async ({ email }: { email: string }) => {
            const response = await requestMagicLinkApi(email)
            return response
        },
    })
}

export function useSignOut() {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: async (): Promise<string | undefined> => {
            // Instance-side signout: the gateway marks the tenant session
            // row ended and clears the cookie. The cloud session is left
            // intact on purpose — instance and cloud lifecycles are
            // decoupled (logging out of an app does not log you out of the
            // IdP). "Sign out everywhere" would be a separate, explicit
            // feature.
            //
            // The backend returns `{ redirect_to: "<cloud_app_url>" }`
            // because navigating to `/login` on the instance host triggers
            // the relay loop: the tenant middleware sees no cookie, 302s
            // to the cloud login, and the cloud — still authed — relays
            // the user straight back into the instance with a fresh
            // cookie, making signout a no-op from the user's POV. Landing
            // on the cloud breaks the loop.
            //
            // Falls back to the legacy ConnectRPC SignOut for self-hosted
            // builds where there is no cloud relay.
            if (isCloudManaged()) {
                // keepalive: true so the request still completes if the
                // user closes the tab during signout. We still require a
                // 2xx before clearing client-side state — a failed signout
                // must not present as a successful logout.
                const res = await fetch('/auth/instance-signout', {
                    method: 'POST',
                    credentials: 'include',
                    keepalive: true,
                })
                if (!res.ok) {
                    throw new Error(`signout failed: HTTP ${res.status}`)
                }
                const body = (await res
                    .json()
                    .catch(() => ({}))) as { redirect_to?: string }
                return body.redirect_to
            }
            await signOutApi()
            return undefined
        },
        onError: (err) => {
            // Do NOT clear queryClient or navigate. Surfacing the failure
            // is the correct behavior — a silent "logout" that leaves the
            // server session alive is exactly the bug pattern this hook is
            // designed to avoid.
            const msg = err instanceof Error ? err.message : 'sign out failed'
            toast.error(`Could not sign out: ${msg}. Please try again.`)
        },
        onSuccess: (redirectTo) => {
            // Only on confirmed success: clear local state and navigate.
            // Land where the backend told us (cloud) so navigating back to
            // /login on the instance can't immediately auto-relay the user
            // back in. Fall back to /login for self-hosted, which doesn't
            // have a relay loop to worry about.
            resetPostHog()
            setActiveOrgId(null)
            queryClient.clear()
            window.location.href = redirectTo || '/login'
        },
    })
}
