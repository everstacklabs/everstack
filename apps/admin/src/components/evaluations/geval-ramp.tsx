import { useState } from 'react'
import { Button, toast } from '@everstack/ui/components'
import { Lock, Sparkles } from 'lucide-react'
import { generateScorer } from '@/server/scoring'
import { EvaluationDisclosure, EvaluationField, evaluationTextareaClass } from './evaluation-form'
import { MustacheTextarea } from './mustache-textarea'

// G-Eval ramp (DeepEval-style): plain-English criteria -> LLM-expanded ordered
// evaluation steps -> user inspects/edits -> locked into the judge prompt.
// Reuses the existing EvalService.GenerateScorer RPC, steered to emit a
// numbered evaluation-steps prompt.

const TEMPLATE_VARS = ['input', 'output', 'expected_output', 'expected', 'context', 'metadata']

/** The generator emits single-brace {vars}; the judge substitutes {{vars}}. */
function normalizeTemplateVars(prompt: string): string {
  let out = prompt
  for (const v of TEMPLATE_VARS) {
    out = out.replace(new RegExp(`(?<!\\{)\\{\\s*${v}\\s*\\}(?!\\})`, 'g'), `{{${v}}}`)
  }
  return out
}

function buildIntent(criteria: string): string {
  return [
    'G-Eval: expand the following plain-English evaluation criteria into an LLM-judge prompt structured as explicit, ordered evaluation steps.',
    'The prompt must contain a section titled "Evaluation steps:" followed by a numbered list of concrete checks the judge performs in order, then instruct the judge to derive a single 0..1 score from those steps.',
    '',
    `Criteria: ${criteria}`,
  ].join('\n')
}

export function GEvalRamp({ onLock }: { onLock: (promptText: string) => void }) {
  const [open, setOpen] = useState(false)
  const [criteria, setCriteria] = useState('')
  const [steps, setSteps] = useState('')
  const [notes, setNotes] = useState('')
  const [generating, setGenerating] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const generate = async () => {
    if (!criteria.trim()) return
    setGenerating(true)
    setError(null)
    try {
      const res = await generateScorer({ intent: buildIntent(criteria), dataType: 'numeric' })
      setSteps(normalizeTemplateVars(res.prompt))
      setNotes(res.notes || '')
    } catch (e) {
      setError((e as Error).message || 'Failed to generate evaluation steps')
    } finally {
      setGenerating(false)
    }
  }

  return (
    <EvaluationDisclosure
      label="G-Eval: generate evaluation steps from criteria"
      open={open}
      onOpenChange={setOpen}
    >
      <EvaluationField
        label="Criteria"
        htmlFor="geval-criteria"
        action={
          <span className="text-[10.5px] text-white/35 light:text-black/40">
            plain English — what should the judge measure?
          </span>
        }
      >
        <textarea
          id="geval-criteria"
          value={criteria}
          onChange={(e) => setCriteria(e.target.value)}
          placeholder="The response should be factually accurate, cite the given context, and avoid speculation."
          rows={3}
          className={`${evaluationTextareaClass} w-full resize-y rounded-md border px-3 py-2`}
        />
      </EvaluationField>

      <div className="flex items-center gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={generate}
          disabled={generating || !criteria.trim()}
        >
          <Sparkles className="h-3.5 w-3.5" />
          {generating ? 'Generating…' : 'Generate evaluation steps'}
        </Button>
      </div>

      {error && (
        <div className="rounded border border-red-500/40 bg-red-500/10 px-3 py-2 text-xs text-red-300 light:text-red-600">
          {error}
        </div>
      )}

      {steps && (
        <>
          <EvaluationField
            label="Evaluation steps"
            action={
              <span className="text-[10.5px] text-white/35 light:text-black/40">
                inspect and edit before locking
              </span>
            }
          >
            <MustacheTextarea value={steps} onChange={setSteps} rows={10} showVarChips={false} />
          </EvaluationField>
          {notes && (
            <p className="text-[11px] leading-relaxed text-amber-300/80 light:text-amber-700">
              {notes}
            </p>
          )}
          <Button
            type="button"
            size="sm"
            onClick={() => {
              onLock(steps)
              toast.success('Evaluation steps locked into the judge prompt')
            }}
          >
            <Lock className="h-3.5 w-3.5" />
            Lock into judge prompt
          </Button>
        </>
      )}
    </EvaluationDisclosure>
  )
}
