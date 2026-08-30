import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { Button, Loader, toast } from '@everstack/ui/components'
import { ui } from '@everstack/ui'
import {
  ChevronRight,
  FlaskConical,
  Play,
  Plus,
  SlidersHorizontal,
  Trash2,
} from 'lucide-react'
import type { BuiltinMetric, ScoreConfig } from '@/server/datasets'
import {
  useBuiltinMetrics,
  useCreateScoreConfig,
  useScoreConfigs,
  useUpdateScoreConfig,
} from '@/hooks/evaluations/use-score-configs'
import { scoreOutput, type ScoreMap } from '@/server/scoring'
import { ModelPicker } from '@/components/providers/model-picker'
import { CodeEditor } from '@/components/deployments/functions/code-editor'
import { MustacheTextarea } from './mustache-textarea'
import {
  EvaluationField,
  evaluationErrorClass,
  evaluationInputClass,
  evaluationSelectContentClass,
  evaluationSelectTriggerClass,
} from './evaluation-form'
import {
  DagPathBreadcrumb,
  DagScorerEditor,
  clearedDagDefinition,
  decodeDagDefinition,
  encodeDagDefinition,
  parseDagPath,
  starterDagDraft,
  validateDagDraft,
  type DagDraft,
  type DagPath,
} from './score-config-dag-editor'
import { GEvalRamp } from './geval-ramp'

const {
  Input,
  Popover,
  PopoverContent,
  PopoverTrigger,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Switch,
} = ui

// Implementation type — how the score is computed. Maps to scorer_type on the
// wire and to a legacy data_type (which the backend command still validates).
type ScorerType = 'llm_judge' | 'dag' | 'typescript' | 'javascript' | 'python' | 'manual'
type OutputType = 'numeric' | 'boolean' | 'categorical' | 'choice'
type MessageDraft = { role: string; content: string }
type ChoiceDraft = { choice: string; score: string }

type Draft = {
  name: string
  slug: string
  slugTouched: boolean
  description: string
  scorerType: ScorerType
  outputType: OutputType
  messages: MessageDraft[]
  evalModel: string
  temperature: string
  topP: string
  maxTokens: string
  choiceScores: ChoiceDraft[]
  useCot: boolean
  passThreshold: string
  minValue: string
  maxValue: string
  scorerCode: string
  useSandbox: boolean
  dag: DagDraft
}

const TYPE_OPTIONS: { value: ScorerType; label: string }[] = [
  { value: 'llm_judge', label: 'LLM judge' },
  { value: 'dag', label: 'Decision DAG' },
  { value: 'typescript', label: 'TypeScript' },
  { value: 'javascript', label: 'JavaScript' },
  { value: 'python', label: 'Python' },
  { value: 'manual', label: 'Human review' },
]

// Output types for an LLM judge (Score / Choice / Categorical / Boolean).
const JUDGE_OUTPUTS: { value: OutputType; label: string; hint: string }[] = [
  { value: 'numeric', label: 'Score', hint: '0–1 number' },
  { value: 'choice', label: 'Choice', hint: 'label → score' },
  { value: 'categorical', label: 'Categorical', hint: 'pick a label' },
  { value: 'boolean', label: 'Boolean', hint: 'pass / fail' },
]

const emptyDraft: Draft = {
  name: '',
  slug: '',
  slugTouched: false,
  description: '',
  scorerType: 'llm_judge',
  outputType: 'numeric',
  messages: [{ role: 'user', content: '' }],
  evalModel: '',
  temperature: '',
  topP: '',
  maxTokens: '',
  choiceScores: [
    { choice: 'A', score: '1' },
    { choice: 'B', score: '0' },
  ],
  useCot: false,
  passThreshold: '',
  minValue: '',
  maxValue: '',
  scorerCode: '',
  useSandbox: false,
  dag: starterDagDraft(),
}

function slugify(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
}

function isCodeType(t: ScorerType): boolean {
  return t === 'typescript' || t === 'javascript' || t === 'python'
}

// Map the implementation type to a legacy data_type value (the backend command
// validates data_type ∈ {NUMERIC,CATEGORICAL,BOOLEAN,LLM_JUDGE,CODE_SCORER}).
function dataTypeFor(d: Draft): string {
  // DAG scorers dispatch on dag_definition presence; LLM_JUDGE is the closest
  // legacy data_type (nodes run through the judge gateway path).
  if (d.scorerType === 'llm_judge' || d.scorerType === 'dag') return 'LLM_JUDGE'
  if (isCodeType(d.scorerType)) return 'CODE_SCORER'
  return d.outputType.toUpperCase() // manual: NUMERIC | BOOLEAN | CATEGORICAL
}

function draftFromMetric(metric: BuiltinMetric): Draft {
  const scorerType = (metric.scorerType as ScorerType) || 'llm_judge'
  const outputType = (metric.outputType as OutputType) || 'numeric'
  const messages: MessageDraft[] =
    metric.messages && metric.messages.length > 0
      ? metric.messages.map((m) => ({ role: m.role || 'user', content: m.content }))
      : [{ role: 'user', content: metric.evalPrompt || '' }]
  return {
    ...emptyDraft,
    name: metric.name,
    slug: slugify(metric.name),
    slugTouched: false,
    description: metric.description,
    scorerType,
    outputType,
    messages,
    useCot: !!metric.useCot,
    choiceScores:
      metric.choiceScores && metric.choiceScores.length > 0
        ? metric.choiceScores.map((c) => ({
            choice: c.choice,
            score: String(c.score),
          }))
        : emptyDraft.choiceScores,
  }
}

