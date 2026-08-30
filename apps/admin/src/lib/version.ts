export const BUNDLE_VERSION: string = import.meta.env.VITE_APP_VERSION || 'dev'

export type ServerVersion = {
    version: string
    commit: string
    date: string
}

export function isUpdateAvailable(serverVersion: string | undefined, bundleVersion: string): boolean {
    if (!serverVersion || !bundleVersion) return false
    if (bundleVersion === 'dev') return false
    return serverVersion !== bundleVersion
}
