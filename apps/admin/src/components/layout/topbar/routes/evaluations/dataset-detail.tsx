import { useState, useCallback, useRef } from 'react'
import { Link, useLocation } from '@tanstack/react-router'
import { Button } from '@everstack/ui/components'
import { ui } from '@everstack/ui'
import { type ActionGroup } from '@/components/layout/topbar/types'
import {
  useDataset,
  useCreateDatasetItem,
  useCreateDatasetItemBatch,
} from '@/hooks/evaluations/use-datasets'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { usePermissions } from '@/hooks/auth'
import { usePermissionSet } from '@/hooks/authz/use-permission-set'
import { Icon } from '@iconify/react'
import { streamTraces, getTrace, getTraceByID } from '@/server/traces'
import { create } from '@everstack/client'
import { ListTracesRequestSchema } from '@everstack/proto/everstack/traces/v1/traces_service_pb'
import type { Trace } from '@everstack/proto/everstack/traces/v1/traces_pb'
import { DatasetJsonEditorDropdown } from '@/components/evaluations/dataset-json-editor-dropdown'
import {
  formatDuration,
  formatCost,
  formatTokensCompact,
  truncateText,
  safeJsonParse,
  safeBigIntToNumber,
} from '@/utils/trace-formatters'
import { ProviderDisplay } from '@/components/providers/provider-icon'
import {
  evaluationErrorClass,
  evaluationInputClass,
  evaluationPanelClass,
  evaluationSheetBodyClass,
  evaluationSheetContentClass,
  evaluationSheetFooterClass,
  evaluationSheetSplitFooterClass,
} from '@/components/evaluations/evaluation-form'

const {
  Label,
  Input,
  Badge,
  Checkbox,
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetBody,
  SheetFooter,
} = ui

function DatasetBreadcrumb() {
  const { pathname } = useLocation()
  const segments = pathname.split('/').filter(Boolean)
  const datasetId = segments.length > 1 ? segments[segments.length - 1] : ''

  const { data: dataset, isLoading } = useDataset(datasetId)

  return (
    <div className="flex items-center gap-1.5">
      <Link
        to="/evaluations/datasets"
        className="text-sm font-normal text-brand-main-300 hover:text-white/80 light:hover:text-black/80 transition-colors"
      >
        Datasets
      </Link>
      {datasetId && (
        <>
          <span className="text-brand-main-400 text-sm">/</span>
          {isLoading ? (
            <span className="inline-block h-4 w-24 rounded bg-white/10 light:bg-black/10 animate-pulse" />
          ) : (
            <span className="text-sm text-white light:text-brand-main-50 font-normal">
              {dataset?.name ?? datasetId}
            </span>
          )}
        </>
      )}
    </div>
  )
}

function useCanEditDataset(datasetId: string): boolean {
  const { can } = usePermissions()
  const { permissions } = usePermissionSet(
    datasetId
      ? [{ permission: 'resource:edit', object: `dataset:${datasetId}` }]
      : [],
  )
  return (
    can('resource:edit') ||
    permissions.has('resource:edit', `dataset:${datasetId}`)
  )
}

// ─── Add Item Button ────────────────────────────────────────────────

