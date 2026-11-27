import { useEffect, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { Icon } from '@iconify/react'
import { ui } from '@everstack/ui'
import { AuthLayout } from '@/components/layout/auth-layout'
import { EverstackLogo } from '@/components/brand/everstack-logo'
import { useVerifyMagicLink } from '@/hooks/auth'

const { Button, Card, CardHeader, CardTitle, CardDescription, CardContent } = ui

// The magic-link email opens /verify-magic-link#token=... The token rides in
// the fragment so it never reaches a server log or a Referer. It is spent
// only when the user clicks "Continue signing in", never on page load: a
// mail scanner that renders the link cannot use it up, and a link planted by
// someone else cannot silently sign the visitor into that person's account.
function readTokenFromFragment(): string {
    if (typeof window === 'undefined') return ''
    return new URLSearchParams(window.location.hash.slice(1)).get('token') ?? ''
}

export function MagicLinkVerification() {
    const [token] = useState(readTokenFromFragment)
    const verify = useVerifyMagicLink()

    useEffect(() => {
        // Drop the token from the address bar and history straight away,
        // whatever happens next.
        if (window.location.hash) {
            window.history.replaceState(window.history.state, '', window.location.pathname)
        }
    }, [])

    const continueSignIn = () => {
        if (!token || verify.isPending) return
        verify
            .mutateAsync({ token })
            .then(() => window.location.replace('/'))
            .catch(() => undefined)
    }

    const failed = !token || verify.isError

    return (
        <AuthLayout>
            <Card className="w-full max-w-md border-zinc-800 light:border-zinc-200 bg-zinc-950 light:bg-zinc-50">
                <CardHeader className="text-center">
                    <div className="flex justify-center mb-4">
                        <EverstackLogo variant="wordmark" size="md" />
                    </div>
                    <CardTitle className="text-2xl text-white light:text-brand-main-50">
                        {failed ? 'Link not valid' : 'Sign in to Everstack'}
                    </CardTitle>
                    <CardDescription className="text-zinc-400 light:text-zinc-600">
                        {failed
                            ? 'This sign-in link is invalid, has expired, or was already used.'
                            : 'Continue only if you requested this sign-in link.'}
                    </CardDescription>
                </CardHeader>
                <CardContent className="space-y-4 text-center text-sm">
                    {failed ? (
                        <p className="text-zinc-400 light:text-zinc-600">
                            Links work once and expire after 24 hours.{' '}
                            <Link
                                to="/login"
                                search={{ returnUrl: undefined }}
                                className="text-brand-secondary-400 hover:underline"
                            >
                                Request a new one
                            </Link>
                        </p>
                    ) : (
                        <Button
                            type="button"
                            onClick={continueSignIn}
                            className="w-full bg-brand-secondary-600 hover:bg-brand-secondary-500 text-white"
                            disabled={verify.isPending}
                        >
                            {verify.isPending ? (
                                <>
                                    <Icon icon="lucide:loader-2" className="mr-2 h-4 w-4 animate-spin" />
                                    Signing in...
                                </>
                            ) : (
                                'Continue signing in'
                            )}
                        </Button>
                    )}
                </CardContent>
            </Card>
        </AuthLayout>
    )
}
