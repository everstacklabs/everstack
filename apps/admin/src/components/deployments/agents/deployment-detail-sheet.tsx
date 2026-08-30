import { useState } from 'react'
import { Check, Copy } from 'lucide-react'
import { ui } from '@everstack/ui'
import { useDeployment, useUpdateDeployment, useDeploymentInvocations } from '@/hooks/deployments/use-agent-deployments'
import { DeploymentKeysSection } from './deployment-keys-section'
import { formatTimestamp } from '@everstack/utils/functions/index'

const {
    Sheet,
    SheetContent,
    SheetHeader,
    SheetTitle,
    SheetBody,
    Button,
    Tabs,
    TabsContent,
    TabsList,
    TabsTrigger,
    CodeBlock,
    CodeBlockBody,
    CodeBlockContent,
    CodeBlockCopyButton,
    CodeBlockItem,
} = ui

type UsageSnippetLanguage = 'bash' | 'javascript' | 'python'

interface DeploymentDetailSheetProps {
    deploymentId: string
    open: boolean
    onOpenChange: (open: boolean) => void
}

export function DeploymentDetailSheet({ deploymentId, open, onOpenChange }: DeploymentDetailSheetProps) {
    const { data: deployment } = useDeployment(deploymentId)
    const { data: invocationData } = useDeploymentInvocations(deploymentId)
    const updateMutation = useUpdateDeployment()
    const [copiedId, setCopiedId] = useState<string | null>(null)

    if (!deployment) return null

    const baseUrl = window.location.origin
    const invokeUrl = `${baseUrl}/v1/deploy/${deployment.agentId}/invoke`
    const streamUrl = `${baseUrl}/v1/deploy/${deployment.agentId}/stream`
    const curlExample = `curl -X POST ${invokeUrl} \\
  -H "Authorization: Bearer <YOUR_KEY>" \\
  -H "Content-Type: application/json" \\
  -d '{"message": "Hello"}'`
    const javascriptExample = `// SDK helper coming soon. Use HTTP invoke endpoint for now.
const response = await fetch('${invokeUrl}', {
  method: 'POST',
  headers: {
    'Authorization': 'Bearer <YOUR_KEY>',
    'Content-Type': 'application/json',
  },
  body: JSON.stringify({ message: 'Hello' }),
})

const data = await response.json()
console.log(data)`
    const pythonExample = `# SDK helper coming soon. Use HTTP invoke endpoint for now.
import requests

response = requests.post(
    '${invokeUrl}',
    headers={
        'Authorization': 'Bearer <YOUR_KEY>',
        'Content-Type': 'application/json',
    },
    json={'message': 'Hello'},
)

print(response.json())`

    const handleStatusChange = (status: string) => {
        updateMutation.mutate({ id: deployment.id, status })
    }

    const handleCopy = async (text: string, id: string) => {
        try {
            await navigator.clipboard.writeText(text)
            setCopiedId(id)
            window.setTimeout(() => {
                setCopiedId((current) => (current === id ? null : current))
            }, 2000)
        } catch {
            setCopiedId(null)
        }
    }

    return (
        <Sheet open={open} onOpenChange={onOpenChange}>
            <SheetContent side="right" className="w-full sm:max-w-[620px] max-h-[100vh] overflow-y-auto scrollbar-macos">
                <SheetHeader>
                    <SheetTitle className="flex items-center gap-2">
                        {deployment.name}
                        <StatusBadge status={deployment.status} />
                    </SheetTitle>
                </SheetHeader>

                <SheetBody className="py-4">
                    <Tabs defaultValue="details" className="space-y-4">
                        <div className="flex items-center justify-between gap-3">
                            <TabsList className="w-fit bg-brand-main-800/50 border border-brand-main-600 rounded p-1 h-auto gap-1">
                                <TabsTrigger className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white transition-colors py-1 light:hover:text-brand-main-50" value="details">Details</TabsTrigger>
                                <TabsTrigger className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white transition-colors py-1 light:hover:text-brand-main-50" value="usage">Usage</TabsTrigger>
                                <TabsTrigger className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white transition-colors py-1 light:hover:text-brand-main-50" value="invocations">Invocations</TabsTrigger>
                            </TabsList>
                            <div className="flex flex-wrap items-center justify-end gap-2">
                                {deployment.status === 'active' && (
                                    <Button
                                        variant="muted"
                                        onClick={() => handleStatusChange('paused')}
                                        className="border border-amber-500/40 text-amber-300 hover:bg-amber-500/10 hover:text-amber-200"
                                    >
                                        Pause
                                    </Button>
                                )}
                                {deployment.status === 'paused' && (
                                    <Button
                                        variant="secondary"
                                        onClick={() => handleStatusChange('active')}
                                        className="border border-green-500/40 bg-green-500/20 text-green-200 hover:bg-green-500/30"
                                    >
                                        Resume
                                    </Button>
                                )}
                                {deployment.status !== 'retired' && (
                                    <Button
                                        variant="destructive"
                                        onClick={() => handleStatusChange('retired')}
                                    >
                                        Retire
                                    </Button>
                                )}
                            </div>
                        </div>

                        <TabsContent value="details" className="mt-0 space-y-4">
                            <div className="rounded-md border border-brand-main-700/30 bg-brand-main-800/20 p-3 space-y-3">
                                <div className="text-[11px] text-white/45 uppercase tracking-wider light:text-black/45">Details</div>
                                <div className="grid grid-cols-2 gap-3">
                                    <InfoField label="Version" value={`v${deployment.version}`} />
                                    <InfoField label="Deployed By" value={deployment.deployedBy || '-'} />
                                    <InfoField label="Created" value={formatTimestamp(deployment.createdAt)} />
                                    <InfoField label="Updated" value={formatTimestamp(deployment.updatedAt)} />
                                    <InfoField label="Rate Limit" value={deployment.rateLimitRpm ? `${deployment.rateLimitRpm} RPM` : 'Unlimited'} />
                                    <InfoField label="Max Concurrent" value={String(deployment.maxConcurrentSessions)} />
                                    <InfoField label="Max Turns" value={deployment.maxTurnsPerSession ? String(deployment.maxTurnsPerSession) : 'Agent default'} />
                                    <InfoField label="Session Timeout" value={`${deployment.sessionTimeoutSeconds}s`} />
                                </div>

                                {deployment.description && (
                                    <div className="space-y-1">
                                        <div className="text-[11px] text-white/45 uppercase tracking-wider light:text-black/45">Description</div>
                                        <div className="text-sm text-brand-main-100">{deployment.description}</div>
                                    </div>
                                )}

                                {deployment.changelog && (
                                    <div className="space-y-1">
                                        <div className="text-[11px] text-white/45 uppercase tracking-wider light:text-black/45">Changelog</div>
                                        <div className="text-sm text-brand-main-100 whitespace-pre-wrap">{deployment.changelog}</div>
                                    </div>
                                )}
                            </div>

                            <DeploymentKeysSection deploymentId={deploymentId} />
                        </TabsContent>

                        <TabsContent value="usage" className="mt-0">
                            <div className="rounded-md border border-brand-main-700/30 bg-brand-main-800/20 p-3 space-y-3">
                                <div className="text-[11px] text-white/45 uppercase tracking-wider light:text-black/45">Endpoints</div>
                                <div className="space-y-1.5">
                                    <EndpointRow
                                        label="Invoke (sync)"
                                        url={invokeUrl}
                                        copied={copiedId === 'endpoint-invoke'}
                                        onCopy={() => void handleCopy(invokeUrl, 'endpoint-invoke')}
                                    />
                                    <EndpointRow
                                        label="Stream (SSE)"
                                        url={streamUrl}
                                        copied={copiedId === 'endpoint-stream'}
                                        onCopy={() => void handleCopy(streamUrl, 'endpoint-stream')}
                                    />
                                </div>

                                <div className="space-y-2">
                                    <div className="text-[11px] text-white/45 uppercase tracking-wider light:text-black/45">Code Examples</div>
                                    <Tabs defaultValue="curl" className="space-y-2">
                                        <TabsList className="w-fit bg-brand-main-800/50 border border-brand-main-600 rounded p-1 h-auto gap-1">
                                            <TabsTrigger className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white transition-colors py-1 light:hover:text-brand-main-50" value="curl">cURL</TabsTrigger>
                                            <TabsTrigger className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white transition-colors py-1 light:hover:text-brand-main-50" value="javascript">JavaScript</TabsTrigger>
                                            <TabsTrigger className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white transition-colors py-1 light:hover:text-brand-main-50" value="python">Python</TabsTrigger>
                                        </TabsList>
                                        <TabsContent value="curl" className="mt-0">
                                            <CodeExampleBlock
                                                code={curlExample}
                                                language="bash"
                                            />
                                        </TabsContent>
                                        <TabsContent value="javascript" className="mt-0">
                                            <CodeExampleBlock
                                                code={javascriptExample}
                                                language="javascript"
                                            />
                                        </TabsContent>
                                        <TabsContent value="python" className="mt-0">
                                            <CodeExampleBlock
                                                code={pythonExample}
                                                language="python"
                                            />
                                        </TabsContent>
                                    </Tabs>
                                </div>
                            </div>
                        </TabsContent>

                        <TabsContent value="invocations" className="mt-0">
                            <div className="rounded-md border border-brand-main-700/30 bg-brand-main-800/20 p-3 space-y-2">
                                <div className="text-[11px] text-white/45 uppercase tracking-wider light:text-black/45">
                                    Recent Invocations {invocationData?.total ? `(${invocationData.total})` : ''}
                                </div>
                                {(!invocationData?.invocations || invocationData.invocations.length === 0) ? (
                                    <div className="text-sm text-brand-main-100 py-2 text-center">No invocations yet</div>
                                ) : (
                                    <div className="space-y-1.5">
                                        {invocationData.invocations.slice(0, 20).map((inv) => (
                                            <div key={inv.id} className="flex items-center justify-between rounded bg-brand-main-800/40 px-3 py-1.5 text-xs border border-brand-main-700/20">
                                                <div className="flex items-center gap-2">
                                                    <InvocationStatusBadge status={inv.status} />
                                                    <span className="text-brand-main-200">{inv.turns} turns</span>
                                                    <span className="text-brand-main-300">{inv.durationMs}ms</span>
                                                </div>
                                                <span className="text-brand-main-300">{formatTimestamp(inv.createdAt)}</span>
                                            </div>
                                        ))}
                                    </div>
                                )}
                            </div>
                        </TabsContent>
                    </Tabs>
                </SheetBody>
            </SheetContent>
        </Sheet>
    )
}

