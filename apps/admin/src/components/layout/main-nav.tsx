import { cn } from '@/lib/utils'
import { useMediaQuery } from '@everstack/ui'
import {
    createContext,
    type ComponentType,
    type Dispatch,
    type PropsWithChildren,
    type ReactNode,
    type SetStateAction,
    useState,
    useEffect,
} from 'react'
import { TopbarNav } from './topbar/topbar-nav'

type SideNavContext = {
    isOpen: boolean
    setIsOpen: Dispatch<SetStateAction<boolean>>
}

export const SideNavContext = createContext<SideNavContext>({
    isOpen: false,
    setIsOpen: () => { },
})

export const MainNav = ({
    children,
    sidebar: Sidebar,
    toolContent,
    newsContent,
}: PropsWithChildren<{
    sidebar: ComponentType<{
        toolContent?: ReactNode
        newsContent?: ReactNode
    }>
    toolContent?: ReactNode
    newsContent?: ReactNode
}>) => {
    const { isMobile } = useMediaQuery()
    const [isOpen, setIsOpen] = useState(false)

    // Prevent body scroll when side nav is open
    useEffect(() => {
        document.body.style.overflow = isOpen && isMobile ? "hidden" : "auto";
    }, [isOpen, isMobile]);

    // Close side nav when pathname changes (only for area nav items, handled in NavItem)
    // useEffect(() => {
    //     setIsOpen(false);
    // }, [pathname]);

    return (
        <SideNavContext.Provider value={{ isOpen, setIsOpen }}>
            <div className="h-screen overflow-hidden scrollbar-hide md:grid md:grid-cols-[min-content_minmax(0,1fr)]">
                {/* Side nav backdrop */}
                <div
                    className={cn(
                        "fixed left-0 top-0 z-50 h-dvh w-screen transition-[background,backdrop-filter] md:sticky md:z-auto md:w-full md:bg-transparent",
                        isOpen
                            ? "bg-black/20 backdrop-blur-sm"
                            : "bg-transparent max-md:pointer-events-none",
                    )}
                    onClick={(e) => {
                        if (e.target === e.currentTarget) {
                            e.stopPropagation();
                            setIsOpen(false);
                        }
                    }}
                >
                    {/* Side nav */}
                    <div
                        className={cn(
                            "h-full w-min max-w-full z-50 md:z-0  bg-brand-main-950 transition-transform md:translate-x-0",
                            !isOpen && "-translate-x-full",
                        )}
                    >
                        <Sidebar toolContent={toolContent} newsContent={newsContent} />
                    </div>
                </div>
                <div className="flex-1 bg-brand-main-950 overflow-auto flex flex-col [--page-margin:0px] md:pr-(--page-margin) md:py-(--page-margin) md:[--page-margin:0rem]">
                    <div className="flex-1 min-h-0 border-l border-brand-main-600 bg-brand-main-950 flex flex-col">
                        <TopbarNav />
                        {children}
                    </div>
                </div>
            </div>
        </SideNavContext.Provider>
    )
}