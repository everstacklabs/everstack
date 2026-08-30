import {
    useMutation,
    useQuery,
    useQueryClient,
    type UseMutationResult,
    type UseQueryResult,
} from '@tanstack/react-query'
import {
    createPrompt,
    createPromptVersion,
    deletePrompt,
    getPrompt,
    listPrompts,
    listPromptVersions,
    setPromptLabels,
    updatePrompt,
    type CreatePromptParams,
    type CreatePromptVersionParams,
    type Prompt,
    type PromptVersion,
    type UpdatePromptParams,
} from '@/server/prompts'
import { useSession } from '@/hooks/auth'

const PROMPTS_KEY = ['prompts']
const PROMPT_VERSIONS_KEY = ['prompt-versions']

// Tenant scoping happens server-side from the session; the org id is only
// used here to partition the query cache across org switches.
function useOrganizationId(): string {
    const { data: session } = useSession()
    return session?.user?.organizations?.[0]?.id ?? ''
}

export function usePrompts(): UseQueryResult<Prompt[], Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...PROMPTS_KEY, orgId],
        queryFn: async () => {
            const response = await listPrompts()
            return response.prompts ?? []
        },
        enabled: !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 30_000,
    })
}

export function usePrompt(promptId: string): UseQueryResult<Prompt | undefined, Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...PROMPTS_KEY, 'detail', orgId, promptId],
        queryFn: async () => {
            const response = await getPrompt({ id: promptId })
            return response.prompt
        },
        enabled: !!promptId && !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 15_000,
    })
}

export function usePromptVersions(promptId: string): UseQueryResult<PromptVersion[], Error> {
    const orgId = useOrganizationId()
    return useQuery({
        queryKey: [...PROMPT_VERSIONS_KEY, orgId, promptId],
        queryFn: async () => {
            const response = await listPromptVersions({ promptId })
            return response.versions ?? []
        },
        enabled: !!promptId && !!orgId,
        refetchOnWindowFocus: false,
        staleTime: 15_000,
    })
}

export function useCreatePrompt(): UseMutationResult<unknown, Error, CreatePromptParams> {
    const queryClient = useQueryClient()
    return useMutation({
        mutationFn: (params) => createPrompt(params),
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: PROMPTS_KEY })
        },
    })
}

export function useUpdatePrompt(): UseMutationResult<unknown, Error, UpdatePromptParams> {
    const queryClient = useQueryClient()
    return useMutation({
        mutationFn: (params) => updatePrompt(params),
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: PROMPTS_KEY })
        },
    })
}

export function useDeletePrompt(): UseMutationResult<unknown, Error, string> {
    const queryClient = useQueryClient()
    return useMutation({
        mutationFn: (id) => deletePrompt(id),
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: PROMPTS_KEY })
        },
    })
}

export function useCreatePromptVersion(): UseMutationResult<unknown, Error, CreatePromptVersionParams> {
    const queryClient = useQueryClient()
    return useMutation({
        mutationFn: (params) => createPromptVersion(params),
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: PROMPT_VERSIONS_KEY })
            await queryClient.invalidateQueries({ queryKey: PROMPTS_KEY })
        },
    })
}

export function useSetPromptLabels(): UseMutationResult<
    unknown,
    Error,
    { promptId: string; version: number; labels: string[] }
> {
    const queryClient = useQueryClient()
    return useMutation({
        mutationFn: (params) => setPromptLabels(params),
        onSuccess: async () => {
            await queryClient.invalidateQueries({ queryKey: PROMPT_VERSIONS_KEY })
            await queryClient.invalidateQueries({ queryKey: PROMPTS_KEY })
        },
    })
}
