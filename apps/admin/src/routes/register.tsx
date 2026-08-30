import { createFileRoute, Navigate } from '@tanstack/react-router'
import { RegisterForm } from '@/components/auth'
import { AuthLayout } from '@/components/layout/auth-layout'
import { useAuthMode, useSession } from '@/hooks/auth'

export const Route = createFileRoute('/register')({
  component: RegisterPage,
})

function RegisterPage() {
  const { data: authMode, isLoading: authModeLoading } = useAuthMode()
  const { data: session, isLoading: sessionLoading } = useSession()

  // Show loading state
  if (authModeLoading || sessionLoading) {
    return (
      <AuthLayout>
        <div className="text-zinc-400 light:text-zinc-600">Loading...</div>
      </AuthLayout>
    )
  }

  // If already authenticated, redirect to dashboard
  if (session?.authenticated) {
    return <Navigate to="/" />
  }

  // If users already exist, redirect to login
  if (authMode?.hasUsers) {
    return <Navigate to="/login" search={{ returnUrl: undefined }} />
  }

  // If cloud mode, registration is not available
  if (authMode?.mode === 'cloud') {
    return (
      <AuthLayout>
        <div className="text-center text-zinc-400 light:text-zinc-600">
          <p>This instance uses SSO for authentication.</p>
          <p className="mt-2">Registration is managed by your identity provider.</p>
        </div>
      </AuthLayout>
    )
  }

  return (
    <AuthLayout>
      <RegisterForm />
    </AuthLayout>
  )
}
