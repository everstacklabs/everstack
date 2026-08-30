import { useMemo } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import {
  useTeamMembers,
  useRemoveTeamMember,
  useRevokeInvitation,
  getRoleName,
  useSession,
  usePermissions,
} from '@/hooks/auth'
import { ui } from '@everstack/ui'
import { Icon } from '@iconify/react'
import { cn } from '@everstack/utils/functions/cn'

const {
  Badge,
  Card,
  CardContent,
  Avatar,
  AvatarFallback,
  AvatarImage,
  Progress,
} = ui

export const Route = createFileRoute('/settings/team')({
  component: TeamSettingsPage,
})

function roleBadgeClass(role: string | number) {
  const name = typeof role === 'number' ? getRoleName(role).toLowerCase() : role.toLowerCase()
  switch (name) {
    case 'owner':
      return 'border-brand-secondary-500/30 bg-brand-secondary-500/10 text-brand-secondary-300'
    case 'admin':
      return 'border-blue-500/30 bg-blue-500/10 text-blue-300 light:text-blue-600'
    case 'member':
      return 'border-emerald-500/30 bg-emerald-500/10 text-emerald-300 light:text-emerald-600'
    default:
      return 'border-brand-main-500/40 bg-brand-main-700/40 text-brand-main-200'
  }
}

export function TeamSettingsPage() {
  const gate = useFeatureGate(FeatureKey.TEAM_MANAGEMENT)

  if (gate.isBlocked) {
    return (
      <FeatureGateBanner
        featureName="Team Management"
        description="Invite and manage team members with role-based access control."
        requiredTier="Basic"
        upgradeUrl={gate.upgradeUrl}
        isCE={gate.isCE}
      />
    )
  }

  return <TeamSettingsPageContent />
}

