import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { motion } from 'framer-motion'
import { Iconify } from '@everstack/ui/icons'
import { ui } from '@everstack/ui'
import { useOnboarding } from '@/hooks/use-onboarding'
import { useOnboardingStore } from '@/stores/onboarding-store'

const {
    Sheet,
    SheetContent,
    SheetHeader,
    SheetTitle,
    SheetBody,
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

export function OnboardingChecklist() {
    const {
        steps,
        launchSteps,
        launchCompletedCount,
        launchAllComplete,
        minimized,
        isLoading,
        firstAgentId,
        hasOpenAI,
    } = useOnboarding()
    const dismiss = useOnboardingStore((s) => s.dismiss)
    const toggleMinimized = useOnboardingStore((s) => s.toggleMinimized)
    const [snippetSheetOpen, setSnippetSheetOpen] = useState(false)

    const displaySteps = launchSteps.length > 0 ? launchSteps : steps
    const completedCount = launchSteps.length > 0 ? launchCompletedCount : steps.filter((s) => s.complete).length
    const allComplete = launchAllComplete
    const totalSteps = displaySteps.length
    const progressPercent = (completedCount / totalSteps) * 100

    // Don't render while loading
    if (isLoading) return null

    // Setup complete: the home-page launch center owns the "you're live" moment.
    if (allComplete) return null

    // Minimized state
    if (minimized) {
        return (
            <button
                type="button"
                onClick={toggleMinimized}
                className="mx-2 mb-2 rounded-sm bg-brand-secondary-700/10 border border-brand-secondary-600/20 p-2.5 flex items-center gap-2 w-[calc(100%-1rem)] hover:bg-brand-secondary-700/15 transition-colors"
            >
                <div className="flex size-5 items-center justify-center rounded bg-brand-secondary-500/20">
                    <Iconify.Icon icon="lucide:rocket" className="size-3 text-brand-secondary-400" />
                </div>
                <span className="text-xs font-medium text-brand-secondary-400 flex-1 text-left">
                    Getting Started
                </span>
                <span className="text-[10px] text-brand-secondary-400/70">
                    {completedCount}/{totalSteps}
                </span>
                <Iconify.Icon icon="lucide:chevron-down" className="size-3 text-brand-secondary-400/50" />
            </button>
        )
    }

    // Find current (first incomplete) step
    const currentStepIndex = displaySteps.findIndex((s) => !s.complete)
    const currentStep = displaySteps[currentStepIndex] ?? displaySteps[displaySteps.length - 1]!

    // Compact launcher state. The immersive flow now lives on the home page.
    return (
        <>
            <motion.div
                initial={{ opacity: 0, y: 8 }}
                animate={{ opacity: 1, y: 0 }}
                className="mx-2 mb-2 rounded-sm bg-brand-secondary-700/10 border border-brand-secondary-600/20 p-3"
            >
                <div className="flex items-center gap-2 mb-3">
                    <div className="flex size-5 items-center justify-center rounded bg-brand-secondary-500/20">
                        <Iconify.Icon icon="lucide:rocket" className="size-3 text-brand-secondary-400" />
                    </div>
                    <span className="text-xs font-medium text-brand-secondary-400 flex-1">
                        Launch Center
                    </span>
                    <button
                        type="button"
                        onClick={toggleMinimized}
                        className="p-0.5 rounded hover:bg-white/5 light:hover:bg-black/5 transition-colors text-white/30 light:text-black/30 hover:text-white/60 light:hover:text-black/60"
                        title="Minimize"
                    >
                        <Iconify.Icon icon="lucide:minus" className="size-3" />
                    </button>
                    <button
                        type="button"
                        onClick={dismiss}
                        className="p-0.5 rounded hover:bg-white/5 light:hover:bg-black/5 transition-colors text-white/30 light:text-black/30 hover:text-white/60 light:hover:text-black/60"
                        title="Dismiss"
                    >
                        <Iconify.Icon icon="lucide:x" className="size-3" />
                    </button>
                </div>

                <div className="mb-3">
                    <div className="flex justify-between text-[10px] text-brand-secondary-400/70 mb-1">
                        <span>{completedCount} of {totalSteps} complete</span>
                    </div>
                    <div className="h-1.5 bg-brand-secondary-500/20 rounded-full overflow-hidden">
                        <motion.div
                            className="h-full rounded-full bg-brand-secondary-500"
                            initial={{ width: 0 }}
                            animate={{ width: `${progressPercent}%` }}
                            transition={{ duration: 0.5, ease: 'easeOut' }}
                        />
                    </div>
                </div>

                <div className="rounded border border-brand-secondary-500/15 bg-brand-main-900/30 p-2.5">
                    <div className="flex items-start gap-2">
                        <StepIcon step={currentStep} isCurrent />
                        <div className="min-w-0 flex-1">
                            <p className="text-xs font-medium text-brand-secondary-300 truncate">
                                {currentStep?.label ?? 'Finish setup'}
                            </p>
                            <p className="mt-0.5 line-clamp-2 text-[10px] leading-4 text-white/35 light:text-black/35">
                                {currentStep?.description ?? 'Open the Launch Center to finish activation.'}
                            </p>
                        </div>
                    </div>
                    <div className="mt-2 flex items-center gap-2">
                        <Link
                            to="/"
                            className="inline-flex flex-1 items-center justify-center gap-1 whitespace-nowrap rounded bg-brand-secondary-500/15 px-2 py-1.5 text-[11px] font-medium text-brand-secondary-300 transition-colors hover:bg-brand-secondary-500/20"
                        >
                            Open Launch Center
                            <Iconify.Icon icon="lucide:arrow-right" className="size-3 shrink-0" />
                        </Link>
                        {currentStep?.id === 'test' && firstAgentId && (
                            <button
                                type="button"
                                onClick={() => setSnippetSheetOpen(true)}
                                className="inline-flex items-center justify-center rounded border border-brand-secondary-500/15 px-2 py-1.5 text-[11px] text-brand-secondary-400/70 transition-colors hover:text-brand-secondary-300"
                                title="View API snippets"
                            >
                                <Iconify.Icon icon="lucide:code-2" className="size-3" />
                            </button>
                        )}
                    </div>
                </div>
            </motion.div>

            {/* API Snippets Sheet */}
            <OnboardingSnippetSheet
                open={snippetSheetOpen}
                onOpenChange={setSnippetSheetOpen}
                agentId={firstAgentId}
                hasOpenAI={hasOpenAI}
            />
        </>
    )
}

function StepIcon({ step, isCurrent }: { step: { complete: boolean; icon: string }; isCurrent: boolean }) {
    if (step.complete) {
        return (
            <motion.div
                initial={{ scale: 0 }}
                animate={{ scale: 1 }}
                transition={{ type: 'spring', stiffness: 400, damping: 15 }}
                className="flex size-5 items-center justify-center rounded bg-emerald-500/20 shrink-0"
            >
                <Iconify.Icon icon="lucide:check" className="size-3 text-emerald-400 light:text-emerald-600" />
            </motion.div>
        )
    }
    if (isCurrent) {
        return (
            <div className="flex size-5 items-center justify-center rounded bg-brand-secondary-500/20 shrink-0">
                <Iconify.Icon icon={step.icon} className="size-3 text-brand-secondary-400" />
            </div>
        )
    }
    return (
        <div className="flex size-5 items-center justify-center rounded bg-white/5 light:bg-black/5 shrink-0">
            <Iconify.Icon icon={step.icon} className="size-3 text-white/20 light:text-black/20" />
        </div>
    )
}

// ─── API Snippets Sheet ────────────────────────────────────────────

const TAB_TRIGGER_CLASS = 'relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1'

function OnboardingSnippetSheet({
    open,
    onOpenChange,
    agentId,
    hasOpenAI,
}: {
    open: boolean
    onOpenChange: (open: boolean) => void
    agentId: string | null
    hasOpenAI: boolean
}) {
    const [copiedId, setCopiedId] = useState<string | null>(null)
    const baseUrl = typeof window !== 'undefined' ? window.location.origin : 'https://your-instance.everstack.ai'

    const snippets = hasOpenAI
        ? getOpenAISnippets(baseUrl, agentId)
        : getChatCompletionsSnippets(baseUrl, agentId)

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

    const agentInvokeUrl = agentId ? `${baseUrl}/v1/deploy/${agentId}/invoke` : null
    const agentStreamUrl = agentId ? `${baseUrl}/v1/deploy/${agentId}/stream` : null
    const gatewayUrl = hasOpenAI ? `${baseUrl}/v1/responses` : `${baseUrl}/v1/chat/completions`

    return (
        <Sheet open={open} onOpenChange={onOpenChange}>
            <SheetContent side="right" className="w-full sm:max-w-[560px] max-h-[100vh] overflow-y-auto scrollbar-macos">
                <SheetHeader>
                    <SheetTitle>API Quick Start</SheetTitle>
                </SheetHeader>
                <SheetBody className="py-4">
                    <div className="rounded-md border border-brand-main-700/30 bg-brand-main-800/20 p-3 space-y-4">
                        {/* Endpoints */}
                        <div className="space-y-2">
                            <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">Endpoints</div>
                            <div className="space-y-1.5">
                                <CopyableEndpointRow
                                    label={hasOpenAI ? 'Responses' : 'Chat'}
                                    url={gatewayUrl}
                                    copied={copiedId === 'gateway'}
                                    onCopy={() => void handleCopy(gatewayUrl, 'gateway')}
                                />
                                {agentInvokeUrl && (
                                    <CopyableEndpointRow
                                        label="Invoke"
                                        url={agentInvokeUrl}
                                        copied={copiedId === 'invoke'}
                                        onCopy={() => void handleCopy(agentInvokeUrl, 'invoke')}
                                    />
                                )}
                                {agentStreamUrl && (
                                    <CopyableEndpointRow
                                        label="Stream"
                                        url={agentStreamUrl}
                                        copied={copiedId === 'stream'}
                                        onCopy={() => void handleCopy(agentStreamUrl, 'stream')}
                                    />
                                )}
                            </div>
                        </div>

                        {/* Code Examples */}
                        <div className="space-y-2">
                            <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">Code Examples</div>
                            <Tabs defaultValue="curl" className="space-y-2">
                                <TabsList className="w-fit bg-brand-main-800/50 border border-brand-main-600 rounded p-1 h-auto gap-1">
                                    <TabsTrigger className={TAB_TRIGGER_CLASS} value="curl">cURL</TabsTrigger>
                                    <TabsTrigger className={TAB_TRIGGER_CLASS} value="javascript">JavaScript</TabsTrigger>
                                    <TabsTrigger className={TAB_TRIGGER_CLASS} value="python">Python</TabsTrigger>
                                </TabsList>
                                <TabsContent value="curl" className="mt-0">
                                    <CodeExampleBlock code={snippets.curl} language="bash" />
                                </TabsContent>
                                <TabsContent value="javascript" className="mt-0">
                                    <CodeExampleBlock code={snippets.javascript} language="javascript" />
                                </TabsContent>
                                <TabsContent value="python" className="mt-0">
                                    <CodeExampleBlock code={snippets.python} language="python" />
                                </TabsContent>
                            </Tabs>
                        </div>

                        {/* Hint */}
                        <div className="rounded bg-brand-main-900/40 border border-brand-main-700/20 px-3 py-2.5">
                            <p className="text-xs text-white/40 light:text-black/40 leading-relaxed">
                                Replace <code className="text-brand-secondary-400/70 font-mono">&lt;YOUR_KEY&gt;</code> with
                                an API key from{' '}
                                <Link to="/vault/api-keys" className="text-brand-secondary-400 hover:underline" onClick={() => onOpenChange(false)}>
                                    Vault &rarr; API Keys
                                </Link>
                                .
                            </p>
                        </div>
                    </div>
                </SheetBody>
            </SheetContent>
        </Sheet>
    )
}

function CopyableEndpointRow({
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
            <span className="text-[10px] text-brand-main-300 w-16 shrink-0">{label}</span>
            <code className="text-xs text-brand-secondary-400 font-mono truncate flex-1 min-w-0">{url}</code>
            <button
                type="button"
                onClick={onCopy}
                title={`Copy ${label} URL`}
                className="inline-flex items-center justify-center rounded border border-brand-main-600/40 bg-brand-main-900/60 p-1 text-brand-main-300 hover:text-white light:hover:text-brand-main-50 hover:border-brand-main-500 transition-colors shrink-0"
            >
                {copied
                    ? <Iconify.Icon icon="lucide:check" className="size-3 text-green-400 light:text-green-600" />
                    : <Iconify.Icon icon="lucide:copy" className="size-3" />
                }
            </button>
        </div>
    )
}

function CodeExampleBlock({ code, language }: { code: string; language: string }) {
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
                            <CodeBlockCopyButton />
                        </div>
                    </CodeBlockItem>
                )}
            </CodeBlockBody>
        </CodeBlock>
    )
}

