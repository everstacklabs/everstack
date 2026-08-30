import { ui } from '@everstack/ui'
import { cn } from '@everstack/utils/functions/cn'
import type { ModelParameter } from '@everstack/proto/everstack/providers/providers_pb'

const {
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} = ui

export const PARAMETER_DESCRIPTIONS: Record<string, string> = {
  max_output_tokens: 'Caps generated output for this model.',
  temperature:
    'Controls sampling randomness. Leave unset to use the provider default.',
  top_p: 'Samples from the smallest set of tokens covering this probability.',
  top_k: 'Samples only from this many highest-probability tokens.',
  frequency_penalty: 'Penalises tokens the more often they have appeared.',
  presence_penalty: 'Penalises tokens that have appeared at all.',
  seed: 'Asks the provider to make sampling repeatable. Best effort, not a guarantee.',
  verbosity:
    'Hints how expansive the answer should be, without capping length.',
  reasoning_effort: 'Trades latency and cost for deeper reasoning.',
  reasoning_budget_tokens:
    'Sets the maximum token budget used for internal reasoning.',
  reasoning_enabled: 'Turns the model’s internal reasoning on or off.',
}

const UNSET = '__default__'

interface ParameterControlProps {
  parameter: ModelParameter
  value: string
  onChange: (value?: string) => void
  /** What an unset control falls back to, e.g. "Provider default". */
  fallbackLabel: string
  /** Shown on the badge when a value is set. */
  setLabel: string
}

/**
 * One catalog-described request parameter. Both tiers of the Parameters tab
 * render through this, so a control looks and behaves the same whether it is a
 * provider-wide default or a per-model override.
 */
export function ParameterControl({
  parameter,
  value,
  onChange,
  fallbackLabel,
  setLabel,
}: ParameterControlProps) {
  const base =
    PARAMETER_DESCRIPTIONS[parameter.key] ??
    `Leave unset to use ${fallbackLabel.toLowerCase()}.`
  const description = parameter.requiresStreaming
    ? `${base} This setting requires streaming requests for this model.`
    : base

  return (
    <div className="rounded border border-brand-main-600/60 bg-brand-main-800/30 p-4">
      <div className="mb-3 flex items-start justify-between gap-4">
        <div>
          <Label className="text-sm text-white light:text-brand-main-50">
            {parameter.displayName}
          </Label>
          <p className="mt-0.5 text-xs text-white/50 light:text-black/50">
            {description}
          </p>
        </div>
        <span
          className={cn(
            'shrink-0 rounded px-2 py-0.5 text-[10px]',
            value
              ? 'bg-brand-secondary-500/15 text-brand-secondary-300'
              : 'bg-white/5 text-white/45 light:bg-black/5 light:text-black/45',
          )}
        >
          {value ? setLabel : fallbackLabel}
        </span>
      </div>

      {parameter.type === 'enum' || parameter.type === 'boolean' ? (
        <Select
          value={value || UNSET}
          onValueChange={(next) => onChange(next === UNSET ? undefined : next)}
        >
          <SelectTrigger className="w-full border-brand-main-500 bg-brand-main-700 text-white light:text-brand-main-50">
            <SelectValue />
          </SelectTrigger>
          <SelectContent className="border-brand-main-600 bg-brand-main-900 text-white">
            <SelectItem value={UNSET}>{fallbackLabel}</SelectItem>
            {parameter.type === 'boolean' ? (
              <>
                <SelectItem value="true">Enabled</SelectItem>
                <SelectItem value="false">Disabled</SelectItem>
              </>
            ) : (
              parameter.options.map((option) => (
                <SelectItem key={option} value={option} className="capitalize">
                  {option}
                </SelectItem>
              ))
            )}
          </SelectContent>
        </Select>
      ) : (
        <div className="space-y-1.5">
          <Input
            type="number"
            value={value}
            min={parameter.hasMinValue ? parameter.minValue : undefined}
            max={parameter.hasMaxValue ? parameter.maxValue : undefined}
            step={parameter.type === 'integer' ? 1 : 0.1}
            placeholder={fallbackLabel}
            onChange={(event) => onChange(event.target.value)}
            className="border-brand-main-500 bg-brand-main-700 text-white light:text-brand-main-50"
          />
          {(parameter.hasMinValue || parameter.hasMaxValue) && (
            <p className="text-[10px] text-white/45 light:text-black/45">
              {parameter.hasMinValue
                ? parameter.minValue.toLocaleString()
                : 'No minimum'}{' '}
              –{' '}
              {parameter.hasMaxValue
                ? parameter.maxValue.toLocaleString()
                : 'No maximum'}
            </p>
          )}
        </div>
      )}
    </div>
  )
}