function draftFromConfig(cfg: ScoreConfig): Draft {
  const dagDraft = decodeDagDefinition(cfg.dagDefinition)
  const scorerType = dagDraft ? 'dag' : (cfg.scorerType as ScorerType) || 'llm_judge'
  const outputType = (cfg.outputType as OutputType) || 'numeric'
  return {
    ...emptyDraft,
    dag: dagDraft ?? starterDagDraft(),
    name: cfg.name,
    slug: cfg.slug || slugify(cfg.name),
    slugTouched: true,
    description: cfg.description || '',
    scorerType,
    outputType,
    messages:
      cfg.messages && cfg.messages.length > 0
        ? cfg.messages.map((m) => ({ role: m.role || 'user', content: m.content }))
        : [{ role: 'user', content: cfg.evalPrompt || '' }],
    evalModel: cfg.evalModel || '',
    temperature: cfg.modelParams?.temperature != null ? String(cfg.modelParams.temperature) : '',
    topP: cfg.modelParams?.topP != null ? String(cfg.modelParams.topP) : '',
    maxTokens: cfg.modelParams?.maxTokens != null ? String(cfg.modelParams.maxTokens) : '',
    choiceScores:
      cfg.choiceScores && cfg.choiceScores.length > 0
        ? cfg.choiceScores.map((c) => ({ choice: c.choice, score: String(c.score) }))
        : emptyDraft.choiceScores,
    useCot: !!cfg.useCot,
    passThreshold: cfg.passThreshold != null ? String(cfg.passThreshold) : '',
    minValue: cfg.minValue != null ? String(cfg.minValue) : '',
    maxValue: cfg.maxValue != null ? String(cfg.maxValue) : '',
    scorerCode: cfg.scorerCode || '',
    useSandbox: !!cfg.useSandbox,
  }
}

// ── Section shell ──────────────────────────────────────────────────────────
function Section({
  title,
  sub,
  action,
  children,
}: {
  title: string
  sub?: string
  action?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <section className="rounded-lg border border-brand-main-700 bg-brand-main-950 light:border-brand-main-200 light:bg-white">
      <div className="flex items-center justify-between gap-3 border-b border-brand-main-700 px-4 py-3 light:border-brand-main-200">
        <div className="min-w-0">
          <h3 className="text-xs font-semibold text-white light:text-brand-main-50">{title}</h3>
          {sub && <p className="mt-0.5 text-[11px] text-white/35 light:text-black/40">{sub}</p>}
        </div>
        {action}
      </div>
      <div className="flex flex-col gap-4 p-4">{children}</div>
    </section>
  )
}

