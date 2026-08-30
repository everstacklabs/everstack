import { useEffect, useState } from 'react'
import { getApiBaseUrl } from '@/lib/api-url'

export interface GatewayVersion {
    version: string
    commit: string
    date: string
}

export function useGatewayVersion(): GatewayVersion | null {
    const [info, setInfo] = useState<GatewayVersion | null>(null)

    useEffect(() => {
        let cancelled = false
        fetch(`${getApiBaseUrl()}/debug/version`)
            .then((r) => (r.ok ? r.json() : null))
            .then((data) => {
                if (!cancelled && data) setInfo(data as GatewayVersion)
            })
            .catch(() => {
                // Silent: footer just hides when unavailable.
            })
        return () => {
            cancelled = true
        }
    }, [])

    return info
}