// ─── Snippet Generators ────────────────────────────────────────────

function getOpenAISnippets(baseUrl: string, agentId: string | null) {
    const invokeUrl = agentId ? `${baseUrl}/v1/deploy/${agentId}/invoke` : null
    const agentCurl = invokeUrl
        ? `\n\n# Or invoke your agent directly:\ncurl -X POST ${invokeUrl} \\\n  -H "x-evs-api-key: <YOUR_KEY>" \\\n  -H "Content-Type: application/json" \\\n  -d '{"message": "Hello"}'`
        : ''

    return {
        curl: `curl ${baseUrl}/v1/responses \\
  -H "x-evs-api-key: <YOUR_KEY>" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "gpt-4o",
    "input": "Hello, how can you help me?"
  }'${agentCurl}`,

        javascript: `import OpenAI from 'openai'

const client = new OpenAI({
  baseURL: '${baseUrl}/v1',
  apiKey: '<YOUR_KEY>',
})

const response = await client.responses.create({
  model: 'gpt-4o',
  input: 'Hello, how can you help me?',
})

console.log(response.output_text)`,

        python: `from openai import OpenAI

client = OpenAI(
    base_url="${baseUrl}/v1",
    api_key="<YOUR_KEY>",
)

response = client.responses.create(
    model="gpt-4o",
    input="Hello, how can you help me?",
)

print(response.output_text)`,
    }
}

