import { useState } from 'react'
import { ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import { useAddSSHKey } from '@/hooks/settings/use-ssh-keys'

const { Dialog, DialogContent, DialogTitle, DialogDescription, Button, Input, Label } = ui

interface AddSSHKeyDialogProps {
    open: boolean
    onOpenChange: (open: boolean) => void
}

export function AddSSHKeyDialog({ open, onOpenChange }: AddSSHKeyDialogProps) {
    const [name, setName] = useState('')
    const [publicKey, setPublicKey] = useState('')
    const addMutation = useAddSSHKey()

    const handleSubmit = (e: React.FormEvent) => {
        e.preventDefault()

        if (!name.trim() || !publicKey.trim()) {
            toast.error('Name and public key are required')
            return
        }

        addMutation.mutate(
            { name: name.trim(), publicKey: publicKey.trim() },
            {
                onSuccess: () => {
                    toast.success('SSH key added')
                    handleClose()
                },
                onError: (err) => toast.error(`Failed to add key: ${err.message}`),
            }
        )
    }

    const handleClose = () => {
        setName('')
        setPublicKey('')
        onOpenChange(false)
    }

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className='w-[600px]'>
                <DialogTitle>Add SSH Key</DialogTitle>
                <DialogDescription className='text-white/70 light:text-black/70'>
                    Add a public key to enable SSH access to sandboxes.
                </DialogDescription>

                <form onSubmit={handleSubmit} className='space-y-4 mt-4'>
                    <div className='space-y-2'>
                        <Label htmlFor='ssh-key-name'>Name *</Label>
                        <Input
                            id='ssh-key-name'
                            placeholder='e.g. My Laptop'
                            value={name}
                            onChange={(e) => setName(e.target.value)}
                            required
                            className='bg-brand-main-900 border-brand-main-600'
                        />
                        <p className='text-xs text-white/50 light:text-black/50'>A descriptive name for this SSH key</p>
                    </div>

                    <div className='space-y-2'>
                        <Label htmlFor='ssh-public-key'>Public Key *</Label>
                        <textarea
                            id='ssh-public-key'
                            value={publicKey}
                            onChange={(e) => setPublicKey(e.target.value)}
                            placeholder='ssh-ed25519 AAAA... or ssh-rsa AAAA...'
                            rows={3}
                            required
                            className='w-full px-3 py-2 bg-brand-main-900 border border-brand-main-600 rounded-md text-sm text-white light:text-brand-main-50 font-mono placeholder:text-white/30 light:placeholder:text-black/30 focus:outline-none focus:border-brand-secondary-500 resize-none'
                        />
                    </div>

                    <div className='flex justify-end gap-3 pt-4'>
                        <Button
                            type='button'
                            variant='ghost'
                            onClick={handleClose}
                            disabled={addMutation.isPending}
                        >
                            Cancel
                        </Button>
                        <Button
                            type='submit'
                            disabled={addMutation.isPending || !name.trim() || !publicKey.trim()}
                        >
                            {addMutation.isPending ? 'Adding...' : 'Add Key'}
                        </Button>
                    </div>
                </form>
            </DialogContent>
        </Dialog>
    )
}