function AddItemButton() {
  const gate = useFeatureGate(FeatureKey.EVALUATIONS)
  const { pathname } = useLocation()
  const datasetId = pathname.split('/').filter(Boolean).pop() ?? ''
  const canEdit = useCanEditDataset(datasetId)
  const createItemMutation = useCreateDatasetItem()
  const batchMutation = useCreateDatasetItemBatch()
  const [open, setOpen] = useState(false)
  const [inputJson, setInputJson] = useState('{}')
  const [expectedOutputJson, setExpectedOutputJson] = useState('{}')
  const [inputOpen, setInputOpen] = useState(false)
  const [expectedOutputOpen, setExpectedOutputOpen] = useState(false)
  const [isDragging, setIsDragging] = useState(false)
  const [fileItems, setFileItems] = useState<
    Array<{ input: any; expectedOutput?: any }>
  >([])
  const [fileError, setFileError] = useState<string | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  if (gate.isBlocked || !canEdit) return null

  const handleSubmitManual = async () => {
    setFileError(null)
    try {
      const input = JSON.parse(inputJson)
      const expectedOutput = expectedOutputJson.trim()
        ? JSON.parse(expectedOutputJson)
        : undefined
      await createItemMutation.mutateAsync({ datasetId, input, expectedOutput })
      resetDraft()
      setOpen(false)
    } catch (err) {
      if (err instanceof SyntaxError) {
        setFileError('Invalid JSON in input or expected output')
      }
    }
  }

  const handleSubmitBatch = async () => {
    if (fileItems.length === 0) return
    await batchMutation.mutateAsync({ datasetId, items: fileItems })
    resetDraft()
    setOpen(false)
  }

  const parseFile = useCallback((file: File) => {
    setFileError(null)
    const reader = new FileReader()
    reader.onload = (e) => {
      try {
        const text = e.target?.result as string
        const parsed = JSON.parse(text)
        const items = Array.isArray(parsed) ? parsed : [parsed]
        const validated = items.map((item: any) => {
          if (!item.input || typeof item.input !== 'object') {
            throw new Error('Each item must have an "input" object field')
          }
          return {
            input: item.input,
            expectedOutput: item.expectedOutput ?? item.expected_output,
            metadata: item.metadata,
          }
        })
        setFileItems(validated)
      } catch (err) {
        setFileError(
          err instanceof Error ? err.message : 'Failed to parse JSON file',
        )
        setFileItems([])
      }
    }
    reader.readAsText(file)
  }, [])

  const handleDrop = useCallback(
    (e: React.DragEvent) => {
      e.preventDefault()
      setIsDragging(false)
      const file = e.dataTransfer.files[0]
      if (
        file &&
        (file.type === 'application/json' ||
          file.name.endsWith('.json') ||
          file.name.endsWith('.jsonl'))
      ) {
        parseFile(file)
      } else {
        setFileError('Please drop a .json or .jsonl file')
      }
    },
    [parseFile],
  )

  const resetDraft = () => {
    setInputJson('{}')
    setExpectedOutputJson('{}')
    setInputOpen(false)
    setExpectedOutputOpen(false)
    setFileItems([])
    setFileError(null)
  }

  const error = createItemMutation.error || batchMutation.error

  return (
    <>
      <Button variant="default" onClick={() => setOpen(true)}>
        Add Item
      </Button>
      <Sheet open={open} onOpenChange={setOpen}>
        <SheetContent
          side="right"
          className={`${evaluationSheetContentClass} sm:max-w-[480px]`}
        >
          <SheetHeader>
            <SheetTitle>Add Dataset Items</SheetTitle>
          </SheetHeader>

          <SheetBody className={evaluationSheetBodyClass}>
            {(error || fileError) && (
              <div className={evaluationErrorClass}>
                {fileError ?? (error as Error)?.message}
              </div>
            )}

            {/* Drag & drop zone */}
            <div
              onDragOver={(e) => {
                e.preventDefault()
                setIsDragging(true)
              }}
              onDragLeave={(e) => {
                e.preventDefault()
                setIsDragging(false)
              }}
              onDrop={handleDrop}
              onClick={() => fileInputRef.current?.click()}
              className={`rounded border border-dashed p-5 text-center cursor-pointer transition-colors ${
                isDragging
                  ? 'border-brand-secondary-500 bg-brand-secondary-500/10'
                  : fileItems.length > 0
                    ? 'border-emerald-500/50 bg-emerald-500/5'
                    : 'border-brand-main-600 bg-brand-main-950/60 hover:border-brand-main-500'
              }`}
            >
              <input
                ref={fileInputRef}
                type="file"
                accept=".json,.jsonl"
                className="hidden"
                onChange={(e) => {
                  const f = e.target.files?.[0]
                  if (f) parseFile(f)
                }}
              />
              {fileItems.length > 0 ? (
                <div className="space-y-1">
                  <Icon
                    icon="lucide:check-circle"
                    className="h-8 w-8 mx-auto text-emerald-400 light:text-emerald-600"
                  />
                  <p className="text-sm text-emerald-400 light:text-emerald-600 font-medium">
                    {fileItems.length} items parsed
                  </p>
                  <p className="text-xs text-white/30 light:text-black/30">
                    Click or drop another file to replace
                  </p>
                </div>
              ) : (
                <div className="space-y-1">
                  <Icon
                    icon="lucide:upload"
                    className="h-8 w-8 mx-auto text-white/30 light:text-black/30"
                  />
                  <p className="text-sm text-white/60 light:text-black/60">
                    Drop a JSON file here or click to browse
                  </p>
                  <p className="text-xs text-white/30 light:text-black/30">
                    Array of objects with "input" and optional "expectedOutput"
                  </p>
                </div>
              )}
            </div>

            {fileItems.length > 0 ? (
              <div
                className={`${evaluationPanelClass} px-3 py-2 text-xs text-white/50 light:text-black/50`}
              >
                Ready to import {fileItems.length} item
                {fileItems.length === 1 ? '' : 's'}.
              </div>
            ) : (
              <>
                <div className="flex items-center gap-3">
                  <div className="h-px flex-1 bg-brand-main-600" />
                  <span className="text-xs text-white/30 light:text-black/30 uppercase tracking-wide">
                    or add manually
                  </span>
                  <div className="h-px flex-1 bg-brand-main-600" />
                </div>

                <div className="space-y-4">
                  <DatasetJsonEditorDropdown
                    label="Input JSON"
                    value={inputJson}
                    onChange={setInputJson}
                    open={inputOpen}
                    onOpenChange={setInputOpen}
                    height="180px"
                  />
                  <DatasetJsonEditorDropdown
                    label="Expected output JSON"
                    value={expectedOutputJson}
                    onChange={setExpectedOutputJson}
                    open={expectedOutputOpen}
                    onOpenChange={setExpectedOutputOpen}
                    height="180px"
                  />
                </div>
              </>
            )}
          </SheetBody>
          <SheetFooter className={evaluationSheetSplitFooterClass}>
            <Button
              type="button"
              variant="outline"
              onClick={resetDraft}
              disabled={createItemMutation.isPending || batchMutation.isPending}
            >
              Reset
            </Button>
            <div className="flex items-center gap-2">
              <Button
                type="button"
                variant="outline"
                onClick={() => setOpen(false)}
                disabled={
                  createItemMutation.isPending || batchMutation.isPending
                }
              >
                Cancel
              </Button>
              {fileItems.length > 0 ? (
                <Button
                  variant="default"
                  onClick={handleSubmitBatch}
                  disabled={batchMutation.isPending}
                >
                  {batchMutation.isPending
                    ? 'Importing...'
                    : `Import ${fileItems.length} Item${fileItems.length === 1 ? '' : 's'}`}
                </Button>
              ) : (
                <Button
                  variant="default"
                  onClick={() => void handleSubmitManual()}
                  disabled={createItemMutation.isPending}
                >
                  {createItemMutation.isPending ? 'Adding...' : 'Add Item'}
                </Button>
              )}
            </div>
          </SheetFooter>
        </SheetContent>
      </Sheet>
    </>
  )
}

