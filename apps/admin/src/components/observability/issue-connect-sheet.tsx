import { useState } from 'react'
import { ui } from '@everstack/ui'
import { Button, toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'

const {
  Sheet,
  SheetTrigger,
  SheetContent,
  SheetHeader,
  SheetBody,
  SheetTitle,
  SheetDescription,
  Tabs,
  TabsList,
  TabsTrigger,
  TabsContent,
} = ui

// Issues are auto-detected from error spans, not created by hand. This sheet
// documents the three ways an error reaches the Issues feed: gateway-proxied
// calls (automatic), the SDK capture helpers, and raw OpenTelemetry. The code
// matches the real SDK surface (capture_exception / span() auto-capture).

type Snippet = { label: string; lang: string; code: string }

const PYTHON_SNIPPETS: Snippet[] = [
  {
    label: 'Install',
    lang: 'bash',
    code: 'pip install everstack',
  },
  {
    label: 'Capture an exception',
    lang: 'python',
    code: `from everstack import Everstack

client = Everstack(api_key="sk-...")

try:
    resp = openai.chat.completions.create(...)
except Exception as e:
    # Grouped by error signature into a single Issue
    client.capture_exception(e, provider="openai", model="gpt-4o")
    raise`,
  },
  {
    label: 'Auto-capture in a span',
    lang: 'python',
    code: `# Any exception raised inside the span is reported automatically.
with client.traces.span(trace_id="req-123", name="generate"):
    resp = openai.chat.completions.create(...)`,
  },
]

const NODE_SNIPPETS: Snippet[] = [
  {
    label: 'Install',
    lang: 'bash',
    code: 'npm install @everstack/node',
  },
  {
    label: 'Capture an exception',
    lang: 'typescript',
    code: `import { Everstack } from "@everstack/node";

const client = new Everstack({ apiKey: "sk-..." });

try {
  await openai.chat.completions.create(...);
} catch (err) {
  // Grouped by error signature into a single Issue
  client.captureException(err, { provider: "openai", model: "gpt-4o" });
  throw err;
}`,
  },
  {
    label: 'Auto-capture in a span',
    lang: 'typescript',
    code: `// Any error thrown inside the span is reported automatically.
await client.traces.withSpan(
  { traceId: "req-123", name: "generate" },
  async () => {
    await openai.chat.completions.create(...);
  },
);`,
  },
]

const OTEL_SNIPPETS: Snippet[] = [
  {
    label: 'OpenTelemetry endpoint',
    lang: 'bash',
    code: `# Point any OpenTelemetry exporter at the public OTLP endpoint.
# Error spans (StatusCode=Error) become Issues automatically.
OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=https://gateway.everstack.ai/api/public/otel/v1/traces
OTEL_EXPORTER_OTLP_TRACES_HEADERS=Authorization=Bearer%20sk-...`,
  },
  {
    label: 'Send a span with curl',
    lang: 'bash',
    code: `curl https://gateway.everstack.ai/api/public/otel/v1/traces \\
  -H "Authorization: Bearer sk-..." \\
  -H "Content-Type: application/json" \\
  -d '{"resourceSpans":[{"scopeSpans":[{"spans":[{
        "name":"openai.chat",
        "traceId":"5b8aa5a2d2c872e8321cf37308d69df2",
        "spanId":"051581bf3cb55c13",
        "status":{"code":2,"message":"rate limit exceeded"},
        "attributes":[
          {"key":"llm.provider","value":{"stringValue":"openai"}},
          {"key":"llm.model","value":{"stringValue":"gpt-4o"}}
        ]
  }]}]}]}'`,
  },
]

function CodeBlock({ code }: { code: string }) {
  const [copied, setCopied] = useState(false)
  const copy = () => {
    navigator.clipboard.writeText(code).then(
      () => {
        setCopied(true)
        toast.success('Copied to clipboard')
        setTimeout(() => setCopied(false), 1500)
      },
      () => toast.error('Could not copy'),
    )
  }
  return (
    <div className="group relative">
      <pre className="overflow-x-auto rounded border border-brand-main-700 bg-brand-main-900/70 p-3 font-mono text-[11px] leading-relaxed text-white/80 light:text-black/80">
        {code}
      </pre>
      <button
        type="button"
        onClick={copy}
        aria-label="Copy code"
        className="absolute right-2 top-2 rounded border border-brand-main-700 bg-brand-main-800 p-1.5 text-white/50 opacity-0 transition hover:text-white/90 light:text-black/50 light:hover:text-black/90 group-hover:opacity-100"
      >
        <Iconify.Icon icon={copied ? 'lucide:check' : 'lucide:copy'} className="size-3.5" />
      </button>
    </div>
  )
}

function SnippetList({ snippets }: { snippets: Snippet[] }) {
  return (
    <div className="space-y-4">
      {snippets.map((s) => (
        <div key={s.label} className="space-y-1.5">
          <div className="text-[11px] font-medium uppercase tracking-wide text-white/40 light:text-black/40">{s.label}</div>
          <CodeBlock code={s.code} />
        </div>
      ))}
    </div>
  )
}

export function IssueConnectSheet({ variant = 'cta' }: { variant?: 'topbar' | 'cta' }) {
  const [open, setOpen] = useState(false)
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        {variant === 'topbar' ? (
          <Button size="sm" variant="outline" className="h-8 gap-1.5">
            <Iconify.Icon icon="lucide:code" className="size-3.5" />
            Connect SDK
          </Button>
        ) : (
          <Button size="sm" className="gap-1.5">
            <Iconify.Icon icon="lucide:code" className="size-3.5" />
            Connect the SDK
          </Button>
        )}
      </SheetTrigger>
      <SheetContent side="right" className="flex w-full flex-col gap-0 sm:min-w-[560px]">
        <SheetHeader>
          <SheetTitle>Send errors from your code</SheetTitle>
          <SheetDescription className="mt-1 text-xs leading-relaxed text-white/55 light:text-black/55">
            Issues are detected automatically, not created by hand. Any failed LLM call you proxy through
            the Everstack gateway already shows up here. To report errors from code that does not go
            through the gateway, use the SDK helpers below. Everstack groups recurring failures by their
            error signature, so one incident becomes a single tracked Issue.
          </SheetDescription>
        </SheetHeader>
        <SheetBody className="flex-1 overflow-y-auto">
          <Tabs defaultValue="python" className="w-full">
            <TabsList className="mb-4 grid w-full grid-cols-3">
              <TabsTrigger value="python">Python</TabsTrigger>
              <TabsTrigger value="node">Node</TabsTrigger>
              <TabsTrigger value="otel">OpenTelemetry</TabsTrigger>
            </TabsList>
            <TabsContent value="python">
              <SnippetList snippets={PYTHON_SNIPPETS} />
            </TabsContent>
            <TabsContent value="node">
              <SnippetList snippets={NODE_SNIPPETS} />
            </TabsContent>
            <TabsContent value="otel">
              <SnippetList snippets={OTEL_SNIPPETS} />
            </TabsContent>
          </Tabs>
        </SheetBody>
      </SheetContent>
    </Sheet>
  )
}