function TeamSettingsPageContent() {
  const { data: team, isLoading } = useTeamMembers()
  const { data: session } = useSession()
  const { can } = usePermissions()
  const canManageMembers = can('org:manage_members')
  const removeMutation = useRemoveTeamMember()
  const revokeMutation = useRevokeInvitation()

  const handleRemove = async (userId: string) => {
    if (!confirm('Are you sure you want to remove this team member?')) return
    await removeMutation.mutateAsync({ userId })
  }

  const handleRevoke = async (invitationId: string) => {
    if (!confirm('Are you sure you want to revoke this invitation?')) return
    await revokeMutation.mutateAsync({ invitationId })
  }

  const currentUserId = session?.user?.user?.id
  const members = team?.members ?? []
  const pendingInvitations = team?.pendingInvitations ?? []

  const seatLimit = team?.seatLimit ?? 0
  const seatsUsed = team?.seatsUsed ?? 0
  const seatPercentage = seatLimit > 0 ? Math.min((seatsUsed / seatLimit) * 100, 100) : 0
  const adminCount = useMemo(
    () => members.filter((member) => ['owner', 'admin'].includes(getRoleName(member.role).toLowerCase())).length,
    [members],
  )

  if (isLoading) {
    return (
      <div className="flex h-full w-full items-center justify-center text-white/70 light:text-black/70">
        <div className="flex items-center gap-2 text-sm">
          <Icon icon="lucide:loader-2" className="size-4 animate-spin" />
          Loading team...
        </div>
      </div>
    )
  }

  return (
    <div className="flex h-full w-full flex-col">
      <div className="flex-1 space-y-4 overflow-y-auto p-8 px-60 mx-auto w-full">

        {/* ── Metrics row ── */}
        <div className="grid grid-cols-2 md:grid-cols-4 gap-2">
          <Card className="border-brand-main-600 bg-brand-main-900/50 rounded">
            <CardContent>
              <div className="flex items-center justify-between gap-2">
                <div className="flex items-center gap-2 min-w-0">
                  <div className="p-1.5 rounded bg-brand-secondary-600/20 border border-brand-secondary-500/30">
                    <Icon icon="lucide:users" className="size-4 text-brand-secondary-300" />
                  </div>
                  <div className="text-xs text-white light:text-brand-main-50 uppercase tracking-wide">Members</div>
                </div>
                <div className="text-sm font-semibold text-white light:text-brand-main-50">{members.length}</div>
              </div>
            </CardContent>
          </Card>
          <Card className="border-brand-main-600 bg-brand-main-900/50 rounded">
            <CardContent>
              <div className="flex items-center justify-between gap-2">
                <div className="flex items-center gap-2 min-w-0">
                  <div className="p-1.5 rounded bg-brand-secondary-600/20 border border-brand-secondary-500/30">
                    <Icon icon="lucide:mail" className="size-4 text-brand-secondary-300" />
                  </div>
                  <div className="text-xs text-white light:text-brand-main-50 uppercase tracking-wide">Pending</div>
                </div>
                <div className="text-sm font-semibold text-white light:text-brand-main-50">{pendingInvitations.length}</div>
              </div>
            </CardContent>
          </Card>
          <Card className="border-brand-main-600 bg-brand-main-900/50 rounded">
            <CardContent>
              <div className="flex items-center justify-between gap-2">
                <div className="flex items-center gap-2 min-w-0">
                  <div className="p-1.5 rounded bg-brand-secondary-600/20 border border-brand-secondary-500/30">
                    <Icon icon="lucide:shield" className="size-4 text-brand-secondary-300" />
                  </div>
                  <div className="text-xs text-white light:text-brand-main-50 uppercase tracking-wide">Admins</div>
                </div>
                <div className="text-sm font-semibold text-white light:text-brand-main-50">{adminCount}</div>
              </div>
            </CardContent>
          </Card>
          <Card className="border-brand-main-600 bg-brand-main-900/50 rounded">
            <CardContent>
              <div className="flex items-center justify-between gap-2">
                <div className="flex items-center gap-2 min-w-0">
                  <div className="p-1.5 rounded bg-brand-secondary-600/20 border border-brand-secondary-500/30">
                    <Icon icon="lucide:armchair" className="size-4 text-brand-secondary-300" />
                  </div>
                  <div className="text-xs text-white light:text-brand-main-50 uppercase tracking-wide">Seats</div>
                </div>
                <div className="text-sm font-semibold text-white light:text-brand-main-50">
                  {seatLimit > 0 ? `${seatsUsed}/${seatLimit}` : 'Unlimited'}
                </div>
              </div>
              {seatLimit > 0 && (
                <Progress value={seatPercentage} className="mt-2 h-1 bg-brand-main-700" />
              )}
            </CardContent>
          </Card>
        </div>

        {/* ── Team members ── */}
        <div className="space-y-1.5">
          <div className="flex items-center justify-between px-1 pb-1">
            <h3 className="text-[13px] font-medium text-white/60 light:text-black/60">
              Members ({members.length})
            </h3>
          </div>

          {members.length === 0 ? (
            <div className="rounded-lg border border-brand-main-600 bg-brand-main-800/50 p-8 text-center">
              <Icon icon="lucide:users" className="mx-auto size-8 text-white/15 light:text-black/15" />
              <p className="mt-2 text-sm text-white/40 light:text-black/40">No team members found</p>
            </div>
          ) : (
            <div className="space-y-1.5">
              {members.map((member) => {
                const isOwner = getRoleName(member.role).toLowerCase() === 'owner'
                const isSelf = member.user.id === currentUserId
                const canRemove = canManageMembers && !isSelf && !isOwner
                return (
                  <div
                    key={member.user.id}
                    className="group flex items-center gap-3 rounded-lg border border-brand-main-600 bg-brand-main-800/50 px-3.5 py-3 transition-colors hover:bg-brand-main-800/70"
                  >
                    <Avatar className="size-9 shrink-0 border border-brand-main-500/50">
                      <AvatarImage src={member.user.avatarUrl} />
                      <AvatarFallback className="bg-brand-main-700 text-sm text-brand-main-100">
                        {(member.user.name || member.user.email).charAt(0).toUpperCase()}
                      </AvatarFallback>
                    </Avatar>

                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <span className="truncate text-sm font-medium text-white light:text-brand-main-50">
                          {member.user.name || member.user.email}
                        </span>
                        {isSelf && (
                          <span className="text-[10px] text-white/30 light:text-black/30">you</span>
                        )}
                      </div>
                      <span className="truncate text-xs text-white/40 light:text-black/40">
                        {member.user.email}
                      </span>
                    </div>

                    <Badge
                      variant="outline"
                      className={cn('shrink-0 text-[11px] capitalize', roleBadgeClass(member.role))}
                    >
                      {getRoleName(member.role)}
                    </Badge>

                    <span className="hidden shrink-0 text-xs text-white/30 light:text-black/30 sm:block">
                      {new Date(member.joinedAt).toLocaleDateString()}
                    </span>

                    <div className="w-8 shrink-0 text-right">
                      {canRemove && (
                        <button
                          onClick={() => handleRemove(member.user.id)}
                          disabled={removeMutation.isPending}
                          className="rounded p-1 text-red-400/50 light:text-red-600/50 opacity-0 transition-all hover:bg-red-500/10 hover:text-red-400 light:hover:text-red-600 group-hover:opacity-100 disabled:opacity-50"
                          title="Remove member"
                        >
                          <Icon icon="lucide:user-minus" className="size-3.5" />
                        </button>
                      )}
                    </div>
                  </div>
                )
              })}
            </div>
          )}
        </div>

        {/* ── Pending invitations ── */}
        {pendingInvitations.length > 0 && (
          <div className="space-y-1.5">
            <div className="flex items-center justify-between px-1 pb-1">
              <h3 className="text-[13px] font-medium text-white/60 light:text-black/60">
                Pending Invitations ({pendingInvitations.length})
              </h3>
            </div>
            <div className="space-y-1.5">
              {pendingInvitations.map((invitation) => (
                <div
                  key={invitation.id}
                  className="group flex items-center gap-3 rounded-lg border border-dashed border-brand-main-600 bg-brand-main-900/40 px-3.5 py-3 transition-colors hover:bg-brand-main-800/40"
                >
                  <div className="flex size-9 shrink-0 items-center justify-center rounded-full border border-brand-main-500/40 bg-brand-main-700/50">
                    <Icon icon="lucide:mail" className="size-4 text-white/30 light:text-black/30" />
                  </div>

                  <div className="min-w-0 flex-1">
                    <span className="truncate text-sm font-medium text-white/80 light:text-black/80">
                      {invitation.email}
                    </span>
                    <div className="flex items-center gap-2 text-xs text-white/30 light:text-black/30">
                      <span>Invited by {invitation.invitedByEmail || 'Unknown'}</span>
                      <span className="text-white/15 light:text-black/15">·</span>
                      <span>Expires {new Date(invitation.expiresAt).toLocaleDateString()}</span>
                    </div>
                  </div>

                  <Badge
                    variant="outline"
                    className={cn('shrink-0 text-[11px] capitalize', roleBadgeClass(invitation.role))}
                  >
                    {getRoleName(invitation.role)}
                  </Badge>

                  <div className="w-8 shrink-0 text-right">
                    {canManageMembers && (
                      <button
                        onClick={() => handleRevoke(invitation.id)}
                        disabled={revokeMutation.isPending}
                        className="rounded p-1 text-red-400/50 light:text-red-600/50 opacity-0 transition-all hover:bg-red-500/10 hover:text-red-400 light:hover:text-red-600 group-hover:opacity-100 disabled:opacity-50"
                        title="Revoke invitation"
                      >
                        <Icon icon="lucide:x" className="size-3.5" />
                      </button>
                    )}
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
