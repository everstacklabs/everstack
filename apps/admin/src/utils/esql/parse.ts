import type {
  EsqlNode,
  EsqlOp,
  EsqlQuery,
  EsqlScope,
  PresetId,
} from './ast'
import { findField, type EsqlField, type EsqlFieldOp } from './fields'
import { PRESETS } from './presets'

export type EsqlParseResult =
  | {
      ok: true
      query: EsqlQuery
    }
  | {
      ok: false
      errors: string[]
    }

const PRESET_IDS = new Set(Object.keys(PRESETS) as PresetId[])
const SPACED_OPERATORS = new Set(['=', '!=', '>', '>=', '<', '<='])

type FieldRef = {
  scope: EsqlScope
  name: string
}

type ParsedField = {
  scope: EsqlScope
  field: EsqlField
}

function tokenize(input: string): string[] {
  const tokens: string[] = []
  let current = ''
  let quote: '"' | "'" | null = null
  let escaped = false

  for (const char of input) {
    if (escaped) {
      current += char
      escaped = false
      continue
    }

    if (char === '\\') {
      escaped = true
      continue
    }

    if (quote) {
      if (char === quote) {
        quote = null
      } else {
        current += char
      }
      continue
    }

    if (char === '"' || char === "'") {
      quote = char
      continue
    }

    if (/\s/.test(char)) {
      if (current) {
        tokens.push(current)
        current = ''
      }
      continue
    }

    current += char
  }

  if (current) tokens.push(current)
  return tokens
}

export function parseDurationMs(value: string): number | null {
  const match = value.trim().match(/^(\d+(?:\.\d+)?)(ms|s|m|h)?$/i)
  if (!match) return null

  const amount = Number(match[1])
  if (!Number.isFinite(amount)) return null

  const unit = (match[2] || 'ms').toLowerCase()
  const multiplier =
    unit === 'h' ? 3_600_000 : unit === 'm' ? 60_000 : unit === 's' ? 1_000 : 1
  return amount * multiplier
}

function isPresetId(value: string): value is PresetId {
  return PRESET_IDS.has(value as PresetId)
}

function isSpacedOperator(value: string | undefined): value is EsqlOp {
  return !!value && SPACED_OPERATORS.has(value)
}