export function ScoreConfigEditorPage({ configId }: { configId?: string }) {
  const navigate = useNavigate()
  const isEdit = !!configId
  const createMutation = useCreateScoreConfig()
  const updateMutation = useUpdateScoreConfig()
  const mutation = isEdit ? updateMutation : createMutation
  const { data: builtinMetrics, isLoading: metricsLoading } = useBuiltinMetrics()
  const { data: configs } = useScoreConfigs()
  const existing = useMemo(
    () => (configId ? (configs ?? []).find((c) => c.id === configId) : undefined),
    [configs, configId],
  )

  const [draft, setDraft] = useState<Draft>({ ...emptyDraft })
  const [hydrated, setHydrated] = useState(false)

  useEffect(() => {
    if (isEdit && existing && !hydrated) {
      setDraft(draftFromConfig(existing))
      setHydrated(true)
    }
  }, [isEdit, existing, hydrated])

  // DAG inspector state: the traversed path from the latest test run. Any
  // graph edit invalidates the highlight.
  const [dagPath, setDagPath] = useState<DagPath | null>(null)
  useEffect(() => {
    setDagPath(null)
  }, [draft.dag])
  const dagNodeIds = useMemo(() => new Set(draft.dag.nodes.map((n) => n.id)), [draft.dag])
  // True when the SAVED config is a DAG scorer — switching away must clear the
  // persisted dag_definition or the runner would keep dispatching to it.
  const hadDagDefinition = useMemo(
    () => decodeDagDefinition(existing?.dagDefinition) != null,
    [existing],
  )
  const dagErrors = useMemo(
    () => (draft.scorerType === 'dag' ? validateDagDraft(draft.dag) : []),
    [draft.scorerType, draft.dag],
  )

  const update = <K extends keyof Draft>(key: K, value: Draft[K]) =>
    setDraft((cur) => ({ ...cur, [key]: value }))

  const setName = (name: string) =>
    setDraft((cur) => ({
      ...cur,
      name,
      slug: cur.slugTouched ? cur.slug : slugify(name),
    }))

  const buildParams = () => {
    const messages = draft.messages
      .filter((m) => m.content.trim() !== '')
      .map((m) => ({ role: m.role, content: m.content }))
    const modelParams =
      draft.temperature || draft.topP || draft.maxTokens
        ? {
            temperature: draft.temperature ? Number(draft.temperature) : undefined,
            topP: draft.topP ? Number(draft.topP) : undefined,
            maxTokens: draft.maxTokens ? Number(draft.maxTokens) : undefined,
          }
        : undefined
    const choiceScores =
      draft.scorerType === 'llm_judge' && draft.outputType === 'choice'
        ? draft.choiceScores
            .filter((c) => c.choice.trim() !== '')
            .map((c) => ({ choice: c.choice.trim(), score: Number(c.score) || 0 }))
        : undefined
    return {
      name: draft.name,
      dataType: dataTypeFor(draft),
      slug: draft.slug || undefined,
      scorerType: draft.scorerType,
      outputType: draft.outputType,
      description: draft.description || undefined,
      minValue:
        draft.scorerType === 'manual' && draft.outputType === 'numeric' && draft.minValue
          ? Number(draft.minValue)
          : undefined,
      maxValue:
        draft.scorerType === 'manual' && draft.outputType === 'numeric' && draft.maxValue
          ? Number(draft.maxValue)
          : undefined,
      messages: draft.scorerType === 'llm_judge' ? messages : undefined,
      evalModel:
        draft.scorerType === 'llm_judge' || draft.scorerType === 'dag'
          ? draft.evalModel || undefined
          : undefined,
      modelParams:
        draft.scorerType === 'llm_judge' || draft.scorerType === 'dag'
          ? modelParams
          : undefined,
      dagDefinition:
        draft.scorerType === 'dag'
          ? encodeDagDefinition(draft.dag)
          : hadDagDefinition
            ? clearedDagDefinition()
            : undefined,
      choiceScores,
      useCot:
        draft.scorerType === 'llm_judge' && draft.outputType === 'choice'
          ? draft.useCot
          : undefined,
      passThreshold: draft.passThreshold ? Number(draft.passThreshold) : undefined,
      scorerCode: isCodeType(draft.scorerType) ? draft.scorerCode || undefined : undefined,
      scorerLanguage: isCodeType(draft.scorerType) ? draft.scorerType : undefined,
      useSandbox:
        draft.scorerType === 'llm_judge' || isCodeType(draft.scorerType)
          ? draft.useSandbox || undefined
          : undefined,
    }
  }

  const handleSubmit = async (event: React.FormEvent) => {
    event.preventDefault()
    if (draft.scorerType === 'llm_judge' && draft.outputType === 'choice') {
      const validChoices = draft.choiceScores.filter((c) => c.choice.trim() !== '')
      if (validChoices.length === 0) {
        toast.error('Add at least one choice for a choice scorer')
        return
      }
    }
    if (draft.scorerType === 'dag') {
      const dagErrors = validateDagDraft(draft.dag)
      if (dagErrors.length > 0) {
        toast.error(dagErrors[0])
        return
      }
    }
    try {
      const params = buildParams()
      if (isEdit && configId) {
        await updateMutation.mutateAsync({ id: configId, ...params })
        toast.success('Score config updated')
        void navigate({ to: '/evaluations/score-configs' })
      } else {
        const res = (await createMutation.mutateAsync(params)) as {
          scoreConfig?: { id?: string }
        }
        toast.success('Score config created')
        const newId = res?.scoreConfig?.id
        // Land on the edit page so live testing is immediately available.
        if (newId) {
          void navigate({
            to: '/evaluations/score-configs/$configId',
            params: { configId: newId },
          })
        } else {
          void navigate({ to: '/evaluations/score-configs' })
        }
      }
    } catch {
      toast.error(isEdit ? 'Failed to update score config' : 'Failed to create score config')
    }
  }

  const isJudge = draft.scorerType === 'llm_judge'
  const isDag = draft.scorerType === 'dag'
  const isChoice = isJudge && draft.outputType === 'choice'
  const isCode = isCodeType(draft.scorerType)
  const isManual = draft.scorerType === 'manual'

  // DAG test inspector plumbing: edits are saved before scoring (ScoreOutput
  // runs saved configs), then the result reason is parsed into the traversed
  // node path and highlighted on the canvas.
  const saveBeforeDagTest = async () => {
    const errs = validateDagDraft(draft.dag)
    if (errs.length > 0) throw new Error(errs[0])
    if (configId) await updateMutation.mutateAsync({ id: configId, ...buildParams() })
  }
  const handleTestResult = (scores: ScoreMap) => {
    if (!isDag) return
    const reason = scores[`${draft.name}_reason`]
    setDagPath(typeof reason === 'string' ? parseDagPath(reason, dagNodeIds) : null)
  }
  const renderDagReason = (reason: string) => {
    const path = parseDagPath(reason, dagNodeIds)
    return path ? (
      <DagPathBreadcrumb path={path} />
    ) : (
      <p className="text-xs leading-relaxed text-white/80 light:text-black/75">{reason}</p>
    )
  }

  // G-Eval lock: write the generated steps into the judge messages — fill the
  // trailing empty user turn if there is one, else append a new user turn.
  const lockGeneratedPrompt = (text: string) =>
    setDraft((cur) => {
      const messages = [...cur.messages]
      const last = messages[messages.length - 1]
      if (last && last.role === 'user' && last.content.trim() === '') {
        messages[messages.length - 1] = { ...last, content: text }
      } else {
        messages.push({ role: 'user', content: text })
      }
      return { ...cur, messages }
    })

  // In edit mode, never render the form against emptyDraft — that would let a
  // Save clobber the config with blank values. Wait for hydration; if the
  // config isn't in the list at all, say so instead of showing a blank form.
  if (isEdit && !hydrated) {
    return (
      <div className="flex h-full w-full items-center justify-center">
        {configs && !existing ? (
          <div className="text-sm text-white/50 light:text-black/50">
            Score config not found.
          </div>
        ) : (
          <Loader loaderText="Loading score config..." />
        )}
      </div>
    )
  }

  return (
    <form onSubmit={handleSubmit} className="flex h-full w-full flex-col">
      {/* header / action bar */}
      <header className="flex shrink-0 items-center justify-between gap-3 border-b border-brand-main-700 bg-brand-main-950 px-5 py-3 light:border-brand-main-200 light:bg-white">
        <div className="flex min-w-0 items-center gap-2 text-sm text-white/50 light:text-black/50">
          <button
            type="button"
            onClick={() => void navigate({ to: '/evaluations/score-configs' })}
            className="hover:text-white light:hover:text-black"
          >
            Score configs
          </button>
          <ChevronRight className="h-3.5 w-3.5 text-white/25 light:text-black/25" />
          <span className="truncate font-medium text-white light:text-brand-main-50">
            {draft.name || 'New scorer'}
          </span>
          {draft.slug && (
            <span className="hidden rounded border border-brand-main-700 bg-brand-main-900 px-1.5 py-0.5 font-mono text-[11px] text-white/40 sm:inline light:border-brand-main-200 light:bg-brand-main-50 light:text-black/45">
              {draft.slug}
            </span>
          )}
        </div>
        <div className="flex items-center gap-2">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => void navigate({ to: '/evaluations/score-configs' })}
            disabled={mutation.isPending}
          >
            Cancel
          </Button>
          <Button type="submit" size="sm" disabled={mutation.isPending}>
            {mutation.isPending
              ? isEdit
                ? 'Saving...'
                : 'Creating...'
              : isEdit
                ? 'Save scorer'
                : 'Create scorer'}
          </Button>
        </div>
      </header>

      <div className="flex min-h-0 flex-1">
        {/* LEFT — definition */}
        <div className="flex-1 overflow-y-auto scrollbar-macos">
          <div
            className={`mx-auto flex ${isDag ? 'max-w-6xl' : 'max-w-3xl'} flex-col gap-4 px-5 py-5`}
          >
            {mutation.error && (
              <div className={evaluationErrorClass}>{(mutation.error as Error).message}</div>
            )}

            <Section title="Scorer type" sub="Batteries included · edit anything">
              <div className="flex flex-wrap gap-1 rounded-lg border border-brand-main-700 bg-brand-main-900 p-0.5 light:border-brand-main-200 light:bg-white">
                {TYPE_OPTIONS.map((opt) => (
                  <button
                    key={opt.value}
                    type="button"
                    onClick={() => update('scorerType', opt.value)}
                    className={`rounded px-2.5 py-1.5 text-xs font-medium transition-[color,background-color,transform] duration-150 ease-out-strong active:scale-[0.98] motion-reduce:transition-none motion-reduce:active:scale-100 ${
                      draft.scorerType === opt.value
                        ? 'bg-brand-main-700 text-white light:bg-brand-main-100 light:text-brand-main-950'
                        : 'text-white/45 hover:text-white light:text-black/45 light:hover:text-black'
                    }`}
                  >
                    {opt.label}
                  </button>
                ))}
              </div>
              <div>
                <span className="mb-1.5 block text-[10.5px] font-semibold uppercase tracking-wide text-white/40 light:text-black/45">
                  Start from a template
                </span>
                <div className="flex gap-2 overflow-x-auto pb-1 scrollbar-macos">
                  {metricsLoading ? (
                    <span className="text-xs text-white/40 light:text-black/45">Loading…</span>
                  ) : (
                    (builtinMetrics ?? []).map((metric) => (
                      <button
                        key={metric.key}
                        type="button"
                        onClick={() => {
                          setDraft(draftFromMetric(metric))
                          setHydrated(true)
                        }}
                        className="shrink-0 rounded-lg border border-brand-main-700 bg-brand-main-900 px-3 py-2 text-left transition-colors hover:border-brand-secondary-500/60 light:border-brand-main-200 light:bg-white"
                      >
                        <div className="text-xs font-semibold text-white light:text-brand-main-50">
                          {metric.name}
                        </div>
                        <div className="text-[10.5px] text-white/40 light:text-black/45">
                          {metric.category}
                        </div>
                      </button>
                    ))
                  )}
                </div>
              </div>
            </Section>

            <Section title="Definition">
              <div className="grid grid-cols-2 gap-3">
                <EvaluationField label="Name" htmlFor="sc-name">
                  <Input
                    id="sc-name"
                    placeholder="Factuality"
                    value={draft.name}
                    onChange={(e) => setName(e.target.value)}
                    required
                    className={evaluationInputClass}
                  />
                </EvaluationField>
                <EvaluationField label="Slug" htmlFor="sc-slug">
                  <Input
                    id="sc-slug"
                    placeholder="factuality"
                    value={draft.slug}
                    onChange={(e) => {
                      update('slug', slugify(e.target.value))
                      update('slugTouched', true)
                    }}
                    className={`${evaluationInputClass} font-mono`}
                  />
                </EvaluationField>
              </div>
              <EvaluationField label="Description" htmlFor="sc-desc">
                <Input
                  id="sc-desc"
                  placeholder="What does this score measure?"
                  value={draft.description}
                  onChange={(e) => update('description', e.target.value)}
                  className={evaluationInputClass}
                />
              </EvaluationField>
              {(isJudge || isDag) && (
                <EvaluationField label="Judge model">
                  <div className="flex items-center gap-2">
                    <div className="min-w-0 flex-1">
                      <ModelPicker
                        value={draft.evalModel}
                        onChange={(m) => update('evalModel', m)}
                        placeholder="Default eval model"
                      />
                    </div>
                    <Popover>
                      <PopoverTrigger asChild>
                        <Button type="button" variant="outline" size="sm">
                          <SlidersHorizontal className="h-3.5 w-3.5" />
                          Params
                        </Button>
                      </PopoverTrigger>
                      <PopoverContent
                        align="end"
                        className="w-64 border-brand-main-700 bg-brand-main-950 light:border-brand-main-200 light:bg-white"
                      >
                        <div className="space-y-3">
                          <p className="text-xs font-medium text-white light:text-brand-main-50">
                            Model parameters
                          </p>
                          <ParamRow label="Temperature" value={draft.temperature} step="0.1" onChange={(v) => update('temperature', v)} />
                          <ParamRow label="Top P" value={draft.topP} step="0.1" onChange={(v) => update('topP', v)} />
                          <ParamRow label="Max tokens" value={draft.maxTokens} step="1" onChange={(v) => update('maxTokens', v)} />
                        </div>
                      </PopoverContent>
                    </Popover>
                  </div>
                </EvaluationField>
              )}
            </Section>

            {isJudge && (
              <Section title="Judge prompt">
                <MessagesEditor
                  messages={draft.messages}
                  onChange={(messages) => update('messages', messages)}
                />
                <GEvalRamp onLock={lockGeneratedPrompt} />
              </Section>
            )}

            {isDag && (
              <Section
                title="Decision graph"
                sub="Task and judgement nodes route by label; only a verdict leaf assigns the score — that is what makes the metric reproducible."
              >
                {dagErrors.length > 0 && (
                  <ul className="space-y-1 rounded border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs text-amber-300 light:text-amber-700">
                    {dagErrors.map((e, i) => (
                      <li key={i}>{e}</li>
                    ))}
                  </ul>
                )}
                <DagScorerEditor
                  value={draft.dag}
                  onChange={(dag) => update('dag', dag)}
                  path={dagPath}
                />
              </Section>
            )}

            {isCode && (
              <Section title="Scorer code" sub="Receives { input, output, expected, metadata }; returns a score.">
                <CodeEditor
                  value={draft.scorerCode}
                  onChange={(v) => update('scorerCode', v)}
                  language={draft.scorerType as 'typescript' | 'javascript' | 'python'}
                  height="320px"
                />
              </Section>
            )}

            {isJudge && (
              <Section title="Output" sub="Structured — the judge can't return an invalid score">
                <div>
                  <span className="mb-1.5 block text-[10.5px] font-semibold uppercase tracking-wide text-white/40 light:text-black/45">
                    Score type
                  </span>
                  <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                    {JUDGE_OUTPUTS.map((o) => (
                      <button
                        key={o.value}
                        type="button"
                        onClick={() => update('outputType', o.value)}
                        className={`rounded-lg border px-3 py-2 text-left transition-colors ${
                          draft.outputType === o.value
                            ? 'border-brand-secondary-500 bg-brand-secondary-500/10'
                            : 'border-brand-main-700 bg-brand-main-900 hover:border-brand-main-600 light:border-brand-main-200 light:bg-white'
                        }`}
                      >
                        <div className="text-xs font-semibold text-white light:text-brand-main-50">
                          {o.label}
                        </div>
                        <div className="text-[10px] text-white/40 light:text-black/45">{o.hint}</div>
                      </button>
                    ))}
                  </div>
                </div>

                {isChoice && (
                  <ChoiceScoresTable
                    choices={draft.choiceScores}
                    onChange={(choiceScores) => update('choiceScores', choiceScores)}
                  />
                )}

                {isChoice && (
                  <ToggleRow
                    checked={draft.useCot}
                    onChange={(v) => update('useCot', v)}
                    label="Use chain of thought (CoT)"
                    hint="Ask the judge to reason step by step before choosing. Reasoning is captured with the score."
                  />
                )}

                <PassThreshold value={draft.passThreshold} onChange={(v) => update('passThreshold', v)} />
                <ToggleRow
                  checked={draft.useSandbox}
                  onChange={(v) => update('useSandbox', v)}
                  label="Run in sandbox"
                  hint="Execute the judge in an isolated sandbox."
                />
              </Section>
            )}

            {isManual && (
              <Section title="Output">
                <EvaluationField label="Output type">
                  <Select value={draft.outputType} onValueChange={(v) => update('outputType', v as OutputType)}>
                    <SelectTrigger className={evaluationSelectTriggerClass}>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent className={evaluationSelectContentClass}>
                      <SelectItem value="numeric">Numeric</SelectItem>
                      <SelectItem value="boolean">Boolean</SelectItem>
                      <SelectItem value="categorical">Categorical</SelectItem>
                    </SelectContent>
                  </Select>
                </EvaluationField>
                {draft.outputType === 'numeric' && (
                  <div className="grid grid-cols-2 gap-3">
                    <EvaluationField label="Min value" htmlFor="sc-min">
                      <Input id="sc-min" type="number" placeholder="0" value={draft.minValue} onChange={(e) => update('minValue', e.target.value)} className={evaluationInputClass} />
                    </EvaluationField>
                    <EvaluationField label="Max value" htmlFor="sc-max">
                      <Input id="sc-max" type="number" placeholder="1" value={draft.maxValue} onChange={(e) => update('maxValue', e.target.value)} className={evaluationInputClass} />
                    </EvaluationField>
                  </div>
                )}
              </Section>
            )}

            {isCode && (
              <Section title="Output">
                <PassThreshold value={draft.passThreshold} onChange={(v) => update('passThreshold', v)} />
                <ToggleRow
                  checked={draft.useSandbox}
                  onChange={(v) => update('useSandbox', v)}
                  label="Run in sandbox"
                  hint="Execute the scorer in an isolated sandbox."
                />
              </Section>
            )}

            {isDag && (
              <Section title="Output" sub="The verdict leaf's score is the metric value (0–1)">
                <PassThreshold value={draft.passThreshold} onChange={(v) => update('passThreshold', v)} />
              </Section>
            )}
          </div>
        </div>

        {/* RIGHT — live test */}
        <ScorerTestPanel
          configId={configId}
          scorerName={draft.name}
          passThreshold={draft.passThreshold ? Number(draft.passThreshold) : undefined}
          choices={draft.choiceScores}
          isChoice={isChoice}
          beforeRun={isDag && isEdit ? saveBeforeDagTest : undefined}
          onResult={isDag ? handleTestResult : undefined}
          renderReason={isDag ? renderDagReason : undefined}
          runLabel={isDag && isEdit ? 'Save & run test' : undefined}
        />
      </div>
    </form>
  )
}

