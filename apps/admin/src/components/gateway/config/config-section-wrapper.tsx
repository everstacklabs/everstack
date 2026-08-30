import type { ConfigSectionName } from '@/server/gateway-config'

interface ConfigSectionWrapperProps {
    title: string
    description: string
    section: ConfigSectionName
    // Kept on the prop type because the route still threads YAML state
    // through. The YAML editor itself is hidden — form is the only
    // surface — but ripping the plumbing out is post-release work.
    yamlContent: string
    onYAMLChange: (yaml: string) => void
    children: React.ReactNode
}

export function ConfigSectionWrapper({
    title,
    description,
    children,
}: ConfigSectionWrapperProps) {
    return (
        <div className="border border-brand-main-600 rounded-lg bg-brand-main-800/30">
            <div className="flex items-start justify-between p-4 pb-0">
                <div>
                    <h2 className="text-white light:text-brand-main-50 text-lg font-semibold">{title}</h2>
                    <p className="text-brand-main-100 text-sm mt-0.5">
                        {description}
                    </p>
                </div>
            </div>
            <div className="p-4">{children}</div>
        </div>
    )
}
