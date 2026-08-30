import { useState } from 'react'
import { useInviteTeamMember, getRoleValue } from '@/hooks/auth'
import { ui } from '@everstack/ui'

const {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} = ui

interface InviteMemberDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function InviteMemberDialog({
  open,
  onOpenChange,
}: InviteMemberDialogProps) {
  const inviteMutation = useInviteTeamMember()
  const [inviteEmail, setInviteEmail] = useState('')
  const [inviteRole, setInviteRole] = useState('member')
  const [upgradeMessage, setUpgradeMessage] = useState<string | null>(null)

  const handleInvite = async (e: React.FormEvent) => {
    e.preventDefault()
    setUpgradeMessage(null)

    try {
      const result = await inviteMutation.mutateAsync({
        email: inviteEmail,
        role: getRoleValue(inviteRole),
      })

      if ((result as { upgradeMessage?: string }).upgradeMessage) {
        setUpgradeMessage(
          (result as { upgradeMessage?: string }).upgradeMessage ?? null,
        )
      } else {
        onOpenChange(false)
        setInviteEmail('')
        setInviteRole('member')
      }
    } catch (error) {
      // Error handled by mutation
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="border-brand-main-600 bg-brand-main-900">
        <DialogHeader>
          <DialogTitle className="text-white light:text-brand-main-50">
            Invite Team Member
          </DialogTitle>
          <DialogDescription className="text-white/55 light:text-black/55">
            Send an invitation to join this Everstack instance.
          </DialogDescription>
        </DialogHeader>

        {upgradeMessage && (
          <div className="rounded-md border border-amber-500/20 bg-amber-500/10 p-3 text-sm text-amber-300 light:text-amber-700">
            {upgradeMessage}
          </div>
        )}

        {inviteMutation.error && (
          <div className="rounded-md border border-red-500/20 bg-red-500/10 p-3 text-sm text-red-300 light:text-red-600">
            {(inviteMutation.error as Error).message}
          </div>
        )}

        <form onSubmit={handleInvite} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="invite-email" className="text-brand-main-200">
              Email
            </Label>
            <Input
              id="invite-email"
              type="email"
              placeholder="colleague@company.com"
              value={inviteEmail}
              onChange={(e) => setInviteEmail(e.target.value)}
              required
              className="border-brand-main-600 bg-brand-main-800 text-white light:text-brand-main-50 placeholder:text-white/30 light:placeholder:text-black/30"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="invite-role" className="text-brand-main-200">
              Role
            </Label>
            <Select value={inviteRole} onValueChange={setInviteRole}>
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
              disabled={inviteMutation.isPending}
              variant="default"
            >
              {inviteMutation.isPending ? 'Sending...' : 'Send Invitation'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
