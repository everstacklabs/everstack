import { useSession, useSignOut } from '@/hooks/auth'
import { ui } from '@everstack/ui'
import { Iconify } from '@everstack/ui/icons'

const { Card, CardHeader, CardTitle, CardDescription, CardContent, Button, Badge } = ui

// Helper to format role from proto enum to display string
function formatRole(role?: number): string {
    switch (role) {
        case 1: return 'owner'
        case 2: return 'admin'
        case 3: return 'member'
        default: return 'member'
    }
}

export function AccountSettingsPage() {
    const { data: session, isLoading } = useSession()
    const signOutMutation = useSignOut()

    if (isLoading) {
        return (
            <div className="flex items-center justify-center h-64">
                <div className="text-zinc-400 light:text-zinc-600">Loading...</div>
            </div>
        )
    }

    if (!session?.authenticated || !session?.user?.user) {
        return (
            <div className="flex items-center justify-center h-64">
                <div className="text-zinc-400 light:text-zinc-600">Not authenticated</div>
            </div>
        )
    }

    // UserWithOrganizations structure: { user: User, organizations: Organization[] }
    const userWithOrgs = session.user
    const userData = userWithOrgs?.user
    const primaryOrg = userWithOrgs?.organizations?.[0]
    const role = formatRole(primaryOrg?.role)

    const handleSignOut = () => {
        signOutMutation.mutate()
    }

    return (
        <div className="flex flex-col space-y-4 py-6 px-60 w-full">
            {/* Profile Card */}
            <Card className=" border-brand-main-600">
                <CardHeader>
                    <CardTitle className="text-white light:text-brand-main-50 flex items-center gap-2">
                        Profile
                    </CardTitle>
                    <CardDescription className="text-brand-main-100">
                        Your personal account information
                    </CardDescription>
                </CardHeader>
                <CardContent className="space-y-4">
                    <div className="flex items-center border-t border-brand-main-500">
                        <div className="grid gap-4 pt-4">
                            <div>
                                <p className="text-sm text-brand-main-100">Name</p>
                                <p className="text-white light:text-brand-main-50 capitalize">{userData?.name}</p>
                            </div>
                            <div>
                                <p className="text-sm text-brand-main-100">Email</p>
                                <p className="text-brand-main-100">{userData?.email}</p>
                            </div>
                        </div>
                    </div>

                    <div className="grid gap-4 pt-4 border-t border-brand-main-500">
                        <div className="flex items-center justify-between">
                            <div>
                                <p className="text-sm text-zinc-400 light:text-zinc-600">Role</p>
                                <p className="text-white light:text-brand-main-50 capitalize">{role}</p>
                            </div>
                            {role === 'owner' && (
                                <Badge className="bg-brand-secondary-500/20 text-brand-secondary-400 border-brand-secondary-500/30">
                                    Owner
                                </Badge>
                            )}
                        </div>
                    </div>
                </CardContent>
            </Card>

            {/* Session Card */}
            <Card className=" border-brand-main-600">
                <CardHeader>
                    <CardTitle className="text-white light:text-brand-main-50 flex items-center gap-2">
                        Session
                    </CardTitle>
                    <CardDescription className="text-brand-main-100">
                        Manage your current session
                    </CardDescription>
                </CardHeader>
                <CardContent>
                    <div className="flex items-center justify-between">
                        <div>
                            <p className="text-white light:text-brand-main-50">Sign out of your account</p>
                            <p className="text-sm text-brand-main-100">
                                You will be redirected to the login page
                            </p>
                        </div>
                        <Button
                            variant="destructive"
                            onClick={handleSignOut}
                            disabled={signOutMutation.isPending}
                            className="bg-red-500/20 text-red-400 light:text-red-600 hover:bg-red-500/30 border-red-500/30"
                        >
                            <Iconify.Icon icon="lucide:log-out" className="mr-2 size-4" />
                            {signOutMutation.isPending ? 'Signing out...' : 'Sign Out'}
                        </Button>
                    </div>
                </CardContent>
            </Card>
        </div>
    )
}
