import type { EsqlNode, PresetId } from './ast'

export const PRESETS: Record<
  PresetId,
  {
    label: string
    expand: () => EsqlNode[]
    tier: 1 | 2
  }
> = {
  failed: {
    label: 'Failed',
    tier: 1,
    expand: () => [
      { kind: 'predicate', scope: 'any', field: 'status', op: ':', value: 'ERROR' },
    ],
  },
  slow: {
    label: 'Slow',
    tier: 1,
    expand: () => [
      { kind: 'predicate', scope: 'any', field: 'duration', op: '>', value: 30_000 },
    ],
  },
  expensive: {
    label: 'Expensive',
    tier: 1,
    expand: () => [
      { kind: 'predicate', scope: 'any', field: 'cost', op: '>', value: 0.1 },
    ],
  },
  no_output: {
    label: 'No Output',
    tier: 2,
    expand: () => [
      { kind: 'exists', scope: 'any', field: 'output', negated: true },
    ],
  },
  tool_error: {
    label: 'Tool Error',
    tier: 2,
    expand: () => [
      { kind: 'exists', scope: 'any', field: 'tool.error' },
    ],
  },
  retry: {
    label: 'Retry',
    tier: 2,
    expand: () => [
      { kind: 'exists', scope: 'any', field: 'fallback.attempt' },
    ],
  },
}
