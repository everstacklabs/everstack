import { useEffect, useMemo, useState } from 'react'
import { ui } from '@everstack/ui'
import { Button, toast } from '@everstack/ui/components'
import {
  Database,
  Plus,
  ChevronDown,
  ChevronRight,
  Braces,
  AlertCircle,
  Check,
} from 'lucide-react'
import { JSONEditor } from '@/components/deployments/functions/json-editor'
import {
  useCreateDataset,
  useCreateDatasetItem,
  useDatasets,
} from '@/hooks/evaluations/use-datasets'
import type { JsonObject } from '@/server/datasets'

const {
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Tabs,
  TabsList,
  TabsTrigger,
  Sheet,
  SheetBody,
  SheetContent,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} = ui

export type AddToDatasetPayload = {
  input: JsonObject
  expectedOutput?: JsonObject
  metadata?: JsonObject
  sourceTraceId?: string
  sourceObservationId?: string
}

type AddToDatasetDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  payload: AddToDatasetPayload | null
  /** Short description of where the item comes from, e.g. "playground run". */
  sourceLabel?: string
}

type Mode = 'row' | 'scorer'

const NEW_DATASET = '__new__'

/** Stringify a JSON object for an editable field; empty object → empty string. */
function toEditable(value?: JsonObject): string {
  if (!value || Object.keys(value).length === 0) return ''
  return JSON.stringify(value, null, 2)
}

/** Parse an editable JSON field. Empty text is valid and yields undefined. */
function parseField(
  text: string,
): { ok: true; value?: JsonObject } | { ok: false; error: string } {
  const trimmed = text.trim()
  if (!trimmed) return { ok: true, value: undefined }
  try {
    const parsed = JSON.parse(trimmed)
    if (
      parsed === null ||
      typeof parsed !== 'object' ||
      Array.isArray(parsed)
    ) {
      return { ok: false, error: 'Must be a JSON object' }
    }
    return { ok: true, value: parsed as JsonObject }
  } catch {
    return { ok: false, error: 'Invalid JSON' }
  }
}

/** One collapsible, Monaco-backed JSON editor for a single row field. */
function JsonField({
  label,
  hint,
  value,
  onChange,
  defaultOpen = false,
  required = false,
}: {
  label: string
  hint?: string
  value: string
  onChange: (v: string) => void
  defaultOpen?: boolean
  required?: boolean
}) {
  const [open, setOpen] = useState(defaultOpen)
  const parsed = parseField(value)
  const error = !parsed.ok
    ? parsed.error
    : required && !value.trim()
      ? 'Required'
      : null

  return (
    <div className="rounded-md border border-brand-main-700/40 bg-brand-main-800/20">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-center justify-between gap-2 px-2.5 py-1.5 text-left"
      >
        <span className="flex min-w-0 items-center gap-2">
          {open ? (
            <ChevronDown className="h-3.5 w-3.5 shrink-0 text-white/40 light:text-black/40" />
          ) : (
            <ChevronRight className="h-3.5 w-3.5 shrink-0 text-white/40 light:text-black/40" />
          )}
          <Braces className="h-3.5 w-3.5 shrink-0 text-brand-secondary-300" />
          <span className="truncate text-xs font-medium text-white/90 light:text-brand-main-50">
            {label}
          </span>
          {required && (
            <span className="shrink-0 text-[10px] text-brand-secondary-300">
              required
            </span>
          )}
        </span>
        {error ? (
          <span className="flex shrink-0 items-center gap-1 text-[10px] text-red-400">
            <AlertCircle className="h-3 w-3" />
            {error}
          </span>
        ) : (
          <span className="flex shrink-0 items-center gap-1 text-[10px] text-emerald-400/70">
            <Check className="h-3 w-3" />
            valid
          </span>
        )}
      </button>
      {open && (
        <div className="border-t border-brand-main-700/40 p-2">
          {hint && (
            <p className="mb-1.5 px-0.5 text-[10px] text-white/40 light:text-black/40">
              {hint}
            </p>
          )}
          <JSONEditor value={value} onChange={onChange} height="280px" />
        </div>
      )}
    </div>
  )
}

/**
 * Shared "Add to dataset" flow used from the playground and from trace span
 * detail. Pick or create a dataset, review and edit the row that will be
 * saved (input / expected output / metadata) in a Monaco JSON editor, and
 * choose whether to save it as a plain dataset row or nested as a scorer
 * input. Trace lineage is preserved via sourceTraceId / sourceObservationId
 * when available.
 */
