import { useState } from 'react'
import { useLogin, useRequestMagicLink } from '@/hooks/auth'
import { ui } from '@everstack/ui'
import { Icon } from '@iconify/react'
import { Link } from '@tanstack/react-router'
import { EverstackLogo } from '@/components/brand/everstack-logo'

const { Button, Card, CardHeader, CardTitle, CardDescription, CardContent, Input, Label, Tabs, TabsContent, TabsList, TabsTrigger } = ui

export interface LoginFormProps {
    onSuccess?: () => void
}

export function LoginForm({ onSuccess }: LoginFormProps) {
    const [email, setEmail] = useState('')
    const [password, setPassword] = useState('')
    const [magicLinkEmail, setMagicLinkEmail] = useState('')
    const [activeTab, setActiveTab] = useState<'password' | 'magic-link'>('password')
    const [magicLinkSent, setMagicLinkSent] = useState(false)

    const loginMutation = useLogin()
    const magicLinkMutation = useRequestMagicLink()

    const handlePasswordLogin = async (e: React.FormEvent) => {
        e.preventDefault()
        try {
            await loginMutation.mutateAsync({ email, password })
            if (onSuccess) {
                onSuccess()
            } else {
                window.location.href = '/'
            }
        } catch (error) {
            // Error is handled by the mutation
        }
    }

    const handleMagicLink = async (e: React.FormEvent) => {
        e.preventDefault()
        try {
            await magicLinkMutation.mutateAsync({ email: magicLinkEmail })
            setMagicLinkSent(true)
        } catch (error) {
            // Error is handled by the mutation
        }
    }

    const error = loginMutation.error || magicLinkMutation.error
    const isLoading = loginMutation.isPending || magicLinkMutation.isPending

    return (
        <Card className="w-full max-w-md border-zinc-800 light:border-zinc-200 bg-zinc-950 light:bg-zinc-50">
            <CardHeader className="text-center">
                <div className="flex justify-center mb-4">
                    <EverstackLogo variant="wordmark" size="md" />
                </div>
                <CardTitle className="text-2xl text-white light:text-brand-main-50">Welcome back</CardTitle>
                <CardDescription className="text-zinc-400 light:text-zinc-600">
                    Sign in to your Everstack instance
                </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
                {error && (
                    <div className="rounded-lg border border-red-500/20 bg-red-500/10 p-3 text-sm text-red-400 light:text-red-600">
                        {(error as Error).message || 'An error occurred'}
                    </div>
                )}

                <Tabs value={activeTab} onValueChange={(v) => setActiveTab(v as 'password' | 'magic-link')}>
                    <TabsList className="grid w-full grid-cols-2 bg-zinc-900 light:bg-zinc-100 text-white light:text-brand-main-50">
                        <TabsTrigger value="password" className="data-[state=active]:bg-zinc-800 light:data-[state=active]:bg-white text-white light:text-brand-main-50">
                            Password
                        </TabsTrigger>
                        <TabsTrigger value="magic-link" className="data-[state=active]:bg-zinc-800 light:data-[state=active]:bg-white text-white light:text-brand-main-50">
                            Magic Link
                        </TabsTrigger>
                    </TabsList>

                    <TabsContent value="password" className="space-y-4 mt-4">
                        <form onSubmit={handlePasswordLogin} className="space-y-4">
                            <div className="space-y-2">
                                <Label htmlFor="email" className="text-zinc-200 light:text-zinc-800">
                                    Email
                                </Label>
                                <Input
                                    id="email"
                                    type="email"
                                    placeholder="you@company.com"
                                    value={email}
                                    onChange={(e) => setEmail(e.target.value)}
                                    required
                                    className="border-zinc-700 light:border-zinc-300 bg-zinc-900 light:bg-zinc-100 text-white light:text-brand-main-50 placeholder:text-zinc-500"
                                />
                            </div>
                            <div className="space-y-2">
                                <Label htmlFor="password" className="text-zinc-200 light:text-zinc-800">
                                    Password
                                </Label>
                                <Input
                                    id="password"
                                    type="password"
                                    placeholder="••••••••"
                                    value={password}
                                    onChange={(e) => setPassword(e.target.value)}
                                    required
                                    className="border-zinc-700 light:border-zinc-300 bg-zinc-900 light:bg-zinc-100 text-white light:text-brand-main-50 placeholder:text-zinc-500"
                                />
                            </div>
                            <Button
                                type="submit"
                                className="w-full bg-brand-secondary-600 hover:bg-brand-secondary-500 text-white"
                                disabled={isLoading}
                            >
                                {isLoading ? (
                                    <>
                                        <Icon icon="lucide:loader-2" className="mr-2 h-4 w-4 animate-spin" />
                                        Signing in...
                                    </>
                                ) : (
                                    'Sign in'
                                )}
                            </Button>
                        </form>
                    </TabsContent>

                    <TabsContent value="magic-link" className="space-y-4 mt-4">
                        {magicLinkSent ? (
                            <div className="text-center space-y-4">
                                <div className="rounded-lg border border-brand-secondary-500/20 bg-brand-secondary-500/10 p-4">
                                    <Icon icon="lucide:mail-check" className="mx-auto h-12 w-12 text-brand-secondary-400 mb-3" />
                                    <p className="text-brand-secondary-400 font-medium">Check your email</p>
                                    <p className="text-zinc-400 light:text-zinc-600 text-sm mt-1">
                                        We've sent a magic link to <span className="text-white light:text-brand-main-50">{magicLinkEmail}</span>
                                    </p>
                                </div>
                                <Button
                                    variant="ghost"
                                    className="text-zinc-400 light:text-zinc-600 hover:text-white light:hover:text-brand-main-50"
                                    onClick={() => {
                                        setMagicLinkSent(false)
                                        setMagicLinkEmail('')
                                    }}
                                >
                                    Use a different email
                                </Button>
                            </div>
                        ) : (
                            <form onSubmit={handleMagicLink} className="space-y-4">
                                <div className="space-y-2">
                                    <Label htmlFor="magic-email" className="text-zinc-200 light:text-zinc-800">
                                        Email
                                    </Label>
                                    <Input
                                        id="magic-email"
                                        type="email"
                                        placeholder="you@company.com"
                                        value={magicLinkEmail}
                                        onChange={(e) => setMagicLinkEmail(e.target.value)}
                                        required
                                        className="border-zinc-700 light:border-zinc-300 bg-zinc-900 light:bg-zinc-100 text-white light:text-brand-main-50 placeholder:text-zinc-500"
                                    />
                                </div>
                                <Button
                                    type="submit"
                                    className="w-full bg-brand-secondary-600 hover:bg-brand-secondary-500 text-white"
                                    disabled={isLoading}
                                >
                                    {isLoading ? (
                                        <>
                                            <Icon icon="lucide:loader-2" className="mr-2 h-4 w-4 animate-spin" />
                                            Sending...
                                        </>
                                    ) : (
                                        'Send Magic Link'
                                    )}
                                </Button>
                            </form>
                        )}
                    </TabsContent>
                </Tabs>

                <div className="text-center text-sm text-zinc-500">
                    Don't have an account?{' '}
                    <Link to="/register" className="text-brand-secondary-400 hover:underline">
                        Create one
                    </Link>
                </div>
            </CardContent>
        </Card>
    )
}
