import { useEffect, useMemo, useState } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import {
  usePlaygroundStore,
  type PlaygroundRole,
} from '@/stores/playground-store'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'
import { FeatureGateBanner } from '@/components/ee/feature-gate-banner'
import {
  useDataset,
  useDatasetItems,
  useCreateDatasetItem,
  useCreateDatasetItemBatch,
  useDeleteDatasetItem,
} from '@/hooks/evaluations/use-datasets'
import { usePermissions } from '@/hooks/auth'
import { usePermissionSet } from '@/hooks/authz/use-permission-set'
import { Button, Loader, toast } from '@everstack/ui/components'
import { ui } from '@everstack/ui'
import { Iconify } from '@everstack/ui/icons'
import { DatasetJsonEditorDropdown } from '@/components/evaluations/dataset-json-editor-dropdown'
import {
  evaluationErrorClass,
  evaluationSheetBodyClass,
  evaluationSheetContentClass,
  evaluationSheetSplitFooterClass,
} from '@/components/evaluations/evaluation-form'
import { ResponsiveTable, type ColumnConfig, type RowAction } from '@/ui/table'
import { formatTimestamp } from '@everstack/utils/functions/index'
import {
  Code2,
  Copy,
  ExternalLink,
  Play,
  Plus,
  Trash2,
  UploadCloud,
} from 'lucide-react'
import { highlightCode } from '@/lib/shiki'

const { Sheet, SheetContent, SheetFooter, SheetHeader, SheetTitle, SheetBody } =
  ui

const DATASETS_DOCS_URL =
  'https://docs.everstack.ai/getting-started/evaluations/datasets'

type ParsedItem = {
  input: Record<string, unknown>
  expectedOutput?: Record<string, unknown>
}

type SdkExample = {
  id: 'node' | 'python' | 'go'
  label: string
  language: string
  docsUrl: string
  code: string
}

const SDK_EXAMPLES: SdkExample[] = [
  {
    id: 'node',
    label: 'Node',
    language: 'typescript',
    docsUrl: DATASETS_DOCS_URL,
    code: `const dataset = await client.datasets.create({
  name: "support-regression",
  description: "Regression dataset for support responses",
})

const datasetId = dataset.dataset?.id

await client.datasets.items.createBatch({
  datasetId,
  items: [
    {
      input: { query: "How do I reset my password?" },
      expectedOutput: { answer: "Send the reset link." },
    },
    {
      input: { query: "How do I update billing info?" },
      expectedOutput: { answer: "Open billing settings." },
    },
  ],
})`,
  },
  {
    id: 'python',
    label: 'Python',
    language: 'python',
    docsUrl: DATASETS_DOCS_URL,
    code: `dataset = client.datasets.create(
    name="support-regression",
    description="Regression dataset for support responses",
)

dataset_id = dataset["dataset"]["id"]

client.datasets.items.create_batch(
    dataset_id,
    items=[
        {
            "input": {"query": "How do I reset my password?"},
            "expected_output": {"answer": "Send the reset link."},
        },
        {
            "input": {"query": "How do I update billing info?"},
            "expected_output": {"answer": "Open billing settings."},
        },
    ],
)`,
  },
  {
    id: 'go',
    label: 'Go',
    language: 'go',
    docsUrl: DATASETS_DOCS_URL,
    code: `dataset, err := client.Datasets.Create(ctx, map[string]any{
  "name": "support-regression",
  "description": "Regression dataset for support responses",
})
if err != nil {
  return err
}

datasetID := dataset["dataset"].(map[string]any)["id"].(string)

_, err = client.Datasets.Items.CreateBatch(ctx, datasetID, map[string]any{
  "items": []map[string]any{
    {
      "input": map[string]any{"query": "How do I reset my password?"},
      "expected_output": map[string]any{"answer": "Send the reset link."},
    },
    {
      "input": map[string]any{"query": "How do I update billing info?"},
      "expected_output": map[string]any{"answer": "Open billing settings."},
    },
  },
})
if err != nil {
  return err
}`,
  },
]

