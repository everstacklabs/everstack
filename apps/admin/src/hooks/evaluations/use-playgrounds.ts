import {
    useQuery,
    useMutation,
    useQueryClient,
    type UseQueryResult,
    type UseMutationResult,
} from '@tanstack/react-query'
import {
    listPlaygrounds,
    getPlayground,
    createPlayground,
    updatePlayground,
    deletePlayground,
    type Playground,
    type PlaygroundConfig,
} from '@/server/playgrounds'
import { useSession } from '@/hooks/auth'

const PLAYGROUNDS_KEY = ['playgrounds']

function useOrganizationId(): string {
    const { data: session } = useSession()
    return session?.user?.organizations?.[0]?.id ?? ''
}

export function usePlaygrounds(): UseQueryResult<Playground[], Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...PLAYGROUNDS_KEY, orgId],
        queryFn: () => listPlaygrounds({ tenantId: orgId }),
        enabled: !!orgId,
        refetchOnWindowFocus: false,
    })
}

export function usePlayground(id: string): UseQueryResult<Playground | undefined, Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...PLAYGROUNDS_KEY, 'detail', orgId, id],
        queryFn: () => getPlayground(id, orgId),
        enabled: !!id && !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 30_000,
    })
}

export function useCreatePlayground(): UseMutationResult<
    Playground | undefined,
    Error,
    { name: string; config: PlaygroundConfig }
> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => createPlayground({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: PLAYGROUNDS_KEY })
        },
    })
}

export function useUpdatePlayground(): UseMutationResult<
    Playground | undefined,
    Error,
    { id: string; name?: string; config?: PlaygroundConfig }
> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (params) => updatePlayground({ ...params, tenantId: orgId }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: PLAYGROUNDS_KEY })
        },
    })
}

export function useDeletePlayground(): UseMutationResult<boolean, Error, string> {
    const queryClient = useQueryClient()
    const orgId = useOrganizationId()
    return useMutation({
        mutationFn: (id: string) => deletePlayground(id, orgId),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: PLAYGROUNDS_KEY })
        },
    })
}
