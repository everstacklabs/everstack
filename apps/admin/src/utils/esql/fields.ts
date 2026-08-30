import type { EsqlOp } from './ast'

export type EsqlFieldOp = EsqlOp | 'exists'

export type EsqlField = {
  id: string
  label: string
  family: 'aggregate' | 'attribute' | 'column'
  type: 'string' | 'number' | 'duration' | 'bool'
  ops: EsqlFieldOp[]
  aliases?: string[]
  suggestValues?: string[]
}

export const ESQL_FIELDS: EsqlField[] = [
  {
    id: 'query',
    label: 'All Text',
    family: 'column',
    type: 'string',
    ops: ['contains'],
    aliases: ['text', 'input', 'name'],
  },
  {
    id: 'status',
    label: 'Status',
    family: 'attribute',
    type: 'string',
    ops: [':', '='],
    aliases: ['statusCode', 'status_code'],
    suggestValues: ['OK', 'ERROR'],
  },
  {
    id: 'trace',
    label: 'Trace ID',
    family: 'attribute',
    type: 'string',
    ops: [':', '='],
    aliases: ['traceId', 'trace_id'],
  },
  {
    id: 'has',
    label: 'Contains span',
    family: 'attribute',
    type: 'string',
    ops: [':', '='],
    suggestValues: [
      'sandbox',
      'tool',
      'agent',
      'memory',
      'browser',
      'mcp',
      'voice',
      'vector',
      'llm',
    ],
  },
  {
    id: 'agent',
    label: 'Agent',
    family: 'attribute',
    type: 'string',
    ops: [':', '='],
    aliases: ['agentName', 'agent_name'],
  },
  {
    id: 'model',
    label: 'Model',
    family: 'attribute',
    type: 'string',
    ops: [':', '='],
  },
  {
    id: 'provider',
    label: 'Provider',
    family: 'attribute',
    type: 'string',
    ops: [':', '='],
  },
  {
    id: 'user',
    label: 'User',
    family: 'attribute',
    type: 'string',
    ops: [':', '='],
    aliases: ['userId', 'user_id'],
  },
  {
    id: 'session',
    label: 'Session',
    family: 'attribute',
    type: 'string',
    ops: [':', '='],
    aliases: ['sessionId', 'session_id'],
  },
  {
    id: 'thread',
    label: 'Thread',
    family: 'attribute',
    type: 'string',
    ops: [':', '='],
    aliases: ['threadId', 'thread_id'],
  },
  {
    id: 'environment',
    label: 'Environment',
    family: 'attribute',
    type: 'string',
    ops: [':', '='],
    aliases: ['env'],
  },
  {
    id: 'correlation',
    label: 'Correlation ID',
    family: 'attribute',
    type: 'string',
    ops: [':', '='],
    aliases: ['correlationId'],
  },
  {
    id: 'tag',
    label: 'Tag',
    family: 'attribute',
    type: 'string',
    ops: [':', '='],
    aliases: ['tags'],
  },
  {
    id: 'cost',
    label: 'Cost (USD)',
    family: 'aggregate',
    type: 'number',
    ops: ['>', '>=', '<', '<='],
    aliases: ['price', 'spend'],
  },
  {
    id: 'duration',
    label: 'Duration',
    family: 'aggregate',
    type: 'duration',
    ops: ['>', '>=', '<', '<='],
    aliases: ['latency', 'latency_ms', 'duration_ms'],
  },
  {
    id: 'tokens.total',
    label: 'Total Tokens',
    family: 'attribute',
    type: 'number',
    ops: ['>', '>=', '<', '<='],
    aliases: ['tokens'],
  },
  {
    id: 'tool.name',
    label: 'Tool Name',
    family: 'attribute',
    type: 'string',
    ops: [':', '='],
  },
  {
    id: 'tool.error',
    label: 'Tool Error',
    family: 'attribute',
    type: 'bool',
    ops: ['exists'],
  },
  {
    id: 'cache.hit',
    label: 'Cache Hit',
    family: 'attribute',
    type: 'bool',
    ops: ['exists'],
  },
  {
    id: 'ttft',
    label: 'Time To First Token',
    family: 'attribute',
    type: 'duration',
    ops: ['>', '>=', '<', '<='],
  },
  {
    id: 'output',
    label: 'Output',
    family: 'column',
    type: 'string',
    ops: ['contains'],
  },
  {
    id: 'fallback.attempt',
    label: 'Fallback Attempt',
    family: 'attribute',
    type: 'bool',
    ops: ['exists'],
  },
]

export function findField(nameOrAlias: string): EsqlField | undefined {
  const name = nameOrAlias.toLowerCase()
  return ESQL_FIELDS.find((field) => {
    return (
      field.id.toLowerCase() === name ||
      field.aliases?.some((alias) => alias.toLowerCase() === name)
    )
  })
}
