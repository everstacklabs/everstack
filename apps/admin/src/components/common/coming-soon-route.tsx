type ComingSoonRouteProps = {
    title: string
    description: string
}

export function ComingSoonRoute({ title, description }: ComingSoonRouteProps) {
    return (
        <div className="flex h-full w-full items-center justify-center p-6">
            <div className="max-w-xl text-center space-y-3">
                <h2 className="text-2xl font-semibold text-white light:text-brand-main-50">{title}</h2>
                <p className="text-sm text-white/60 light:text-black/60">{description}</p>
            </div>
        </div>
    )
}
