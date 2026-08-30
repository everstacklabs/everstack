import { useState } from 'react'
import { ui } from '@everstack/ui'
import { Switch } from '@everstack/ui/components'
import { useDeployAgent } from '@/hooks/deployments/use-agent-deployments'

const { Sheet, SheetContent, SheetHeader, SheetTitle, SheetBody, Button, Input, Label, Textarea } = ui

interface DeployAgentSheetProps {
    agentId: string
    agentName: string
    open: boolean
    onOpenChange: (open: boolean) => void
}

export function DeployAgentDialog({ agentId, agentName, open, onOpenChange }: DeployAgentSheetProps) {
    const [description, setDescription] = useState('')
    const [changelog, setChangelog] = useState('')
    const [rateLimitRpm, setRateLimitRpm] = useState('')
    const [maxConcurrent, setMaxConcurrent] = useState('10')
    const [maxTurns, setMaxTurns] = useState('')
    const [sessionTimeout, setSessionTimeout] = useState('300')
    const [disableSessionTracking, setDisableSessionTracking] = useState(false)

    const deployMutation = useDeployAgent()

    const handleDeploy = () => {
        deployMutation.mutate(
            {
                agentId,
                description,
                changelog,
                rateLimitRpm: rateLimitRpm ? parseInt(rateLimitRpm) : undefined,
                maxConcurrentSessions: maxConcurrent ? parseInt(maxConcurrent) : undefined,
                maxTurnsPerSession: maxTurns ? parseInt(maxTurns) : undefined,
                sessionTimeoutSeconds: sessionTimeout ? parseInt(sessionTimeout) : undefined,
                disableSessionTracking,
            },
            {
                onSuccess: () => {
                    onOpenChange(false)
                    setDescription('')
                    setChangelog('')
                },
            }
        )
    }

    return (
        <Sheet open={open} onOpenChange={onOpenChange}>
            <SheetContent side="right" className="w-full sm:max-w-[480px] max-h-[100vh] overflow-y-auto scrollbar-macos">
                <SheetHeader>
                    <SheetTitle>Deploy {agentName}</SheetTitle>
                </SheetHeader>

                <SheetBody className="py-4">
                    <div className="space-y-4">
                        <p className="text-sm text-brand-main-100">
                            This will create a new versioned deployment with a snapshot of the current agent configuration.
                            The deployment will be accessible via API with its own authentication keys.
                        </p>

                        <div className="space-y-2">
                            <Label>Description</Label>
                            <Input
                                value={description}
                                onChange={(e) => setDescription(e.target.value)}
                                placeholder="What this deployment is for..."
                            />
                        </div>

                        <div className="space-y-2">
                            <Label>Changelog</Label>
                            <Textarea
                                value={changelog}
                                onChange={(e) => setChangelog(e.target.value)}
                                placeholder="What changed since last version..."
                                className="min-h-[60px]"
                            />
                        </div>

                        <div className="grid grid-cols-2 gap-3">
                            <div className="space-y-1">
                                <Label className="text-xs">Rate Limit (RPM)</Label>
                                <Input
                                    type="number"
                                    value={rateLimitRpm}
                                    onChange={(e) => setRateLimitRpm(e.target.value)}
                                    placeholder="Unlimited"
                                />
                            </div>
                            <div className="space-y-1">
                                <Label className="text-xs">Max Concurrent</Label>
                                <Input
                                    type="number"
                                    value={maxConcurrent}
                                    onChange={(e) => setMaxConcurrent(e.target.value)}
                                />
                            </div>
                            <div className="space-y-1">
                                <Label className="text-xs">Max Turns / Session</Label>
                                <Input
                                    type="number"
                                    value={maxTurns}
                                    onChange={(e) => setMaxTurns(e.target.value)}
                                    placeholder="Agent default"
                                />
                            </div>
                            <div className="space-y-1">
                                <Label className="text-xs">Session Timeout (s)</Label>
                                <Input
                                    type="number"
                                    value={sessionTimeout}
                                    onChange={(e) => setSessionTimeout(e.target.value)}
                                />
                            </div>
                        </div>

                        <div className="flex items-center justify-between rounded-md border border-brand-main-700/30 bg-brand-main-800/20 p-3">
                            <div className="space-y-0.5">
                                <Label className="text-sm">Private sessions</Label>
                                <div className="text-xs text-brand-main-200">
                                    When enabled, API invocations won't appear in the agent's sessions list
                                </div>
                            </div>
                            <Switch
                                checked={disableSessionTracking}
                                onCheckedChange={setDisableSessionTracking}
                            />
                        </div>

                        <div className="flex justify-end gap-3 pt-2 border-t border-brand-main-700/60">
                            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
                                Cancel
                            </Button>
                            <Button onClick={handleDeploy} disabled={deployMutation.isPending}>
                                {deployMutation.isPending ? 'Deploying...' : 'Deploy'}
                            </Button>
                        </div>
                    </div>
                </SheetBody>
            </SheetContent>
        </Sheet>
    )
}
