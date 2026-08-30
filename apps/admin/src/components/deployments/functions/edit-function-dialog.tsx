import { useState, useEffect } from 'react'
import { ui } from '@everstack/ui'
import { ExecutionMode, type Function } from '@/server/functions'
import {
  useUpdateFunction,
  useFunctions,
} from '@/hooks/deployments/use-functions'
import { toast } from '@everstack/ui/components'
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
  Switch,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} = ui

interface EditFunctionDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  functionData: Function | null
}

const HTTP_METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE']

const MODE_HELP: Record<ExecutionMode, string> = {
  [ExecutionMode.UNSPECIFIED]:
    'Select an execution mode when creating a function.',
  [ExecutionMode.WEBHOOK]:
    'Calls your backend or internal service as a reusable tool.',
  [ExecutionMode.PROXY]:
    'Wraps an upstream API as a reusable tool with request mapping.',
  [ExecutionMode.ISOLATED]:
    'Runs lightweight custom code in an isolated runtime.',
}

export function EditFunctionDialog({
  open,
  onOpenChange,
  functionData,
}: EditFunctionDialogProps) {
  // Basic info
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [enabled, setEnabled] = useState(true)

  // Mode (read-only in edit)
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

  // Isolated config
  const [runtime, setRuntime] = useState('nodejs20')
  const [code, setCode] = useState('')
  const [packages, setPackages] = useState('')
  const [dockerHost, setDockerHost] = useState('')

  // Advanced settings
  const [timeoutMs, setTimeoutMs] = useState('30000')
  const [memoryMb, setMemoryMb] = useState('128')
  const [maxRetries, setMaxRetries] = useState('3')

  // Parameters (JSON Schema)
  const [parametersJson, setParametersJson] = useState('')

  const updateFunctionMutation = useUpdateFunction()
  const listFunctionsQuery = useFunctions()

  // Populate form when functionData changes
  useEffect(() => {
    if (functionData) {
      setName(functionData.name)
      setDescription(functionData.description || '')
      setEnabled(functionData.enabled)
      setMode(functionData.mode)
      setTimeoutMs(functionData.timeoutMs?.toString() || '30000')
      setMemoryMb(functionData.memoryMb?.toString() || '128')
      setMaxRetries(functionData.maxRetries?.toString() || '3')

      if (functionData.parameters) {
        setParametersJson(JSON.stringify(functionData.parameters, null, 2))
      } else {
        setParametersJson('')
      }

      if (functionData.webhook) {
        setWebhookUrl(functionData.webhook.url)
        setWebhookMethod(functionData.webhook.method)
        setWebhookHeaders(
          Object.keys(functionData.webhook.headers).length > 0
            ? JSON.stringify(functionData.webhook.headers, null, 2)
            : '',
        )
        setWebhookTimeout(functionData.webhook.timeoutMs?.toString() || '30000')
      }

      if (functionData.proxy) {
        setProxyBaseUrl(functionData.proxy.baseUrl)
        setProxyPath(functionData.proxy.path)
        setProxyMethod(functionData.proxy.method)
      }

      if (functionData.isolated) {
        setRuntime(functionData.isolated.runtime || 'nodejs20')
        setCode(functionData.isolated.code || '')
        setPackages((functionData.isolated.packages ?? []).join('\n'))
        setDockerHost(functionData.isolated.dockerHost || '')
      }
    }
  }, [functionData])

  const parseHeaders = (headersStr: string): { [key: string]: string } => {
    if (!headersStr.trim()) return {}
    try {
      return JSON.parse(headersStr)
    } catch {
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
    if (!functionData) return

    try {
      const params = parseParameters()

      await updateFunctionMutation.mutateAsync({
        id: functionData.id,
        name: name.trim(),
        description: description.trim() || undefined,
        enabled,
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

      toast.success('Function updated successfully!')
      onOpenChange(false)
      listFunctionsQuery.refetch()
    } catch (error) {
      console.error('Failed to update function:', error)
      toast.error('Failed to update function')
    }
  }

  const getModeLabel = (mode: ExecutionMode): string => {
    switch (mode) {
      case ExecutionMode.WEBHOOK:
        return 'Webhook'
      case ExecutionMode.PROXY:
        return 'Proxy'
      case ExecutionMode.ISOLATED:
        return 'Isolated'
      default:
        return 'Unknown'
    }
  }

  const isValid =
    name.trim().length > 0 &&
    ((mode === ExecutionMode.WEBHOOK && webhookUrl.trim().length > 0) ||
      (mode === ExecutionMode.PROXY && proxyBaseUrl.trim().length > 0) ||
      (mode === ExecutionMode.ISOLATED && code.trim().length > 0))

  if (!functionData) return null

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="flex h-[100vh] w-full flex-col overflow-hidden sm:max-w-[620px]"
      >
        <SheetHeader>
          <SheetTitle className="flex items-center gap-2">
            {functionData.name}
            <span
              className={`rounded px-1.5 py-0.5 text-[10px] font-medium ${
                enabled
                  ? 'bg-emerald-500/15 text-emerald-300 light:text-emerald-600 border border-emerald-500/25'
                  : 'bg-brand-main-500/30 text-brand-main-200 border border-brand-main-500/25'
              }`}
            >
              {enabled ? 'Active' : 'Disabled'}
            </span>
            <span className="rounded px-1.5 py-0.5 text-[10px] font-medium bg-brand-secondary-600/15 text-brand-secondary-300 border border-brand-secondary-500/25">
              {getModeLabel(mode)}
            </span>
          </SheetTitle>
          <p className="text-sm leading-relaxed text-white/55 light:text-black/55">
            {MODE_HELP[mode]}
          </p>
        </SheetHeader>

        <SheetBody className="flex-1 overflow-y-auto py-4 scrollbar-macos">
          <Tabs defaultValue="details" className="space-y-4">
            <TabsList>
              <TabsTrigger value="details">Details</TabsTrigger>
              <TabsTrigger value="config">Configuration</TabsTrigger>
              <TabsTrigger value="advanced">Advanced</TabsTrigger>
            </TabsList>

            <TabsContent value="details" className="mt-0 space-y-4">
              <div className="flex items-center justify-between rounded-lg border border-brand-main-600 bg-brand-main-800 p-3">
                <div>
                  <Label>Enabled</Label>
                  <p className="text-xs text-white/40 light:text-black/40">
                    Function is available to agents and workflows when selected
                  </p>
                </div>
                <Switch checked={enabled} onCheckedChange={setEnabled} />
              </div>

              <div className="space-y-2">
                <Label htmlFor="name">Name *</Label>
                <Input
                  id="name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  required
                  className="bg-brand-main-800 border-brand-main-600"
                />
              </div>

              <div className="space-y-2">
                <Label htmlFor="description">Description</Label>
                <Textarea
                  id="description"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  className="bg-brand-main-800 border-brand-main-600 min-h-[80px]"
                />
                <p className="text-xs text-white/40 light:text-black/40">
                  Describe when the model should use this function and what
                  outcome it returns.
                </p>
              </div>

              <div className="space-y-2">
                <Label>Mode</Label>
                <div className="px-3 py-2 rounded border border-brand-main-600 bg-brand-main-800 text-white/50 light:text-black/50 text-sm">
                  {getModeLabel(mode)}
                </div>
                <p className="text-xs text-white/40 light:text-black/40">
                  {MODE_HELP[mode]} Execution mode cannot be changed after
                  creation.
                </p>
              </div>

              {mode !== ExecutionMode.ISOLATED && (
                <div className="space-y-2">
                  <Label htmlFor="parameters">Parameters (JSON Schema)</Label>
                  <JSONEditor
                    value={parametersJson}
                    onChange={setParametersJson}
                    height="160px"
                  />
                  <p className="text-xs text-white/40 light:text-black/40">
                    Keep parameter schemas tight so the runtime only exposes the
                    inputs this tool really needs.
                  </p>
                </div>
              )}
            </TabsContent>

            <TabsContent value="config" className="mt-0 space-y-4">
              {mode === ExecutionMode.WEBHOOK && (
                <>
                  <div className="space-y-2">
                    <Label htmlFor="webhookUrl">URL *</Label>
                    <Input
                      id="webhookUrl"
                      value={webhookUrl}
                      onChange={(e) => setWebhookUrl(e.target.value)}
                      required
                      className="bg-brand-main-800 border-brand-main-600"
                    />
                  </div>

                  <div className="grid grid-cols-2 gap-4">
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
                      <Label htmlFor="webhookTimeout">Timeout (ms)</Label>
                      <Input
                        id="webhookTimeout"
                        type="number"
                        value={webhookTimeout}
                        onChange={(e) => setWebhookTimeout(e.target.value)}
                        className="bg-brand-main-800 border-brand-main-600"
                      />
                    </div>
                  </div>

                  <div className="space-y-2">
                    <Label htmlFor="webhookHeaders">Headers (JSON)</Label>
                    <JSONEditor
                      value={webhookHeaders}
                      onChange={setWebhookHeaders}
                      height="100px"
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
                      value={proxyBaseUrl}
                      onChange={(e) => setProxyBaseUrl(e.target.value)}
                      required
                      className="bg-brand-main-800 border-brand-main-600"
                    />
                  </div>

                  <div className="grid grid-cols-2 gap-4">
                    <div className="space-y-2">
                      <Label htmlFor="proxyPath">Path</Label>
                      <Input
                        id="proxyPath"
                        value={proxyPath}
                        onChange={(e) => setProxyPath(e.target.value)}
                        className="bg-brand-main-800 border-brand-main-600"
                      />
                    </div>

                    <div className="space-y-2">
                      <Label htmlFor="proxyMethod">Method</Label>
                      <Select
                        value={proxyMethod}
                        onValueChange={setProxyMethod}
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
                        runtime === 'deno'
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
                      {runtime === 'python3'
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
            </TabsContent>

            <TabsContent value="advanced" className="mt-0 space-y-4">
              <div className="space-y-2">
                <Label htmlFor="timeoutMs">Execution Timeout (ms)</Label>
                <Input
                  id="timeoutMs"
                  type="number"
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
                  value={maxRetries}
                  onChange={(e) => setMaxRetries(e.target.value)}
                  className="bg-brand-main-800 border-brand-main-600"
                />
                <p className="text-xs text-white/40 light:text-black/40">
                  Number of retries on failure
                </p>
              </div>
            </TabsContent>
          </Tabs>
        </SheetBody>

        {/* Footer */}
        <div className="flex gap-3 px-6 py-4 border-t border-brand-main-700/60 shrink-0 w-full">
          <Button
            type="button"
            variant="outline"
            className="w-1/2"
            onClick={() => onOpenChange(false)}
            disabled={updateFunctionMutation.isPending}
          >
            Cancel
          </Button>
          <Button
            type="button"
            className="w-1/2"
            onClick={handleSubmit}
            disabled={updateFunctionMutation.isPending || !isValid}
          >
            {updateFunctionMutation.isPending ? 'Updating...' : 'Update'}
          </Button>
        </div>
      </SheetContent>
    </Sheet>
  )
}
