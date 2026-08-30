import type { EsqlNode, EsqlQuery } from './ast'
import { findField } from './fields'

function quoteValue(value: string): string {
  if (!value) return '""'
  if (!/\s|"|\\/.test(value)) return value
  return `"${value.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`
}

function valueText(value: string | number): string {
  return typeof value === 'number' ? String(value) : quoteValue(value)
}

function scopedField(node: Extract<EsqlNode, { kind: 'predicate' | 'exists' }>) {
  return node.scope === 'any' ? node.field : `${node.scope}.${node.field}`
}

function serializePredicate(node: Extract<EsqlNode, { kind: 'predicate' }>): string {
  const field = scopedField(node)
  const catalogField = findField(node.field)
  const value =
    catalogField?.type === 'duration' && typeof node.value === 'number'
      ? `${node.value}ms`
      : valueText(node.value)

  if (node.field.startsWith('metadata.') && node.op === ':') {
    return `@${node.field.slice('metadata.'.length)}:${value}`
  }

  if (node.op === 'contains') return `${field} contains ${value}`
  if (node.op === ':') return `${field}:${value}`
  return `${field} ${node.op} ${value}`
}

function serializeNode(node: EsqlNode): string {
  if (node.kind === 'text') return quoteValue(node.value)
  if (node.kind === 'predicate') return serializePredicate(node)
  if (node.kind === 'exists') {
    const operator = node.negated ? '!exists' : 'exists'
    return `${scopedField(node)} ${operator}`
  }
  if (node.kind === 'preset') return node.id
  return `sequence(${node.steps.map((step) => `${step.field}${step.op}${valueText(step.value)}`).join(' -> ')})`
}

export function serializeEsql(query: EsqlQuery): string {
  return query.nodes.map(serializeNode).join(' ')
}

function hasValue(value: unknown): boolean {
  return value !== undefined && value !== null && String(value).trim() !== ''
}

function splitDelimited(value: unknown): string[] {
  if (Array.isArray(value)) {
    return value.flatMap((item) => splitDelimited(item))
  }

  if (!hasValue(value)) return []
  return String(value)
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean)
}

export function esqlFromLegacyParams(search: Record<string, unknown>): string {
  const tokens: string[] = []

  if (hasValue(search.query)) tokens.push(quoteValue(String(search.query)))

  const scalarMappings: Array<[string, string]> = [
    ['statusCode', 'status'],
    ['model', 'model'],
    ['provider', 'provider'],
    ['userId', 'user'],
    ['sessionId', 'session'],
    ['threadId', 'thread'],
    ['environment', 'environment'],
    ['correlationId', 'correlation'],
  ]

  for (const [paramKey, field] of scalarMappings) {
    if (!hasValue(search[paramKey])) continue
    tokens.push(`${field}:${quoteValue(String(search[paramKey]))}`)
  }

  for (const tag of splitDelimited(search.tags)) {
    tokens.push(`tag:${quoteValue(tag)}`)
  }

  for (const pred of splitDelimited(search.metadata)) {
    const [key, ...valueParts] = pred.split('=')
    if (!key || valueParts.length === 0) continue
    tokens.push(`@${key}:${quoteValue(valueParts.join('='))}`)
  }

  if (hasValue(search.minCost)) tokens.push(`cost >= ${search.minCost}`)
  if (hasValue(search.maxCost)) tokens.push(`cost <= ${search.maxCost}`)
  if (hasValue(search.minDuration)) {
    tokens.push(`duration >= ${search.minDuration}ms`)
  }
  if (hasValue(search.maxDuration)) {
    tokens.push(`duration <= ${search.maxDuration}ms`)
  }

  return tokens.join(' ')
}