export const Route = createFileRoute('/evaluations/datasets_/$datasetId')({
  component: DatasetDetailPage,
})

function hasJsonContent(value: unknown): boolean {
  if (value === null || value === undefined) return false
  if (Array.isArray(value)) return value.length > 0
  if (typeof value === 'object') return Object.keys(value).length > 0
  return true
}

function jsonText(value: unknown): string {
  if (!hasJsonContent(value)) return 'null'
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

function jsonPreview(value: unknown): string {
  if (!hasJsonContent(value)) return '-'
  try {
    return JSON.stringify(value)
  } catch {
    return String(value)
  }
}

function normalizeJsonObject(
  value: unknown,
  fallbackKey = 'text',
): Record<string, unknown> {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return value as Record<string, unknown>
  }
  return { [fallbackKey]: value ?? '' }
}

function ShikiCodeBlock({
  code,
  language,
}: {
  code: string
  language: string
}) {
  const [highlighted, setHighlighted] = useState<{
    innerHtml: string
    codeClass: string
  } | null>(null)

  useEffect(() => {
    let cancelled = false
    setHighlighted(null)
    void highlightCode(code, language).then((result) => {
      if (!cancelled) setHighlighted(result)
    })
    return () => {
      cancelled = true
    }
  }, [code, language])

  return (
    <pre className="max-h-[340px] overflow-auto rounded border border-brand-main-700 bg-brand-main-900 p-4 text-xs leading-relaxed text-white/65 light:border-brand-main-200 light:bg-white light:text-black/65">
      {highlighted ? (
        <code
          className={highlighted.codeClass}
          dangerouslySetInnerHTML={{ __html: highlighted.innerHtml }}
        />
      ) : (
        <code className={`language-${language}`}>{code}</code>
      )}
    </pre>
  )
}

function parseCSV(text: string): ParsedItem[] {
  const lines = text.trim().split('\n')
  if (lines.length < 2) return []

  const headers = lines[0]
    .split(',')
    .map((header) => header.trim().toLowerCase())
  const inputIdx = headers.findIndex((header) => header === 'input')
  const expectedIdx = headers.findIndex((header) =>
    ['expected_output', 'expectedoutput', 'expected'].includes(header),
  )

  if (inputIdx === -1) return []

  return lines.slice(1).flatMap((line) => {
    const cols = line.split(',').map((col) => col.trim())
    const rawInput = cols[inputIdx]
    if (!rawInput) return []

    let input: Record<string, unknown>
    try {
      input = normalizeJsonObject(JSON.parse(rawInput))
    } catch {
      input = { text: rawInput }
    }

    let expectedOutput: Record<string, unknown> | undefined
    if (expectedIdx !== -1 && cols[expectedIdx]) {
      try {
        expectedOutput = normalizeJsonObject(JSON.parse(cols[expectedIdx]))
      } catch {
        expectedOutput = { text: cols[expectedIdx] }
      }
    }

    return [{ input, expectedOutput }]
  })
}

function parseJSON(text: string): ParsedItem[] {
  const data = JSON.parse(text)
  const rows = Array.isArray(data) ? data : (data.items ?? data.data ?? [])
  return rows
    .filter((item: any) => item.input !== undefined)
    .map((item: any) => {
      const rawExpected =
        item.expectedOutput ?? item.expected_output ?? item.expected
      return {
        input:
          typeof item.input === 'string'
            ? { text: item.input }
            : normalizeJsonObject(item.input),
        expectedOutput:
          rawExpected === undefined
            ? undefined
            : typeof rawExpected === 'string'
              ? { text: rawExpected }
              : normalizeJsonObject(rawExpected),
      }
    })
}

