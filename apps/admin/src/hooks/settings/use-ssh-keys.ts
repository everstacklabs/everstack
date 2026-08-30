import { useQuery, useMutation, useQueryClient, type UseQueryResult, type UseMutationResult } from '@tanstack/react-query'
import { listSSHKeys, addSSHKey, deleteSSHKey, type SSHKey } from '@/server/ssh'

const SSH_KEYS_KEY = ['ssh-keys']

export function useSSHKeys(): UseQueryResult<{ keys: SSHKey[] }, Error> {
    return useQuery({
        queryKey: SSH_KEYS_KEY,
        queryFn: () => listSSHKeys(),
    })
}

export function useAddSSHKey(): UseMutationResult<{ key: SSHKey }, Error, { name: string; publicKey: string }> {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: ({ name, publicKey }: { name: string; publicKey: string }) =>
            addSSHKey(name, publicKey),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: SSH_KEYS_KEY })
        },
    })
}

export function useDeleteSSHKey(): UseMutationResult<{ success: boolean }, Error, number> {
    const queryClient = useQueryClient()

    return useMutation({
        mutationFn: (keyId: number) => deleteSSHKey(keyId),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: SSH_KEYS_KEY })
        },
    })
}
