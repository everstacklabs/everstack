import { useState } from 'react'
import { ui } from '@everstack/ui'
import { MessageSquarePlus, Plus, Loader2, User } from 'lucide-react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { listTraceAnnotations, createTraceAnnotation } from '@/server/traces'
import { JsonViewer } from '@/ui/json-viewer'

const { Badge, Button } = ui

interface AnnotationsPanelProps {
  traceId: string
  /** Optional: scope new annotations to a specific span/observation. */
  observationId?: string
  viewMode?: 'formatted' | 'json'
}

function formatWhen(ts: unknown): string {
  // proto Timestamp { seconds: bigint, nanos: number } or a Date/string.
  try {
    if (ts && typeof ts === 'object' && 'seconds' in (ts as any)) {
      const secs = Number((ts as any).seconds)
      if (secs > 0) return new Date(secs * 1000).toLocaleString()
    }
    if (typeof ts === 'string' || typeof ts === 'number') {
      const d = new Date(ts)
      if (!Number.isNaN(d.getTime())) return d.toLocaleString()
    }
  } catch {
    /* ignore */
  }
  return ''
}

/**
 * Human annotations on a trace. Backed by the append-only
 * CreateTraceAnnotation / ListTraceAnnotations RPCs (the overlay recorder),
 * which had no UI until now. Mirrors ScoresPanel's shape.
 */
export function AnnotationsPanel({
  traceId,
  observationId,
  viewMode = 'formatted',
}: AnnotationsPanelProps) {
  const queryClient = useQueryClient()
  const [showAddForm, setShowAddForm] = useState(false)
  const [body, setBody] = useState('')

  const { data: annotations = [], isLoading } = useQuery({
    queryKey: ['trace-annotations', traceId],
    queryFn: () => listTraceAnnotations(traceId),
    enabled: !!traceId,
  })

  const createMutation = useMutation({
    mutationFn: (text: string) =>
      createTraceAnnotation({ traceId, observationId, body: text }),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: ['trace-annotations', traceId],
      })
      setShowAddForm(false)
      setBody('')
    },
  })

  const inputCls =
    'w-full bg-brand-main-700/50 text-xs text-brand-main-50 rounded px-2 py-1.5 border border-brand-main-500 focus:border-brand-secondary-500 outline-none transition-colors placeholder:text-brand-main-50 light:text-black light:placeholder:text-black'

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-24">
        <Loader2 className="size-4 animate-spin text-brand-main-50 light:text-black" />
      </div>
    )
  }

  if (viewMode === 'json') {
    return (
      <div className="rounded border border-brand-main-500 bg-brand-main-900/35 p-3 light:bg-white/70">
        <JsonViewer
          data={{
            traceId,
            observationId: observationId || null,
            annotations,
          }}
        />
      </div>
    )
  }

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-1.5">
          <MessageSquarePlus className="h-3.5 w-3.5 text-brand-main-200" />
          <span className="text-xs font-medium text-brand-main-50 light:text-black">
            Annotations
          </span>
          <Badge
            variant="outline"
            className="text-[9px] py-0 px-1 bg-brand-main-600/20 text-brand-main-50 border-brand-main-500 light:text-black"
          >
            {annotations.length}
          </Badge>
        </div>
        <button
          className="flex items-center gap-1 text-[10px] text-brand-main-50 hover:text-brand-main-50 transition-colors light:text-black light:hover:text-black"
          onClick={() => setShowAddForm(!showAddForm)}
        >
          <Plus className="h-3 w-3" />
          Add
        </button>
      </div>

      {showAddForm && (
        <form
          onSubmit={(e) => {
            e.preventDefault()
            if (body.trim()) createMutation.mutate(body.trim())
          }}
          className="space-y-1.5"
        >
          <textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder="Add a note about this trace..."
            rows={3}
            className={inputCls + ' resize-y'}
            autoFocus
          />
          <div className="flex justify-end gap-1.5">
            <button
              type="button"
              className="px-2 py-1 text-[11px] text-brand-main-50 hover:text-brand-main-50 rounded transition-colors light:text-black light:hover:text-black"
              onClick={() => {
                setShowAddForm(false)
                setBody('')
              }}
            >
              Cancel
            </button>
            <Button
              type="submit"
              size="sm"
              className="h-6 text-[11px] px-2.5"
              disabled={!body.trim() || createMutation.isPending}
            >
              {createMutation.isPending ? (
                <Loader2 className="h-3 w-3 animate-spin" />
              ) : (
                'Save'
              )}
            </Button>
          </div>
        </form>
      )}

      {annotations.length === 0 && !showAddForm ? (
        <div className="flex flex-col items-center justify-center h-20 text-brand-main-50 gap-1.5 light:text-black">
          <MessageSquarePlus className="size-5" />
          <span className="text-[10px]">No annotations yet</span>
        </div>
      ) : (
        <div className="space-y-1.5">
          {annotations.map((a) => {
            const when = formatWhen((a as any).createdAt)
            return (
              <div
                key={a.id}
                className="rounded-md border border-brand-main-500 bg-brand-main-600/10 px-2.5 py-2"
              >
                <div className="text-xs text-brand-main-50 whitespace-pre-wrap break-words light:text-black">
                  {a.body}
                </div>
                <div className="mt-1 flex items-center gap-2 text-[10px] text-brand-main-50 light:text-black">
                  {a.authorUserId && (
                    <span className="flex items-center gap-1">
                      <User className="h-2.5 w-2.5" />
                      {a.authorUserId}
                    </span>
                  )}
                  {when && <span>{when}</span>}
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