// ── Live test panel ─────────────────────────────────────────────────────────
function ScorerTestPanel({
  configId,
  scorerName,
  passThreshold,
  choices,
  isChoice,
  beforeRun,
  onResult,
  renderReason,
  runLabel,
}: {
  configId?: string
  scorerName: string
  passThreshold?: number
  choices: ChoiceDraft[]
  isChoice: boolean
  /** Runs before scoring (e.g. save the in-progress config); throwing aborts. */
  beforeRun?: () => Promise<void>
  onResult?: (scores: ScoreMap) => void
  /** Custom rendering for the result reason (e.g. the DAG path breadcrumb). */
  renderReason?: (reason: string) => React.ReactNode
  runLabel?: string
}) {
  const [input, setInput] = useState('')
  const [output, setOutput] = useState('')
  const [expected, setExpected] = useState('')
  const [running, setRunning] = useState(false)
  const [result, setResult] = useState<ScoreMap | null>(null)
  const [error, setError] = useState<string | null>(null)

  const parse = (s: string): unknown => {
    const t = s.trim()
    if (!t) return undefined
    try {
      return JSON.parse(t)
    } catch {
      return s
    }
  }

  const run = async () => {
    if (!configId) return
    setRunning(true)
    setError(null)
    try {
      if (beforeRun) await beforeRun()
      const scores = await scoreOutput({
        input: parse(input),
        output: parse(output) ?? '',
        expectedOutput: parse(expected),
        scorerConfigIds: [configId],
      })
      setResult(scores)
      onResult?.(scores)
    } catch (e) {
      setError((e as Error).message || 'Test run failed')
    } finally {
      setRunning(false)
    }
  }

  const rawScore = result ? result[scorerName] : undefined
  const scoreNum = typeof rawScore === 'number' ? rawScore : Number(rawScore)
  const hasScore = result != null && rawScore != null && !Number.isNaN(scoreNum)
  const reason = result?.[`${scorerName}_reason`] as string | undefined
  const runErr = result?.[`${scorerName}_error`] as string | undefined
  const passes = passThreshold != null && hasScore ? scoreNum >= passThreshold : undefined

  return (
    <aside className="flex w-[400px] shrink-0 flex-col border-l border-brand-main-700 bg-brand-main-950 light:border-brand-main-200 light:bg-white max-lg:hidden">
      <div className="flex shrink-0 items-center justify-between border-b border-brand-main-700 px-4 py-3 light:border-brand-main-200">
        <h3 className="flex items-center gap-2 text-xs font-semibold text-white light:text-brand-main-50">
          <FlaskConical className="h-3.5 w-3.5 text-brand-secondary-300 light:text-brand-secondary-700" />
          Live test
        </h3>
      </div>

      <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto p-4 scrollbar-macos">
        {!configId ? (
          <div className="rounded-lg border border-dashed border-brand-main-700 p-4 text-center text-xs leading-relaxed text-white/45 light:border-brand-main-300 light:text-black/50">
            Save this scorer to run live tests against real inputs and traces.
          </div>
        ) : (
          <>
            <TestInput label="Input" value={input} onChange={setInput} placeholder="What is the capital of Australia?" />
            <TestInput label="Output" value={output} onChange={setOutput} placeholder="The capital is Canberra." />
            <TestInput label="Expected" value={expected} onChange={setExpected} placeholder="Canberra" />
            <Button type="button" onClick={run} disabled={running} className="w-full">
              <Play className="h-3.5 w-3.5" />
              {running ? 'Scoring…' : (runLabel ?? 'Run scorer')}
            </Button>

            {error && (
              <div className="rounded border border-red-500/40 bg-red-500/10 px-3 py-2 text-xs text-red-300 light:text-red-600">
                {error}
              </div>
            )}

            {runErr && (
              <div className="rounded border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs text-amber-300 light:text-amber-700">
                {runErr}
              </div>
            )}

            {result && !runErr && (
              <div className="overflow-hidden rounded-lg border border-brand-main-700 light:border-brand-main-200">
                <div className="flex items-center justify-between gap-3 border-b border-brand-main-700 bg-brand-main-900/60 px-4 py-3 light:border-brand-main-200 light:bg-brand-main-50">
                  <div className="flex items-center gap-3">
                    <span
                      className={`font-mono text-lg font-bold tabular-nums ${
                        passes === false ? 'text-red-400' : 'text-emerald-400'
                      }`}
                    >
                      {hasScore ? scoreNum.toFixed(2) : '—'}
                    </span>
                    <span className="text-[11px] text-white/45 light:text-black/50">
                      {scorerName || 'score'}
                    </span>
                  </div>
                  {passes != null && (
                    <span
                      className={`rounded-full px-2 py-0.5 text-[10px] font-semibold ${
                        passes
                          ? 'bg-emerald-500/15 text-emerald-300 light:text-emerald-600'
                          : 'bg-red-500/15 text-red-300 light:text-red-600'
                      }`}
                    >
                      {passes ? 'PASS' : 'FAIL'} ≥ {passThreshold?.toFixed(2)}
                    </span>
                  )}
                </div>
                <div className="flex flex-col gap-3 p-4">
                  {reason && (
                    <div>
                      <span className="mb-1 block text-[10px] font-semibold uppercase tracking-wide text-white/40 light:text-black/45">
                        {renderReason ? 'Path taken' : 'Reasoning'}
                      </span>
                      {renderReason ? (
                        renderReason(reason)
                      ) : (
                        <p className="text-xs leading-relaxed text-white/80 light:text-black/75">{reason}</p>
                      )}
                    </div>
                  )}
                  {isChoice && choices.length > 0 && (
                    <div className="flex flex-wrap gap-1.5">
                      {choices
                        .filter((c) => c.choice.trim() !== '')
                        .map((c) => {
                          const picked = hasScore && Number(c.score) === scoreNum
                          return (
                            <span
                              key={c.choice}
                              className={`rounded border px-2 py-0.5 font-mono text-[10.5px] ${
                                picked
                                  ? 'border-emerald-500 bg-emerald-500/10 text-emerald-300 light:text-emerald-600'
                                  : 'border-brand-main-700 text-white/45 light:border-brand-main-200 light:text-black/45'
                              }`}
                            >
                              {c.choice} · {c.score}
                            </span>
                          )
                        })}
                    </div>
                  )}
                </div>
              </div>
            )}
          </>
        )}

        <p className="mt-auto pt-2 text-[10.5px] leading-relaxed text-white/25 light:text-black/35">
          Template variables:{' '}
          <code className="text-white/45 light:text-black/50">
            {'{{input}} {{output}} {{expected_output}} {{context}} {{metadata}}'}
          </code>
          . Dataset &amp; Logs test sources are coming next.
        </p>
      </div>
    </aside>
  )
}

