export type EsqlScope = 'trace' | 'any' | 'root'
export type EsqlOp = ':' | '=' | '!=' | '>' | '>=' | '<' | '<=' | 'contains'

export type PresetId =
  | 'failed'
  | 'slow'
  | 'expensive'
  | 'no_output'
  | 'tool_error'
  | 'retry'

export type EsqlNode =
  | {
      kind: 'text'
      value: string
    }
  | {
      kind: 'predicate'
      scope: EsqlScope
      field: string
      op: EsqlOp
      value: string | number
    }
  | {
      kind: 'exists'
      scope: EsqlScope
      field: string
      negated?: boolean
    }
  | {
      kind: 'preset'
      id: PresetId
    }
  | {
      kind: 'sequence'
      steps: SequenceStep[]
    }

export type SequenceStep = {
  field: string
  op: EsqlOp
  value: string | number
}

export type EsqlQuery = { nodes: EsqlNode[] }
