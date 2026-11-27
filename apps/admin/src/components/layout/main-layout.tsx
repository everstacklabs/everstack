import { type PropsWithChildren } from 'react'
import { MainNav } from './main-nav'
import { AppSidebarNav } from './sidebar/app-sidebar-nav'
import { useOnboardingSync } from '@/hooks/use-onboarding-sync'
import { useRouterState } from '@tanstack/react-router'
import { isReplayReadableRoute, replayMask } from '@/lib/replay-privacy'

type MainLayoutProps = PropsWithChildren<{}>

export function MainLayout({ children }: MainLayoutProps) {
    // Hydrate onboarding state from the server and persist changes back. Mounted
    // here, once, inside the authenticated shell so the session is established
    // before the GET fires.
    useOnboardingSync()
    // Session replay masks page content by default; see replay-privacy.ts.
    const pathname = useRouterState({ select: (s) => s.location.pathname })
    const mask = isReplayReadableRoute(pathname) ? {} : replayMask

    return (
        <MainNav sidebar={AppSidebarNav}>
            <main {...mask} className="flex-1 min-h-0 flex flex-col overflow-y-auto scrollbar-macos">
                {children}
            </main>
        </MainNav>
    )
}