function AddItemSheet({
  datasetId,
  open,
  onOpenChange,
}: {
  datasetId: string
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const createItemMutation = useCreateDatasetItem()
  const [inputJson, setInputJson] = useState('{}')
  const [expectedOutputJson, setExpectedOutputJson] = useState('{}')
  const [inputOpen, setInputOpen] = useState(false)
  const [expectedOutputOpen, setExpectedOutputOpen] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const reset = () => {
    setInputJson('{}')
    setExpectedOutputJson('{}')
    setInputOpen(false)
    setExpectedOutputOpen(false)
    setError(null)
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError(null)

    try {
      const input = normalizeJsonObject(JSON.parse(inputJson))
      const expectedOutput = expectedOutputJson.trim()
        ? normalizeJsonObject(JSON.parse(expectedOutputJson))
        : undefined
      await createItemMutation.mutateAsync({
        datasetId,
        input: input as any,
        expectedOutput: expectedOutput as any,
      })
      toast.success('Dataset item added')
      reset()
      onOpenChange(false)
    } catch (err) {
      if (err instanceof SyntaxError) {
        setError('Input and expected output must be valid JSON.')
        return
      }
      toast.error('Failed to add dataset item')
    }
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className={`${evaluationSheetContentClass} sm:max-w-[480px]`}
      >
        <SheetHeader>
          <SheetTitle>Add Dataset Item</SheetTitle>
        </SheetHeader>

        <form onSubmit={handleSubmit} className="flex min-h-0 flex-1 flex-col">
          <SheetBody className={evaluationSheetBodyClass}>
            {(error || createItemMutation.error) && (
              <div className={evaluationErrorClass}>
                {error ?? (createItemMutation.error as Error).message}
              </div>
            )}

            <DatasetJsonEditorDropdown
              label="Input JSON"
              value={inputJson}
              onChange={setInputJson}
              open={inputOpen}
              onOpenChange={setInputOpen}
              height="260px"
            />
            <DatasetJsonEditorDropdown
              label="Expected output JSON"
              value={expectedOutputJson}
              onChange={setExpectedOutputJson}
              open={expectedOutputOpen}
              onOpenChange={setExpectedOutputOpen}
              height="260px"
            />
          </SheetBody>
          <SheetFooter className={evaluationSheetSplitFooterClass}>
            <Button
              type="button"
              variant="outline"
              onClick={reset}
              disabled={createItemMutation.isPending}
            >
              Reset
            </Button>
            <div className="flex items-center gap-2">
              <Button
                type="button"
                variant="outline"
                onClick={() => onOpenChange(false)}
                disabled={createItemMutation.isPending}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={createItemMutation.isPending}>
                {createItemMutation.isPending ? 'Adding...' : 'Add Item'}
              </Button>
            </div>
          </SheetFooter>
        </form>
      </SheetContent>
    </Sheet>
  )
}

function EmptyDatasetDropzone({
  datasetId,
  onAddItem,
}: {
  datasetId: string
  onAddItem: () => void
}) {
  const batchMutation = useCreateDatasetItemBatch()
  const [isDragging, setIsDragging] = useState(false)
  const [selectedSdk, setSelectedSdk] = useState<SdkExample['id']>('node')
  const selectedExample =
    SDK_EXAMPLES.find((example) => example.id === selectedSdk) ??
    SDK_EXAMPLES[0]

  const importFile = (file: File) => {
    const reader = new FileReader()
    reader.onload = async () => {
      try {
        const text = String(reader.result ?? '')
        const isJson =
          file.name.endsWith('.json') || file.type === 'application/json'
        const items = isJson ? parseJSON(text) : parseCSV(text)
        if (items.length === 0) {
          toast.error(
            isJson
              ? 'No valid items found. Expected objects with an input field.'
              : 'No valid rows found. CSV must include an input column.',
          )
          return
        }
        await batchMutation.mutateAsync({ datasetId, items: items as any })
        toast.success(
          `Imported ${items.length} item${items.length === 1 ? '' : 's'}`,
        )
      } catch (err) {
        toast.error(
          `Failed to import file: ${err instanceof Error ? err.message : String(err)}`,
        )
      }
    }
    reader.readAsText(file)
  }

  const handleFiles = (files: FileList | null) => {
    const file = files?.[0]
    if (!file) return
    importFile(file)
  }

  const copySdkExample = async () => {
    await navigator.clipboard.writeText(selectedExample.code)
    toast.success(`Copied ${selectedExample.label} example`)
  }

  return (
    <div className="flex flex-1 items-center justify-center px-8 py-12">
      <div className="grid w-full max-w-6xl gap-4 lg:grid-cols-[minmax(0,1.15fr)_minmax(420px,0.85fr)]">
        <label
          onDragOver={(e) => {
            e.preventDefault()
            setIsDragging(true)
          }}
          onDragLeave={(e) => {
            e.preventDefault()
            setIsDragging(false)
          }}
          onDrop={(e) => {
            e.preventDefault()
            setIsDragging(false)
            handleFiles(e.dataTransfer.files)
          }}
          className={`flex min-h-[420px] w-full cursor-pointer flex-col items-center justify-center rounded border border-dashed px-8 text-center transition-colors ${
            isDragging
              ? 'border-brand-secondary-500 bg-brand-secondary-500/10'
              : 'border-brand-main-600 bg-brand-main-950 hover:border-brand-main-500 light:border-brand-main-200 light:bg-brand-main-50'
          }`}
        >
          <input
            type="file"
            accept=".csv,.json"
            className="hidden"
            onChange={(e) => handleFiles(e.target.files)}
          />
          <div className="mb-4 flex h-12 w-12 items-center justify-center rounded border border-brand-main-600 bg-brand-main-950/70 text-white/40 light:border-brand-main-200 light:bg-white light:text-black/40">
            {batchMutation.isPending ? (
              <Loader loaderText="" />
            ) : (
              <UploadCloud className="h-6 w-6" />
            )}
          </div>
          <div className="text-sm font-medium text-white light:text-brand-main-50">
            {batchMutation.isPending ? 'Importing...' : 'Drop CSV or JSON'}
          </div>
          <div className="mt-1 text-xs text-white/40 light:text-black/45">
            CSV requires <span className="font-mono">input</span>;{' '}
            <span className="font-mono">expected_output</span> is optional.
          </div>
        </label>

        <div className="flex min-h-[420px] flex-col rounded border border-brand-main-700 bg-brand-main-950 light:border-brand-main-200 light:bg-brand-main-50">
          <div className="flex items-center justify-between gap-3 border-b border-brand-main-700 px-4 py-3 light:border-brand-main-200">
            <div className="flex min-w-0 items-center gap-2">
              <Code2 className="h-3.5 w-3.5 text-brand-secondary-300 light:text-brand-secondary-700" />
              <span className="truncate text-xs font-medium text-white light:text-brand-main-50">
                SDK examples
              </span>
            </div>
            <div className="flex items-center gap-2">
              <a
                href={selectedExample.docsUrl}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex h-7 items-center gap-1 rounded border border-brand-main-700 px-2 text-xs text-white/65 transition-colors hover:border-brand-main-500 hover:text-white light:border-brand-main-200 light:text-black/60 light:hover:text-black"
              >
                <ExternalLink className="h-3 w-3" />
                Docs
              </a>
              <Button variant="outline" onClick={copySdkExample}>
                <Copy className="h-3 w-3" />
                Copy
              </Button>
            </div>
          </div>
          <div className="space-y-4 p-4">
            <div className="flex rounded border border-brand-main-700 bg-brand-main-900 p-0.5 light:border-brand-main-200 light:bg-white">
              {SDK_EXAMPLES.map((example) => (
                <button
                  key={example.id}
                  type="button"
                  onClick={() => setSelectedSdk(example.id)}
                  className={`flex-1 rounded px-2 py-1 text-xs font-medium transition-colors ${
                    selectedSdk === example.id
                      ? 'bg-brand-main-700 text-white light:bg-brand-main-100 light:text-brand-main-950'
                      : 'text-white/45 hover:text-white light:text-black/45 light:hover:text-black'
                  }`}
                >
                  {example.label}
                </button>
              ))}
            </div>
            <ShikiCodeBlock
              code={selectedExample.code}
              language={selectedExample.language}
            />
            <Button
              variant="default"
              className="w-full"
              onClick={onAddItem}
              disabled={batchMutation.isPending}
            >
              <Plus className="h-3.5 w-3.5" />
              Add Item
            </Button>
          </div>
        </div>
      </div>
    </div>
  )
}

function DatasetDetailPage() {
  const gate = useFeatureGate(FeatureKey.EVALUATIONS)

  if (gate.isBlocked) {
    return (
      <FeatureGateBanner
        featureName="Datasets"
        description="Create and manage evaluation datasets with input/expected output pairs."
        requiredTier="Pro"
        upgradeUrl={gate.upgradeUrl}
        isCE={gate.isCE}
      />
    )
  }

  return <DatasetDetailPageContent />
}

function DatasetDetailPageContent() {
  const { datasetId } = Route.useParams()
  const { data: dataset, isLoading: datasetLoading } = useDataset(datasetId)
  const { data: items, isLoading: itemsLoading } = useDatasetItems(datasetId)
  const deleteItemMutation = useDeleteDatasetItem()
  const { can } = usePermissions()
  const { permissions } = usePermissionSet([
    { permission: 'resource:edit', object: `dataset:${datasetId}` },
  ])
  const canEdit =
    can('resource:edit') ||
    permissions.has('resource:edit', `dataset:${datasetId}`)
  const [addItemOpen, setAddItemOpen] = useState(false)
  const navigate = useNavigate()
  const loadConversation = usePlaygroundStore((s) => s.loadConversation)

  const itemList = items ?? []

  const openInPlayground = (item: any) => {
    const input = item.input ?? {}
    const roles = new Set(['system', 'user', 'assistant'])
    const messages = Array.isArray(input.messages)
      ? input.messages
          .map((message: any) => ({
            role: (roles.has(message?.role)
              ? message.role
              : 'user') as PlaygroundRole,
            text:
              typeof message?.content === 'string'
                ? message.content
                : JSON.stringify(message?.content ?? ''),
          }))
          .filter((message: { text: string }) => message.text)
      : [
          {
            role: 'user' as PlaygroundRole,
            text: JSON.stringify(input, null, 2),
          },
        ]
    loadConversation({ messages })
    void navigate({ to: '/evaluations/playground' })
  }

  const deleteItem = (item: any) => {
    if (!confirm('Delete this dataset item?')) return
    deleteItemMutation.mutate(item.id)
  }

  const columns: ColumnConfig<any>[] = useMemo(
    () => [
      {
        id: 'case',
        header: 'Case',
        width: 90,
        minWidth: 70,
        render: (item: any) => {
          const index =
            itemList.findIndex((candidate: any) => candidate.id === item.id) + 1
          return (
            <span className="font-mono text-xs tabular-nums text-white/45 light:text-black/45">
              #{index || '-'}
            </span>
          )
        },
      },
      {
        id: 'input',
        header: 'Input',
        width: 390,
        minWidth: 220,
        kind: 'json',
        copyValue: (item: any) => jsonText(item.input),
        render: (item: any) => (
          <span className="truncate font-mono text-xs text-white light:text-brand-main-50">
            {jsonPreview(item.input)}
          </span>
        ),
      },
      {
        id: 'expectedOutput',
        header: 'Expected',
        width: 320,
        minWidth: 180,
        kind: 'json',
        copyValue: (item: any) => jsonText(item.expectedOutput),
        render: (item: any) =>
          hasJsonContent(item.expectedOutput) ? (
            <span className="truncate font-mono text-xs text-white/65 light:text-black/65">
              {jsonPreview(item.expectedOutput)}
            </span>
          ) : (
            <span className="text-xs text-white/35 light:text-black/35">-</span>
          ),
      },
      {
        id: 'source',
        header: 'Source',
        width: 100,
        minWidth: 80,
        render: (item: any) =>
          item.sourceTraceId ? (
            <span className="shrink-0 rounded px-1.5 py-0.5 text-[11px] font-medium bg-blue-500/15 text-blue-400 light:text-blue-600">
              trace
            </span>
          ) : (
            <span className="shrink-0 rounded px-1.5 py-0.5 text-[11px] font-medium bg-white/10 light:bg-black/10 text-white/45 light:text-black/45">
              manual
            </span>
          ),
      },
      {
        id: 'createdAt',
        header: 'Created',
        width: 170,
        minWidth: 140,
        render: (item: any) => (
          <span className="truncate text-xs text-brand-main-100">
            {item.createdAt ? formatTimestamp(item.createdAt) : '-'}
          </span>
        ),
      },
    ],
    [itemList],
  )

  const rowActions: RowAction<any>[] = [
    {
      label: 'Try in playground',
      icon: <Play className="h-4 w-4" />,
      onClick: openInPlayground,
    },
    ...(canEdit
      ? [
          {
            label: 'Delete',
            icon: <Trash2 className="h-4 w-4" />,
            variant: 'destructive' as const,
            onClick: deleteItem,
          },
        ]
      : []),
  ]

  if (datasetLoading) {
    return (
      <div className="flex flex-1 items-center justify-center">
        <Loader loaderText="Loading dataset..." />
      </div>
    )
  }

  if (!dataset) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-4 text-white/70 light:text-black/70">
        <div className="flex flex-col items-center justify-center space-y-2 text-center">
          <span className="mb-4 inline-block rounded-md bg-brand-secondary-200 p-2">
            <Iconify.Icon
              icon="heroicons:circle-stack"
              className="size-10 text-brand-secondary-700"
            />
          </span>
          <h3 className="text-lg font-medium text-white light:text-brand-main-50">
            Dataset not found
          </h3>
          <p className="mb-4 w-2/3 text-center text-sm text-white/60 light:text-black/60">
            The dataset you're looking for doesn't exist or has been deleted.
          </p>
        </div>
      </div>
    )
  }

  return (
    <div className="flex h-full w-full flex-col overflow-hidden">
      {itemsLoading ? (
        <div className="flex flex-1 items-center justify-center">
          <Loader loaderText="Loading items..." />
        </div>
      ) : itemList.length === 0 && canEdit ? (
        <EmptyDatasetDropzone
          datasetId={datasetId}
          onAddItem={() => setAddItemOpen(true)}
        />
      ) : itemList.length === 0 ? (
        <div className="flex flex-1 items-center justify-center text-sm text-white/45 light:text-black/45">
          No items yet.
        </div>
      ) : (
        <ResponsiveTable
          tableId="evaluations-dataset-items"
          columns={columns}
          data={itemList}
          enableResizing={true}
          minTableWidth="100%"
          rowKey={(item: any) => item.id}
          rowActions={rowActions}
          enableCellTooltips={true}
          emptyMessage={
            <div className="flex flex-col items-center justify-center gap-3">
              <span className="inline-block rounded-md bg-brand-secondary-200 p-2">
                <Iconify.Icon
                  icon="heroicons:inbox"
                  className="size-8 text-brand-secondary-700"
                />
              </span>
              <p className="text-sm text-white/40 light:text-black/40">
                No items yet. Add items manually or import from traces.
              </p>
            </div>
          }
        />
      )}

      {canEdit && (
        <AddItemSheet
          datasetId={datasetId}
          open={addItemOpen}
          onOpenChange={setAddItemOpen}
        />
      )}
    </div>
  )
}