function TestInput({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string
  value: string
  onChange: (v: string) => void
  placeholder?: string
}) {
  return (
    <div>
      <span className="mb-1 block text-[10.5px] font-semibold uppercase tracking-wide text-white/40 light:text-black/45">
        {label}
      </span>
      <textarea
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
        rows={2}
        spellCheck={false}
        className={`${evaluationInputClass} min-h-[48px] resize-y font-mono text-xs`}
      />
    </div>
  )
}

function ToggleRow({
  checked,
  onChange,
  label,
  hint,
}: {
  checked: boolean
  onChange: (v: boolean) => void
  label: string
  hint?: string
}) {
  return (
    <div className="flex items-center justify-between gap-3 rounded-lg border border-brand-main-700 bg-brand-main-900/50 px-3 py-2.5 light:border-brand-main-200 light:bg-brand-main-50">
      <div className="min-w-0">
        <div className="text-sm text-white light:text-brand-main-50">{label}</div>
        {hint && <div className="text-xs text-white/40 light:text-black/45">{hint}</div>}
      </div>
      <Switch checked={checked} onCheckedChange={onChange} />
    </div>
  )
}

function PassThreshold({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <EvaluationField label="Pass threshold" htmlFor="sc-thresh">
      <div className="flex items-center gap-3">
        <input
          id="sc-thresh"
          type="range"
          min="0"
          max="1"
          step="0.05"
          value={value || '0'}
          onChange={(e) => onChange(e.target.value)}
          className="flex-1 accent-brand-secondary-500"
        />
        <span className="w-10 text-right font-mono text-xs tabular-nums text-white/70 light:text-black/70">
          {value ? Number(value).toFixed(2) : '—'}
        </span>
      </div>
    </EvaluationField>
  )
}

