import { AnimatedSizeContainer } from '@/components/animated-size-container'
import { cn } from '@/lib/utils'
import { ClientOnly, useMediaQuery, ui } from '@everstack/ui'
import {
    ArrowUpRight,
    BookOpen,
    ChevronDown,
    ChevronLeft,
    Iconify,
    Lock,
    type Icon,
} from '@everstack/ui/icons'
import { Link, useLocation } from '@tanstack/react-router'
import { AnimatePresence, motion } from 'framer-motion'
import React, {
    Suspense,
    useContext,
    useEffect,
    useMemo,
    useState,
    type CSSProperties,
    type ComponentType,
    type PropsWithChildren,
    type ReactNode,
} from 'react'
import { SideNavContext } from '../main-nav'
import { EverstackLogo } from '@/components/brand/everstack-logo'

const { Tooltip, TooltipProvider } = ui

export type NavItemCommon = {
    name: string
    href: `/${string}`
    exact?: boolean
    isActive?: (pathname: string, href: string) => boolean
    badge?: ReactNode
    arrow?: boolean
    locked?: boolean
}

export type NavSubItemType = NavItemCommon

export type NavItemType = NavItemCommon & {
    icon: Icon | typeof Iconify.Icon | ReactNode
    items?: NavSubItemType[]
}

export type NavGroupType = {
    name: string
    icon: Icon | typeof Iconify.Icon | ReactNode
    href: string
    active: boolean
    onClick?: () => void
    popup?: ComponentType<{
        referenceElement: HTMLElement | null
    }>
    badge?: ReactNode
    description?: string
    learnMoreHref?: string
    showTooltip?: boolean
    // Optional custom renderer used when the default icon/link affordance isn't
    // sufficient (e.g. a popover-backed Help button in the bottom group).
    render?: () => ReactNode
}

export type SidebarNavGroups<T extends Record<any, any>> = (
    args: T,
) => NavGroupType[]

export type SidebarNavAreas<T extends Record<any, any>> = Record<
    string,
    (args: T) => {
        title?: string | ReactNode
        backHref?: string
        showNews?: boolean
        hideSwitcherIcons?: boolean
        direction?: 'left' | 'right'
        content: {
            name?: string
            items: NavItemType[]
        }[]
    }
>

const SIDEBAR_WIDTH = 304
const SIDEBAR_GROUPS_WIDTH = 64
const SIDEBAR_AREAS_WIDTH = SIDEBAR_WIDTH - SIDEBAR_GROUPS_WIDTH

export function NavGroupTooltip({
    name,
    description,
    learnMoreHref,
    disabled,
    children,
}: PropsWithChildren<{
    name: string
    description?: string
    learnMoreHref?: string
    disabled?: boolean
}>) {
    return (
        <Tooltip
            side="right"
            delayDuration={1000}
            disabled={disabled}
            contentClassName="rounded-lg bg-brand-main-600 border-brand-main-500 px-3 py-1.5 text-sm font-medium text-white light:text-brand-main-50"
            content={
                <div>
                    <span>{name}</span>
                    {description && (
                        <motion.div
                            initial={{ opacity: 0, width: 0, height: 0 }}
                            animate={{ opacity: 1, width: 'auto', height: 'auto' }}
                            transition={{ delay: 0.5, duration: 0.25, type: 'spring' }}
                            className="overflow-hidden"
                        >
                            <div className="w-44 py-1 text-xs tracking-tight">
                                <p className="text-brand-main-100">{description}</p>
                                {learnMoreHref && (
                                    <div className="mt-2.5">
                                        <a
                                            href={learnMoreHref}
                                            target="_blank"
                                            rel="noopener noreferrer"
                                            className="font-semibold text-white light:text-brand-main-50 underline"
                                        >
                                            Learn more
                                        </a>
                                    </div>
                                )}
                            </div>
                        </motion.div>
                    )}
                </div>
            }
        >
            {children}
        </Tooltip>
    )
}