function CopyIconButton({ copied, onCopy, label }: { copied: boolean; onCopy: () => void; label: string }) {
    return (
        <button
            type="button"
            onClick={onCopy}
            title={label}
            className="inline-flex items-center justify-center rounded border border-brand-main-600/40 bg-brand-main-900/60 p-1 text-brand-main-300 hover:text-white hover:border-brand-main-500 transition-colors light:hover:text-brand-main-50"
        >
            {copied ? <Check className="size-3 text-green-400" /> : <Copy className="size-3" />}
        </button>
    )
}

function CodeExampleBlock({ code, language }: { code: string; language: UsageSnippetLanguage }) {
    const codeBlockData = [
        {
            language,
            filename: `example.${language === 'bash' ? 'sh' : language}`,
            code,
        },
    ]

    return (
        <CodeBlock data={codeBlockData} defaultValue={language}>
            <CodeBlockBody>
                {(item) => (
                    <CodeBlockItem key={item.language} value={item.language} className="relative">
                        <CodeBlockContent language={language}>
                            {item.code}
                        </CodeBlockContent>
                        <div className="absolute right-2 top-2">
                            <CodeBlockCopyButton size="sm" />
                        </div>
                    </CodeBlockItem>
                )}
            </CodeBlockBody>
        </CodeBlock>
    )
}

