import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useTraceUser } from '@/hooks/observability/use-users'
import { ui } from '@everstack/ui'
import { Icon } from '@iconify/react'
import { LinkedTracesTable } from '@/components/observability/linked-traces-table'

type ProtoTimestamp = { seconds?: bigint | number; nanos?: number; toDate?: () => Date }

const {
    Button,
    Card,
    CardContent,
    CardHeader,
    CardTitle,
} = ui

export const Route = createFileRoute('/observability/users_/$userId')({
    component: UserDetailPage,
})

function tsToDate(ts: ProtoTimestamp | undefined): Date | null {
    if (!ts) return null
    if (typeof ts.toDate === 'function') return ts.toDate()
    const seconds = typeof ts.seconds === 'bigint' ? Number(ts.seconds) : Number(ts.seconds ?? 0)
    return new Date(seconds * 1000 + Math.floor((ts.nanos ?? 0) / 1_000_000))
}

function UserDetailPage() {
    const { userId } = Route.useParams()
    const navigate = useNavigate()
    const { data, isLoading } = useTraceUser(userId)

    if (isLoading) {
        return (
            <div className="flex flex-col h-full w-full items-center justify-center">
                <div className="text-white/60 light:text-black/60">Loading user details...</div>
            </div>
        )
    }

    const user = data?.user

    if (!user) {
        return (
            <div className="flex flex-col h-full w-full items-center justify-center">
                <div className="text-white/60 light:text-black/60">User not found.</div>
            </div>
        )
    }

    return (
        <div className="flex flex-col h-full w-full overflow-hidden">
            <div className="flex items-center gap-3 px-6 py-3 border-b border-brand-main-800/40">
                <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => navigate({ to: '/observability/users' })}
                    className="text-white/60 light:text-black/60 hover:text-white light:hover:text-brand-main-50"
                >
                    <Icon icon="lucide:arrow-left" className="h-4 w-4 mr-1" />
                    Back
                </Button>
                <div>
                    <span className="text-white light:text-brand-main-50 font-medium">{user.userId}</span>
                    <span className="text-white/40 light:text-black/40 ml-3 text-sm">
                        First seen: {tsToDate(user.firstSeen)?.toLocaleString() ?? '-'} | Last seen: {tsToDate(user.lastSeen)?.toLocaleString() ?? '-'}
                    </span>
                </div>
            </div>

            <div className="flex-1 min-h-0 overflow-y-auto px-6 py-4 space-y-4">
                <div className="grid grid-cols-2 md:grid-cols-5 gap-4">
                    <Card className="border-brand-main-500 rounded-md">
                        <CardContent className="pt-4 pb-4">
                            <div className="text-white/60 light:text-black/60 text-sm">Sessions</div>
                            <div className="text-2xl font-bold text-white light:text-brand-main-50 mt-1">{user.sessionCount}</div>
                        </CardContent>
                    </Card>
                    <Card className="border-brand-main-500 rounded-md">
                        <CardContent className="pt-4 pb-4">
                            <div className="text-white/60 light:text-black/60 text-sm">Traces</div>
                            <div className="text-2xl font-bold text-white light:text-brand-main-50 mt-1">{user.traceCount}</div>
                        </CardContent>
                    </Card>
                    <Card className="border-brand-main-500 rounded-md">
                        <CardContent className="pt-4 pb-4">
                            <div className="text-white/60 light:text-black/60 text-sm">Total Tokens</div>
                            <div className="text-2xl font-bold text-white light:text-brand-main-50 mt-1">{Number(user.totalTokens).toLocaleString()}</div>
                        </CardContent>
                    </Card>
                    <Card className="border-brand-main-500 rounded-md">
                        <CardContent className="pt-4 pb-4">
                            <div className="text-white/60 light:text-black/60 text-sm">Total Cost</div>
                            <div className="text-2xl font-bold text-white light:text-brand-main-50 mt-1">${user.totalCost?.toFixed(4)}</div>
                        </CardContent>
                    </Card>
                    <Card className="border-brand-main-500 rounded-md">
                        <CardContent className="pt-4 pb-4">
                            <div className="text-white/60 light:text-black/60 text-sm">Error Rate</div>
                            <div className={`text-2xl font-bold mt-1 ${user.errorRate > 0.05 ? 'text-red-400 light:text-red-600' : 'text-white light:text-brand-main-50'}`}>
                                {(user.errorRate * 100).toFixed(1)}%
                            </div>
                        </CardContent>
                    </Card>
                </div>

                <Card className="border-brand-main-500 rounded-md">
                    <CardHeader>
                        <CardTitle className="text-white light:text-brand-main-50 text-base">Traces by this user</CardTitle>
                    </CardHeader>
                    <CardContent className="p-0">
                        <LinkedTracesTable userId={userId} />
                    </CardContent>
                </Card>
            </div>
        </div>
    )
}
