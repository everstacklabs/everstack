import type { EsqlNode, EsqlQuery } from './ast'
import { parseDurationMs } from './parse'
import { PRESETS } from './presets'

function appendQuery(params: Record<string, unknown>, value: string) {
  if (!value) return
  const existing = typeof params.query === 'string' ? params.query : ''
  params.query = [existing, value].filter(Boolean).join(' ')
}

function pushListParam(
  params: Record<string, unknown>,
  key: string,
  value: string,
) {
  const existing = params[key]
  if (Array.isArray(existing)) {
    params[key] = [...existing, value]
    return
  }

  params[key] = [value]
}

function numericValue(value: string | number): number | null {
  if (typeof value === 'number') return Number.isFinite(value) ? value : null
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : null
}

function durationValue(value: string | number): number | null {
  if (typeof value === 'number') return Number.isFinite(value) ? value : null
  return parseDurationMs(value)
}

function normalizeStatus(value: string | number): string {
  const normalized = String(value).trim().toUpperCase()
  if (
    normalized === 'ERROR' ||
    normalized === 'ERR' ||
    normalized === 'FAILED'
  ) {
    return 'ERROR'
  }
  if (
    normalized === 'SUCCESS' ||
    normalized === 'OK' ||
    normalized === 'PASS'
  ) {
    return 'OK'
  }
  return normalized
}

function mergeParams(
  target: Record<string, unknown>,
  source: Record<string, unknown>,
) {
  for (const [key, value] of Object.entries(source)) {
    if (key === 'query' && typeof value === 'string') {
      appendQuery(target, value)
      continue
    }

    if ((key === 'tags' || key === 'metadata') && Array.isArray(value)) {
      for (const item of value) pushListParam(target, key, String(item))
      continue
    }

    target[key] = value
  }
}

function compilePredicate(
  node: Extract<EsqlNode, { kind: 'predicate' }>,
  params: Record<string, unknown>,
  unsupported: EsqlNode[],
) {
  if (node.scope !== 'any') {
    unsupported.push(node)
    return
  }

  if (node.field === 'query' && node.op === 'contains') {
    appendQuery(params, String(node.value))
    return
  }

  if (node.field === 'output' && node.op === 'contains') {
    appendQuery(params, String(node.value))
    return
  }

  if ((node.op === ':' || node.op === '=') && node.field === 'status') {
    params.statusCode = normalizeStatus(node.value)
    return
  }

  if ((node.op === ':' || node.op === '=') && node.field === 'model') {
    params.model = String(node.value)
    return
  }

  if ((node.op === ':' || node.op === '=') && node.field === 'provider') {
    params.provider = String(node.value)
    return
  }

  if ((node.op === ':' || node.op === '=') && node.field === 'user') {
    params.userId = String(node.value)
    return
  }

  if ((node.op === ':' || node.op === '=') && node.field === 'session') {
    params.sessionId = String(node.value)
    return
  }

  if ((node.op === ':' || node.op === '=') && node.field === 'thread') {
    params.threadId = String(node.value)
    return
  }

  if ((node.op === ':' || node.op === '=') && node.field === 'environment') {
    params.environment = String(node.value)
    return
  }

  if ((node.op === ':' || node.op === '=') && node.field === 'correlation') {
    params.correlationId = String(node.value)
    return
  }

  if ((node.op === ':' || node.op === '=') && node.field === 'tag') {
    pushListParam(params, 'tags', String(node.value))
    return
  }

  if ((node.op === ':' || node.op === '=') && node.field.startsWith('metadata.')) {
    const key = node.field.slice('metadata.'.length)
    if (!key) {
      unsupported.push(node)
      return
    }
    pushListParam(params, 'metadata', `${key}=${String(node.value)}`)
    return
  }

  if (node.field === 'cost') {
    const value = numericValue(node.value)
    if (value === null) {
      unsupported.push(node)
      return
    }
    if (node.op === '>' || node.op === '>=') {
      params.minCost = value
      return
    }
    if (node.op === '<' || node.op === '<=') {
      params.maxCost = value
      return
    }
  }

  if (node.field === 'duration') {
    const value = durationValue(node.value)
    if (value === null) {
      unsupported.push(node)
      return
    }
    if (node.op === '>' || node.op === '>=') {
      params.minDurationMs = value
      return
    }
    if (node.op === '<' || node.op === '<=') {
      params.maxDurationMs = value
      return
    }
  }

  unsupported.push(node)
}

function compileNode(
  node: EsqlNode,
  params: Record<string, unknown>,
  unsupported: EsqlNode[],
) {
  if (node.kind === 'text') {
    appendQuery(params, node.value)
    return
  }

  if (node.kind === 'predicate') {
    compilePredicate(node, params, unsupported)
    return
  }

  if (node.kind === 'preset') {
    const presetParams: Record<string, unknown> = {}
    const presetUnsupported: EsqlNode[] = []
    const preset = PRESETS[node.id]
    for (const expanded of preset.expand()) {
      compileNode(expanded, presetParams, presetUnsupported)
    }
    mergeParams(params, presetParams)
    if (presetUnsupported.length > 0) {
      unsupported.push(...(preset.tier === 2 ? [node] : presetUnsupported))
    }
    return
  }

  unsupported.push(node)
}

export function compileToListTracesParams(query: EsqlQuery): {
  params: Record<string, unknown>
  unsupported: EsqlNode[]
} {
  const params: Record<string, unknown> = {}
  const unsupported: EsqlNode[] = []

  for (const node of query.nodes) {
    compileNode(node, params, unsupported)
  }

  return { params, unsupported }
}
