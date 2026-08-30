import { Iconify } from '@everstack/ui/icons'

interface EnvironmentCardProps {
    name: string
    description: string
    icon: string
    iconColor: string
    imageTag: string
    isCustom?: boolean
    selected: boolean
    onClick: () => void
}

export function EnvironmentCard({ name, description, icon, iconColor, imageTag, isCustom, selected, onClick }: EnvironmentCardProps) {
    return (
        <button
            type="button"
            onClick={onClick}
            className={`relative flex flex-col items-start gap-3 p-4 rounded-lg border transition-all text-left
                ${selected
                    ? 'border-brand-secondary-500 ring-1 ring-brand-secondary-500/40 bg-brand-secondary-600/10'
                    : 'border-brand-main-600 bg-brand-main-800/40 hover:border-brand-main-500 hover:bg-brand-main-800/60'
                }`}
        >
            {selected && (
                <div className="absolute top-2.5 right-2.5 text-brand-secondary-400">
                    <Iconify.Icon icon="heroicons:check-circle-solid" className="size-5" />
                </div>
            )}
            <div
                className="flex items-center justify-center size-10 rounded-lg"
                style={{ backgroundColor: `${iconColor}15` }}
            >
                <Iconify.Icon icon={icon} className="size-5" style={{ color: iconColor }} />
            </div>
            <div className="flex flex-col gap-1">
                <span className="text-sm font-medium text-white light:text-brand-main-50">{name}</span>
                <span className="text-xs text-white/50 light:text-black/50">{description}</span>
            </div>
            {!isCustom && (
                <span className="inline-flex items-center px-2 py-0.5 rounded text-[10px] font-mono text-white/40 light:text-black/40 bg-brand-main-700/50 border border-brand-main-600">
                    {imageTag}
                </span>
            )}
        </button>
    )
}