export function SidebarNav<T extends Record<any, any>>({
    groups,
    groupsTop,
    groupsBottom,
    areas,
    currentArea,
    data,
    toolContent,
    newsContent,
    switcher,
    bottom,
}: {
    groups?: SidebarNavGroups<T>
    groupsTop?: SidebarNavGroups<T>
    groupsBottom?: SidebarNavGroups<T>
    areas: SidebarNavAreas<T>
    currentArea: string | null
    data: T
    toolContent?: ReactNode
    newsContent?: ReactNode
    switcher?: ReactNode
    bottom?: ReactNode
}) {
    const [isAreaCollapsed, setIsAreaCollapsed] = useState(false)
    const [iconVisible, setIconVisible] = useState(true)
    const [hoveredArea, setHoveredArea] = useState<string | null>(null)
    const pathname = useLocation().pathname
    const isHome = pathname === '/'

    // Auto-collapse sidebar when on studio editor pages
    useEffect(() => {
        const isStudioEditor = pathname === '/deployments/studio/new' ||
            /^\/deployments\/studio\/[^/]+$/.test(pathname)
        setIsAreaCollapsed(isStudioEditor)
    }, [pathname])

    // The displayed area is the hovered area if hovering, otherwise the current active area.
    // On home page, suppress hover-triggered area expansion (no sidebar content to show).
    const displayedArea = isHome ? currentArea : (hoveredArea || currentArea)
    return (
        <TooltipProvider>
            <div
                className={cn(
                    'h-full min-h-dvh w-(--sidebar-width) transition-[width] duration-300',
                )}
                style={
                    {
                        '--sidebar-width': `${displayedArea === null || isAreaCollapsed ? SIDEBAR_GROUPS_WIDTH : SIDEBAR_WIDTH}px`,
                        '--sidebar-groups-width': `${SIDEBAR_GROUPS_WIDTH}px`,
                        '--sidebar-areas-width': `${SIDEBAR_AREAS_WIDTH}px`,
                    } as CSSProperties
                }
            >
                <ClientOnly className="size-full h-full">
                    <nav className="grid size-full h-full grid-cols-[var(--sidebar-groups-width)_1fr]">
                        <div className="flex min-h-dvh flex-col items-center justify-between">
                            <div className="flex flex-col items-center px-2 pt-0">
                                <div className="pb-1 pt-0">
                                    <Link
                                        to="/vault/api-keys"
                                        className="group relative block rounded-md px-1 py-4 outline-none transition-opacity focus-visible:ring-2 focus-visible:ring-black/50"
                                        onMouseEnter={() => !isHome && setIconVisible(false)}
                                        onMouseLeave={() => !isHome && setIconVisible(true)}
                                        onClick={(e) => {
                                            e.preventDefault()
                                            !isHome && setIsAreaCollapsed((prev) => !prev)
                                        }}
                                    >
                                        <motion.div
                                            className="relative"
                                            initial={false}
                                            animate={{
                                                filter: !iconVisible ? 'blur(2px)' : 'blur(0px)',
                                                opacity: !iconVisible ? 0.7 : 1,
                                            }}
                                            transition={{ duration: 0.2 }}
                                        >
                                            <EverstackLogo size="sm" />
                                        </motion.div>
                                        <motion.div
                                            className="absolute inset-0 flex items-center justify-center"
                                            initial={{ opacity: 0, scale: 0.8 }}
                                            animate={{
                                                opacity: !iconVisible ? 1 : 0,
                                                scale: !iconVisible ? 1 : 0.8,
                                            }}
                                            transition={{ duration: 0.2 }}
                                        >
                                            {isAreaCollapsed ? (
                                                <Iconify.Icon
                                                    icon="tabler:layout-sidebar-right-collapse-filled"
                                                    className="h-6 w-6 text-white light:text-brand-main-50"
                                                />
                                            ) : (
                                                <Iconify.Icon
                                                    icon="tabler:layout-sidebar-left-collapse-filled"
                                                    className="h-6 w-6 text-white light:text-brand-main-50"
                                                />
                                            )}
                                        </motion.div>
                                    </Link>
                                </div>
                                {/* {(!currentArea ||
                                !areas[currentArea](data).hideSwitcherIcons) && ( */}
                                <div className="flex flex-col gap-3">
                                    {groupsTop?.(data).map((group) => (
                                        <NavGroupItem
                                            key={group.name}
                                            group={group}
                                            onHoverChange={setHoveredArea}
                                        />
                                    ))}
                                    {groups?.(data).map((group) => (
                                        <NavGroupItem
                                            key={group.name}
                                            group={group}
                                            onHoverChange={setHoveredArea}
                                        />
                                    ))}
                                </div>
                                {/* )} */}
                            </div>
                            <div className="flex flex-col items-center gap-3 py-3 mb-2">
                                {groupsBottom?.(data).map((group) =>
                                    group.render ? (
                                        <div key={group.name}>{group.render()}</div>
                                    ) : (
                                        <NavGroupItem
                                            key={group.name}
                                            group={group}
                                            onHoverChange={setHoveredArea}
                                        />
                                    ),
                                )}
                                <Suspense fallback={null}>{toolContent}</Suspense>
                                {bottom}
                            </div>
                        </div>
                        {!isHome && (
                            <div
                                className={cn('size-full min-h-dvh overflow-hidden py-0 pr-2 transition-opacity duration-300',
                                    isAreaCollapsed && 'opacity-0 pointer-events-none',
                                )}
                            >
                                <div className="scrollbar-hide relative flex h-full w-[calc(var(--sidebar-areas-width)-0rem)]  flex-col overflow-y-auto overflow-x-hidden bg-brand-main-900">
                                    <div className="relative flex h-full min-h-0 grow flex-col px-3 py-2.5 text-white light:text-brand-main-50">
                                        {switcher && (
                                            <div className="mb-2 -mx-1">
                                                {switcher}
                                            </div>
                                        )}
                                        <div className="relative w-full h-full min-h-0 grow ">
                                            {Object.entries(areas).map(([area, areaConfig]) => {
                                                const { title, backHref, content, direction } =
                                                    areaConfig(data)

                                                const TitleContainer = backHref ? Link : 'div'

                                                return (
                                                    <Area
                                                        key={area}
                                                        visible={area === displayedArea}
                                                        direction={direction ?? 'right'}
                                                    >
                                                        {title &&
                                                            (typeof title === 'string' ? (
                                                                <TitleContainer
                                                                    to={backHref ?? '#'}
                                                                    className="group mb-1.5 flex items-center gap-3 px-2 py-1"
                                                                >
                                                                    {backHref && (
                                                                        <div
                                                                            className={cn(
                                                                                'text-brand-main-50 bg-brand-main-500 flex size-6 items-center justify-center rounded-md',
                                                                                'group-hover:bg-brand-secondary-50/10 group-hover:text-content-subtle transition-[transform,background-color,color] duration-150 group-hover:-translate-x-0.5',
                                                                            )}
                                                                        >
                                                                            <ChevronLeft className="size-3 **:stroke-2" />
                                                                        </div>
                                                                    )}
                                                                    <span className="text-white light:text-brand-main-50 text-lg font-semibold">
                                                                        {title}
                                                                    </span>
                                                                </TitleContainer>
                                                            ) : (
                                                                title
                                                            ))}
                                                        <div className="flex flex-col gap-6">
                                                            {content.map(({ name, items }, idx) => (
                                                                <div
                                                                    key={idx}
                                                                    className="flex flex-col gap-0.5"
                                                                >
                                                                    {name && (
                                                                        <div className="mb-1.5 pl-2 text-xs text-brand-main-100">
                                                                            {name}
                                                                        </div>
                                                                    )}
                                                                    <div className="flex flex-col gap-0.5">
                                                                        {items.map((item) => (
                                                                            <NavItem key={item.name} item={item} />
                                                                        ))}
                                                                    </div>
                                                                </div>
                                                            ))}
                                                        </div>
                                                    </Area>
                                                )
                                            })}
                                        </div>
                                    </div>

                                    {/* Fixed bottom sections */}
                                    <div className="flex flex-col gap-2 mb-2">
                                        {data.showConversionGuides && (
                                            <div className="px-3 pb-2">
                                                <Link
                                                    to={`/`}
                                                    className="flex items-center gap-2 rounded-lg bg-neutral-200/75 px-2.5 py-2 text-xs text-neutral-700 transition-colors hover:bg-neutral-200"
                                                >
                                                    <BookOpen className="size-4" />
                                                    Set up conversion tracking
                                                </Link>
                                            </div>
                                        )}

                                        <AnimatePresence>
                                            {displayedArea && typeof areas?.[displayedArea] === 'function' && areas[displayedArea](data).showNews && (
                                                <motion.div
                                                    initial={{ opacity: 0, y: 10 }}
                                                    animate={{ opacity: 1, y: 0 }}
                                                    exit={{ opacity: 0, y: 10 }}
                                                    transition={{
                                                        duration: 0.1,
                                                        ease: 'easeInOut',
                                                    }}
                                                >
                                                    {newsContent}
                                                </motion.div>
                                            )}
                                        </AnimatePresence>
                                    </div>
                                </div>
                            </div>
                        )}
                    </nav>
                </ClientOnly>
            </div>
        </TooltipProvider>
    )
}

