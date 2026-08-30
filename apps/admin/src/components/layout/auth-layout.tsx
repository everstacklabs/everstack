import type { ReactNode } from 'react'

export interface AuthLayoutProps {
    children: ReactNode
}

export function AuthLayout({ children }: AuthLayoutProps) {
    return (
        <div className="min-h-screen bg-zinc-950 light:bg-zinc-50 flex flex-col">
            {/* Background pattern */}
            <div
                className="fixed inset-0 opacity-30"
                style={{
                    backgroundImage: `radial-gradient(circle at 25% 25%, rgba(var(--brand-secondary-500), 0.1) 0%, transparent 50%),
                           radial-gradient(circle at 75% 75%, rgba(var(--brand-secondary-500), 0.1) 0%, transparent 50%)`,
                }}
            />

            {/* Content */}
            <div className="relative z-10 flex-1 flex items-center justify-center p-4">
                {children}
            </div>

            {/* Footer */}
            <footer className="relative z-10 text-center py-4 text-zinc-600 text-sm">
                <p>
                    Powered by{' '}
                    <a
                        href="https://everstack.ai"
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-brand-secondary-500 hover:underline"
                    >
                        Everstack
                    </a>
                </p>
            </footer>
        </div>
    )
}