function MessagesEditor({
  messages,
  onChange,
}: {
  messages: MessageDraft[]
  onChange: (messages: MessageDraft[]) => void
}) {
  const setAt = (i: number, patch: Partial<MessageDraft>) =>
    onChange(messages.map((m, idx) => (idx === i ? { ...m, ...patch } : m)))
  const remove = (i: number) => onChange(messages.filter((_, idx) => idx !== i))
  const add = () => onChange([...messages, { role: 'user', content: '' }])

  return (
    <div className="space-y-2">
      {messages.map((m, i) => (
        <div
          key={i}
          className="rounded-lg border border-brand-main-700 bg-brand-main-900 p-2 light:border-brand-main-200 light:bg-white"
        >
          <div className="mb-1.5 flex items-center justify-between gap-2">
            <Select value={m.role} onValueChange={(v) => setAt(i, { role: v })}>
              <SelectTrigger className={`${evaluationSelectTriggerClass} h-7 w-32`}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent className={evaluationSelectContentClass}>
                <SelectItem value="system">System</SelectItem>
                <SelectItem value="user">User</SelectItem>
                <SelectItem value="assistant">Assistant</SelectItem>
              </SelectContent>
            </Select>
            {messages.length > 1 && (
              <button
                type="button"
                onClick={() => remove(i)}
                className="text-white/35 hover:text-red-400 light:text-black/35"
                aria-label="Remove message"
              >
                <Trash2 className="h-3.5 w-3.5" />
              </button>
            )}
          </div>
          <MustacheTextarea
            value={m.content}
            onChange={(content) => setAt(i, { content })}
            placeholder={'Compare {{output}} to {{expected_output}} for {{input}}...'}
            rows={6}
            showVarChips={i === messages.length - 1}
          />
        </div>
      ))}
      <Button type="button" variant="outline" size="sm" onClick={add}>
        <Plus className="h-3.5 w-3.5" />
        Add message
      </Button>
    </div>
  )
}

