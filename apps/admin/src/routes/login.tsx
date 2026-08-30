import { createFileRoute, Navigate } from '@tanstack/react-router'
import { LoginForm } from '@/components/auth'
import { AuthLayout } from '@/components/layout/auth-layout'
import { useAuthMode } from '@/hooks/auth'
import {
    POST_AUTH_RETURN_URL_KEY,
    safeLocalReturnURL,
} from '@/lib/local-return-url'

export const Route = createFileRoute('/login')({
    component: LoginPage,
    validateSearch: (search: Record<string, unknown>) => ({
        returnUrl: typeof search.returnUrl === 'string' ? search.returnUrl : undefined,
    }),
})

function LoginPage() {
    const { data: authMode, isLoading: authModeLoading } = useAuthMode()
    const { returnUrl } = Route.useSearch()
    const postLoginURL = safeLocalReturnURL(returnUrl)

    if (authModeLoading) {
        return (
            <AuthLayout>
                <div className="text-zinc-400 light:text-zinc-600">Loading...</div>
            </AuthLayout>
        )
    }

    if (authMode?.mode === 'self_hosted' && !authMode?.hasUsers) {
        return <Navigate to="/register" />
    }

    // Cloud-managed instance: this page is the signed-out landing target
    // for the instance. It is always rendered as the "sign in to this
    // instance" page regardless of any cloud session the browser may still
    // be carrying, because:
    //
    //   - Cloud and instance sessions are independent (the Sentry/Zitadel
    //     model). The cloud being signed in does not mean this instance is
    //     signed in.
    //   - The proxied AuthService/GetSession on the instance can return
    //     `authenticated: true` based on the parent-domain cloud cookie
    //     even when there is no instance session — relying on it here
    //     would auto-navigate the user back to `/` and trigger the relay
    //     loop, defeating signout entirely.
    //   - Users explicitly click "Continue with Everstack" to consent to
    //     re-entering this instance, even if they're already cloud-authed.
    if (authMode?.mode === 'cloud') {
        return (
            <AuthLayout>
                <div className="text-center space-y-4">
                    <h2 className="text-lg font-semibold text-white light:text-brand-main-50">Sign in</h2>
                    <p className="text-sm text-zinc-400 light:text-zinc-600 max-w-sm mx-auto">
                        You're signed out of this instance. Sign in again with your Everstack account.
                    </p>
                    <button
                        type="button"
                        onClick={() => {
                            window.sessionStorage.setItem(
                                POST_AUTH_RETURN_URL_KEY,
                                postLoginURL,
                            )
                            window.location.href = '/api/auth/signin'
                        }}
                        className="inline-flex items-center justify-center px-4 py-2 rounded-md text-sm font-medium bg-brand-secondary-500 hover:bg-brand-secondary-400 text-white transition-colors"
                    >
                        Continue with Everstack
                    </button>
                </div>
            </AuthLayout>
        )
    }

    return (
        <AuthLayout>
            <LoginForm onSuccess={() => { window.location.href = postLoginURL }} />
        </AuthLayout>
    )
}
