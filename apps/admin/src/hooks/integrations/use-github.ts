import { useQuery, useMutation, useQueryClient, type UseQueryResult, type UseMutationResult } from '@tanstack/react-query'
import {
    listGitHubInstallations,
    linkGitHubInstallation,
    removeGitHubInstallation,
    listGitHubRepositories,
    listGitHubBranches,
    listGitHubRepoTree,
    type GitHubInstallation,
    type GitHubRepository,
    type GitHubBranch,
    type GitHubTreeFile,
} from '@/server/github'
import { useSession } from '@/hooks/auth/use-auth'

const GITHUB_INSTALLATIONS_KEY = ['github-installations']
const GITHUB_REPOS_KEY = ['github-repos']
const GITHUB_BRANCHES_KEY = ['github-branches']
const GITHUB_TREE_KEY = ['github-tree']
const GITHUB_TREE_SEARCH_KEY = ['github-tree-search']

function useOrganizationContext() {
    const sessionQuery = useSession()
    const orgId = sessionQuery.data?.user?.organizations?.[0]?.id ?? ''
    const isSessionReady = sessionQuery.isSuccess && sessionQuery.data?.authenticated === true
    return { orgId, isSessionReady }
}

// ─── Query Hooks ────────────────────────────────────────────────────

export function useGitHubInstallations(
    options?: { enabled?: boolean }
): UseQueryResult<GitHubInstallation[], Error> {
    const { orgId, isSessionReady } = useOrganizationContext()
    const externallyEnabled = options?.enabled ?? true

    return useQuery({
        queryKey: [...GITHUB_INSTALLATIONS_KEY, orgId],
        queryFn: () => listGitHubInstallations(orgId),
        enabled: externallyEnabled && isSessionReady && !!orgId,
        staleTime: 2 * 60 * 1000,
        gcTime: 15 * 60 * 1000,
        refetchOnMount: false,
        refetchOnWindowFocus: false,
        refetchOnReconnect: false,
        retry: false,
    })
}

export function useGitHubRepositories(
    installationId: number,
    opts?: { query?: string; page?: number; perPage?: number }
): UseQueryResult<{ repositories: GitHubRepository[]; total: number }, Error> {
    const { orgId, isSessionReady } = useOrganizationContext()
    const repoQuery = opts?.query
    const repoPage = opts?.page
    const repoPerPage = opts?.perPage

    return useQuery({
        queryKey: [...GITHUB_REPOS_KEY, orgId, installationId, repoQuery ?? null, repoPage ?? null, repoPerPage ?? null],
        queryFn: () => listGitHubRepositories(orgId, installationId, opts),
        enabled: isSessionReady && !!orgId && installationId > 0,
        staleTime: 60 * 1000,
        refetchOnWindowFocus: false,
        retry: false,
    })
}

export function useGitHubBranches(
    installationId: number,
    owner: string,
    repo: string,
    opts?: { page?: number; perPage?: number }
): UseQueryResult<GitHubBranch[], Error> {
    const { orgId, isSessionReady } = useOrganizationContext()
    const branchPage = opts?.page
    const branchPerPage = opts?.perPage

    return useQuery({
        queryKey: [...GITHUB_BRANCHES_KEY, orgId, installationId, owner, repo, branchPage ?? null, branchPerPage ?? null],
        queryFn: () => listGitHubBranches(orgId, installationId, owner, repo, opts),
        enabled: isSessionReady && !!orgId && installationId > 0 && !!owner && !!repo,
        staleTime: 60 * 1000,
        refetchOnWindowFocus: false,
        retry: false,
    })
}

export function useGitHubRepoTree(
    installationId: number,
    owner: string,
    repo: string,
    opts?: { ref?: string; path?: string; enabled?: boolean }
): UseQueryResult<GitHubTreeFile[], Error> {
    const { orgId, isSessionReady } = useOrganizationContext()

    return useQuery({
        queryKey: [...GITHUB_TREE_KEY, orgId, installationId, owner, repo, opts?.ref, opts?.path],
        queryFn: () => listGitHubRepoTree(orgId, installationId, owner, repo, { ref: opts?.ref, path: opts?.path }),
        enabled: (opts?.enabled ?? true) && isSessionReady && !!orgId && installationId > 0 && !!owner && !!repo,
        staleTime: 5 * 60 * 1000, // tree changes infrequently
        refetchOnWindowFocus: false,
        retry: false,
    })
}

export function useGitHubRepoSearch(
    installationId: number,
    owner: string,
    repo: string,
    search: string,
    opts?: { ref?: string; enabled?: boolean }
): UseQueryResult<GitHubTreeFile[], Error> {
    const { orgId, isSessionReady } = useOrganizationContext()

    return useQuery({
        queryKey: [...GITHUB_TREE_SEARCH_KEY, orgId, installationId, owner, repo, opts?.ref, search],
        queryFn: () => listGitHubRepoTree(orgId, installationId, owner, repo, { ref: opts?.ref, search }),
        enabled: (opts?.enabled ?? true) && isSessionReady && !!orgId && installationId > 0 && !!owner && !!repo && search.length > 0,
        staleTime: 60 * 1000,
        refetchOnWindowFocus: false,
        retry: false,
    })
}

// ─── Mutation Hooks ─────────────────────────────────────────────────

export function useLinkGitHubInstallation(): UseMutationResult<
    GitHubInstallation,
    Error,
    { installationId: number }
> {
    const { orgId } = useOrganizationContext()
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: ({ installationId }) => linkGitHubInstallation(orgId, installationId),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: GITHUB_INSTALLATIONS_KEY })
        },
    })
}

export function useRemoveGitHubInstallation(): UseMutationResult<
    void,
    Error,
    { installationId: number }
> {
    const { orgId } = useOrganizationContext()
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: ({ installationId }) => removeGitHubInstallation(orgId, installationId),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: GITHUB_INSTALLATIONS_KEY })
        },
    })
}