// ─── Import from Traces ─────────────────────────────────────────────

function getInputPreview(traceInput: string): string {
  if (!traceInput) return 'No input'
  const messages = safeJsonParse<any[]>(traceInput, [])
  if (Array.isArray(messages) && messages.length > 0) {
    const firstMsg = messages[0]
    let content = firstMsg.content || ''
    if (typeof content === 'object' && content !== null) {
      if (Array.isArray(content)) {
        content = content
          .map(
            (block: any) =>
              block.text || block.content || JSON.stringify(block),
          )
          .join(' ')
      } else {
        content = content.text || content.content || JSON.stringify(content)
      }
    }
    return truncateText(String(content), 120)
  }
  return truncateText(traceInput, 120)
}

function ImportFromTracesButton() {
  const gate = useFeatureGate(FeatureKey.EVALUATIONS)
  const { pathname } = useLocation()
  const datasetId = pathname.split('/').filter(Boolean).pop() ?? ''
  const canEdit = useCanEditDataset(datasetId)
  const batchMutation = useCreateDatasetItemBatch()
  const [open, setOpen] = useState(false)
  const [loading, setLoading] = useState(false)
  const [traces, setTraces] = useState<Trace[]>([])
  const [selectedTraceIds, setSelectedTraceIds] = useState<Set<string>>(
    new Set(),
  )
  const [searchLoaded, setSearchLoaded] = useState(false)
  const [limit, setLimit] = useState('50')
  const [previewTrace, setPreviewTrace] = useState<Trace | null>(null)

  if (gate.isBlocked || !canEdit) return null

  const handleSearch = async () => {
    setLoading(true)
    setTraces([])
    setSelectedTraceIds(new Set())
    setPreviewTrace(null)
    try {
      const request = create(ListTracesRequestSchema, {
        limit: parseInt(limit) || 50,
        tenantId: '',
        model: '',
        provider: '',
        statusCode: '',
        correlationId: '',
        userId: '',
        sessionId: '',
        environment: '',
      })
      const collected: Trace[] = []
      for await (const trace of streamTraces(request)) {
        collected.push(trace)
        if (collected.length >= (parseInt(limit) || 50)) break
      }
      setTraces(collected)
      setSearchLoaded(true)
    } catch (err) {
      console.error('Failed to fetch traces:', err)
    } finally {
      setLoading(false)
    }
  }

  const toggleTrace = (traceId: string) => {
    setSelectedTraceIds((prev) => {
      const next = new Set(prev)
      if (next.has(traceId)) next.delete(traceId)
      else next.add(traceId)
      return next
    })
  }

  const toggleAll = () => {
    if (selectedTraceIds.size === traces.length) {
      setSelectedTraceIds(new Set())
    } else {
      setSelectedTraceIds(new Set(traces.map((t) => t.traceId)))
    }
  }

  const handleImport = async () => {
    const selected = traces.filter((t) => selectedTraceIds.has(t.traceId))
    if (selected.length === 0) return

    const items = await Promise.all(
      selected.map(async (t) => {
        let traceInput = t.traceInput
        let traceOutput = t.traceOutput
        let model = t.llmModel || t.requestedModel || ''

        // If traceInput/traceOutput are empty, fetch full trace + spans
        if (!traceInput || !traceOutput) {
          const [full, spans] = await Promise.all([
            getTrace(t.traceId).catch(() => null),
            getTraceByID(t.traceId).catch(() => []),
          ])
          if (full) {
            traceInput = traceInput || full.traceInput
            traceOutput = traceOutput || full.traceOutput
            if (!model) model = full.llmModel || full.requestedModel || ''
          }
          // Extract from root span attributes if still missing
          if ((!traceInput || !traceOutput) && spans.length > 0) {
            const rootSpan = spans.find((s) => !s.parentSpanId) || spans[0]
            const attrs = rootSpan.spanAttributes ?? {}
            if (!traceInput && attrs['trace.input']) {
              traceInput = attrs['trace.input']
            }
            if (!traceOutput && attrs['trace.output']) {
              traceOutput = attrs['trace.output']
            }
            // Try gen_ai / llm attributes as fallback
            if (!traceInput && attrs['gen_ai.request.messages']) {
              traceInput = attrs['gen_ai.request.messages']
            }
            if (!traceOutput && attrs['gen_ai.response.text']) {
              traceOutput = attrs['gen_ai.response.text']
            }
            if (!model && attrs['llm.request.model']) {
              model = attrs['llm.request.model']
            }
            if (!model && attrs['model.requested']) {
              model = attrs['model.requested']
            }
          }
        }

        // Parse and normalize input into an object with expected keys
        let inputObj: Record<string, any>
        if (traceInput) {
          const parsed = safeJsonParse(traceInput, { raw: traceInput })
          if (Array.isArray(parsed)) {
            inputObj = { messages: parsed }
          } else if (typeof parsed === 'string') {
            inputObj = { content: parsed }
          } else if (typeof parsed === 'object' && parsed !== null) {
            inputObj = parsed as Record<string, any>
          } else {
            inputObj = { content: String(parsed) }
          }
        } else {
          inputObj = { traceId: t.traceId }
        }

        // Parse and normalize output
        let outputObj: Record<string, any> | undefined
        if (traceOutput) {
          const parsed = safeJsonParse(traceOutput, { raw: traceOutput })
          if (Array.isArray(parsed)) {
            outputObj = { messages: parsed }
          } else if (typeof parsed === 'string') {
            outputObj = { content: parsed }
          } else if (typeof parsed === 'object' && parsed !== null) {
            outputObj = parsed as Record<string, any>
          } else {
            outputObj = { content: String(parsed) }
          }
        }

        return {
          input: inputObj as any,
          expectedOutput: outputObj as any,
          sourceTraceId: t.traceId,
          metadata: {
            model,
            provider: t.provider || '',
            totalCost: String(t.totalCost ?? 0),
          },
        }
      }),
    )

    await batchMutation.mutateAsync({ datasetId, items })
    resetAndClose()
  }

  const resetAndClose = () => {
    setOpen(false)
    setTraces([])
    setSelectedTraceIds(new Set())
    setSearchLoaded(false)
    setLoading(false)
    setPreviewTrace(null)
  }

  const totalTokens = (t: Trace) => {
    if (!t.tokenBreakdown) return 0
    return safeBigIntToNumber(t.tokenBreakdown.totalTokens)
  }

  return (
    <>
      <Button variant="outline" onClick={() => setOpen(true)}>
        <Icon icon="lucide:arrow-down-to-line" className="h-4 w-4" />
        Import from Traces
      </Button>
      <Sheet open={open} onOpenChange={(v) => !v && resetAndClose()}>
        <SheetContent
          side="right"
          className={`${evaluationSheetContentClass} sm:max-w-2xl`}
        >
          <SheetHeader>
            <SheetTitle>Import from Traces</SheetTitle>
          </SheetHeader>

          {batchMutation.error && (
            <div className={`mx-6 mt-5 ${evaluationErrorClass}`}>
              {(batchMutation.error as Error).message}
            </div>
          )}

          {/* Search controls */}
          <div className="flex items-center gap-2 border-b border-brand-main-700 px-6 py-4 flex-shrink-0">
            <div className="flex items-center gap-2 flex-1">
              <Label className="text-sm font-normal text-white light:text-brand-main-50 whitespace-nowrap">
                Limit
              </Label>
              <Input
                type="number"
                value={limit}
                onChange={(e: React.ChangeEvent<HTMLInputElement>) =>
                  setLimit(e.target.value)
                }
                className={`${evaluationInputClass} w-20`}
              />
            </div>
            <Button variant="default" onClick={handleSearch} disabled={loading}>
              {loading ? (
                <>
                  <Icon
                    icon="lucide:loader-2"
                    className="mr-1.5 h-3.5 w-3.5 animate-spin"
                  />
                  Fetching...
                </>
              ) : (
                'Fetch Traces'
              )}
            </Button>
          </div>

          {/* Traces list */}
          <div className="flex-1 min-h-0 overflow-y-auto scrollbar-macos">
            {!searchLoaded ? (
              <div className="flex flex-col items-center justify-center h-full gap-3 text-white/20 light:text-black/20">
                <Icon icon="lucide:search" className="h-10 w-10" />
                <p className="text-sm">Fetch traces to get started</p>
              </div>
            ) : traces.length === 0 ? (
              <div className="flex flex-col items-center justify-center h-full gap-3 text-white/20 light:text-black/20">
                <Icon icon="lucide:inbox" className="h-10 w-10" />
                <p className="text-sm">No traces found</p>
              </div>
            ) : (
              <div className="divide-y divide-brand-main-800">
                {/* Select all header */}
                <div className="flex items-center gap-3 px-6 py-2 bg-brand-main-950/80 sticky top-0 z-10">
                  <Checkbox
                    checked={
                      selectedTraceIds.size === traces.length &&
                      traces.length > 0
                    }
                    onCheckedChange={toggleAll}
                  />
                  <span className="text-xs text-white/50 light:text-black/50">
                    {selectedTraceIds.size > 0
                      ? `${selectedTraceIds.size} of ${traces.length} selected`
                      : `${traces.length} traces`}
                  </span>
                </div>

                {traces.map((trace) => (
                  <div
                    key={trace.traceId}
                    className={`flex items-start gap-3 px-6 py-3 cursor-pointer transition-colors ${
                      selectedTraceIds.has(trace.traceId)
                        ? 'bg-brand-secondary-600/10'
                        : 'hover:bg-brand-main-900/70'
                    }`}
                    onClick={() => toggleTrace(trace.traceId)}
                  >
                    <div className="pt-0.5">
                      <Checkbox
                        checked={selectedTraceIds.has(trace.traceId)}
                        onCheckedChange={() => toggleTrace(trace.traceId)}
                      />
                    </div>
                    <div className="flex-1 min-w-0 space-y-1.5">
                      {/* Row 1: ID + badges */}
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className="font-mono text-xs text-white light:text-brand-main-50">
                          {trace.traceId.substring(0, 12)}...
                        </span>
                        <Badge
                          variant="secondary"
                          className={`text-[10px] px-1.5 py-0 ${
                            trace.status === 'success'
                              ? 'bg-emerald-500/20 text-emerald-400 light:text-emerald-600'
                              : trace.status === 'error'
                                ? 'bg-red-500/20 text-red-400 light:text-red-600'
                                : 'bg-yellow-500/20 text-yellow-400 light:text-yellow-700'
                          }`}
                        >
                          {trace.status || 'unknown'}
                        </Badge>
                        {(trace.llmModel || trace.requestedModel) && (
                          <Badge
                            variant="secondary"
                            className="bg-brand-main-600 text-white/60 light:text-black/60 text-[10px] px-1.5 py-0"
                          >
                            {trace.llmModel || trace.requestedModel}
                          </Badge>
                        )}
                        {trace.provider && (
                          <span className="text-[10px] text-white/30 light:text-black/30">
                            <ProviderDisplay
                              providerName={trace.provider}
                              isActive={false}
                              useImage={false}
                            />
                          </span>
                        )}
                      </div>

                      {/* Row 2: Input preview */}
                      <p className="text-xs text-white/50 light:text-black/50 leading-relaxed line-clamp-2">
                        {getInputPreview(trace.traceInput)}
                      </p>

                      {/* Row 3: Stats */}
                      <div className="flex items-center gap-3 text-[10px] text-white/30 light:text-black/30">
                        <span>{formatDuration(trace.totalDuration)}</span>
                        <span className="text-white/10 light:text-black/10">
                          |
                        </span>
                        <span>{formatCost(trace.totalCost)}</span>
                        <span className="text-white/10 light:text-black/10">
                          |
                        </span>
                        <span>
                          {formatTokensCompact(
                            totalTokens(trace) > 0 ? totalTokens(trace) : null,
                          )}{' '}
                          tokens
                        </span>
                        <span className="text-white/10 light:text-black/10">
                          |
                        </span>
                        <span>{trace.spanCount} spans</span>
                      </div>
                    </div>

                    {/* Preview button */}
                    <button
                      onClick={(e) => {
                        e.stopPropagation()
                        setPreviewTrace(trace)
                      }}
                      className="text-white/20 light:text-black/20 hover:text-white/60 light:hover:text-black/60 transition-colors p-1 mt-0.5"
                      title="Preview trace"
                    >
                      <Icon icon="lucide:eye" className="h-3.5 w-3.5" />
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>

          {/* Footer */}
          <SheetFooter className={evaluationSheetSplitFooterClass}>
            <Button variant="outline" onClick={resetAndClose}>
              Cancel
            </Button>
            <Button
              variant="default"
              onClick={handleImport}
              disabled={selectedTraceIds.size === 0 || batchMutation.isPending}
            >
              {batchMutation.isPending
                ? 'Importing...'
                : `Import ${selectedTraceIds.size} Trace${selectedTraceIds.size !== 1 ? 's' : ''}`}
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>

      {/* Trace preview sheet */}
      {previewTrace && (
        <Sheet open={!!previewTrace} onOpenChange={() => setPreviewTrace(null)}>
          <SheetContent
            side="right"
            className={`${evaluationSheetContentClass} sm:max-w-[640px]`}
          >
            <SheetHeader>
              <SheetTitle className="text-white light:text-brand-main-50 text-sm font-mono">
                Trace {previewTrace.traceId.substring(0, 16)}...
              </SheetTitle>
            </SheetHeader>

            <SheetBody className={evaluationSheetBodyClass}>
              <div className="space-y-4">
                {/* Trace metadata */}
                <div className="grid grid-cols-2 gap-3">
                  <div className="space-y-1">
                    <span className="text-[10px] text-white/30 light:text-black/30 uppercase tracking-wide">
                      Model
                    </span>
                    <p className="text-xs text-white light:text-brand-main-50">
                      {previewTrace.llmModel ||
                        previewTrace.requestedModel ||
                        '-'}
                    </p>
                  </div>
                  <div className="space-y-1">
                    <span className="text-[10px] text-white/30 light:text-black/30 uppercase tracking-wide">
                      Provider
                    </span>
                    <p className="text-xs text-white light:text-brand-main-50">
                      {previewTrace.provider || '-'}
                    </p>
                  </div>
                  <div className="space-y-1">
                    <span className="text-[10px] text-white/30 light:text-black/30 uppercase tracking-wide">
                      Duration
                    </span>
                    <p className="text-xs text-white light:text-brand-main-50">
                      {formatDuration(previewTrace.totalDuration)}
                    </p>
                  </div>
                  <div className="space-y-1">
                    <span className="text-[10px] text-white/30 light:text-black/30 uppercase tracking-wide">
                      Cost
                    </span>
                    <p className="text-xs text-white light:text-brand-main-50">
                      {formatCost(previewTrace.totalCost)}
                    </p>
                  </div>
                  <div className="space-y-1">
                    <span className="text-[10px] text-white/30 light:text-black/30 uppercase tracking-wide">
                      Tokens
                    </span>
                    <p className="text-xs text-white light:text-brand-main-50">
                      {previewTrace.tokenBreakdown
                        ? `${safeBigIntToNumber(previewTrace.tokenBreakdown.inputTokens).toLocaleString()} in / ${safeBigIntToNumber(previewTrace.tokenBreakdown.outputTokens).toLocaleString()} out`
                        : '-'}
                    </p>
                  </div>
                  <div className="space-y-1">
                    <span className="text-[10px] text-white/30 light:text-black/30 uppercase tracking-wide">
                      Status
                    </span>
                    <Badge
                      variant="secondary"
                      className={`text-[10px] ${
                        previewTrace.status === 'success'
                          ? 'bg-emerald-500/20 text-emerald-400 light:text-emerald-600'
                          : previewTrace.status === 'error'
                            ? 'bg-red-500/20 text-red-400 light:text-red-600'
                            : 'bg-yellow-500/20 text-yellow-400 light:text-yellow-700'
                      }`}
                    >
                      {previewTrace.status || 'unknown'}
                    </Badge>
                  </div>
                </div>

                {/* Input */}
                <div className="space-y-1.5">
                  <span className="text-[10px] text-white/30 light:text-black/30 uppercase tracking-wide">
                    Input (will be used as dataset input)
                  </span>
                  <pre className="text-xs text-white/70 light:text-black/70 bg-brand-main-800/50 border border-brand-main-600 rounded-md p-3 overflow-x-auto max-h-48 whitespace-pre-wrap">
                    {previewTrace.traceInput
                      ? (() => {
                          try {
                            return JSON.stringify(
                              JSON.parse(previewTrace.traceInput),
                              null,
                              2,
                            )
                          } catch {
                            return previewTrace.traceInput
                          }
                        })()
                      : 'No input'}
                  </pre>
                </div>

                {/* Output */}
                <div className="space-y-1.5">
                  <span className="text-[10px] text-white/30 light:text-black/30 uppercase tracking-wide">
                    Output (will be used as expected output)
                  </span>
                  <pre className="text-xs text-white/70 light:text-black/70 bg-brand-main-800/50 border border-brand-main-600 rounded-md p-3 overflow-x-auto max-h-48 whitespace-pre-wrap">
                    {previewTrace.traceOutput
                      ? (() => {
                          try {
                            return JSON.stringify(
                              JSON.parse(previewTrace.traceOutput),
                              null,
                              2,
                            )
                          } catch {
                            return previewTrace.traceOutput
                          }
                        })()
                      : 'No output'}
                  </pre>
                </div>
              </div>
            </SheetBody>
            <SheetFooter className={evaluationSheetFooterClass}>
              <Button variant="outline" onClick={() => setPreviewTrace(null)}>
                Close
              </Button>
            </SheetFooter>
          </SheetContent>
        </Sheet>
      )}
    </>
  )
}

export const EvaluationsDatasetsDetailActions: ActionGroup[] = [
  {
    title: <DatasetBreadcrumb />,
  },
  {
    actions: [
      {
        type: 'custom',
        key: 'import-traces',
        label: 'Import from Traces',
        component: ImportFromTracesButton,
      },
      {
        type: 'custom',
        key: 'add-item',
        label: 'Add Item',
        component: AddItemButton,
      },
    ],
  },
]
