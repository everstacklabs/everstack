import { useEffect } from 'react'
import { Navigate, useLocation } from '@tanstack/react-router'
import { useAuthMode, useSession, useGlobalLogoutWatcher } from '@/hooks/auth'
import { isCloudManaged, getCloudConsoleUrl } from '@/lib/cloud-mode'

interface AuthGuardProps {
    children: React.ReactNode
}

/**
 * AuthGuard wraps protected routes and handles authentication redirects.
 *
 * Flow:
 * 1. Cloud-managed instances fail closed if authentication cannot be verified
 * 2. If loading, show loading state (prevents flash)
 * 3. If authenticated, render children
 * 4. If not authenticated:
 *    - Self-hosted with no users → redirect to /register
 *    - Self-hosted with users → redirect to local /login
 *    - **Cloud-managed instance → redirect to the cloud's /login**
 *      (local /login can't actually re-auth the user — only the cloud can)
 */
export function AuthGuard({ children }: AuthGuardProps) {
    const location = useLocation()
    const { data: authMode, isLoading: authModeLoading, isError: authModeError } = useAuthMode()
    const { data: session, isLoading: sessionLoading, isError: sessionError } = useSession()

    // Cross-tab cloud signout watcher. Polls for the `evs_global_logout_at`
    // parent-domain cookie set by the cloud control plane's signout cascade
    // and navigates to `${cloudConsoleUrl}/login` within ~1s when it
    // changes. Cloud-managed mode only — no-op otherwise.
    useGlobalLogoutWatcher()

    // Public routes that don't require authentication
    const publicRoutes = ['/login', '/register', '/invite', '/device']
    const isPublicRoute = publicRoutes.some(route => location.pathname.startsWith(route))

    const unauthed = !!sessionError || !session?.authenticated
    const cloudManaged = isCloudManaged()
    const authModeUnknown =
        !!authModeError || !authMode?.mode || authMode.mode === 'unspecified'

    // A cloud-managed instance must not expose protected content when either
    // the session or the auth-mode check cannot be verified. Local /login
    // cannot establish the cloud identity, so redirect to the cloud console.
    useEffect(() => {
        if (isPublicRoute) return
        if (authModeLoading || sessionLoading) return
        if (!cloudManaged) return
        if (!unauthed && !authModeUnknown) return
        const target = `${getCloudConsoleUrl()}/login`
        if (typeof window !== 'undefined' && window.location.href !== target) {
            window.location.href = target
        }
    }, [
        isPublicRoute,
        authModeLoading,
        sessionLoading,
        cloudManaged,
        authModeUnknown,
        unauthed,
    ])

    // If on a public route, don't guard
    if (isPublicRoute) {
        return <>{children}</>
    }

    // Show loading state while checking auth
    if (authModeLoading || sessionLoading) {
        return (
            <div className="flex h-screen items-center justify-center bg-zinc-950 light:bg-zinc-50">
                <div className="text-zinc-400 light:text-zinc-600">Loading...</div>
            </div>
        )
    }

    if (cloudManaged && (authModeUnknown || unauthed)) {
        return (
            <div className="flex h-screen items-center justify-center bg-zinc-950 light:bg-zinc-50">
                <div className="text-zinc-400 light:text-zinc-600">Redirecting to cloud sign-in…</div>
            </div>
        )
    }

    // Self-hosted/local deployments retain the historical behavior when auth
    // is disabled or the auth endpoint is unavailable.
    if (authModeUnknown) {
        return <>{children}</>
    }

    // Unauthenticated self-hosted deployment.
    if (unauthed) {
        return (
            <div className="flex h-screen items-center justify-center bg-zinc-950 light:bg-zinc-50">
                <div className="text-zinc-400 light:text-zinc-600">Redirecting…</div>
                <Navigate to={authMode.mode === 'self_hosted' && !authMode.hasUsers ? '/register' : '/login'} />
            </div>
        )
    }

    // Authenticated - allow access
    return <>{children}</>
}