function ParamRow({
  label,
  value,
  step,
  onChange,
}: {
  label: string
  value: string
  step: string
  onChange: (v: string) => void
}) {
  return (
    <label className="flex items-center justify-between gap-3">
      <span className="text-xs text-white/60 light:text-black/60">{label}</span>
      <Input
        type="number"
        step={step}
        placeholder="default"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className={`${evaluationInputClass} h-7 w-24`}
      />
    </label>
  )
}

function ChoiceScoresTable({
  choices,
  onChange,
}: {
  choices: ChoiceDraft[]
  onChange: (choices: ChoiceDraft[]) => void
}) {
  const setAt = (i: number, patch: Partial<ChoiceDraft>) =>
    onChange(choices.map((c, idx) => (idx === i ? { ...c, ...patch } : c)))
  const remove = (i: number) => onChange(choices.filter((_, idx) => idx !== i))
  const add = () => onChange([...choices, { choice: '', score: '0' }])

  return (
    <div>
      <span className="mb-1.5 block text-[10.5px] font-semibold uppercase tracking-wide text-white/40 light:text-black/45">
        Choice scores
      </span>
      <div className="space-y-1.5">
        <div className="flex gap-2 px-1 text-[10px] uppercase tracking-wide text-white/35 light:text-black/35">
          <span className="flex-1">Choice</span>
          <span className="w-24">Score (0-1)</span>
          <span className="w-6" />
        </div>
        {choices.map((c, i) => (
          <div key={i} className="flex items-center gap-2">
            <Input
              value={c.choice}
              onChange={(e) => setAt(i, { choice: e.target.value })}
              placeholder="A"
              className={`${evaluationInputClass} flex-1`}
            />
            <Input
              type="number"
              step="0.1"
              min="0"
              max="1"
              value={c.score}
              onChange={(e) => setAt(i, { score: e.target.value })}
              className={`${evaluationInputClass} w-24 text-right tabular-nums`}
            />
            {choices.length > 1 && (
              <button
                type="button"
                onClick={() => remove(i)}
                className="w-6 text-white/35 hover:text-red-400 light:text-black/35"
                aria-label="Remove choice"
              >
                <Trash2 className="h-3.5 w-3.5" />
              </button>
            )}
          </div>
        ))}
        <Button type="button" variant="outline" size="sm" onClick={add}>
          <Plus className="h-3.5 w-3.5" />
          Add choice
        </Button>
      </div>
    </div>
  )
}
