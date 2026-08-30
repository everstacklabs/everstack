import { useQueries } from '@tanstack/react-query'
import { useConfiguredProviders } from '@/hooks/vault/use-providers'
import { providerAPIKeyKeys } from '@/hooks/vault/use-provider-api-keys'
import { listProviderAPIKeys } from '@/server/provider-api-keys'

export type ProviderKeyInfo = { name: string; provider: string }

/**
 * Resolves upstream provider API key ids -> { name, provider } for display in
 * observability (metrics stamp only the key id). Fans out one lightweight,
 * long-cached ListProviderAPIKeys query per configured provider via useQueries,
 * so there is no hook-in-loop violation and the map stays fresh cheaply.
 */
export function useProviderKeyNames(): Record<string, ProviderKeyInfo> {
  const { data } = useConfiguredProviders()

  const configs = (data?.providers ?? [])
    .map((p) => p.configuration)
    .filter(
      (c): c is NonNullable<typeof c> => !!c && !!c.id && !!c.providerName,
    )

  const results = useQueries({
    queries: configs.map((c) => ({
      queryKey: providerAPIKeyKeys.list(c.id),
      queryFn: () => listProviderAPIKeys(c.id),
      staleTime: 300_000,
    })),
  })

  const map: Record<string, ProviderKeyInfo> = {}
  results.forEach((res, i) => {
    const provider = configs[i]?.providerName ?? ''
    for (const key of res.data?.keys ?? []) {
      map[key.id] = { name: key.keyName || key.id, provider }
    }
  })
  return map
}
