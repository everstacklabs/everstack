import { useQuery } from '@tanstack/react-query'
import { BUNDLE_VERSION, isUpdateAvailable, type ServerVersion } from '@/lib/version'

async function fetchServerVersion(): Promise<ServerVersion> {
    const res = await fetch('/version', { headers: { Accept: 'application/json' } })
    if (!res.ok) throw new Error(`/version returned ${res.status}`)
    return res.json()
}

export function useVersionCheck() {
    const { data } = useQuery({
        queryKey: ['app-version'],
        queryFn: fetchServerVersion,
        refetchInterval: 60_000,
        refetchOnWindowFocus: true,
        staleTime: 0,
        retry: false,
    })

    const serverVersion = data?.version
    return {
        serverVersion,
        bundleVersion: BUNDLE_VERSION,
        updateAvailable: isUpdateAvailable(serverVersion, BUNDLE_VERSION),
    }
}
