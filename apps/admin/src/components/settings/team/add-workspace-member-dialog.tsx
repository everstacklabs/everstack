import { useState } from 'react'
import {
  useAddWorkspaceMember,
  useAvailableWorkspaceMembers,
  getWsRoleValue,
} from '@/hooks/auth'
import { ui } from '@everstack/ui'
import { Icon } from '@iconify/react'

const {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} = ui

interface AddWorkspaceMemberDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  workspaceId: string
}

export function AddWorkspaceMemberDialog({ open, onOpenChange, workspaceId }: AddWorkspaceMemberDialogProps) {
  const addMutation = useAddWorkspaceMember(workspaceId)
  const { data: availableMembers, isLoading: loadingAvailable } = useAvailableWorkspaceMembers(
    open ? workspaceId : undefined
  )
  const [selectedUserId, setSelectedUserId] = useState('')
  const [selectedRole, setSelectedRole] = useState('member')

  const handleAdd = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!selectedUserId) return

    try {
      await addMutation.mutateAsync({
        userId: selectedUserId,
        role: getWsRoleValue(selectedRole),
      })
      onOpenChange(false)
      setSelectedUserId('')
      setSelectedRole('member')
    } catch {
      // Error handled by mutation
    }
  }

  const members = availableMembers ?? []

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="border-brand-main-600 bg-brand-main-900">
        <DialogHeader>
          <DialogTitle className="text-white light:text-brand-main-50">Add Instance Member</DialogTitle>
          <DialogDescription className="text-white/55 light:text-black/55">
            Grant an organization member access to this instance.
          </DialogDescription>
        </DialogHeader>

        {addMutation.error && (
          <div className="rounded-md border border-red-500/20 bg-red-500/10 p-3 text-sm text-red-300 light:text-red-600">
            {(addMutation.error as Error).message}
          </div>
        )}

        <form onSubmit={handleAdd} className="space-y-4">
          <div className="space-y-2">
            <Label className="text-brand-main-200">Member</Label>
            {loadingAvailable ? (
              <div className="flex items-center gap-2 rounded-md border border-brand-main-600 bg-brand-main-800 px-3 py-2 text-sm text-white/40 light:text-black/40">
                <Icon icon="lucide:loader-2" className="size-3.5 animate-spin" />
                Loading available members...
              </div>
            ) : members.length === 0 ? (
              <div className="rounded-md border border-brand-main-600 bg-brand-main-800 px-3 py-2 text-sm text-white/40 light:text-black/40">
                No available members to add. All org members already have access.
              </div>
            ) : (
              <Select value={selectedUserId} onValueChange={setSelectedUserId}>
                <SelectTrigger className="border-brand-main-600 bg-brand-main-800 text-white light:text-brand-main-50">
                  <SelectValue placeholder="Select a member..." />
                </SelectTrigger>
                <SelectContent className="border-brand-main-600 bg-brand-main-900">
                  {members.map((member) => (
                    <SelectItem key={member.userId} value={member.userId}>
                      <span className="flex items-center gap-2">
                        <span>{member.name ?? member.email}</span>
                        {member.name && (
                          <span className="text-white/40 light:text-black/40">{member.email}</span>
                        )}
                      </span>
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </div>

          <div className="space-y-2">
            <Label className="text-brand-main-200">Role</Label>
            <Select value={selectedRole} onValueChange={setSelectedRole}>
              <SelectTrigger className="border-brand-main-600 bg-brand-main-800 text-white light:text-brand-main-50">
                <SelectValue />
              </SelectTrigger>
              <SelectContent className="border-brand-main-600 bg-brand-main-900">
                <SelectItem value="admin">Admin</SelectItem>
                <SelectItem value="member">Member</SelectItem>
                <SelectItem value="viewer">Viewer</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <DialogFooter className="pt-2">
            <Button
              type="button"
              variant="outline"
              onClick={() => onOpenChange(false)}
            >
              Cancel
            </Button>
            <Button
              type="submit"
              disabled={addMutation.isPending || !selectedUserId || members.length === 0}
              variant="default"
            >
              {addMutation.isPending ? 'Adding...' : 'Add Member'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
