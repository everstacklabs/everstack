import { useMemo, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import {
  useCurrentWorkspace,
  useWorkspaceMembers,
  useRemoveWorkspaceMember,
  getWsRoleName,
  getRoleName,
  useSession,
  usePermissions,
  type WorkspaceMemberInfo,
} from '@/hooks/auth'
import { AddWorkspaceMemberDialog } from '@/components/settings/team/add-workspace-member-dialog'
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
  Button,
} = ui

export const Route = createFileRoute('/settings/members')({
  component: MembersSettingsPage,
})

function roleBadgeClass(role: string) {
  switch (role.toLowerCase()) {
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

function displayRole(member: WorkspaceMemberInfo): string {
  if (member.accessSource === 'implicit') {
    return getRoleName(member.orgRole)
  }
  return getWsRoleName(member.role)
}

function MembersSettingsPage() {
  const { data: workspace, isLoading: loadingWorkspace } = useCurrentWorkspace()
  const workspaceId = workspace?.id
  const { data: members, isLoading: loadingMembers } = useWorkspaceMembers(workspaceId)
  const { data: session } = useSession()
  const removeMutation = useRemoveWorkspaceMember(workspaceId)
  const { can } = usePermissions()
  const canManage = can('workspace:manage')
  const [showAddDialog, setShowAddDialog] = useState(false)

  const currentUserId = session?.user?.user?.id
  const isLoading = loadingWorkspace || loadingMembers

  const allMembers = members ?? []
  const implicitMembers = useMemo(
    () => allMembers.filter((m) => m.accessSource === 'implicit'),
    [allMembers],
  )
  const explicitMembers = useMemo(
    () => allMembers.filter((m) => m.accessSource === 'explicit'),
    [allMembers],
  )
  const adminCount = useMemo(
    () => allMembers.filter((m) => {
      const role = displayRole(m).toLowerCase()
      return role === 'owner' || role === 'admin'
    }).length,
    [allMembers],
  )

  const handleRemove = async (userId: string) => {
    if (!confirm('Are you sure you want to remove this member from the instance?')) return
    await removeMutation.mutateAsync({ userId })
  }

  if (isLoading) {
    return (
      <div className="flex h-full w-full items-center justify-center text-white/70 light:text-black/70">
        <div className="flex items-center gap-2 text-sm">
          <Icon icon="lucide:loader-2" className="size-4 animate-spin" />
          Loading members...
        </div>
      </div>
    )
  }

  return (
    <div className="flex h-full w-full flex-col">
      <div className="flex-1 space-y-4 overflow-y-auto p-8 px-60 mx-auto w-full">

        {/* ── Metrics row ── */}
        <div className="grid grid-cols-2 md:grid-cols-3 gap-2">
          <Card className="border-brand-main-600 bg-brand-main-900/50 rounded">
            <CardContent>
              <div className="flex items-center justify-between gap-2">
                <div className="flex items-center gap-2 min-w-0">
                  <div className="p-1.5 rounded bg-brand-secondary-600/20 border border-brand-secondary-500/30">
                    <Icon icon="lucide:users" className="size-4 text-brand-secondary-300" />
                  </div>
                  <div className="text-xs text-white light:text-brand-main-50 uppercase tracking-wide">Members</div>
                </div>
                <div className="text-sm font-semibold text-white light:text-brand-main-50">{allMembers.length}</div>
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
                    <Icon icon="lucide:building" className="size-4 text-brand-secondary-300" />
                  </div>
                  <div className="text-xs text-white light:text-brand-main-50 uppercase tracking-wide">Via Org</div>
                </div>
                <div className="text-sm font-semibold text-white light:text-brand-main-50">{implicitMembers.length}</div>
              </div>
            </CardContent>
          </Card>
        </div>

        {/* ── Implicit members (org admins/owners) ── */}
        {implicitMembers.length > 0 && (
          <div className="space-y-1.5">
            <div className="flex items-center justify-between px-1 pb-1">
              <h3 className="text-[13px] font-medium text-white/60 light:text-black/60">
                Organization Admins ({implicitMembers.length})
              </h3>
              <span className="text-[11px] text-white/30 light:text-black/30">
                Managed at org level
              </span>
            </div>
            <div className="space-y-1.5">
              {implicitMembers.map((member) => (
                <MemberRow
                  key={member.userId}
                  member={member}
                  currentUserId={currentUserId}
                  onRemove={handleRemove}
                  removePending={removeMutation.isPending}
                  canManage={canManage}
                />
              ))}
            </div>
          </div>
        )}

        {/* ── Explicit members (instance-level) ── */}
        <div className="space-y-1.5">
          <div className="flex items-center justify-between px-1 pb-1">
            <h3 className="text-[13px] font-medium text-white/60 light:text-black/60">
              Instance Members ({explicitMembers.length})
            </h3>
            {workspaceId && canManage && (
              <Button
                variant="outline"
                size="sm"
                className="h-7 gap-1.5 text-xs"
                onClick={() => setShowAddDialog(true)}
              >
                <Icon icon="lucide:user-plus" className="size-3.5" />
                Add Member
              </Button>
            )}
          </div>

          {explicitMembers.length === 0 ? (
            <div className="rounded-lg border border-brand-main-600 bg-brand-main-800/50 p-8 text-center">
              <Icon icon="lucide:users" className="mx-auto size-8 text-white/15 light:text-black/15" />
              <p className="mt-2 text-sm text-white/40 light:text-black/40">
                No instance-specific members. Add org members to grant them access.
              </p>
            </div>
          ) : (
            <div className="space-y-1.5">
              {explicitMembers.map((member) => (
                <MemberRow
                  key={member.userId}
                  member={member}
                  currentUserId={currentUserId}
                  onRemove={handleRemove}
                  removePending={removeMutation.isPending}
                  canManage={canManage}
                />
              ))}
            </div>
          )}
        </div>
      </div>

      {workspaceId && (
        <AddWorkspaceMemberDialog
          open={showAddDialog}
          onOpenChange={setShowAddDialog}
          workspaceId={workspaceId}
        />
      )}
    </div>
  )
}

function MemberRow({
  member,
  currentUserId,
  onRemove,
  removePending,
  canManage,
}: {
  member: WorkspaceMemberInfo
  currentUserId: string | undefined
  onRemove: (userId: string) => void
  removePending: boolean
  canManage: boolean
}) {
  const isImplicit = member.accessSource === 'implicit'
  const isSelf = member.userId === currentUserId
  const canRemove = canManage && !isImplicit && !isSelf
  const role = displayRole(member)

  return (
    <div
      className={cn(
        'group flex items-center gap-3 rounded-lg border px-3.5 py-3 transition-colors',
        isImplicit
          ? 'border-brand-main-600/60 bg-brand-main-900/30 hover:bg-brand-main-800/40'
          : 'border-brand-main-600 bg-brand-main-800/50 hover:bg-brand-main-800/70',
      )}
    >
      <Avatar className="size-9 shrink-0 border border-brand-main-500/50">
        <AvatarImage src={member.avatarUrl} />
        <AvatarFallback className="bg-brand-main-700 text-sm text-brand-main-100">
          {(member.name || member.email).charAt(0).toUpperCase()}
        </AvatarFallback>
      </Avatar>

      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-sm font-medium text-white light:text-brand-main-50">
            {member.name || member.email}
          </span>
          {isSelf && (
            <span className="text-[10px] text-white/30 light:text-black/30">you</span>
          )}
        </div>
        <span className="truncate text-xs text-white/40 light:text-black/40">
          {member.email}
        </span>
      </div>

      <div className="flex items-center gap-2 shrink-0">
        <Badge
          variant="outline"
          className={cn('text-[11px] capitalize', roleBadgeClass(role))}
        >
          {role}
        </Badge>

        {isImplicit && (
          <Badge
            variant="outline"
            className="text-[10px] border-brand-main-500/30 bg-brand-main-700/30 text-brand-main-300"
          >
            via Org
          </Badge>
        )}
      </div>

      <span className="hidden shrink-0 text-xs text-white/30 light:text-black/30 sm:block">
        {new Date(member.createdAt).toLocaleDateString()}
      </span>

      <div className="w-8 shrink-0 text-right">
        {canRemove && (
          <button
            onClick={() => onRemove(member.userId)}
            disabled={removePending}
            className="rounded p-1 text-red-400/50 light:text-red-600/50 opacity-0 transition-all hover:bg-red-500/10 hover:text-red-400 light:hover:text-red-600 group-hover:opacity-100 disabled:opacity-50"
            title="Remove from instance"
          >
            <Icon icon="lucide:user-minus" className="size-3.5" />
          </button>
        )}
      </div>
    </div>
  )
}
