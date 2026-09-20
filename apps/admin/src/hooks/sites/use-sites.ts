import {
    useMutation,
    useQuery,
    useQueryClient,
    type UseMutationResult,
    type UseQueryResult,
} from '@tanstack/react-query'
import {
    deleteSite,
    getHostingCapabilities,
    getSite,
    listAllSites,
    updateSite,
    type HostingCapabilities,
    type Site,
    type UpdateSiteParams,
} from '@/server/sites'

// evs.run hosted sites. Tenant-scoped resources: no session/org arg
// needed; the client authenticates via the session cookie and the
// server scopes by the authenticated tenant.

const SITES_QUERY_KEY = ['sites']

/**
 * Whether this deployment has hosting configured at all. Answers even when
 * hosting is off, so the UI can distinguish "switched off here" from "the
 * request failed". Deployment-level and effectively immutable for the life
 * of the session, hence no refetching.
 */
export function useHostingCapabilities(): UseQueryResult<
    HostingCapabilities,
    Error
> {
    return useQuery({
        queryKey: [...SITES_QUERY_KEY, 'capabilities'],
        queryFn: getHostingCapabilities,
        refetchOnWindowFocus: false,
        staleTime: Infinity,
        retry: false,
    })
}

export function useSites(): UseQueryResult<Site[], Error> {
    return useQuery({
        queryKey: SITES_QUERY_KEY,
        queryFn: listAllSites,
        refetchOnWindowFocus: false,
        staleTime: 30_000,
    })
}

export function useSite(slug: string): UseQueryResult<Site, Error> {
    return useQuery({
        queryKey: [...SITES_QUERY_KEY, 'detail', slug],
        queryFn: () => getSite(slug),
        enabled: !!slug,
        refetchOnWindowFocus: false,
        staleTime: 30_000,
    })
}

export function useUpdateSite(): UseMutationResult<Site, Error, UpdateSiteParams> {
    const queryClient = useQueryClient()
    return useMutation({
        mutationFn: (params) => updateSite(params),
        onSuccess: (site) => {
            // Seed the detail cache with the server response so the page
            // reflects the change immediately, then refresh the list.
            queryClient.setQueryData([...SITES_QUERY_KEY, 'detail', site.slug], site)
            queryClient.invalidateQueries({ queryKey: SITES_QUERY_KEY, exact: true })
        },
    })
}

export function useDeleteSite(): UseMutationResult<void, Error, string> {
    const queryClient = useQueryClient()
    return useMutation({
        mutationFn: (slug: string) => deleteSite(slug),
        onSuccess: (_data, slug) => {
            queryClient.removeQueries({ queryKey: [...SITES_QUERY_KEY, 'detail', slug] })
            queryClient.invalidateQueries({ queryKey: SITES_QUERY_KEY, exact: true })
        },
    })
}
