import { Route } from '@/routes/observability/traces'
import {
  ObservabilityControls,
  type AdvancedSearchField,
} from '@/components/common/observability-controls'
import type { TimeRangePreset } from '@/stores/logs-store'

const TRACE_ADVANCED_SEARCH_FIELDS: AdvancedSearchField[] = [
  {
    id: 'query',
    label: 'All Text',
    token: 'text:',
    searchKey: 'query',
    placeholder: 'Search input or output...',
  },
  {
    id: 'traceId',
    label: 'Trace ID',
    token: 'traceId:',
    searchKey: 'trace',
    placeholder: 'tr_...',
    aliases: ['trace'],
    clearKeys: ['span'],
  },
  {
    id: 'correlationId',
    label: 'Correlation ID',
    token: 'correlationId:',
    searchKey: 'correlationId',
    placeholder: 'Correlation ID...',
    aliases: ['correlation'],
  },
  {
    id: 'status',
    label: 'Status',
    token: 'status:',
    searchKey: 'statusCode',
    placeholder: 'OK or ERROR',
    aliases: ['statusCode'],
    normalize: (value) => value.toUpperCase(),
  },
  {
    id: 'userId',
    label: 'User ID',
    token: 'userId:',
    searchKey: 'userId',
    placeholder: 'User ID...',
    aliases: ['user'],
  },
  {
    id: 'sessionId',
    label: 'Session ID',
    token: 'sessionId:',
    searchKey: 'sessionId',
    placeholder: 'Session ID...',
    aliases: ['session'],
  },
  {
    id: 'threadId',
    label: 'Thread ID',
    token: 'threadId:',
    searchKey: 'threadId',
    placeholder: 'Thread ID...',
    aliases: ['thread'],
  },
  {
    id: 'model',
    label: 'Model',
    token: 'model:',
    searchKey: 'model',
    placeholder: 'gpt-4o, claude...',
  },
  {
    id: 'provider',
    label: 'Provider',
    token: 'provider:',
    searchKey: 'provider',
    placeholder: 'openai, anthropic...',
  },
  {
    id: 'environment',
    label: 'Environment',
    token: 'env:',
    searchKey: 'environment',
    placeholder: 'production',
    aliases: ['environment'],
  },
  {
    id: 'tags',
    label: 'Tag',
    token: 'tag:',
    searchKey: 'tags',
    placeholder: 'customer-facing',
    aliases: ['tags'],
  },
  {
    id: 'metadata',
    label: 'Metadata',
    token: 'metadata:',
    searchKey: 'metadata',
    placeholder: 'key=value',
    aliases: ['meta'],
  },
  {
    id: 'minCost',
    label: 'Min Cost',
    token: 'costMin:',
    searchKey: 'minCost',
    placeholder: '0.01',
  },
  {
    id: 'maxCost',
    label: 'Max Cost',
    token: 'costMax:',
    searchKey: 'maxCost',
    placeholder: '1.00',
  },
  {
    id: 'minDuration',
    label: 'Min Duration',
    token: 'durationMin:',
    searchKey: 'minDuration',
    placeholder: '1000',
  },
  {
    id: 'maxDuration',
    label: 'Max Duration',
    token: 'durationMax:',
    searchKey: 'maxDuration',
    placeholder: '5000',
  },
]

interface TracesControlsProps {
  isLoading?: boolean
  onRefresh?: () => void
}

export function TracesControls({ isLoading, onRefresh }: TracesControlsProps) {
  const search = Route.useSearch()
  const navigate = Route.useNavigate()

  // Parse URL params
  const isLiveMode = search.live === 'true'
  const timeRange = search.range as TimeRangePreset

  return (
    <ObservabilityControls
      search={search}
      navigate={navigate}
      isLiveMode={isLiveMode}
      timeRange={timeRange}
      searchPlaceholder="Start typing to full-text search"
      advancedSearchFields={TRACE_ADVANCED_SEARCH_FIELDS}
      isLoading={isLoading}
      onRefresh={onRefresh}
    />
  )
}
