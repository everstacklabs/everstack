import { ui, Iconify } from '@everstack/ui'
import { cn } from '@/lib/utils'
import { useGatewayVersion } from '@/hooks/use-gateway-version'

const { Popover, PopoverTrigger, PopoverContent } = ui

type HelpLink = {
    label: string
    href: string
    icon: string
}

const LINKS: HelpLink[] = [
    { label: 'Documentation', href: 'https://docs.everstack.ai', icon: 'hugeicons:book-02' },
    { label: 'Changelog', href: 'https://everstack.ai/changelog', icon: 'hugeicons:note-03' },
    { label: 'Roadmap', href: 'https://everstack.ai/roadmap', icon: 'hugeicons:route-01' },
    { label: 'Feedback', href: 'https://github.com/everstacklabs/everstack/issues/new', icon: 'hugeicons:message-question' },
]

export function HelpMenu() {
    const info = useGatewayVersion()
    const shortCommit = info?.commit ? info.commit.slice(0, 7) : ''
    const versionLabel = info?.version
        ? shortCommit
            ? `${info.version}`
            : info.version
        : 'unknown'

    return (
        <Popover>
            <PopoverTrigger asChild>
                <button
                    type="button"
                    aria-label="Help"
                    className={cn(
                        'relative flex size-10 items-center justify-center rounded transition-colors duration-150 border border-transparent',
                        'outline-none focus-visible:ring-2 focus-visible:ring-black/50',
                        'hover:bg-brand-secondary-500/15 hover:border-brand-secondary-500/25 active:bg-brand-secondary-50/10 text-brand-secondary-100',
                    )}
                >
                    <Iconify.Icon
                        icon="hugeicons:help-circle"
                        className="text-content-default size-5"
                    />
                </button>
            </PopoverTrigger>
            <PopoverContent
                side="right"
                align="end"
                sideOffset={8}
                className="w-60 p-1 bg-brand-main-800 border-brand-main-600 text-brand-secondary-100"
            >
                <div className="flex flex-col">
                    {LINKS.map((link) => (
                        <a
                            key={link.href}
                            href={link.href}
                            target="_blank"
                            rel="noopener noreferrer"
                            className="flex items-center gap-2 px-2 py-1.5 mb-1 rounded border border-transparent text-xs hover:bg-brand-secondary-500/15 hover:border-brand-secondary-500/25 active:bg-brand-secondary-50/10 text-brand-secondary-100 transition-colors"
                        >
                            <Iconify.Icon icon={link.icon} className="size-4 opacity-80" />
                            <span className="flex-1">{link.label}</span>
                            <Iconify.Icon
                                icon="hugeicons:arrow-up-right-01"
                                className="size-3.5 opacity-60"
                            />
                        </a>
                    ))}
                </div>
                <div
                    className="border-t border-brand-main-600/60 px-2.5 pt-2 pb-1 flex items-start justify-between gap-1.5 text-[12px] text-brand-main-400"
                    title={info?.commit || undefined}
                >
                    <span>VERSION</span>
                    <span className="font-mono text-brand-main-50 truncate">
                        {versionLabel}
                    </span>
                </div>
            </PopoverContent>
        </Popover>
    )
}
