import { useState } from 'react'
import { useRegister } from '../../hooks/auth/use-auth'
import { ui } from '@everstack/ui'
import { Icon } from '@iconify/react'
import { Link } from '@tanstack/react-router'
import { EverstackLogo } from '@/components/brand/everstack-logo'

const { Button, Card, CardHeader, CardTitle, CardDescription, CardContent, Input, Label } = ui

export interface RegisterFormProps {
    onSuccess?: () => void
}

export function RegisterForm({ onSuccess }: RegisterFormProps) {
    const [email, setEmail] = useState('')
    const [password, setPassword] = useState('')
    const [confirmPassword, setConfirmPassword] = useState('')
    const [name, setName] = useState('')
    const [validationError, setValidationError] = useState<string | null>(null)

    const registerMutation = useRegister()

    const handleRegister = async (e: React.FormEvent) => {
        e.preventDefault()
        setValidationError(null)

        // Validate password match
        if (password !== confirmPassword) {
            setValidationError('Passwords do not match')
            return
        }

        // Validate password strength
        if (password.length < 8) {
            setValidationError('Password must be at least 8 characters')
            return
        }

        try {
            await registerMutation.mutateAsync({
                email,
                password,
                name: name || undefined
            })
            onSuccess?.()
            window.location.href = '/'
        } catch (error) {
            // Error is handled by the mutation
        }
    }

    const error = validationError || registerMutation.error
    const isLoading = registerMutation.isPending

    return (
        <Card className="w-full max-w-md border-zinc-800 light:border-zinc-200 bg-zinc-950 light:bg-zinc-50">
            <CardHeader className="text-center">
                <div className="flex justify-center mb-4">
                    <EverstackLogo variant="wordmark" size="md" />
                </div>
                <CardTitle className="text-2xl text-white light:text-brand-main-50">Create your account</CardTitle>
                <CardDescription className="text-zinc-400 light:text-zinc-600">
                    Set up your Everstack instance administrator
                </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
                {error && (
                    <div className="rounded-lg border border-red-500/20 bg-red-500/10 p-3 text-sm text-red-400 light:text-red-600">
                        {typeof error === 'string' ? error : (error as Error).message || 'An error occurred'}
                    </div>
                )}

                <div className="rounded-lg border border-amber-500/20 bg-amber-500/10 p-3 text-sm text-amber-400 light:text-amber-700">
                    <div className="flex items-start gap-2">
                        <Icon icon="lucide:info" className="h-4 w-4 mt-0.5 flex-shrink-0" />
                        <div>
                            <p className="font-medium">First user registration</p>
                            <p className="text-amber-400/80 light:text-amber-700/80 text-xs mt-1">
                                This account will become the instance owner with full admin access.
                            </p>
                        </div>
                    </div>
                </div>

                <form onSubmit={handleRegister} className="space-y-4">
                    <div className="space-y-2">
                        <Label htmlFor="name" className="text-zinc-200 light:text-zinc-800">
                            Name <span className="text-zinc-500">(optional)</span>
                        </Label>
                        <Input
                            id="name"
                            type="text"
                            placeholder="John Doe"
                            value={name}
                            onChange={(e) => setName(e.target.value)}
                            className="border-zinc-700 light:border-zinc-300 bg-zinc-900 light:bg-zinc-100 text-white light:text-brand-main-50 placeholder:text-zinc-500"
                        />
                    </div>
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
                            minLength={8}
                            className="border-zinc-700 light:border-zinc-300 bg-zinc-900 light:bg-zinc-100 text-white light:text-brand-main-50 placeholder:text-zinc-500"
                        />
                        <p className="text-xs text-zinc-500">Must be at least 8 characters</p>
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="confirm-password" className="text-zinc-200 light:text-zinc-800">
                            Confirm Password
                        </Label>
                        <Input
                            id="confirm-password"
                            type="password"
                            placeholder="••••••••"
                            value={confirmPassword}
                            onChange={(e) => setConfirmPassword(e.target.value)}
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
                                Creating account...
                            </>
                        ) : (
                            'Create Account'
                        )}
                    </Button>
                </form>

                <div className="text-center text-sm text-zinc-500">
                    Already have an account?{' '}
                    <Link to="/login" search={{ returnUrl: undefined }} className="text-brand-secondary-400 hover:underline">
                        Sign in
                    </Link>
                </div>
            </CardContent>
        </Card>
    )
}
