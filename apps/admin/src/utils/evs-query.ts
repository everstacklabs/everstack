export type EvsQueryField = {
  id: string
  label: string
  token: string
  searchKey: string
  placeholder?: string
  description?: string
  aliases?: string[]
  clearKeys?: string[]
  normalize?: (value: string) => string
}

export type EvsQueryParseResult =
  | {
      ok: true
      filters: Record<string, string | undefined>
    }
  | {
      ok: false
      errors: string[]
    }

const COMPARISON_KEYS = new Set(['cost', 'duration'])

function stripTokenSuffix(token: string): string {
  return token.replace(/:$/, '').toLowerCase()
}

function quoteValue(value: string): string {
  if (!value) return '""'
  if (!/\s|"/.test(value)) return value
  return `"${value.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`
}

function splitDelimited(value: string | undefined): string[] {
  if (!value) return []
  return value
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean)
}

function appendDelimited(
  filters: Record<string, string | undefined>,
  key: string,
  value: string,
) {
  const existing = splitDelimited(filters[key])
  existing.push(value)
  filters[key] = Array.from(new Set(existing)).join(',')
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

function findField(
  rawName: string,
  fields: EvsQueryField[],
): EvsQueryField | undefined {
  const name = rawName.toLowerCase()
  return fields.find((field) => {
    const token = stripTokenSuffix(field.token)
    return (
      field.id.toLowerCase() === name ||
      token === name ||
      field.aliases?.some((alias) => alias.toLowerCase() === name)
    )
  })
}

function normalizeValue(field: EvsQueryField, rawValue: string): string {
  const value = rawValue.trim()
  return field.normalize ? field.normalize(value) : value
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
  if (normalized === 'UNSET') return 'UNSET'
  return normalized
}

function parseDurationMs(value: string): string | null {
  const match = value.trim().match(/^(\d+(?:\.\d+)?)(ms|s|m|h)?$/i)
  if (!match) return null

  const amount = Number(match[1])
  if (!Number.isFinite(amount)) return null

  const unit = (match[2] || 'ms').toLowerCase()
  const multiplier =
    unit === 'h' ? 3_600_000 : unit === 'm' ? 60_000 : unit === 's' ? 1_000 : 1
  return String(amount * multiplier)
}

function comparisonTarget(
  key: string,
  operator: string,
): 'minCost' | 'maxCost' | 'minDuration' | 'maxDuration' | null {
  if (key === 'cost') {
    if (operator === '>' || operator === '>=') return 'minCost'
    if (operator === '<' || operator === '<=') return 'maxCost'
  }

  if (key === 'duration') {
    if (operator === '>' || operator === '>=') return 'minDuration'
    if (operator === '<' || operator === '<=') return 'maxDuration'
  }

  return null
}

function applyMetadataFilter(
  filters: Record<string, string | undefined>,
  rawKey: string,
  rawValue: string,
  errors: string[],
) {
  const key = rawKey.trim()
  const value = rawValue.trim()
  if (!key || !value) {
    errors.push('Metadata filters must be written as metadata.key:value.')
    return
  }
  appendDelimited(filters, 'metadata', `${key}=${value}`)
}

export function parseEvsQuery(
  input: string,
  fields: EvsQueryField[],
  defaultFieldId = 'query',
): EvsQueryParseResult {
  const filters: Record<string, string | undefined> = {}
  const errors: string[] = []
  const defaultField =
    fields.find((field) => field.id === defaultFieldId) ?? fields[0]

  for (const token of tokenize(input.trim())) {
    const comparison = token.match(/^([a-zA-Z][\w.-]*)(<=|>=|<|>|=)(.+)$/)
    if (comparison) {
      const [, rawKey, operator, rawValue] = comparison
      const key = rawKey.toLowerCase()
      if (!COMPARISON_KEYS.has(key)) {
        errors.push(`Unknown comparison field: ${rawKey}`)
        continue
      }

      if (operator === '=') {
        const minTarget = comparisonTarget(key, '>=')!
        const maxTarget = comparisonTarget(key, '<=')!
        const value =
          key === 'duration' ? parseDurationMs(rawValue) : rawValue.trim()
        if (!value) {
          errors.push(`Invalid ${key} value: ${rawValue}`)
          continue
        }
        filters[minTarget] = value
        filters[maxTarget] = value
        continue
      }

      const target = comparisonTarget(key, operator)
      if (!target) {
        errors.push(`Unsupported operator for ${rawKey}: ${operator}`)
        continue
      }

      const value =
        key === 'duration' ? parseDurationMs(rawValue) : rawValue.trim()
      if (!value || (key === 'cost' && !Number.isFinite(Number(value)))) {
        errors.push(`Invalid ${key} value: ${rawValue}`)
        continue
      }
      filters[target] = value
      continue
    }

    const attribute = token.match(/^@([\w.-]+):(.*)$/)
    if (attribute) {
      const [, rawKey, rawValue] = attribute
      applyMetadataFilter(filters, rawKey, rawValue, errors)
      continue
    }

    const tagged = token.match(/^([a-zA-Z][\w.-]*):(.*)$/)
    if (!tagged) {
      if (defaultField) {
        filters[defaultField.searchKey] = [
          filters[defaultField.searchKey],
          token,
        ]
          .filter(Boolean)
          .join(' ')
      }
      continue
    }

    const [, rawName, rawValue] = tagged
    const name = rawName.toLowerCase()

    if (name.startsWith('metadata.') || name.startsWith('meta.')) {
      const key = rawName.slice(rawName.indexOf('.') + 1)
      applyMetadataFilter(filters, key, rawValue, errors)
      continue
    }

    const field = findField(rawName, fields)
    if (!field) {
      errors.push(`Unknown field: ${rawName}`)
      continue
    }

    if (field.searchKey === 'metadata') {
      const [metadataKey, ...valueParts] = rawValue.split('=')
      if (valueParts.length === 0) {
        errors.push('Metadata filters must be written as metadata:key=value.')
        continue
      }
      applyMetadataFilter(filters, metadataKey, valueParts.join('='), errors)
      continue
    }

    const value =
      field.searchKey === 'statusCode'
        ? normalizeStatus(rawValue)
        : normalizeValue(field, rawValue)
    if (!value) continue

    if (field.searchKey === defaultField?.searchKey) {
      filters[field.searchKey] = [filters[field.searchKey], value]
        .filter(Boolean)
        .join(' ')
    } else if (field.searchKey === 'tags') {
      appendDelimited(filters, field.searchKey, value)
    } else {
      filters[field.searchKey] = value
    }
  }

  return errors.length > 0 ? { ok: false, errors } : { ok: true, filters }
}

export function evsQueryFromSearch(
  search: Record<string, unknown>,
  fields: EvsQueryField[],
): string {
  const tokens: string[] = []
  const bySearchKey = new Map(fields.map((field) => [field.searchKey, field]))

  const text = search.query ? String(search.query) : ''
  if (text) tokens.push(`text:${quoteValue(text)}`)

  for (const key of [
    'statusCode',
    'trace',
    'correlationId',
    'userId',
    'sessionId',
    'threadId',
    'model',
    'provider',
    'environment',
  ]) {
    if (!search[key]) continue
    const field = bySearchKey.get(key)
    if (!field) continue
    tokens.push(
      `${stripTokenSuffix(field.token)}:${quoteValue(String(search[key]))}`,
    )
  }

  for (const tag of splitDelimited(search.tags ? String(search.tags) : '')) {
    tokens.push(`tag:${quoteValue(tag)}`)
  }

  for (const pred of splitDelimited(
    search.metadata ? String(search.metadata) : '',
  )) {
    const [key, ...valueParts] = pred.split('=')
    if (!key || valueParts.length === 0) continue
    tokens.push(`@${key}:${quoteValue(valueParts.join('='))}`)
  }

  if (search.minCost) tokens.push(`cost>=${search.minCost}`)
  if (search.maxCost) tokens.push(`cost<=${search.maxCost}`)
  if (search.minDuration) tokens.push(`duration>=${search.minDuration}ms`)
  if (search.maxDuration) tokens.push(`duration<=${search.maxDuration}ms`)

  return tokens.join(' ')
}

export function evsManagedSearchKeys(fields: EvsQueryField[]): string[] {
  return Array.from(
    new Set(
      fields.flatMap((field) => [field.searchKey, ...(field.clearKeys ?? [])]),
    ),
  )
}
