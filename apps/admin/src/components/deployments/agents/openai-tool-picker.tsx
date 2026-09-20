import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useSession } from '@/hooks/auth/use-auth'
import { ExecutionMode, listFunctions } from '@/server/functions'
import { getApiBaseUrl } from '@/lib/api-url'
import { listFederatedTools } from '@/server/mcp'
import type { OpenAIToolSpec } from '@/server/openai-agents'

export function OpenAIToolPicker({
  supported,
  value,
  onChange,
  disabled,
}: {
  supported: string[]
  value: OpenAIToolSpec[]
  onChange: (tools: OpenAIToolSpec[]) => void
  disabled: boolean
}) {
  const [open, setOpen] = useState(false)
  const { data: session } = useSession()
  const orgId = session?.user?.organizations?.[0]?.id ?? ''
  const functions = useQuery({
    queryKey: ['openai-tool-picker', getApiBaseUrl(), orgId, 'functions'],
    queryFn: () =>
      listFunctions({ tenantId: orgId, enabled: true, limit: 100 }),
    enabled: open && !!orgId && supported.includes('function'),
    retry: false,
  })
  const mcp = useQuery({
    queryKey: ['openai-tool-picker', getApiBaseUrl(), orgId, 'mcp'],
    queryFn: () => listFederatedTools(orgId),
    enabled: open && !!orgId && supported.includes('mcp'),
    retry: false,
  })
  const options: OpenAIToolSpec[] = [
    ...(functions.data?.functions ?? [])
      .filter(
        (f) =>
          f.mode === ExecutionMode.WEBHOOK || f.mode === ExecutionMode.PROXY,
      )
      .map((f) => ({
        type: 'function' as const,
        resource_id: f.id,
        name: f.name,
      })),
    ...(mcp.data ?? []).map((t) => ({
      type: 'mcp' as const,
      resource_id: t.serverId,
      name: t.name,
    })),
  ]
  const same = (a: OpenAIToolSpec, b: OpenAIToolSpec) =>
    a.type === b.type && a.resource_id === b.resource_id && a.name === b.name
  if (!supported.length) return null
  return (
    <details
      onToggle={(event) => setOpen(event.currentTarget.open)}
      className="space-y-2 text-sm"
    >
      <summary className="cursor-pointer">
        Tools {value.length ? `(${value.length} selected)` : '(optional)'}
      </summary>
      <p className="text-xs text-muted-foreground">
        Select the tools this session may use. Actions that require approval
        pause for your review.
      </p>
      {(functions.isFetching || mcp.isFetching) && (
        <p role="status">Loading tools…</p>
      )}
      {(functions.error || mcp.error) && (
        <p role="alert" className="text-red-400 light:text-red-700">
          Could not load all tools. Close and reopen to retry, or start without
          tools.
        </p>
      )}
      <div className="max-h-48 overflow-auto space-y-2">
        {options.map((option) => (
          <label
            className="flex items-center gap-2"
            key={`${option.type}:${option.resource_id}:${option.name}`}
          >
            <input
              type="checkbox"
              disabled={disabled}
              checked={value.some((item) => same(item, option))}
              onChange={(event) =>
                onChange(
                  event.target.checked
                    ? [...value, option]
                    : value.filter((item) => !same(item, option)),
                )
              }
            />
            <span>
              {option.name}{' '}
              <span className="text-muted-foreground">
                ({option.type === 'function' ? 'Function' : 'MCP'})
              </span>
            </span>
          </label>
        ))}
        {!functions.isFetching &&
          !mcp.isFetching &&
          !functions.error &&
          !mcp.error &&
          !options.length &&
          open && (
            <p className="text-muted-foreground">
              No enabled tools are available.
            </p>
          )}
      </div>
    </details>
  )
}
