import { useState } from 'react'
import { createFileRoute, Navigate } from '@tanstack/react-router'
import { AuthLayout } from '@/components/layout/auth-layout'
import { EverstackLogo } from '@/components/brand/everstack-logo'
import { useSession, useAcceptInvitation } from '@/hooks/auth'
import { ui } from '@everstack/ui'
import { Icon } from '@iconify/react'

const { Button, Card, CardHeader, CardTitle, CardDescription, CardContent, Input, Label } = ui

export const Route = createFileRoute('/invite/$token')({
  component: AcceptInvitePage,
})

function AcceptInvitePage() {
  const { token } = Route.useParams()
  const { data: session, isLoading: sessionLoading } = useSession()

  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [validationError, setValidationError] = useState<string | null>(null)

  const acceptMutation = useAcceptInvitation()

  // If already authenticated, redirect to dashboard
  if (!sessionLoading && session?.authenticated) {
    return <Navigate to="/" />
  }

  const handleAccept = async (e: React.FormEvent) => {
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
      await acceptMutation.mutateAsync({
        token,
        password,
        name: name || undefined
      })
      window.location.href = '/'
    } catch (error) {
      // Error is handled by the mutation
    }
  }

  const error = validationError || acceptMutation.error
  const isLoading = acceptMutation.isPending

  return (
    <AuthLayout>
      <Card className="w-full max-w-md border-zinc-800 light:border-zinc-200 bg-zinc-950 light:bg-zinc-50">
        <CardHeader className="text-center">
          <div className="flex justify-center mb-4">
            <EverstackLogo variant="wordmark" size="md" />
          </div>
          <CardTitle className="text-2xl text-white light:text-brand-main-50">Join the team</CardTitle>
          <CardDescription className="text-zinc-400 light:text-zinc-600">
            You've been invited to join this Everstack instance
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {error && (
            <div className="rounded-lg border border-red-500/20 bg-red-500/10 p-3 text-sm text-red-400 light:text-red-600">
              {typeof error === 'string' ? error : (error as Error).message || 'An error occurred'}
            </div>
          )}

          <form onSubmit={handleAccept} className="space-y-4">
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
                className="border-zinc-700 light:border-zinc-300 bg-zinc-900 light:bg-white text-white light:text-brand-main-50 placeholder:text-zinc-500"
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
                className="border-zinc-700 light:border-zinc-300 bg-zinc-900 light:bg-white text-white light:text-brand-main-50 placeholder:text-zinc-500"
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
                className="border-zinc-700 light:border-zinc-300 bg-zinc-900 light:bg-white text-white light:text-brand-main-50 placeholder:text-zinc-500"
              />
            </div>
            <Button
              type="submit"
              className="w-full bg-emerald-600 hover:bg-emerald-500 text-white"
              disabled={isLoading}
            >
              {isLoading ? (
                <>
                  <Icon icon="lucide:loader-2" className="mr-2 h-4 w-4 animate-spin" />
                  Joining...
                </>
              ) : (
                'Accept Invitation'
              )}
            </Button>
          </form>
        </CardContent>
      </Card>
    </AuthLayout>
  )
}
