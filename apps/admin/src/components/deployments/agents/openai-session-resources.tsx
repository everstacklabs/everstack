import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ui } from '@everstack/ui'
import {
  openAIRequest,
  type OpenAIApproval,
  type OpenAIArtifact,
  type OpenAICapabilities,
  type OpenAIUsage,
} from '@/server/openai-agents'

const { Button, Input } = ui
const message = (error: unknown) =>
  error instanceof Error
    ? error.message
    : 'Request failed. Refresh to check the current state.'

export function OpenAISessionResources({
  sessionId,
  scope,
  sandbox,
  capabilities,
}: {
  sessionId: string
  scope: string
  sandbox: boolean
  capabilities: OpenAICapabilities
}) {
  const base = `/sessions/${encodeURIComponent(sessionId)}`
  const key = ['openai-agents', scope, 'resources', sessionId]
  const cache = useQueryClient()
  const [file, setFile] = useState<File | null>(null)
  const [destination, setDestination] = useState('')
  const [exportPath, setExportPath] = useState('')
  const filesEnabled = sandbox && capabilities.file_operations.length > 0
  const approvals = useQuery({
    queryKey: [...key, 'approvals'],
    queryFn: () => openAIRequest<{ data: OpenAIApproval[] }>('/approvals'),
    enabled: capabilities.tool_types.length > 0,
    refetchInterval: 3000,
    retry: false,
  })
  const artifacts = useQuery({
    queryKey: [...key, 'artifacts'],
    queryFn: () =>
      openAIRequest<{ data: OpenAIArtifact[] }>(base + '/artifacts'),
    enabled: filesEnabled,
    refetchInterval: 5000,
    retry: false,
  })
  const usage = useQuery({
    queryKey: [...key, 'usage'],
    queryFn: () => openAIRequest<OpenAIUsage>(base + '/usage'),
    refetchInterval: 5000,
    retry: false,
  })
  const refresh = () => cache.invalidateQueries({ queryKey: key })
  const review = useMutation({
    mutationFn: ({
      id,
      decision,
    }: {
      id: string
      decision: 'approved' | 'denied'
    }) =>
      openAIRequest(`/approvals/${encodeURIComponent(id)}/resolve`, 'POST', {
        decision,
      }),
    onSuccess: refresh,
    retry: false,
  })
  const upload = useMutation({
    mutationFn: async () => {
      if (!file) throw new Error('Select a file first.')
      if (file.size > 1024 * 1024) throw new Error('Choose a file up to 1 MiB.')
      const bytes = new Uint8Array(await file.arrayBuffer())
      let binary = ''
      for (const byte of bytes) binary += String.fromCharCode(byte)
      return openAIRequest<OpenAIArtifact>(base + '/inputs', 'POST', {
        filename: file.name,
        content_type: file.type || 'application/octet-stream',
        content_base64: btoa(binary),
        ...(destination.trim() ? { destination: destination.trim() } : {}),
      })
    },
    onSuccess: refresh,
    retry: false,
  })
  const exportFile = useMutation({
    mutationFn: () =>
      openAIRequest<OpenAIArtifact>(base + '/artifacts/export', 'POST', {
        path: exportPath,
      }),
    onSuccess: refresh,
    retry: false,
  })
  const remove = useMutation({
    mutationFn: (id: string) =>
      openAIRequest(base + '/artifacts/' + encodeURIComponent(id), 'DELETE'),
    onSuccess: refresh,
    retry: false,
  })
  const download = useMutation({
    mutationFn: async (id: string) => {
      const result = await openAIRequest<{ url: string }>(
        base + '/artifacts/' + encodeURIComponent(id) + '/download',
      )
      const url = new URL(result.url)
      if (!['https:', 'http:'].includes(url.protocol))
        throw new Error('The download address is invalid.')
      const link = document.createElement('a')
      link.href = url.href
      link.rel = 'noreferrer'
      link.target = '_blank'
      link.click()
    },
    retry: false,
  })
  const errors = [
    approvals.error,
    artifacts.error,
    usage.error,
    review.error,
    upload.error,
    exportFile.error,
    remove.error,
    download.error,
  ].filter(Boolean)
  return (
    <div className="space-y-4">
      {approvals.data?.data
        .filter((item) => item.session_id === sessionId)
        .map((item) => (
          <section
            key={item.id}
            className="space-y-2 rounded-md bg-muted/40 p-3"
          >
            <h4 className="text-sm font-medium">
              Approval needed: {item.tool_name}
            </h4>
            {item.arguments && (
              <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-words text-xs">
                {JSON.stringify(item.arguments, null, 2)}
              </pre>
            )}
            <p className="text-xs text-muted-foreground">
              Expires {new Date(item.expires_at).toLocaleString()}
            </p>
            <div className="flex gap-2">
              <Button
                size="sm"
                disabled={
                  !capabilities.can_resolve_approvals || review.isPending
                }
                onClick={() =>
                  review.mutate({ id: item.id, decision: 'approved' })
                }
              >
                Approve
              </Button>
              <Button
                size="sm"
                variant="outline"
                disabled={
                  !capabilities.can_resolve_approvals || review.isPending
                }
                onClick={() =>
                  review.mutate({ id: item.id, decision: 'denied' })
                }
              >
                Deny
              </Button>
            </div>
          </section>
        ))}
      {filesEnabled && (
        <details className="space-y-3 text-sm">
          <summary className="cursor-pointer font-medium">Files</summary>
          {artifacts.isPending && <p role="status">Loading retained files…</p>}
          {artifacts.data?.data.length === 0 && (
            <p className="text-muted-foreground">No retained files yet.</p>
          )}
          {artifacts.data?.data.map((item) => (
            <div
              className="flex flex-wrap items-center gap-2"
              key={item.object_id}
            >
              <span className="min-w-0 flex-1 break-words">
                {item.filename}{' '}
                <span className="text-muted-foreground">
                  ({item.size_bytes.toLocaleString()} bytes)
                </span>
              </span>
              <Button
                size="sm"
                variant="ghost"
                disabled={download.isPending}
                onClick={() => download.mutate(item.object_id)}
              >
                Download
              </Button>
              <Button
                size="sm"
                variant="ghost"
                disabled={!capabilities.can_run_sessions || remove.isPending}
                onClick={() => {
                  if (window.confirm(`Delete retained file ${item.filename}?`))
                    remove.mutate(item.object_id)
                }}
              >
                Delete file
              </Button>
            </div>
          ))}
          {sandbox && capabilities.can_run_sessions && (
            <>
              <form
                className="space-y-2"
                onSubmit={(event) => {
                  event.preventDefault()
                  upload.mutate()
                }}
              >
                <label className="block space-y-1">
                  <span>Input file (up to 1 MiB)</span>
                  <Input
                    type="file"
                    onChange={(event) => {
                      setFile(event.target.files?.[0] ?? null)
                      upload.reset()
                    }}
                  />
                </label>
                <label className="block space-y-1">
                  <span>Workspace destination (optional)</span>
                  <Input
                    value={destination}
                    onChange={(event) => setDestination(event.target.value)}
                    placeholder="/workspace/input.txt"
                  />
                </label>
                <Button
                  type="submit"
                  size="sm"
                  disabled={!file || upload.isPending}
                >
                  {upload.isPending ? 'Uploading…' : 'Upload input'}
                </Button>
                {upload.isSuccess && <p role="status">Input uploaded.</p>}
              </form>
              <form
                className="space-y-2"
                onSubmit={(event) => {
                  event.preventDefault()
                  exportFile.mutate()
                }}
              >
                <label className="block space-y-1">
                  <span>Output file to retain</span>
                  <Input
                    value={exportPath}
                    onChange={(event) => {
                      setExportPath(event.target.value)
                      exportFile.reset()
                    }}
                    placeholder="/workspace/result.csv"
                  />
                </label>
                <Button
                  type="submit"
                  size="sm"
                  disabled={!exportPath.trim() || exportFile.isPending}
                >
                  {exportFile.isPending ? 'Exporting…' : 'Export file'}
                </Button>
                {exportFile.isSuccess && (
                  <p role="status">
                    File retained. It is available in the list above.
                  </p>
                )}
              </form>
            </>
          )}
        </details>
      )}
      <section
        className="text-xs text-muted-foreground"
        aria-label="OpenAI model usage"
      >
        {usage.isPending ? (
          'Loading model usage…'
        ) : usage.error ? (
          'Model usage could not be loaded.'
        ) : usage.data?.status === 'reported' ? (
          <>
            Reported model tokens: input{' '}
            {usage.data.input_tokens?.toLocaleString() ?? 'unavailable'}, output{' '}
            {usage.data.output_tokens?.toLocaleString() ?? 'unavailable'}, total{' '}
            {usage.data.total_tokens?.toLocaleString() ?? 'unavailable'}.
          </>
        ) : (
          'Model usage is unavailable. OpenAI has not provided a usage report.'
        )}
        <p>Sandbox and retained storage usage are accounted for separately.</p>
        {usage.data?.updated_at && (
          <p>
            Last reported {new Date(usage.data.updated_at).toLocaleString()}.
          </p>
        )}
      </section>
      {errors.map((error, i) => (
        <p
          key={i}
          role="alert"
          className="text-sm text-red-400 light:text-red-700"
        >
          {message(error)}
        </p>
      ))}
    </div>
  )
}