function NavGroupItem({
    group: {
        icon: Icon,
        href,
        active,
        badge,
        onClick,
        popup: Popup,
        name,
        description,
        learnMoreHref,
        showTooltip = true,
    },
    onHoverChange,
}: {
    group: NavGroupType
    onHoverChange?: (area: string | null) => void
}) {
    const [_element, setElement] = useState<HTMLAnchorElement | null>(null)
    const [hovered, setHovered] = useState(false)

    // Extract area name from href (e.g., "/gateway" -> "gateway")
    const areaName = useMemo(() => {
        const match = href.match(/^\/([^/]+)/)
        return match ? match[1] : null
    }, [href])

    const linkElement = (
        <Link
            ref={Popup ? setElement : undefined}
            to={href}
            onPointerEnter={() => {
                setHovered(true)
                if (onHoverChange && areaName) {
                    onHoverChange(areaName)
                }
            }}
            onPointerLeave={() => {
                setHovered(false)
                if (onHoverChange) {
                    onHoverChange(null)
                }
            }}
            onClick={onClick}
            className={cn(
                'relative flex size-10 items-center justify-center rounded-sm transition-colors duration-150 border border-transparent',
                'outline-none focus-visible:ring-2 focus-visible:ring-black/50',
                active
                    ? 'bg-brand-secondary-200 font-medium text-brand-secondary-800 hover:bg-brand-secondary-200  active:bg-brand-secondary-200'
                    : 'hover:bg-brand-secondary-500/15 hover:border-brand-secondary-500/25 active:bg-brand-secondary-50/10 text-brand-secondary-100',
            )}
        >
            {Icon && (
                <Iconify.Icon
                    icon={Icon as string}
                    className="text-content-default size-5"
                    data-hovered={hovered}
                />
            )}
            {badge && (
                <div className="absolute right-0.5 top-0.5 flex size-3.5 items-center justify-center rounded-full bg-brand-secondary-500 text-[0.625rem] font-semibold text-brand-secondary-800">
                    {badge}
                </div>
            )}
        </Link>
    )

    return (
        <>
            <div>
                {showTooltip ? (
                    <NavGroupTooltip
                        name={name}
                        description={description}
                        learnMoreHref={learnMoreHref}
                    >
                        {linkElement}
                    </NavGroupTooltip>
                ) : (
                    linkElement
                )}
            </div>
        </>
    )
}

