import { useState } from 'react'
import { useCreateTrigger } from '@/hooks/deployments/use-agent-triggers'
import { useAgents } from '@/hooks/deployments/use-agents'
import { ui } from '@everstack/ui'

const { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter, Button, Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Textarea } = ui

type Props = {
    agentId: string
    open: boolean
    onOpenChange: (open: boolean) => void
}

export function CreateTriggerDialog({ agentId, open, onOpenChange }: Props) {
    const createTrigger = useCreateTrigger()
    const { data: agents = [] } = useAgents()
    const [step, setStep] = useState<'type' | 'config' | 'limits'>('type')
    const [triggerType, setTriggerType] = useState<string>('')
    const [name, setName] = useState('')
    const [cronExpression, setCronExpression] = useState('')
    const [cronTimezone, setCronTimezone] = useState('UTC')
    const [eventSourceAgentId, setEventSourceAgentId] = useState('')
    const [eventType, setEventType] = useState('session.end')
    const [inputTemplate, setInputTemplate] = useState('')
    const [maxRetries, setMaxRetries] = useState(0)
    const [timeoutSeconds, setTimeoutSeconds] = useState(300)
    const [maxConcurrent, setMaxConcurrent] = useState(1)

    const [webhookResult, setWebhookResult] = useState<{ secret: string; url: string } | null>(null)

    const resetForm = () => {
        setStep('type')
        setTriggerType('')
        setName('')
        setCronExpression('')
        setCronTimezone('UTC')
        setEventSourceAgentId('')
        setEventType('session.end')
        setInputTemplate('')
        setMaxRetries(0)
        setTimeoutSeconds(300)
        setMaxConcurrent(1)
        setWebhookResult(null)
    }

    const handleCreate = async () => {
        const result = await createTrigger.mutateAsync({
            agentId,
            name,
            triggerType,
            cronExpression: triggerType === 'cron' ? cronExpression : undefined,
            cronTimezone: triggerType === 'cron' ? cronTimezone : undefined,
            eventSourceAgentId: triggerType === 'event' ? eventSourceAgentId : undefined,
            eventType: triggerType === 'event' ? eventType : undefined,
            inputTemplate: inputTemplate || undefined,
            maxRetries,
            timeoutSeconds,
            maxConcurrent,
        })

        if (triggerType === 'webhook' && result.webhookSecret) {
            setWebhookResult({ secret: result.webhookSecret, url: result.webhookUrl })
        } else {
            resetForm()
            onOpenChange(false)
        }
    }

    const handleClose = () => {
        resetForm()
        onOpenChange(false)
    }

    const configurationComplete =
        triggerType === 'cron'
            ? cronExpression.trim().length > 0 && cronTimezone.trim().length > 0
            : triggerType === 'event'
              ? eventSourceAgentId.length > 0 && eventType.length > 0
              : triggerType === 'webhook'

    return (
        <Dialog open={open} onOpenChange={handleClose}>
            <DialogContent className="max-w-lg">
                <DialogHeader>
                    <DialogTitle>Create Trigger</DialogTitle>
                    <DialogDescription>
                        {webhookResult
                            ? 'Webhook created. Save the secret — it will not be shown again.'
                            : 'Configure an automated trigger for this agent.'}
                    </DialogDescription>
                </DialogHeader>

                {webhookResult ? (
                    <div className="space-y-4">
                        <div className="space-y-2">
                            <Label>Webhook URL</Label>
                            <Input readOnly value={webhookResult.url} className="font-mono text-xs" />
                        </div>
                        <div className="space-y-2">
                            <Label>Webhook Secret</Label>
                            <Input readOnly value={webhookResult.secret} className="font-mono text-xs" />
                            <p className="text-[11px] text-yellow-400 light:text-yellow-700">
                                Copy this secret now. It will not be shown again.
                            </p>
                        </div>
                        <DialogFooter>
                            <Button onClick={handleClose}>Done</Button>
                        </DialogFooter>
                    </div>
                ) : step === 'type' ? (
                    <div className="space-y-4">
                        <div className="space-y-2">
                            <Label>Name</Label>
                            <Input
                                value={name}
                                onChange={(e) => setName(e.target.value)}
                                placeholder="e.g., Daily Report, GitHub Webhook, On Agent A Complete"
                            />
                        </div>
                        <div className="space-y-2">
                            <Label>Trigger Type</Label>
                            <div className="grid grid-cols-3 gap-2">
                                {[
                                    { type: 'cron', label: 'Cron', desc: 'Run on a schedule' },
                                    { type: 'webhook', label: 'Webhook', desc: 'HTTP POST endpoint' },
                                    { type: 'event', label: 'Event', desc: 'On agent completion' },
                                ].map(({ type, label, desc }) => (
                                    <button
                                        key={type}
                                        className={`p-3 rounded-md border text-left transition-colors ${
                                            triggerType === type
                                                ? 'border-brand-secondary-500 bg-brand-secondary-500/10'
                                                : 'border-brand-main-600 hover:border-brand-main-400'
                                        }`}
                                        onClick={() => setTriggerType(type)}
                                    >
                                        <div className="text-sm font-medium text-brand-secondary-100">{label}</div>
                                        <div className="text-[11px] text-white/40 light:text-black/40">{desc}</div>
                                    </button>
                                ))}
                            </div>
                        </div>
                        <DialogFooter>
                            <Button variant="outline" onClick={handleClose}>Cancel</Button>
                            <Button
                                disabled={!name.trim() || !triggerType}
                                onClick={() => setStep('config')}
                            >
                                Next
                            </Button>
                        </DialogFooter>
                    </div>
                ) : step === 'config' ? (
                    <div className="space-y-4">
                        {triggerType === 'cron' && (
                            <>
                                <div className="space-y-2">
                                    <Label>Cron Expression</Label>
                                    <Input
                                        value={cronExpression}
                                        onChange={(e) => setCronExpression(e.target.value)}
                                        placeholder="*/5 * * * *"
                                        className="font-mono"
                                    />
                                    <p className="text-[11px] text-white/40 light:text-black/40">Standard cron format: minute hour day month weekday</p>
                                </div>
                                <div className="space-y-2">
                                    <Label>Timezone</Label>
                                    <Input
                                        value={cronTimezone}
                                        onChange={(e) => setCronTimezone(e.target.value)}
                                        placeholder="UTC"
                                    />
                                </div>
                            </>
                        )}

                        {triggerType === 'webhook' && (
                            <div className="text-sm text-white/60 light:text-black/60">
                                A unique webhook URL and HMAC secret will be generated automatically.
                            </div>
                        )}

                        {triggerType === 'event' && (
                            <>
                                <div className="space-y-2">
                                    <Label>Source Agent</Label>
                                    <Select value={eventSourceAgentId} onValueChange={setEventSourceAgentId}>
                                        <SelectTrigger>
                                            <SelectValue placeholder="Select agent..." />
                                        </SelectTrigger>
                                        <SelectContent>
                                            {agents.filter(a => a.id !== agentId).map((a) => (
                                                <SelectItem key={a.id} value={a.id}>{a.name}</SelectItem>
                                            ))}
                                        </SelectContent>
                                    </Select>
                                </div>
                                <div className="space-y-2">
                                    <Label>Event Type</Label>
                                    <Select value={eventType} onValueChange={setEventType}>
                                        <SelectTrigger>
                                            <SelectValue />
                                        </SelectTrigger>
                                        <SelectContent>
                                            <SelectItem value="session.end">Session End</SelectItem>
                                            <SelectItem value="session.error">Session Error</SelectItem>
                                        </SelectContent>
                                    </Select>
                                </div>
                            </>
                        )}

                        <div className="space-y-2">
                            <Label>Input Template (optional)</Label>
                            <Textarea
                                value={inputTemplate}
                                onChange={(e) => setInputTemplate(e.target.value)}
                                placeholder={'Go template for agent input. Available: {{.trigger.name}}, {{.payload}}'}
                                rows={3}
                                className="font-mono text-xs"
                            />
                        </div>

                        <DialogFooter>
                            <Button variant="outline" onClick={() => setStep('type')}>Back</Button>
                            <Button onClick={() => setStep('limits')} disabled={!configurationComplete}>Next</Button>
                        </DialogFooter>
                    </div>
                ) : (
                    <div className="space-y-4">
                        <div className="grid grid-cols-3 gap-3">
                            <div className="space-y-2">
                                <Label>Max Retries</Label>
                                <Input type="number" value={maxRetries} onChange={(e) => setMaxRetries(Number(e.target.value))} min={0} max={10} />
                            </div>
                            <div className="space-y-2">
                                <Label>Timeout (s)</Label>
                                <Input type="number" value={timeoutSeconds} onChange={(e) => setTimeoutSeconds(Number(e.target.value))} min={30} max={3600} />
                            </div>
                            <div className="space-y-2">
                                <Label>Max Concurrent</Label>
                                <Input type="number" value={maxConcurrent} onChange={(e) => setMaxConcurrent(Number(e.target.value))} min={1} max={50} />
                            </div>
                        </div>

                        <DialogFooter>
                            <Button variant="outline" onClick={() => setStep('config')}>Back</Button>
                            <Button
                                onClick={handleCreate}
                                disabled={createTrigger.isPending}
                            >
                                {createTrigger.isPending ? 'Creating...' : 'Create Trigger'}
                            </Button>
                        </DialogFooter>
                    </div>
                )}
            </DialogContent>
        </Dialog>
    )
}
