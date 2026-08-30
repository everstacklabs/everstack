import { type PropsWithChildren } from 'react'
import { MainNav } from './main-nav'
import { AppSidebarNav } from './sidebar/app-sidebar-nav'
import { useOnboardingSync } from '@/hooks/use-onboarding-sync'

type MainLayoutProps = PropsWithChildren<{}>

export function MainLayout({ children }: MainLayoutProps) {
    // Hydrate onboarding state from the server and persist changes back. Mounted
    // here, once, inside the authenticated shell so the session is established
    // before the GET fires.
    useOnboardingSync()

    return (
        <MainNav sidebar={AppSidebarNav}>
            <main className="flex-1 min-h-0 flex flex-col overflow-y-auto scrollbar-macos">
                {children}
            </main>
        </MainNav>
    )
}
