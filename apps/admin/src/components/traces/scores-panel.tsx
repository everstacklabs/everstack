import { useState } from 'react'
import { ui } from '@everstack/ui'
import { cn } from '@everstack/utils/functions/cn'
import { statusTint, statusBadge, scoreBarClass } from './trace-viz'
import {
  Star,
  Plus,
  Trash2,
  Pencil,
  CheckCircle2,
  XCircle,
  Hash,
  Tag,
  MessageSquare,
  Loader2,
} from 'lucide-react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { getTraceScores, createScore, deleteScore } from '@/server/traces'
import { listScoreConfigs } from '@/server/datasets'
import { JsonViewer } from '@/ui/json-viewer'

type ScoreConfigOption = {
  id: string
  name: string
  dataType: 'NUMERIC' | 'CATEGORICAL' | 'BOOLEAN'
  minValue?: number
  maxValue?: number
  categories: string[]
  description: string
}

const { Badge, Button } = ui

type ScoreParams = {
  traceId: string
  name: string
  source: string
  dataType: string
  numericValue?: number
  stringValue?: string
  booleanValue?: boolean
  comment?: string
}

interface ScoresPanelProps {
  traceId: string
  viewMode?: 'formatted' | 'json'
}

export function ScoresPanel({
  traceId,
  viewMode = 'formatted',
}: ScoresPanelProps) {
  const queryClient = useQueryClient()
  const [showAddForm, setShowAddForm] = useState(false)
  const [editingScoreId, setEditingScoreId] = useState<string | null>(null)

  const { data: scores = [], isLoading } = useQuery({
    queryKey: ['trace-scores', traceId],
    queryFn: () => getTraceScores(traceId),
    enabled: !!traceId,
  })

  const { data: scoreConfigsData } = useQuery({
    queryKey: ['score-configs'],
    queryFn: () => listScoreConfigs(),
    staleTime: 60_000,
  })

  const scoreConfigs: ScoreConfigOption[] = (
    scoreConfigsData?.scoreConfigs ?? []
  )
    .filter((c) => !c.isArchived)
    .map((c): ScoreConfigOption | null => {
      const dt = c.dataType?.toLowerCase()
      // Only manual-annotation-compatible types — skip llm_judge / code_scorer here
      let mapped: 'NUMERIC' | 'CATEGORICAL' | 'BOOLEAN'
      if (dt === 'numeric') mapped = 'NUMERIC'
      else if (dt === 'categorical') mapped = 'CATEGORICAL'
      else if (dt === 'boolean') mapped = 'BOOLEAN'
      else return null
      const cats = c.categories?.fields ?? {}
      return {
        id: c.id,
        name: c.name,
        dataType: mapped,
        minValue: c.minValue,
        maxValue: c.maxValue,
        categories: Object.keys(cats),
        description: c.description,
      }
    })
    .filter((c): c is ScoreConfigOption => c !== null)

  const invalidateScores = () =>
    queryClient.invalidateQueries({ queryKey: ['trace-scores', traceId] })

  const createMutation = useMutation({
    mutationFn: (params: ScoreParams) => createScore(params),
    onSuccess: () => {
      invalidateScores()
      setShowAddForm(false)
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (params: { scoreId: string; traceId: string }) =>
      deleteScore(params.scoreId, params.traceId),
    onSuccess: invalidateScores,
  })

  const editMutation = useMutation({
    mutationFn: async (params: ScoreParams & { oldScoreId: string }) => {
      const { oldScoreId, ...createParams } = params
      await deleteScore(oldScoreId, params.traceId)
      return createScore(createParams)
    },
    onSuccess: () => {
      invalidateScores()
      setEditingScoreId(null)
    },
  })

  const groupedScores = scores.reduce<Record<string, typeof scores>>(
    (acc, score) => {
      const name = score.name || 'Unnamed'
      if (!acc[name]) acc[name] = []
      acc[name].push(score)
      return acc
    },
    {},
  )

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
            scores,
            scoreConfigs,
          }}
        />
      </div>
    )
  }

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-1.5">
          <Star className="h-3.5 w-3.5 text-brand-main-200" />
          <span className="text-xs font-medium text-brand-main-50 light:text-black">
            Scores
          </span>
          <Badge
            variant="outline"
            className="text-[9px] py-0 px-1 bg-brand-main-600/20 text-brand-main-50 border-brand-main-500 light:text-black"
          >
            {scores.length}
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
        <ScoreForm
          traceId={traceId}
          scoreConfigs={scoreConfigs}
          onSubmit={(params) => createMutation.mutate(params)}
          onCancel={() => setShowAddForm(false)}
          isSubmitting={createMutation.isPending}
        />
      )}

      {scores.length === 0 && !showAddForm ? (
        <div className="flex flex-col items-center justify-center h-20 text-brand-main-50 gap-1.5 light:text-black">
          <Star className="size-5" />
          <span className="text-[10px]">No scores yet</span>
        </div>
      ) : (
        <div className="space-y-1.5">
          {Object.entries(groupedScores).map(([name, nameScores]) => (
            <div
              key={name}
              className="border border-brand-main-500 rounded-md overflow-hidden"
            >
              <div className="flex items-center justify-between px-2.5 py-1.5 bg-brand-main-600/20">
                <span className="text-xs font-medium text-brand-main-50 light:text-black">
                  {name}
                </span>
                {nameScores.length > 1 && (
                  <Badge
                    variant="outline"
                    className="text-[9px] py-0 px-1 text-brand-main-50 border-brand-main-500 light:text-black"
                  >
                    {nameScores.length}
                  </Badge>
                )}
              </div>
              <div className="bg-brand-main-600/10">
                {nameScores.map((score) =>
                  editingScoreId === score.id ? (
                    <div
                      key={score.id}
                      className="px-2.5 py-2 border-b border-brand-main-500/20 last:border-0"
                    >
                      <ScoreForm
                        traceId={traceId}
                        scoreConfigs={scoreConfigs}
                        initialValues={{
                          name: score.name,
                          dataType: score.dataType as
                            | 'NUMERIC'
                            | 'CATEGORICAL'
                            | 'BOOLEAN',
                          numericValue: score.numericValue,
                          stringValue: score.stringValue,
                          booleanValue: score.booleanValue,
                          comment: score.comment,
                          source: score.source,
                        }}
                        onSubmit={(params) =>
                          editMutation.mutate({
                            ...params,
                            oldScoreId: score.id,
                          })
                        }
                        onCancel={() => setEditingScoreId(null)}
                        isSubmitting={editMutation.isPending}
                        submitLabel="Update"
                      />
                    </div>
                  ) : (
                    <ScoreCard
                      key={score.id}
                      score={score}
                      onEdit={() => setEditingScoreId(score.id)}
                      onDelete={() =>
                        deleteMutation.mutate({ scoreId: score.id, traceId })
                      }
                    />
                  ),
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

// Type and source are plain labels, not statuses — they read as neutral chips so
// colour stays reserved for things that mean pass/fail.
const metaBadge = statusBadge('neutral')

function ScoreCard({
  score,
  onEdit,
  onDelete,
}: {
  score: {
    id: string
    dataType: string
    source: string
    numericValue?: number
    stringValue?: string
    booleanValue?: boolean
    comment?: string
  }
  onEdit: () => void
  onDelete: () => void
}) {
  return (
    <div className="group/score flex items-center justify-between gap-2 px-2.5 py-1.5 border-b border-brand-main-500/20 last:border-0">
      <div className="flex items-center gap-2 flex-1 min-w-0">
        {score.dataType === 'NUMERIC' && score.numericValue !== undefined && (
          <div className="flex items-center gap-1.5">
            <Hash className={cn('h-3 w-3', statusTint.neutral.text)} />
            <span className="text-xs text-brand-main-50 light:text-black">
              {score.numericValue.toFixed(score.numericValue % 1 === 0 ? 0 : 2)}
            </span>
            {score.numericValue >= 0 && score.numericValue <= 1 && (
              <div className="w-14 h-1 bg-brand-main-700 rounded-full overflow-hidden">
                <div
                  className={cn(
                    'h-full rounded-full',
                    scoreBarClass(score.numericValue),
                  )}
                  style={{ width: `${score.numericValue * 100}%` }}
                />
              </div>
            )}
          </div>
        )}
        {score.dataType === 'BOOLEAN' &&
          score.booleanValue !== undefined &&
          (score.booleanValue ? (
            <CheckCircle2
              className={cn('h-3.5 w-3.5', statusTint.success.text)}
            />
          ) : (
            <XCircle className={cn('h-3.5 w-3.5', statusTint.error.text)} />
          ))}
        {score.dataType === 'CATEGORICAL' && score.stringValue && (
          <div className="flex items-center gap-1">
            <Tag className={cn('h-3 w-3', statusTint.neutral.text)} />
            <span className="text-[11px] text-brand-main-50 light:text-black">
              {score.stringValue}
            </span>
          </div>
        )}

        <Badge
          variant="outline"
          className={cn('text-[9px] py-0 px-1', metaBadge)}
        >
          {score.dataType}
        </Badge>
        <Badge
          variant="outline"
          className={cn('text-[9px] py-0 px-1', metaBadge)}
        >
          {score.source}
        </Badge>

        {score.comment && (
          <span
            className="text-[10px] text-brand-main-50 truncate light:text-black"
            title={score.comment}
          >
            <MessageSquare className="h-2.5 w-2.5 inline mr-0.5 relative -top-px" />
            {score.comment}
          </span>
        )}
      </div>

      <div className="flex items-center gap-px shrink-0 opacity-0 group-hover/score:opacity-100 transition-opacity">
        <button
          onClick={onEdit}
          className="flex items-center justify-center w-5 h-5 rounded hover:bg-brand-main-500/20 transition-colors"
        >
          <Pencil className="h-2.5 w-2.5 text-brand-main-50 light:text-black" />
        </button>
        <button
          onClick={onDelete}
          className="flex items-center justify-center w-5 h-5 rounded hover:bg-rose-500/15 transition-colors"
        >
          <Trash2 className="h-2.5 w-2.5 text-brand-main-50 light:text-black" />
        </button>
      </div>
    </div>
  )
}

function ScoreForm({
  traceId,
  scoreConfigs,
  initialValues,
  onSubmit,
  onCancel,
  isSubmitting,
  submitLabel = 'Save',
}: {
  traceId: string
  scoreConfigs: ScoreConfigOption[]
  initialValues?: {
    name: string
    dataType: 'NUMERIC' | 'CATEGORICAL' | 'BOOLEAN'
    numericValue?: number
    stringValue?: string
    booleanValue?: boolean
    comment?: string
    source?: string
  }
  onSubmit: (params: ScoreParams) => void
  onCancel: () => void
  isSubmitting: boolean
  submitLabel?: string
}) {
  const isEdit = !!initialValues
  const matchingConfig = initialValues
    ? scoreConfigs.find(
        (c) =>
          c.name === initialValues.name &&
          c.dataType === initialValues.dataType,
      )
    : undefined
  const [selectedConfigId, setSelectedConfigId] = useState<string>(
    matchingConfig?.id ?? '',
  )
  const selectedConfig = scoreConfigs.find((c) => c.id === selectedConfigId)

  const [name, setName] = useState(initialValues?.name ?? '')
  const [dataType, setDataType] = useState<
    'NUMERIC' | 'CATEGORICAL' | 'BOOLEAN'
  >(initialValues?.dataType ?? 'NUMERIC')
  const [numericValue, setNumericValue] = useState(
    initialValues?.numericValue !== undefined
      ? String(initialValues.numericValue)
      : '',
  )
  const [stringValue, setStringValue] = useState(
    initialValues?.stringValue ?? '',
  )
  const [booleanValue, setBooleanValue] = useState(
    initialValues?.booleanValue ?? true,
  )
  const [comment, setComment] = useState(initialValues?.comment ?? '')
  const [validationError, setValidationError] = useState<string | null>(null)

  const onPickConfig = (configId: string) => {
    setSelectedConfigId(configId)
    setValidationError(null)
    if (!configId) return
    const cfg = scoreConfigs.find((c) => c.id === configId)
    if (!cfg) return
    setName(cfg.name)
    setDataType(cfg.dataType)
    if (
      cfg.dataType === 'CATEGORICAL' &&
      cfg.categories.length > 0 &&
      !cfg.categories.includes(stringValue)
    ) {
      setStringValue(cfg.categories[0])
    }
  }

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    if (!name.trim()) return
    setValidationError(null)

    const params: ScoreParams = {
      traceId,
      name: name.trim(),
      source: initialValues?.source ?? 'ANNOTATION',
      dataType,
    }

    if (dataType === 'NUMERIC') {
      const n = parseFloat(numericValue)
      if (Number.isNaN(n)) {
        setValidationError('Numeric value required')
        return
      }
      if (
        selectedConfig?.minValue !== undefined &&
        n < selectedConfig.minValue
      ) {
        setValidationError(`Min ${selectedConfig.minValue}`)
        return
      }
      if (
        selectedConfig?.maxValue !== undefined &&
        n > selectedConfig.maxValue
      ) {
        setValidationError(`Max ${selectedConfig.maxValue}`)
        return
      }
      params.numericValue = n
    } else if (dataType === 'CATEGORICAL') {
      if (!stringValue.trim()) {
        setValidationError('Category required')
        return
      }
      if (
        selectedConfig &&
        selectedConfig.categories.length > 0 &&
        !selectedConfig.categories.includes(stringValue)
      ) {
        setValidationError('Pick from allowed categories')
        return
      }
      params.stringValue = stringValue
    } else if (dataType === 'BOOLEAN') {
      params.booleanValue = booleanValue
    }

    if (comment.trim()) {
      params.comment = comment.trim()
    }

    onSubmit(params)
  }

  const inputCls =
    'w-full bg-brand-main-700/50 text-xs text-brand-main-50 rounded px-2 py-1.5 border border-brand-main-500 focus:border-brand-secondary-500 outline-none transition-colors placeholder:text-brand-main-50 light:text-black light:placeholder:text-black'

  const locked = !!selectedConfig
  const numericPlaceholder =
    selectedConfig?.dataType === 'NUMERIC'
      ? `${selectedConfig.minValue ?? '–∞'} … ${selectedConfig.maxValue ?? '∞'}`
      : '0.0'

  return (
    <form onSubmit={handleSubmit} className="space-y-2">
      {scoreConfigs.length > 0 && !isEdit && (
        <div className="space-y-1">
          <label className="text-[10px] text-brand-main-50 light:text-black">
            Template (optional)
          </label>
          <select
            value={selectedConfigId}
            onChange={(e) => onPickConfig(e.target.value)}
            className={inputCls}
          >
            <option value="">— Custom —</option>
            {scoreConfigs.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name} ({c.dataType})
              </option>
            ))}
          </select>
          {selectedConfig?.description && (
            <p className="text-[10px] text-brand-main-50 leading-tight light:text-black">
              {selectedConfig.description}
            </p>
          )}
        </div>
      )}

      <div className="grid grid-cols-2 gap-2">
        <div className="space-y-1">
          <label className="text-[10px] text-brand-main-50 light:text-black">
            Name
          </label>
          <input
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g., quality"
            className={cn(inputCls, locked && 'opacity-60 cursor-not-allowed')}
            disabled={locked}
          />
        </div>
        <div className="space-y-1">
          <label className="text-[10px] text-brand-main-50 light:text-black">
            Type
          </label>
          <select
            value={dataType}
            onChange={(e) => setDataType(e.target.value as typeof dataType)}
            className={cn(inputCls, locked && 'opacity-60 cursor-not-allowed')}
            disabled={locked}
          >
            <option value="NUMERIC">Numeric</option>
            <option value="CATEGORICAL">Categorical</option>
            <option value="BOOLEAN">Boolean</option>
          </select>
        </div>
      </div>

      <div className="space-y-1">
        <label className="text-[10px] text-brand-main-50 light:text-black">
          Value
          {selectedConfig?.dataType === 'NUMERIC' &&
            (selectedConfig.minValue !== undefined ||
              selectedConfig.maxValue !== undefined) && (
              <span className="ml-1 text-brand-main-50 light:text-black">
                [{selectedConfig.minValue ?? '–∞'},{' '}
                {selectedConfig.maxValue ?? '∞'}]
              </span>
            )}
        </label>
        {dataType === 'NUMERIC' && (
          <input
            type="number"
            step="any"
            min={selectedConfig?.minValue}
            max={selectedConfig?.maxValue}
            value={numericValue}
            onChange={(e) => setNumericValue(e.target.value)}
            placeholder={numericPlaceholder}
            className={inputCls}
          />
        )}
        {dataType === 'CATEGORICAL' &&
          (selectedConfig && selectedConfig.categories.length > 0 ? (
            <div className="flex flex-wrap gap-1">
              {selectedConfig.categories.map((cat) => (
                <button
                  key={cat}
                  type="button"
                  onClick={() => setStringValue(cat)}
                  className={cn(
                    'px-2 py-1 rounded text-[11px] border transition-colors',
                    stringValue === cat
                      ? statusBadge('info')
                      : 'bg-brand-main-700/50 border-brand-main-500 text-brand-main-50 hover:text-brand-main-50 light:text-black light:hover:text-black',
                  )}
                >
                  {cat}
                </button>
              ))}
            </div>
          ) : (
            <input
              type="text"
              value={stringValue}
              onChange={(e) => setStringValue(e.target.value)}
              placeholder="Category label"
              className={inputCls}
            />
          ))}
        {dataType === 'BOOLEAN' && (
          <div className="flex gap-1.5">
            <button
              type="button"
              onClick={() => setBooleanValue(true)}
              className={cn(
                'flex-1 py-1.5 rounded text-xs border transition-colors',
                booleanValue
                  ? statusBadge('success')
                  : 'bg-brand-main-700/50 border-brand-main-500 text-brand-main-50 light:text-black',
              )}
            >
              True
            </button>
            <button
              type="button"
              onClick={() => setBooleanValue(false)}
              className={cn(
                'flex-1 py-1.5 rounded text-xs border transition-colors',
                !booleanValue
                  ? statusBadge('error')
                  : 'bg-brand-main-700/50 border-brand-main-500 text-brand-main-50 light:text-black',
              )}
            >
              False
            </button>
          </div>
        )}
        {validationError && (
          <p className={cn('text-[10px]', statusTint.error.text)}>
            {validationError}
          </p>
        )}
      </div>

      <div className="space-y-1">
        <label className="text-[10px] text-brand-main-50 light:text-black">
          Comment
        </label>
        <input
          type="text"
          value={comment}
          onChange={(e) => setComment(e.target.value)}
          placeholder="Optional note..."
          className={inputCls}
        />
      </div>

      <div className="flex justify-end gap-1.5">
        <button
          type="button"
          className="px-2 py-1 text-[11px] text-brand-main-50 hover:text-brand-main-50 rounded transition-colors light:text-black light:hover:text-black"
          onClick={onCancel}
        >
          Cancel
        </button>
        <Button
          type="submit"
          size="sm"
          className="h-6 text-[11px] px-2.5"
          disabled={!name.trim() || isSubmitting}
        >
          {isSubmitting ? (
            <Loader2 className="h-3 w-3 animate-spin" />
          ) : (
            submitLabel
          )}
        </Button>
      </div>
    </form>
  )
}
