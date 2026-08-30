import { useQuery, useMutation, useQueryClient, type UseQueryResult, type UseMutationResult } from '@tanstack/react-query'
import {
    createSandboxSSHToken,
    getSandboxSSHInfo,
    grantSandboxSSHAccess,
    listSandboxSSHTokens,
    revokeSandboxSSHAccess,
    revokeSandboxSSHToken,
    type CreateSandboxSSHTokenResponse,
    type SandboxSSHToken,
    type SSHInfo,
} from '@/server/ssh'

const SANDBOX_SSH_INFO_KEY = ['sandbox-ssh-info']
const SANDBOX_SSH_TOKENS_KEY = ['sandbox-ssh-tokens']

export function useSandboxSSHInfo(sandboxId: string | undefined): UseQueryResult<SSHInfo, Error> {
    return useQuery({
        queryKey: [...SANDBOX_SSH_INFO_KEY, sandboxId],
        queryFn: () => getSandboxSSHInfo(sandboxId!),
        enabled: !!sandboxId,
    })
}

export function useGrantSSHAccess(): UseMutationResult<{ success: boolean }, Error, { sandboxId: string; userId: string }> {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: ({ sandboxId, userId }: { sandboxId: string; userId: string }) =>
            grantSandboxSSHAccess(sandboxId, userId),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: SANDBOX_SSH_INFO_KEY })
        },
    })
}

export function useRevokeSSHAccess(): UseMutationResult<{ success: boolean }, Error, { sandboxId: string; userId: string }> {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: ({ sandboxId, userId }: { sandboxId: string; userId: string }) =>
            revokeSandboxSSHAccess(sandboxId, userId),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: SANDBOX_SSH_INFO_KEY })
        },
    })
}

export function useSandboxSSHTokens(sandboxId: string | undefined): UseQueryResult<{ tokens: SandboxSSHToken[] }, Error> {
    return useQuery({
        queryKey: [...SANDBOX_SSH_TOKENS_KEY, sandboxId],
        queryFn: () => listSandboxSSHTokens(sandboxId!),
        enabled: !!sandboxId,
        refetchInterval: 15_000,
    })
}

export function useCreateSandboxSSHToken(): UseMutationResult<CreateSandboxSSHTokenResponse, Error, { sandboxId: string; expiresInMinutes: number }> {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: ({ sandboxId, expiresInMinutes }) => createSandboxSSHToken(sandboxId, expiresInMinutes),
        onSuccess: (_data, variables) => {
            queryClient.invalidateQueries({ queryKey: [...SANDBOX_SSH_TOKENS_KEY, variables.sandboxId] })
        },
    })
}

export function useRevokeSandboxSSHToken(): UseMutationResult<{ success: boolean }, Error, { sandboxId: string; tokenId: string }> {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: ({ sandboxId, tokenId }) => revokeSandboxSSHToken(sandboxId, tokenId),
        onSuccess: (_data, variables) => {
            queryClient.invalidateQueries({ queryKey: [...SANDBOX_SSH_TOKENS_KEY, variables.sandboxId] })
        },
    })
}
