import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useTraceSession } from '@/hooks/observability/use-sessions'
import { ui } from '@everstack/ui'
import { Icon } from '@iconify/react'
import { LinkedTracesTable } from '@/components/observability/linked-traces-table'

const {
    Button,
    Card,
    CardHeader,
    CardTitle,
    CardDescription,
    CardContent,
} = ui

export const Route = createFileRoute('/observability/sessions_/$sessionId')({
    component: SessionDetailPage,
})

function formatDuration(ns: number): string {
    const ms = ns / 1_000_000
    if (ms < 1000) return `${ms.toFixed(0)}ms`
    if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`
    return `${(ms / 60_000).toFixed(1)}m`
}

function SessionDetailPage() {
    const { sessionId } = Route.useParams()
    const navigate = useNavigate()
    const { data, isLoading } = useTraceSession(sessionId)

    if (isLoading) {
        return (
            <div className="flex flex-col h-full w-full items-center justify-center">
                <div className="text-white/60 light:text-black/60">Loading session...</div>
            </div>
        )
    }

    const session = data?.session

    if (!session) {
        return (
            <div className="flex flex-col h-full w-full items-center justify-center">
                <div className="text-white/60 light:text-black/60">Session not found.</div>
            </div>
        )
    }

    const totalTokens = Number(session.totalInputTokens ?? 0) + Number(session.totalOutputTokens ?? 0)

    return (
        <div className="flex flex-col h-full w-full overflow-hidden">
            <div className="flex items-center gap-3 px-6 py-3 border-b border-brand-main-800/40">
                <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => navigate({ to: '/observability/sessions' })}
                    className="text-white/60 light:text-black/60 hover:text-white light:hover:text-brand-main-50"
                >
                    <Icon icon="lucide:arrow-left" className="h-4 w-4 mr-1" />
                    Back
                </Button>
                <div>
                    <span className="text-white light:text-brand-main-50 font-mono text-sm">{session.sessionId}</span>
                    {session.userId && (
                        <span className="text-white/40 light:text-black/40 ml-3 text-sm">User: {session.userId}</span>
                    )}
                </div>
            </div>

            <div className="flex-1 min-h-0 overflow-y-auto px-6 py-4 space-y-4">
                <div className="grid grid-cols-2 md:grid-cols-5 gap-4">
                    <Card className="border-brand-main-500 rounded-md">
                        <CardContent className="pt-4 pb-4">
                            <div className="text-white/60 light:text-black/60 text-sm">Traces</div>
                            <div className="text-2xl font-bold text-white light:text-brand-main-50 mt-1">{session.traceCount}</div>
                        </CardContent>
                    </Card>
                    <Card className="border-brand-main-500 rounded-md">
                        <CardContent className="pt-4 pb-4">
                            <div className="text-white/60 light:text-black/60 text-sm">Duration</div>
                            <div className="text-2xl font-bold text-white light:text-brand-main-50 mt-1">{formatDuration(Number(session.totalDurationNs))}</div>
                        </CardContent>
                    </Card>
                    <Card className="border-brand-main-500 rounded-md">
                        <CardContent className="pt-4 pb-4">
                            <div className="text-white/60 light:text-black/60 text-sm">Total Tokens</div>
                            <div className="text-2xl font-bold text-white light:text-brand-main-50 mt-1">{totalTokens.toLocaleString()}</div>
                        </CardContent>
                    </Card>
                    <Card className="border-brand-main-500 rounded-md">
                        <CardContent className="pt-4 pb-4">
                            <div className="text-white/60 light:text-black/60 text-sm">Total Cost</div>
                            <div className="text-2xl font-bold text-white light:text-brand-main-50 mt-1">${session.totalCost?.toFixed(4) ?? '0.0000'}</div>
                        </CardContent>
                    </Card>
                    <Card className="border-brand-main-500 rounded-md">
                        <CardContent className="pt-4 pb-4">
                            <div className="text-white/60 light:text-black/60 text-sm">Errors</div>
                            <div className={`text-2xl font-bold mt-1 ${(session.errorCount ?? 0) > 0 ? 'text-red-400 light:text-red-600' : 'text-white light:text-brand-main-50'}`}>
                                {session.errorCount ?? 0}
                            </div>
                        </CardContent>
                    </Card>
                </div>

                <Card className="border-brand-main-500 rounded-md">
                    <CardHeader>
                        <CardTitle className="text-white light:text-brand-main-50 text-lg">Session Details</CardTitle>
                        <CardDescription className="text-white/40 light:text-black/40">
                            Models: {session.models?.join(', ') || 'N/A'} | Environment: {session.environment || 'N/A'}
                        </CardDescription>
                    </CardHeader>
                </Card>

                <Card className="border-brand-main-500 rounded-md">
                    <CardHeader>
                        <CardTitle className="text-white light:text-brand-main-50 text-base">Traces in this session</CardTitle>
                    </CardHeader>
                    <CardContent className="p-0">
                        <LinkedTracesTable sessionId={sessionId} />
                    </CardContent>
                </Card>
            </div>
        </div>
    )
}