function getChatCompletionsSnippets(baseUrl: string, agentId: string | null) {
    const invokeUrl = agentId ? `${baseUrl}/v1/deploy/${agentId}/invoke` : null
    const agentCurl = invokeUrl
        ? `\n\n# Or invoke your agent directly:\ncurl -X POST ${invokeUrl} \\\n  -H "x-evs-api-key: <YOUR_KEY>" \\\n  -H "Content-Type: application/json" \\\n  -d '{"message": "Hello"}'`
        : ''

    return {
        curl: `curl ${baseUrl}/v1/chat/completions \\
  -H "x-evs-api-key: <YOUR_KEY>" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "claude-sonnet-4-20250514",
    "messages": [
      {"role": "user", "content": "Hello, how can you help me?"}
    ]
  }'${agentCurl}`,

        javascript: `const response = await fetch('${baseUrl}/v1/chat/completions', {
  method: 'POST',
  headers: {
    'x-evs-api-key': '<YOUR_KEY>',
    'Content-Type': 'application/json',
  },
  body: JSON.stringify({
    model: 'claude-sonnet-4-20250514',
    messages: [
      { role: 'user', content: 'Hello, how can you help me?' },
    ],
  }),
})

const data = await response.json()
console.log(data.choices[0].message.content)`,

        python: `import requests

response = requests.post(
    "${baseUrl}/v1/chat/completions",
    headers={
        "x-evs-api-key": "<YOUR_KEY>",
        "Content-Type": "application/json",
    },
    json={
        "model": "claude-sonnet-4-20250514",
        "messages": [
            {"role": "user", "content": "Hello, how can you help me?"}
        ],
    },
)

print(response.json()["choices"][0]["message"]["content"])`,
    }
}