export function AddToDatasetDialog({
  open,
  onOpenChange,
  payload,
  sourceLabel,
}: AddToDatasetDialogProps) {
  const { data: datasets, isLoading } = useDatasets()
  const createItem = useCreateDatasetItem()
  const createDataset = useCreateDataset()

  const [mode, setMode] = useState<Mode>('row')
  const [datasetId, setDatasetId] = useState('')
  const [newName, setNewName] = useState('')
  const [newDescription, setNewDescription] = useState('')

  const [inputText, setInputText] = useState('')
  const [expectedText, setExpectedText] = useState('')
  const [metadataText, setMetadataText] = useState('')

  const hasDatasets = (datasets?.length ?? 0) > 0
  const creatingNew = datasetId === NEW_DATASET || !hasDatasets

  // Re-seed the editable fields whenever a fresh payload is opened.
  useEffect(() => {
    if (!open || !payload) return
    setInputText(toEditable(payload.input))
    setExpectedText(toEditable(payload.expectedOutput))
    setMetadataText(toEditable(payload.metadata))
    setMode('row')
    setNewName('')
    setNewDescription('')
    setDatasetId((prev) => (prev === NEW_DATASET ? '' : prev))
  }, [open, payload])

  const parsedInput = useMemo(() => parseField(inputText), [inputText])
  const parsedExpected = useMemo(() => parseField(expectedText), [expectedText])
  const parsedMetadata = useMemo(() => parseField(metadataText), [metadataText])

  const fieldsValid =
    parsedInput.ok &&
    parsedExpected.ok &&
    parsedMetadata.ok &&
    !!inputText.trim()

  const datasetReady = creatingNew ? !!newName.trim() : !!datasetId
  const submitting = createItem.isPending || createDataset.isPending

  const submit = async () => {
    if (!fieldsValid || !datasetReady || submitting) return
    const input = (parsedInput.ok && parsedInput.value) || {}
    const expected = parsedExpected.ok ? parsedExpected.value : undefined
    const metadata = parsedMetadata.ok ? parsedMetadata.value : undefined

    try {
      // Resolve the target dataset, creating one inline when requested.
      let targetId = datasetId
      if (creatingNew) {
        const res = await createDataset.mutateAsync({
          name: newName.trim(),
          description: newDescription.trim() || undefined,
        })
        targetId = (res as { dataset?: { id?: string } })?.dataset?.id ?? ''
        if (!targetId) throw new Error('Could not create dataset')
      }

      // In scorer mode the whole span (input / expected / metadata) is
      // nested into the row's input so scorers can be evaluated against it.
      const rowInput =
        mode === 'scorer'
          ? ({
              input,
              ...(expected ? { expected } : {}),
              ...(metadata ? { metadata } : {}),
            } as JsonObject)
          : input
      const rowExpected = mode === 'scorer' ? undefined : expected
      const rowMetadata = mode === 'scorer' ? undefined : metadata

      await createItem.mutateAsync({
        datasetId: targetId,
        input: rowInput,
        expectedOutput: rowExpected,
        metadata: rowMetadata,
        sourceTraceId: payload?.sourceTraceId,
        sourceObservationId: payload?.sourceObservationId,
      })
      toast.success(
        creatingNew
          ? `Created "${newName.trim()}" and added item`
          : 'Added to dataset',
      )
      onOpenChange(false)
    } catch (err) {
      toast.error((err as Error)?.message ?? 'Failed to add to dataset')
    }
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="flex h-[100vh] w-full flex-col overflow-hidden sm:max-w-md"
        onKeyDown={(e) => {
          if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
            e.preventDefault()
            void submit()
          }
        }}
      >
        <SheetHeader>
          <SheetTitle className="flex items-center gap-2 py-2.5">
            <Database className="h-4 w-4 text-brand-secondary-300" />
            Add to dataset
          </SheetTitle>
        </SheetHeader>

        <SheetBody className="flex-1 space-y-4 overflow-y-auto py-4 scrollbar-macos">
          <p className="text-xs text-white/50 light:text-black/50">
            Save this {sourceLabel ?? 'item'} as a dataset item for evaluation
            runs. Review the row below before adding.
          </p>

          {/* Mode: plain row vs nested scorer input */}
          <div className="space-y-2">
            <Tabs value={mode} onValueChange={(v) => setMode(v as Mode)}>
              <TabsList className="grid h-auto w-full grid-cols-2 gap-1 rounded border border-brand-main-600 bg-brand-main-800/50 p-1">
                <TabsTrigger
                  value="row"
                  className="py-1 text-brand-secondary-100 transition-colors hover:text-white data-[state=active]:border-brand-secondary-500/30 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 light:hover:text-brand-main-50"
                >
                  Dataset row
                </TabsTrigger>
                <TabsTrigger
                  value="scorer"
                  className="py-1 text-brand-secondary-100 transition-colors hover:text-white data-[state=active]:border-brand-secondary-500/30 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 light:hover:text-brand-main-50"
                >
                  As scorer input
                </TabsTrigger>
              </TabsList>
            </Tabs>
            {mode === 'scorer' && (
              <p className="text-[11px] leading-relaxed text-white/45 light:text-black/45">
                Nests this {sourceLabel ?? 'item'}&apos;s input, expected
                output, and metadata into the dataset row&apos;s{' '}
                <span className="font-mono text-white/70">input</span>. Useful
                for building datasets that evaluate scorers.
              </p>
            )}
          </div>

          {/* Dataset selection / inline create */}
          <div className="space-y-2">
            <div className="text-[11px] uppercase tracking-wider text-white/45 light:text-black/45">
              Dataset
            </div>
            {creatingNew ? (
              <div className="space-y-2 rounded-md border border-brand-main-700/40 bg-brand-main-800/20 p-3">
                <div className="flex items-center gap-2 text-[11px] text-brand-secondary-300">
                  <Plus className="h-3.5 w-3.5" />
                  New dataset
                </div>
                <Input
                  autoFocus={!hasDatasets}
                  value={newName}
                  onChange={(e) => setNewName(e.target.value)}
                  placeholder="Dataset name"
                  className="h-8 border-brand-main-600 bg-brand-main-900/50 text-sm"
                />
                <Input
                  value={newDescription}
                  onChange={(e) => setNewDescription(e.target.value)}
                  placeholder="Description (optional)"
                  className="h-8 border-brand-main-600 bg-brand-main-900/50 text-sm"
                />
                {hasDatasets && (
                  <button
                    type="button"
                    onClick={() => setDatasetId('')}
                    className="text-[11px] text-white/45 hover:text-white/70 light:text-black/45"
                  >
                    Pick an existing dataset instead
                  </button>
                )}
              </div>
            ) : (
              <div className="flex items-center gap-2">
                <Select value={datasetId} onValueChange={setDatasetId}>
                  <SelectTrigger className="h-8 flex-1 border-brand-main-600 bg-brand-main-900/50 text-sm">
                    <SelectValue
                      placeholder={isLoading ? 'Loading…' : 'Pick a dataset'}
                    />
                  </SelectTrigger>
                  <SelectContent className="border-brand-main-500 bg-brand-main-900">
                    {(datasets ?? []).map((d: { id: string; name: string }) => (
                      <SelectItem
                        key={d.id}
                        value={d.id}
                        className="text-xs text-white/80 light:text-black/80"
                      >
                        {d.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Button
                  variant="outline"
                  onClick={() => setDatasetId(NEW_DATASET)}
                  className="h-8 shrink-0"
                >
                  <Plus className="h-3.5 w-3.5" />
                  New
                </Button>
              </div>
            )}
          </div>

          {/* Editable row fields */}
          <div className="space-y-2">
            <div className="text-[11px] uppercase tracking-wider text-white/45 light:text-black/45">
              Row
            </div>
            <JsonField
              label="Input"
              value={inputText}
              onChange={setInputText}
              required
              defaultOpen
            />
            <JsonField
              label="Expected output"
              hint="The reference answer to score generations against."
              value={expectedText}
              onChange={setExpectedText}
            />
            <JsonField
              label="Metadata"
              hint="Arbitrary key-value context stored alongside the row."
              value={metadataText}
              onChange={setMetadataText}
            />
          </div>
        </SheetBody>

        <SheetFooter className="flex-row items-center justify-between gap-3">
          <span className="text-[10px] text-white/35 light:text-black/35">
            ⌘⏎ to add
          </span>
          <div className="flex gap-2">
            <Button variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button
              onClick={() => void submit()}
              disabled={!fieldsValid || !datasetReady || submitting}
            >
              {submitting
                ? 'Adding…'
                : creatingNew
                  ? 'Create & add'
                  : 'Add item'}
            </Button>
          </div>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
