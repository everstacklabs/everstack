import { cn } from '@/lib/utils'
import { Iconify } from '@everstack/ui/icons'
import { ui } from '@everstack/ui'

const { Card, CardContent, CardHeader, CardTitle, Badge } = ui

type PlannedAgent = {
    role: string
    task: string
    model?: string
    tools?: string[]
    depends_on?: string[]
}

type PlanCardProps = {
    strategy: string
    subAgents: PlannedAgent[]
    reasoning: string
    durationMs?: number
    className?: string
}

const STRATEGY_LABELS: Record<string, { label: string; icon: string; color: string }> = {
    single: { label: 'Single Agent', icon: 'lucide:user', color: 'text-neutral-500' },
    parallel: { label: 'Parallel', icon: 'lucide:git-branch', color: 'text-blue-500' },
    sequential: { label: 'Sequential', icon: 'lucide:arrow-down', color: 'text-amber-500' },
    pipeline: { label: 'Pipeline', icon: 'lucide:workflow', color: 'text-purple-500' },
}

export function PlanCard({ strategy, subAgents, reasoning, durationMs, className }: PlanCardProps) {
    const strategyInfo = STRATEGY_LABELS[strategy] ?? STRATEGY_LABELS.single

    return (
        <Card className={cn('border-dashed', className)}>
            <CardHeader className="pb-2">
                <div className="flex items-center justify-between">
                    <CardTitle className="flex items-center gap-2 text-sm">
                        <Iconify.Icon icon="lucide:brain" className="h-4 w-4 text-purple-500" />
                        Task Plan
                    </CardTitle>
                    <div className="flex items-center gap-2">
                        <Badge variant="outline" className={cn('text-xs', strategyInfo.color)}>
                            <Iconify.Icon icon={strategyInfo.icon} className="mr-1 h-3 w-3" />
                            {strategyInfo.label}
                        </Badge>
                        {durationMs !== undefined && (
                            <span className="text-xs text-muted-foreground">{durationMs}ms</span>
                        )}
                    </div>
                </div>
            </CardHeader>
            <CardContent className="space-y-3">
                {reasoning && (
                    <p className="text-xs text-muted-foreground italic">{reasoning}</p>
                )}

                {subAgents.length > 0 && (
                    <div className="space-y-2">
                        {subAgents.map((agent, i) => (
                            <div
                                key={agent.role}
                                className="flex items-start gap-2 rounded-md border bg-muted/30 p-2"
                            >
                                <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-purple-100 text-xs font-medium text-purple-700">
                                    {i + 1}
                                </span>
                                <div className="min-w-0 flex-1">
                                    <div className="flex items-center gap-1.5">
                                        <span className="text-xs font-semibold">{agent.role}</span>
                                        {agent.model && (
                                            <span className="text-xs text-muted-foreground">({agent.model})</span>
                                        )}
                                    </div>
                                    <p className="text-xs text-muted-foreground line-clamp-2">{agent.task}</p>
                                    {agent.depends_on && agent.depends_on.length > 0 && (
                                        <div className="mt-1 flex items-center gap-1 text-xs text-muted-foreground">
                                            <Iconify.Icon icon="lucide:arrow-right" className="h-3 w-3" />
                                            Depends on: {agent.depends_on.join(', ')}
                                        </div>
                                    )}
                                </div>
                            </div>
                        ))}
                    </div>
                )}
            </CardContent>
        </Card>
    )
}