function StatusBadge({ status }: { status: string }) {
    const styles: Record<string, string> = {
        active: 'bg-green-500/20 text-green-300',
        paused: 'bg-amber-500/20 text-amber-300',
        retired: 'bg-gray-500/20 text-gray-400',
    }
    return (
        <span className={`px-1.5 py-0.5 rounded text-[10px] font-medium ${styles[status] ?? styles.retired}`}>
            {status}
        </span>
    )
}

function InvocationStatusBadge({ status }: { status: string }) {
    const styles: Record<string, string> = {
        completed: 'bg-green-500/20 text-green-300',
        running: 'bg-blue-500/20 text-blue-300',
        failed: 'bg-red-500/20 text-red-300',
        timeout: 'bg-amber-500/20 text-amber-300',
    }
    return (
        <span className={`px-1 py-0.5 rounded text-[10px] font-medium ${styles[status] ?? 'bg-gray-500/20 text-gray-400'}`}>
            {status}
        </span>
    )
}

function InfoField({ label, value }: { label: string; value: string }) {
    return (
        <div className="space-y-0.5">
            <div className="text-[10px] text-white/40 uppercase tracking-wider light:text-black/40">{label}</div>
            <div className="text-sm text-brand-main-100">{value}</div>
        </div>
    )
}

function EndpointRow({
    label,
    url,
    copied,
    onCopy,
}: {
    label: string
    url: string
    copied: boolean
    onCopy: () => void
}) {
    return (
        <div className="flex items-center gap-2 rounded bg-brand-main-800/40 px-3 py-1.5 border border-brand-main-700/20">
            <span className="text-[10px] text-brand-main-300 w-20 shrink-0">{label}</span>
            <code className="text-xs text-brand-secondary-400 font-mono truncate flex-1">{url}</code>
            <CopyIconButton copied={copied} onCopy={onCopy} label={`Copy ${label} URL`} />
        </div>
    )
}