function normalizeStatus(value: string): string {
  const normalized = value.trim().toUpperCase()
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

function resolveFieldRef(
  rawName: string,
  errors: string[],
): FieldRef | null {
  const firstDot = rawName.indexOf('.')
  if (firstDot === -1) return { scope: 'any', name: rawName }

  const maybeScope = rawName.slice(0, firstDot).toLowerCase()
  if (maybeScope === 'parent' || maybeScope === 'child') {
    errors.push('topology scope not supported yet')
    return null
  }

  if (
    maybeScope === 'any' ||
    maybeScope === 'root' ||
    maybeScope === 'trace'
  ) {
    return {
      scope: maybeScope,
      name: rawName.slice(firstDot + 1),
    }
  }

  return { scope: 'any', name: rawName }
}

function validateFieldOp(
  field: EsqlField,
  op: EsqlFieldOp,
  rawName: string,
  errors: string[],
): boolean {
  if (field.ops.includes(op)) return true
  errors.push(`Unsupported operator for ${rawName}: ${op}`)
  return false
}

function parseValue(
  field: EsqlField,
  rawValue: string,
  errors: string[],
): string | number | null {
  const value = rawValue.trim()

  if (field.type === 'duration') {
    const duration = parseDurationMs(value)
    if (duration === null) {
      errors.push(`Invalid ${field.id} value: ${rawValue}`)
      return null
    }
    return duration
  }

  if (field.type === 'number') {
    const numberValue = Number(value)
    if (!Number.isFinite(numberValue)) {
      errors.push(`Invalid ${field.id} value: ${rawValue}`)
      return null
    }
    return numberValue
  }

  if (field.id === 'status') return normalizeStatus(value)
  return value
}

function predicateOpForTagged(field: EsqlField): EsqlOp {
  return field.family === 'column' ? 'contains' : ':'
}

function parseField(
  rawName: string,
  errors: string[],
): ParsedField | null {
  const ref = resolveFieldRef(rawName, errors)
  if (!ref) return null

  const field = findField(ref.name)
  if (!field) {
    errors.push(`Unknown field: ${ref.name}`)
    return null
  }

  return { scope: ref.scope, field }
}

function parsePredicate(
  rawName: string,
  rawOp: EsqlOp,
  rawValue: string,
  nodes: EsqlNode[],
  errors: string[],
) {
  const parsedField = parseField(rawName, errors)
  if (!parsedField) return

  const { scope, field } = parsedField
  const op = rawOp === ':' ? predicateOpForTagged(field) : rawOp
  if (!validateFieldOp(field, op, rawName, errors)) return

  const value = parseValue(field, rawValue, errors)
  if (value === null) return
  nodes.push({ kind: 'predicate', scope, field: field.id, op, value })
}

function parseExists(
  rawName: string,
  nodes: EsqlNode[],
  errors: string[],
) {
  const parsedField = parseField(rawName, errors)
  if (!parsedField) return

  const { scope, field } = parsedField
  if (!validateFieldOp(field, 'exists', rawName, errors)) return
  nodes.push({ kind: 'exists', scope, field: field.id })
}

function parseMetadata(
  rawKey: string,
  rawValue: string,
  nodes: EsqlNode[],
  errors: string[],
) {
  const key = rawKey.trim()
  const value = rawValue.trim()
  if (!key) {
    errors.push('Metadata filters must be written as @key:value.')
    return
  }
  nodes.push({
    kind: 'predicate',
    scope: 'any',
    field: `metadata.${key}`,
    op: ':',
    value,
  })
}

export function parseEsql(input: string): EsqlParseResult {
  const nodes: EsqlNode[] = []
  const errors: string[] = []
  const tokens = tokenize(input.trim())
  let index = 0

  while (index < tokens.length) {
    const token = tokens[index]
    const lowerToken = token.toLowerCase()
    const upperToken = token.toUpperCase()

    if (upperToken === 'AND') {
      index += 1
      continue
    }
    if (upperToken === 'OR' || upperToken === 'NOT') {
      errors.push('OR/NOT not supported in v1')
      index += 1
      continue
    }
    if (lowerToken.startsWith('sequence(')) {
      errors.push('sequence syntax not supported yet')
      index += 1
      continue
    }
    if (isPresetId(lowerToken)) {
      nodes.push({ kind: 'preset', id: lowerToken })
      index += 1
      continue
    }

    const containsKeyword = tokens[index + 1]?.toLowerCase()
    if (containsKeyword === 'contains') {
      const value = tokens[index + 2]
      if (value === undefined) {
        errors.push(`Missing value for ${token}`)
        index += 1
      } else {
        parsePredicate(token, 'contains', value, nodes, errors)
        index += 3
      }
      continue
    }

    const existsKeyword = tokens[index + 1]?.toLowerCase()
    if (existsKeyword === 'exists') {
      parseExists(token, nodes, errors)
      index += 2
      continue
    }

    const spacedOp = tokens[index + 1]
    if (isSpacedOperator(spacedOp)) {
      const value = tokens[index + 2]
      if (value === undefined) {
        errors.push(`Missing value for ${token}`)
        index += 1
      } else {
        parsePredicate(token, spacedOp, value, nodes, errors)
        index += 3
      }
      continue
    }

    const metadata = token.match(/^@([\w.-]+):(.*)$/)
    if (metadata) {
      const [, rawKey, rawValue] = metadata
      parseMetadata(rawKey, rawValue, nodes, errors)
      index += 1
      continue
    }

    const comparison = token.match(/^([a-zA-Z][\w.-]*)(<=|>=|!=|=|<|>)(.*)$/)
    if (comparison) {
      const [, rawName, rawOp, rawValue] = comparison
      const parsedField = parseField(rawName, errors)
      if (!parsedField) {
        index += 1
        continue
      }

      const { field } = parsedField
      const op =
        rawOp === '=' && field.type === 'string'
          ? predicateOpForTagged(field)
          : (rawOp as EsqlOp)
      parsePredicate(rawName, op, rawValue, nodes, errors)
      index += 1
      continue
    }

    const tagged = token.match(/^([a-zA-Z][\w.-]*):(.*)$/)
    if (tagged) {
      const [, rawName, rawValue] = tagged
      parsePredicate(rawName, ':', rawValue, nodes, errors)
      index += 1
      continue
    }

    nodes.push({ kind: 'text', value: token })
    index += 1
  }

  return errors.length > 0 ? { ok: false, errors } : { ok: true, query: { nodes } }
}
