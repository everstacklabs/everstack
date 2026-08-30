import { useEffect, useState } from 'react'
import { ui } from '@everstack/ui'
import { Iconify } from '@everstack/ui/icons'
import { useTheme } from '@everstack/ui/components'
import { useSession, useSignOut } from '@/hooks/auth'
import { Link } from '@tanstack/react-router'
import { cn } from '@everstack/utils/functions/cn'

const {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
} = ui

const themeOptions = [
  { value: 'system', label: 'System', icon: 'lucide:monitor' },
  { value: 'light', label: 'Light', icon: 'lucide:sun' },
  { value: 'dark', label: 'Dark', icon: 'lucide:moon' },
] as const

export function UserMenu() {
  const { data: session } = useSession()
  const signOutMutation = useSignOut()
  const { theme, setTheme } = useTheme()
  const [themeMounted, setThemeMounted] = useState(false)

  useEffect(() => {
    setThemeMounted(true)
  }, [])

  // Get the actual user data (nested as user.user in the API response)
  const user = session?.user?.user
  const isAuthenticated = session?.authenticated
  const selectedTheme =
    themeMounted &&
    (theme === 'system' || theme === 'light' || theme === 'dark')
      ? theme
      : 'system'

  const handleSignOut = () => {
    signOutMutation.mutate()
  }

  // Get user initials for avatar
  const getInitials = (name?: string, email?: string) => {
    if (name) {
      return name
        .split(' ')
        .map((n) => n[0])
        .join('')
        .toUpperCase()
        .slice(0, 2)
    }
    if (email) {
      return email[0].toUpperCase()
    }
    return 'U'
  }

  const initials = getInitials(user?.name, user?.email)

  // Always render the user icon - it provides access to account settings
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          className={cn(
            'relative flex size-10.5 items-center justify-center rounded-sm transition-colors duration-150 border border-transparent',
            'outline-none focus-visible:ring-2 focus-visible:ring-transparent',
            'hover:bg-brand-secondary-500/15 hover:border-brand-secondary-500/25 active:bg-brand-secondary-50/10 text-brand-secondary-100',
          )}
          aria-label="User menu"
        >
          {user?.avatarUrl ? (
            <img
              src={user.avatarUrl}
              alt={user.name || user.email || ''}
              className="size-8 rounded-full object-cover"
              referrerPolicy="no-referrer"
            />
          ) : (
            <div className="flex size-8 items-center justify-center rounded-full bg-brand-secondary-500 text-xs font-semibold text-brand-secondary-100">
              {initials}
            </div>
          )}
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        side="right"
        align="end"
        sideOffset={8}
        className="w-56 space-y-2 bg-brand-main-600 border-brand-main-500 text-brand-main-100"
      >
        {user && (
          <>
            <DropdownMenuLabel className="font-normal">
              <div className="flex flex-col space-y-1">
                {user.name && (
                  <p className="text-sm font-medium text-zinc-100 light:text-zinc-900">
                    {user.name}
                  </p>
                )}
                {user.email && (
                  <p className="text-xs text-zinc-400 light:text-zinc-600">
                    {user.email}
                  </p>
                )}
              </div>
            </DropdownMenuLabel>
          </>
        )}
        <div className="px-1">
          <div
            className="grid grid-cols-3 gap-1"
            role="group"
            aria-label="Theme"
          >
            {themeOptions.map((option) => {
              const selected = selectedTheme === option.value
              return (
                <button
                  key={option.value}
                  type="button"
                  aria-pressed={selected}
                  aria-label={`${option.label} theme`}
                  title={option.label}
                  onClick={() => setTheme(option.value)}
                  className={cn(
                    'inline-flex h-8 items-center justify-center rounded border text-[11px] font-medium transition-[background-color,border-color,color] duration-75 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-transparent',
                    selected
                      ? 'border-brand-secondary-500/50 bg-brand-secondary-500/15 text-brand-main-50'
                      : 'border-transparent text-brand-main-50/60 hover:border-brand-secondary-500/50 hover:bg-brand-secondary-500/15 hover:text-brand-main-50',
                  )}
                >
                  <Iconify.Icon icon={option.icon} className="size-4" />
                </button>
              )
            })}
          </div>
        </div>
        <DropdownMenuItem
          asChild
          className={cn(
            'text-brand-main-50 group flex items-center rounded px-2 py-1.5 text-sm leading-none transition-[background-color,color,font-weight] duration-75 border border-transparent cursor-pointer',
            'outline-none focus-visible:ring-2 focus-visible:ring-transparent',
            'hover:bg-brand-secondary-500/15 hover:border-brand-secondary-500/50 ',
          )}
        >
          <Link to="/account/profile">
            <Iconify.Icon icon="lucide:user" className="size-4" />
            Account Settings
          </Link>
        </DropdownMenuItem>
        {isAuthenticated && (
          <>
            <DropdownMenuItem
              onClick={handleSignOut}
              disabled={signOutMutation.isPending}
              className="cursor-pointer text-red-400 focus:bg-red-500/10 focus:text-red-400 light:text-red-600 light:focus:text-red-600"
            >
              <Iconify.Icon icon="lucide:log-out" className="size-4" />
              {signOutMutation.isPending ? 'Signing out...' : 'Logout'}
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
