import { useState } from 'react'
import { ui, CheckCircle2 } from '@everstack/ui'
import { ExecutionMode } from '@/server/functions'
import {
  useCreateFunction,
  useFunctions,
  useIsolationStatus,
} from '@/hooks/deployments/use-functions'
import { toast } from '@everstack/ui/components'
import { cn } from '@/lib/utils'
import { JSONEditor } from './json-editor'
import { CodeEditor } from './code-editor'

const {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetBody,
  Button,
  Input,
  Label,
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
  Textarea,
} = ui

interface CreateFunctionDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

type Step = 'basic' | 'mode' | 'config' | 'advanced' | 'success'

const HTTP_METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE']

export function CreateFunctionDialog({
  open,
  onOpenChange,
}: CreateFunctionDialogProps) {
  const [step, setStep] = useState<Step>('mode')

  // Basic info
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')

  // Mode selection
  const [mode, setMode] = useState<ExecutionMode>(ExecutionMode.WEBHOOK)

  // Webhook config
  const [webhookUrl, setWebhookUrl] = useState('')
  const [webhookMethod, setWebhookMethod] = useState('POST')
  const [webhookHeaders, setWebhookHeaders] = useState('')
  const [webhookTimeout, setWebhookTimeout] = useState('30000')

  // Proxy config
  const [proxyBaseUrl, setProxyBaseUrl] = useState('')
  const [proxyPath, setProxyPath] = useState('')
  const [proxyMethod, setProxyMethod] = useState('POST')

  // Advanced settings
  const [timeoutMs, setTimeoutMs] = useState('30000')
  const [memoryMb, setMemoryMb] = useState('128')
  const [maxRetries, setMaxRetries] = useState('3')

  // Isolated config
  const [runtime, setRuntime] = useState('nodejs20')
  const [code, setCode] = useState('')
  const [packages, setPackages] = useState('')
  const [dockerHost, setDockerHost] = useState('')

  // Parameters (JSON Schema)
  const [parametersJson, setParametersJson] = useState('')

  // Success state
  const [createdFunctionId, setCreatedFunctionId] = useState<string | null>(
    null,
  )

  const createFunctionMutation = useCreateFunction()
  const listFunctionsQuery = useFunctions()

  const isolationStatus = useIsolationStatus()
  const isIsolationAvailable = isolationStatus.data?.available ?? false

  const parseHeaders = (headersStr: string): { [key: string]: string } => {
    if (!headersStr.trim()) return {}
    try {
      return JSON.parse(headersStr)
    } catch {
      // Try parsing as key: value pairs
      const headers: { [key: string]: string } = {}
      headersStr.split('\n').forEach((line) => {
        const colonIndex = line.indexOf(':')
        if (colonIndex > 0) {
          const key = line.substring(0, colonIndex).trim()
          const value = line.substring(colonIndex + 1).trim()
          if (key) headers[key] = value
        }
      })
      return headers
    }
  }

  const parseParameters = (): object | undefined => {
    if (!parametersJson.trim()) return undefined
    try {
      return JSON.parse(parametersJson)
    } catch {
      return undefined
    }
  }

  const handleSubmit = async () => {
    try {
      const params = parseParameters()

      const response = await createFunctionMutation.mutateAsync({
        name: name.trim(),
        description: description.trim() || undefined,
        mode,
        parameters: params as any,
        webhook:
          mode === ExecutionMode.WEBHOOK
            ? {
                url: webhookUrl.trim(),
                method: webhookMethod,
                headers: parseHeaders(webhookHeaders),
                timeoutMs: parseInt(webhookTimeout) || 30000,
              }
            : undefined,
        proxy:
          mode === ExecutionMode.PROXY
            ? {
                baseUrl: proxyBaseUrl.trim(),
                path: proxyPath.trim(),
                method: proxyMethod,
              }
            : undefined,
        isolated:
          mode === ExecutionMode.ISOLATED
            ? {
                runtime,
                code,
                packages: packages
                  .split(/[,\n]/)
                  .map((s) => s.trim())
                  .filter(Boolean),
                dockerHost: dockerHost.trim() || undefined,
              }
            : undefined,
        timeoutMs: parseInt(timeoutMs) || undefined,
        memoryMb: parseInt(memoryMb) || undefined,
        maxRetries: parseInt(maxRetries) || undefined,
      })

      if (response.function?.id) {
        setCreatedFunctionId(response.function.id)
        setStep('success')
        toast.success('Function created successfully!')
      }
    } catch (error) {
      console.error('Failed to create function:', error)
      toast.error('Failed to create function')
    }
  }

  const handleClose = () => {
    // Reset form
    setStep('mode')
    setName('')
    setDescription('')
    setMode(ExecutionMode.WEBHOOK)
    setWebhookUrl('')
    setWebhookMethod('POST')
    setWebhookHeaders('')
    setWebhookTimeout('30000')
    setProxyBaseUrl('')
    setProxyPath('')
    setProxyMethod('POST')
    setRuntime('nodejs20')
    setCode('')
    setPackages('')
    setDockerHost('')
    setTimeoutMs('30000')
    setMemoryMb('128')
    setMaxRetries('3')
    setParametersJson('')
    setCreatedFunctionId(null)
    onOpenChange(false)
    listFunctionsQuery.refetch()
  }

  const canProceedFromBasic = name.trim().length > 0
  const canProceedFromMode = mode !== ExecutionMode.UNSPECIFIED
  const canProceedFromConfig = () => {
    if (mode === ExecutionMode.WEBHOOK) {
      return webhookUrl.trim().length > 0
    }
    if (mode === ExecutionMode.PROXY) {
      return proxyBaseUrl.trim().length > 0
    }
    if (mode === ExecutionMode.ISOLATED) {
      return code.trim().length > 0
    }
    return true
  }

  const getModeDescription = (mode: ExecutionMode): string => {
    switch (mode) {
      case ExecutionMode.WEBHOOK:
        return 'Call your backend or internal service as a reusable tool'
      case ExecutionMode.PROXY:
        return 'Wrap an upstream API as a tool with request mapping'
      case ExecutionMode.ISOLATED:
        return 'Run lightweight custom code in an isolated runtime'
      default:
        return ''
    }
  }

  const stepIndex = ['mode', 'basic', 'config', 'advanced', 'success'].indexOf(step)
  const totalSteps = 4

  return (
    <Sheet open={open} onOpenChange={(v) => { if (!v) handleClose(); else onOpenChange(v) }}>
      <SheetContent
        side="right"
        className="flex h-[100vh] w-full flex-col overflow-hidden sm:max-w-[620px]"
      >
        <SheetHeader>
          <SheetTitle className="flex items-center gap-2">
            {step === 'success' ? (
              <>
                <CheckCircle2 className="text-emerald-400 light:text-emerald-600" size={20} />
                Function Created
              </>
            ) : (
              'Create Function'
            )}
            {step !== 'success' && (
              <span className="rounded px-1.5 py-0.5 text-[10px] font-medium bg-brand-secondary-600/15 text-brand-secondary-300 border border-brand-secondary-500/25">
                Step {stepIndex + 1} of {totalSteps}
              </span>
            )}
          </SheetTitle>
        </SheetHeader>

        <SheetBody className="flex-1 overflow-y-auto py-4 scrollbar-macos">
          {step === 'mode' && (
            <div className="space-y-4">
              <p className="text-sm text-white/50 light:text-black/50">
                Choose how your function will be executed.
              </p>

              <div className="grid gap-3">
                {[
                  ExecutionMode.WEBHOOK,
                  ExecutionMode.PROXY,
                  ExecutionMode.ISOLATED,
                ].map((m) => {
                  const isDisabled =
                    m === ExecutionMode.ISOLATED && !isIsolationAvailable
                  return (
                    <button
                      key={m}
                      type="button"
                      onClick={() => !isDisabled && setMode(m)}
                      disabled={isDisabled}
                      className={cn(
                        'p-4 rounded-lg border text-left transition-colors',
                        isDisabled
                          ? 'border-brand-main-700 bg-brand-main-900/50 opacity-50 cursor-not-allowed'
                          : mode === m
                            ? 'border-brand-secondary-500 bg-brand-secondary-500/10'
                            : 'border-brand-main-600 bg-brand-main-800 hover:border-brand-main-500',
                      )}
                    >
                      <div className="flex items-center justify-between">
                        <span className="font-medium text-white light:text-brand-main-50">
                          {m === ExecutionMode.WEBHOOK && 'Webhook'}
                          {m === ExecutionMode.PROXY && 'Proxy'}
                          {m === ExecutionMode.ISOLATED && 'Isolated'}
                        </span>
                        {isDisabled && (
                          <span className="text-xs text-amber-300/80 light:text-amber-700/80 bg-amber-400/10 px-2 py-0.5 rounded border border-amber-500/25">
                            Docker unavailable
                          </span>
                        )}
                      </div>
                      <p className="text-sm text-white/50 light:text-black/50 mt-1">
                        {getModeDescription(m)}
                      </p>
                      {isDisabled && (
                        <p className="text-xs text-amber-300/60 light:text-amber-700/60 mt-1">
                          Start Docker to use isolated functions.
                        </p>
                      )}
                    </button>
                  )
                })}
              </div>
            </div>
          )}

          {step === 'basic' && (
            <div className="space-y-4">
              <p className="text-sm text-white/50 light:text-black/50">
                Enter basic information about your function.
              </p>

              <div className="space-y-2">
                <Label htmlFor="name">Name *</Label>
                <Input
                  id="name"
                  placeholder="get_weather"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  required
                  className="bg-brand-main-800 border-brand-main-600"
                />
                <p className="text-xs text-white/40 light:text-black/40">
                  A unique name for your function. Use snake_case for tool
                  compatibility.
                </p>
              </div>

              <div className="space-y-2">
                <Label htmlFor="description">Description</Label>
                <Textarea
                  id="description"
                  placeholder="Get current weather for a given location"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  className="bg-brand-main-800 border-brand-main-600 min-h-[80px]"
                />
                <p className="text-xs text-white/40 light:text-black/40">
                  A brief description of what this function does. This will be
                  shown to LLMs.
                </p>
              </div>

              {mode !== ExecutionMode.ISOLATED && (
                <div className="space-y-2">
                  <Label htmlFor="parameters">Parameters (JSON Schema)</Label>
                  <JSONEditor
                    value={parametersJson}
                    onChange={setParametersJson}
                    height="160px"
                    placeholder={`{
  "type": "object",
  "properties": {
    "location": {
      "type": "string",
      "description": "City name"
    }
  },
  "required": ["location"]
}`}
                  />
                  <p className="text-xs text-white/40 light:text-black/40">
                    Optional JSON Schema defining the function's parameters for
                    tool use.
                  </p>
                </div>
              )}
            </div>
          )}

          {step === 'config' && (
            <div className="space-y-4">
              <p className="text-sm text-white/50 light:text-black/50">
                Configure your{' '}
                {mode === ExecutionMode.WEBHOOK
                  ? 'webhook'
                  : mode === ExecutionMode.PROXY
                    ? 'proxy'
                    : 'isolated function'}
                .
              </p>

              {mode === ExecutionMode.WEBHOOK && (
                <>
                  <div className="space-y-2">
                    <Label htmlFor="webhookUrl">URL *</Label>
                    <Input
                      id="webhookUrl"
                      placeholder="https://api.example.com/weather"
                      value={webhookUrl}
                      onChange={(e) => setWebhookUrl(e.target.value)}
                      required
                      className="bg-brand-main-800 border-brand-main-600"
                    />
                  </div>

                  <div className="space-y-2">
                    <Label htmlFor="webhookMethod">Method</Label>
                    <Select
                      value={webhookMethod}
                      onValueChange={setWebhookMethod}
                    >
                      <SelectTrigger className="bg-brand-main-800 border-brand-main-600 text-white light:text-brand-main-50">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent className="bg-brand-main-800 border-brand-main-600 text-white light:text-brand-main-50">
                        {HTTP_METHODS.map((method) => (
                          <SelectItem key={method} value={method}>
                            {method}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>

                  <div className="space-y-2">
                    <Label htmlFor="webhookHeaders">Headers (JSON)</Label>
                    <JSONEditor
                      value={webhookHeaders}
                      onChange={setWebhookHeaders}
                      height="100px"
                      placeholder={`{"Authorization": "Bearer token"}`}
                    />
                  </div>

                  <div className="space-y-2">
                    <Label htmlFor="webhookTimeout">Timeout (ms)</Label>
                    <Input
                      id="webhookTimeout"
                      type="number"
                      placeholder="30000"
                      value={webhookTimeout}
                      onChange={(e) => setWebhookTimeout(e.target.value)}
                      className="bg-brand-main-800 border-brand-main-600"
                    />
                  </div>
                </>
              )}

              {mode === ExecutionMode.PROXY && (
                <>
                  <div className="space-y-2">
                    <Label htmlFor="proxyBaseUrl">Base URL *</Label>
                    <Input
                      id="proxyBaseUrl"
                      placeholder="https://api.example.com"
                      value={proxyBaseUrl}
                      onChange={(e) => setProxyBaseUrl(e.target.value)}
                      required
                      className="bg-brand-main-800 border-brand-main-600"
                    />
                  </div>

                  <div className="space-y-2">
                    <Label htmlFor="proxyPath">Path</Label>
                    <Input
                      id="proxyPath"
                      placeholder="/v1/weather"
                      value={proxyPath}
                      onChange={(e) => setProxyPath(e.target.value)}
                      className="bg-brand-main-800 border-brand-main-600"
                    />
                  </div>

                  <div className="space-y-2">
                    <Label htmlFor="proxyMethod">Method</Label>
                    <Select value={proxyMethod} onValueChange={setProxyMethod}>
                      <SelectTrigger className="bg-brand-main-800 border-brand-main-600 text-white light:text-brand-main-50">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent className="bg-brand-main-800 border-brand-main-600 text-white light:text-brand-main-50">
                        {HTTP_METHODS.map((method) => (
                          <SelectItem key={method} value={method}>
                            {method}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                </>
              )}

              {mode === ExecutionMode.ISOLATED && (
                <>
                  <div className="space-y-2">
                    <Label htmlFor="runtime">Runtime *</Label>
                    <Select value={runtime} onValueChange={setRuntime}>
                      <SelectTrigger className="bg-brand-main-800 border-brand-main-600 text-white light:text-brand-main-50">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent className="bg-brand-main-800 border-brand-main-600 text-white light:text-brand-main-50">
                        <SelectItem value="nodejs20">Node.js 20</SelectItem>
                        <SelectItem value="deno">Deno (TypeScript)</SelectItem>
                        <SelectItem value="python3">Python 3</SelectItem>
                      </SelectContent>
                    </Select>
                  </div>

                  <div className="space-y-2">
                    <Label>Code *</Label>
                    <CodeEditor
                      value={code}
                      onChange={setCode}
                      language={
                        runtime ==='deno'
                          ? 'typescript'
                          : runtime === 'python3'
                            ? 'python'
                            : 'javascript'
                      }
                      height="250px"
                      placeholder={
                        runtime === 'python3'
                          ? 'async def handler(args: dict) -> dict:\n    return {"result": "hello"}'
                          : runtime === 'deno'
                            ? 'export default async function(args: Record<string, unknown>) {\n    return { result: "hello" };\n}'
                            : 'export default async function(args) {\n    return { result: "hello" };\n}'
                      }
                    />
                  </div>

                  <div className="space-y-2">
                    <Label htmlFor="packages">Packages</Label>
                    <Textarea
                      id="packages"
                      placeholder={
                        runtime === 'python3'
                          ? 'requests\nnumpy\npandas'
                          : runtime === 'deno'
                            ? 'Deno uses URL imports — no packages needed'
                            : 'axios\nlodash\nnode-fetch'
                      }
                      value={packages}
                      onChange={(e) => setPackages(e.target.value)}
                      className="bg-brand-main-800 border-brand-main-600 min-h-[80px] font-mono text-sm"
                    />
                    <p className="text-xs text-white/40 light:text-black/40">
                      {runtime ==='python3'
                        ? 'Pip packages, one per line or comma-separated'
                        : runtime === 'deno'
                          ? 'Deno uses URL imports in code — no package list needed'
                          : 'npm packages, one per line or comma-separated'}
                    </p>
                  </div>

                  <div className="space-y-2">
                    <Label htmlFor="dockerHost">Docker Host</Label>
                    <Input
                      id="dockerHost"
                      placeholder="Leave empty for default (local Docker)"
                      value={dockerHost}
                      onChange={(e) => setDockerHost(e.target.value)}
                      className="bg-brand-main-800 border-brand-main-600"
                    />
                    <p className="text-xs text-white/40 light:text-black/40">
                      Optional: specify a remote Docker host (e.g.,
                      tcp://remote:2375)
                    </p>
                  </div>
                </>
              )}
            </div>
          )}

          {step ==='advanced' && (
            <div className="space-y-4">
              <p className="text-sm text-white/50 light:text-black/50">
                Configure advanced execution settings (optional).
              </p>

              <div className="space-y-2">
                <Label htmlFor="timeoutMs">Execution Timeout (ms)</Label>
                <Input
                  id="timeoutMs"
                  type="number"
                  placeholder="30000"
                  value={timeoutMs}
                  onChange={(e) => setTimeoutMs(e.target.value)}
                  className="bg-brand-main-800 border-brand-main-600"
                />
                <p className="text-xs text-white/40 light:text-black/40">
                  Maximum time the function can run
                </p>
              </div>

              <div className="space-y-2">
                <Label htmlFor="memoryMb">Memory Limit (MB)</Label>
                <Input
                  id="memoryMb"
                  type="number"
                  placeholder="128"
                  value={memoryMb}
                  onChange={(e) => setMemoryMb(e.target.value)}
                  className="bg-brand-main-800 border-brand-main-600"
                />
                <p className="text-xs text-white/40 light:text-black/40">
                  Memory allocated for the function
                </p>
              </div>

              <div className="space-y-2">
                <Label htmlFor="maxRetries">Max Retries</Label>
                <Input
                  id="maxRetries"
                  type="number"
                  placeholder="3"
                  value={maxRetries}
                  onChange={(e) => setMaxRetries(e.target.value)}
                  className="bg-brand-main-800 border-brand-main-600"
                />
                <p className="text-xs text-white/40 light:text-black/40">
                  Number of retries on failure
                </p>
              </div>
            </div>
          )}

          {step ==='success' && (
            <div className="space-y-4">
              <p className="text-sm text-white/50 light:text-black/50">
                Your function is ready to use with chat completions.
              </p>

              <div className="rounded-lg p-4 border border-brand-main-600 bg-brand-main-800">
                <p className="text-sm">
                  <span className="text-white/50 light:text-black/50">Function ID: </span>
                  <code className="font-mono text-white light:text-brand-main-50">
                    {createdFunctionId}
                  </code>
                </p>
                <p className="text-sm mt-2">
                  <span className="text-white/50 light:text-black/50">Name: </span>
                  <code className="font-mono text-white light:text-brand-main-50">{name}</code>
                </p>
              </div>

              <div className="bg-brand-secondary-500/10 border border-brand-secondary-500/25 rounded-lg p-4">
                <p className="text-sm text-brand-secondary-300">
                  Use this function name in your chat completion's{' '}
                  <code className="font-mono">tools</code> array to enable
                  function calling.
                </p>
              </div>
            </div>
          )}
        </SheetBody>

        {/* Footer */}
        <div className="flex gap-3 px-6 py-4 border-t border-brand-main-700/60 shrink-0 w-full">
          {step === 'mode' && (
            <>
              <Button type="button" variant="outline" className="w-1/2" onClick={handleClose}>
                Cancel
              </Button>
              <Button
                type="button"
                className="w-1/2"
                onClick={() => setStep('basic')}
                disabled={!canProceedFromMode}
              >
                Next
              </Button>
            </>
          )}

          {step === 'basic' && (
            <>
              <Button
                type="button"
                variant="outline"
                className="w-1/2"
                onClick={() => setStep('mode')}
              >
                Back
              </Button>
              <Button
                type="button"
                className="w-1/2"
                onClick={() => setStep('config')}
                disabled={!canProceedFromBasic}
              >
                Next
              </Button>
            </>
          )}

          {step === 'config' && (
            <>
              <Button
                type="button"
                variant="outline"
                className="w-1/2"
                onClick={() => setStep('basic')}
              >
                Back
              </Button>
              <Button
                type="button"
                className="w-1/2"
                onClick={() => setStep('advanced')}
                disabled={!canProceedFromConfig()}
              >
                Next
              </Button>
            </>
          )}

          {step === 'advanced' && (
            <>
              <Button
                type="button"
                variant="outline"
                className="w-1/2"
                onClick={() => setStep('config')}
              >
                Back
              </Button>
              <Button
                type="button"
                className="w-1/2"
                onClick={handleSubmit}
                disabled={createFunctionMutation.isPending}
              >
                {createFunctionMutation.isPending
                  ? 'Creating...'
                  : 'Create'}
              </Button>
            </>
          )}

          {step === 'success' && (
            <Button type="button" className="w-full" onClick={handleClose}>
              Done
            </Button>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}