function NavItem({ item }: { item: NavItemType | NavSubItemType }) {
    const { name, href, exact, isActive: customIsActive, locked } = item

    const Icon = 'icon' in item ? item.icon : undefined
    const items = 'items' in item ? item.items : undefined

    const [hovered, setHovered] = useState(false)
    const [expanded, setExpanded] = useState(false)

    const pathname = useLocation().pathname
    const { setIsOpen } = useContext(SideNavContext)
    const { isMobile } = useMediaQuery()

    const isActive = useMemo(() => {
        if (customIsActive) {
            return customIsActive(pathname, href)
        }

        const hrefWithoutQuery = href.split('?')[0]
        return exact
            ? pathname === hrefWithoutQuery
            : pathname.startsWith(hrefWithoutQuery)
    }, [pathname, href, exact, customIsActive])

    return (
        <div className="space-y-1 my-0.5">
            <Link
                to={items ? '.' : (href as any)}
                data-active={isActive}
                data-expanded={expanded}
                onPointerEnter={() => !locked && setHovered(true)}
                onPointerLeave={() => !locked && setHovered(false)}
                onClick={(e) => {
                    if (items && !locked) {
                        e.preventDefault()
                        setExpanded((v) => !v)
                    } else if (
                        !locked &&
                        (href === '/' || href.includes('/')) &&
                        isMobile
                    ) {
                        // Close sidebar for any navigation item that has an href (actual links) on mobile
                        setIsOpen(false)
                    }
                }}
                className={cn(
                    'text-brand-main-50 group flex items-center justify-between rounded px-2 py-1.5 text-sm leading-none transition-[background-color,color,font-weight] duration-75 border border-transparent ',
                    'outline-none focus-visible:ring-2 focus-visible:ring-black/50',
                    isActive && !items
                        ? 'bg-brand-secondary-200 font-medium text-brand-secondary-800 hover:bg-brand-secondary-200  active:bg-brand-secondary-200'
                        : locked
                            ? 'cursor-not-allowed opacity-75'
                            : 'hover:bg-brand-secondary-500/15 hover:border-brand-secondary-500/25 active:bg-brand-secondary-50/10',
                )}
                aria-disabled={locked}
            >
                <span className="flex items-center gap-2">
                    {locked ? (
                        <Lock className="size-4" />
                    ) : (
                        (() => {
                            if (!Icon) return null
                            // If a string was provided, treat it as an Iconify icon name
                            if (typeof Icon === 'string') {
                                return (
                                    <Iconify.Icon
                                        icon={Icon as string}
                                        color="currentColor"
                                        className={cn(
                                            'size-4',
                                            !items && 'group-data-[active=true]:text-brand-secondary-600',
                                        )}
                                        data-hovered={hovered}
                                    />
                                )
                            }
                            // If a valid ReactNode was provided, render it
                            if (React.isValidElement(Icon)) {
                                const currentClass = (Icon.props as any)?.className as
                                    | string
                                    | undefined
                                return React.cloneElement(
                                    Icon as React.ReactElement<any>,
                                    {
                                        className: cn(
                                            'size-4',
                                            currentClass,
                                            !items && 'group-data-[active=true]:text-brand-secondary-600',
                                        ),
                                    } as any,
                                )
                            }
                            // Otherwise assume it's a React component (e.g. Lucide icon component)
                            const Comp = Icon as ComponentType<{ className?: string }>
                            return (
                                <Comp
                                    className={cn(
                                        'size-4',
                                        !items && 'group-data-[active=true]:text-brand-secondary-600',
                                    )}
                                />
                            )
                        })()
                    )}
                    {name}
                </span>
                <span className="ml-2 flex items-center gap-2">
                    {'badge' in item && item.badge && (
                        <span
                            className={cn(
                                'flex items-center justify-center rounded px-1.5 py-0.5 text-xs font-semibold',
                                isActive && !items
                                    ? 'bg-brand-secondary-800 text-brand-secondary-200'
                                    : 'bg-brand-secondary-500/15 text-brand-secondary-400',
                            )}
                        >
                            {item.badge}
                        </span>
                    )}
                    {items && (
                        <ChevronDown className="size-3.5 text-neutral-500 transition-transform duration-75 group-data-[expanded=true]:rotate-180" />
                    )}
                    {item.arrow && (
                        <ArrowUpRight className="text-content-default size-3.5 transition-transform duration-75 group-hover:-translate-y-px group-hover:translate-x-px" />
                    )}
                </span>
            </Link>

            {items && (
                <AnimatePresence initial={false}>
                    {/* {(isActive || expanded) && ( */}
                    <AnimatedSizeContainer
                        key="submenu"
                        height
                        transition={{ duration: 0.2, ease: 'easeInOut' }}
                    >
                        <div className="pl-px pt-1">
                            <div className="pl-3.5">
                                <div className="flex flex-col gap-0.5 border-l border-neutral-200 text-brand-main-400 pl-2">
                                    {items.map((item) => (
                                        <NavItem key={item.name} item={item} />
                                    ))}
                                </div>
                            </div>
                        </div>
                    </AnimatedSizeContainer>
                    {/* )} */}
                </AnimatePresence>
            )}
        </div>
    )
}

function Area({
    visible,
    direction,
    children,
}: PropsWithChildren<{ visible: boolean; direction: 'left' | 'right' }>) {
    return (
        <motion.div
            className={cn(
                visible ? 'relative' : 'absolute inset-0',
                'w-full flex flex-col',
            )}
            style={{ willChange: 'transform, opacity' }}
            aria-hidden={!visible ? 'true' : undefined}
            inert={!visible}
            initial={{
                x: visible ? 0 : direction === 'left' ? '-2%' : '50%',
                opacity: visible ? 1 : 0,
            }}
            animate={{
                x: visible ? 0 : direction === 'left' ? '-2%' : '50%',
                opacity: visible ? 1 : 0,
            }}
            transition={{ duration: 0.2, ease: 'anticipate' }}
        >
            {children}
        </motion.div>
    )
}
