/**
 * span-type-summary: per-type "what happened" fields for a span (M4-T1).
 *
 * The emitter now produces semantically-typed spans (sandbox / browser /
 * retriever / embedding / workflow / scorer / integration / harness / http). The
 * generic attribute table is fine, but a compact per-type summary (the command
 * that ran, the URL navigated to, the query and result count) makes the trace
 * detail readable at a glance. This is a pure mapping from a span's attributes to
 * the handful of fields that matter for its category.
 */

import type { Span } from '@everstack/proto/everstack/traces/v1/traces_pb'
import { getAttr, type SpanCategory } from './traces-common'

export interface SpanSummaryField {
  label: string
  value: string
}

function field(label: string, value: unknown): SpanSummaryField | null {
  if (value == null || value === '') return null
  return { label, value: String(value) }
}

/**
 * Return the key summary fields for a span given its category. Empty array when
 * the category has no dedicated summary (callers fall back to the attribute table).
 */
export function spanTypeSummary(span: Span, category: SpanCategory): SpanSummaryField[] {
  const a = (key: string) => getAttr(span, key)
  let fields: Array<SpanSummaryField | null> = []

  switch (category) {
    case 'sandbox':
      fields = [
        field('Command', a('sandbox.command')),
        field('Exit code', a('sandbox.exit_code')),
        field('Path', a('sandbox.fs.path')),
        field('Duration (ms)', a('sandbox.duration_ms')),
      ]
      break
    case 'browser':
      fields = [
        field('Action', a('browser.action')),
        field('URL', a('browser.url')),
        field('Selector', a('browser.selector')),
      ]
      break
    case 'retriever':
      fields = [
        field('Operation', a('memory.operation') ?? a('vector.operation')),
        field('Collection', a('vector.collection_id')),
        field('Top K', a('vector.top_k')),
        field('Results', a('memory.result_count') ?? a('vector.result_count')),
      ]
      break
    case 'embedding':
      fields = [
        field('Model', a('embedding.model')),
        field('Dimension', a('embedding.dimension')),
        field('Inputs', a('embedding.input_count')),
      ]
      break
    case 'workflow':
      fields = [
        field('Node type', a('node.type')),
        field('Node', a('node.name')),
        field('Run', a('run.id')),
      ]
      break
    case 'scorer':
      fields = [
        field('Scorer', a('scorer.name')),
        field('Scores', a('scorer.score_count')),
        field('State', a('scoring.state')),
      ]
      break
    case 'integration':
      fields = [
        field('Provider', a('integration.provider')),
        field('Method', a('http.method')),
        field('Status', a('http.status_code')),
      ]
      break
    case 'harness':
      fields = [
        field('Exit code', a('sandbox.exit_code')),
        field('Packages', a('harness.packages')),
      ]
      break
    case 'http':
      fields = [
        field('Method', a('http.method')),
        field('URL', a('http.url')),
        field('Status', a('http.status_code')),
      ]
      break
    default:
      return []
  }

  return fields.filter((f): f is SpanSummaryField => f !== null)
}
