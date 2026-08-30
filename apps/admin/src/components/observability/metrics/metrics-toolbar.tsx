import { useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import dayjs from 'dayjs'
import { ui } from '@everstack/ui'
import { Download, RefreshCw, SlidersHorizontal } from 'lucide-react'
import type { BoardGraphKey } from './metrics-board'
import { BOARD_GRAPHS } from './metrics-board'
import { Badge } from '@everstack/ui/components'

const {
  Button,
  DateTimePicker,
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Popover,
  PopoverContent,
  PopoverTrigger,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Switch,
  Tooltip,
  TooltipProvider,
} = ui

export function MetricsToolbar({
  provider,
  model,
  environment,
  granularity,
  compare,
  timeRange,
  from,
  to,
  providerOptions,
  modelOptions,
  environmentOptions,
  visibleGraphs,
  lastUpdatedAt,
  exportPayload,
  onProviderChange,
  onModelChange,
  onEnvironmentChange,
  onGranularityChange,
  onCompareChange,
  onTimeRangeChange,
  onCustomRangeChange,
  onGraphToggle,
}: {
  provider: string
  model: string
  environment: string
  granularity: string
  compare: boolean
  timeRange: string
  from?: string
  to?: string
  providerOptions: string[]
  modelOptions: string[]
  environmentOptions: string[]
  visibleGraphs: Record<BoardGraphKey, boolean>
  lastUpdatedAt?: number
  exportPayload: unknown
  onProviderChange: (value: string) => void
  onModelChange: (value: string) => void
  onEnvironmentChange: (value: string) => void
  onGranularityChange: (value: string) => void
  onCompareChange: (value: boolean) => void
  onTimeRangeChange: (value: string) => void
  onCustomRangeChange: (range: { start: Date; end: Date }) => void
  onGraphToggle: (key: BoardGraphKey, value: boolean) => void
}) {
  const queryClient = useQueryClient()
  const [rangeOpen, setRangeOpen] = useState(false)
  const customRange =
    from && to ? { start: new Date(from), end: new Date(to) } : null
  const formatCustomRange = () => {
    if (!customRange) return 'Select dates'
    return `${dayjs(customRange.start).format('MMM D, HH:mm')} - ${dayjs(customRange.end).format('MMM D, HH:mm')}`
  }
  const graphsCount = useMemo(
    () => Object.values(visibleGraphs).filter(Boolean).length,
    [visibleGraphs],
  )
  const lastUpdated = lastUpdatedAt
    ? new Date(lastUpdatedAt).toLocaleTimeString()
    : 'never'

  const download = (format: 'json' | 'csv') => {
    const name = `metrics-board-${new Date().toISOString().replace(/[:.]/g, '-')}`
    let content = ''
    let mime = ''
    if (format === 'json') {
      content = JSON.stringify(exportPayload, null, 2)
      mime = 'application/json'
    } else {
      content = toCsv(exportPayload)
      mime = 'text/csv'
    }
    const blob = new Blob([content], { type: mime })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${name}.${format}`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
  }

  return (
    <TooltipProvider>
      <div className="flex flex-wrap items-center justify-end gap-2">
        <Select value={environment} onValueChange={onEnvironmentChange}>
          <SelectTrigger className="h-8 min-w-[132px] border-brand-main-600 bg-brand-main-800/50 text-sm text-white light:text-brand-main-50">
            <SelectValue placeholder="All envs" />
          </SelectTrigger>
          <SelectContent className="border-brand-main-600 bg-brand-main-900">
            <SelectItem value="all">All envs</SelectItem>
            {environmentOptions.map((opt) => (
              <SelectItem key={opt} value={opt}>
                {opt}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        <Select value={provider} onValueChange={onProviderChange}>
          <SelectTrigger className="h-8 min-w-[150px] border-brand-main-600 bg-brand-main-800/50 text-sm text-white light:text-brand-main-50">
            <SelectValue placeholder="All providers" />
          </SelectTrigger>
          <SelectContent className="border-brand-main-600 bg-brand-main-900">
            <SelectItem value="all">All providers</SelectItem>
            {providerOptions.map((opt) => (
              <SelectItem key={opt} value={opt}>
                {opt}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        <Select value={model} onValueChange={onModelChange}>
          <SelectTrigger className="h-8 min-w-[168px] border-brand-main-600 bg-brand-main-800/50 text-sm text-white light:text-brand-main-50">
            <SelectValue placeholder="All models" />
          </SelectTrigger>
          <SelectContent className="border-brand-main-600 bg-brand-main-900">
            <SelectItem value="all">All models</SelectItem>
            {modelOptions.map((opt) => (
              <SelectItem key={opt} value={opt}>
                {opt}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        <Select value={granularity} onValueChange={onGranularityChange}>
          <SelectTrigger className="h-8 w-[118px] border-brand-main-600 bg-brand-main-800/50 text-sm text-white light:text-brand-main-50">
            <SelectValue />
          </SelectTrigger>
          <SelectContent className="border-brand-main-600 bg-brand-main-900">
            <SelectItem value="hour">Hourly</SelectItem>
            <SelectItem value="6hour">6 hours</SelectItem>
            <SelectItem value="day">Daily</SelectItem>
            <Tooltip content="Metrics are pre-aggregated hourly">
              <div>
                <SelectItem value="minute" disabled>
                  By minute
                </SelectItem>
              </div>
            </Tooltip>
          </SelectContent>
        </Select>

        <Popover open={rangeOpen} onOpenChange={setRangeOpen}>
          <Select
            value={timeRange}
            onValueChange={(value) => {
              if (value === 'custom') setRangeOpen(true)
              onTimeRangeChange(value)
            }}
          >
            {timeRange === 'custom' ? (
              <PopoverTrigger asChild>
                <SelectTrigger className="h-8 min-w-[200px] border-brand-main-600 bg-brand-main-800/50 text-sm text-white light:text-brand-main-50">
                  <SelectValue>{formatCustomRange()}</SelectValue>
                </SelectTrigger>
              </PopoverTrigger>
            ) : (
              <SelectTrigger className="h-8 w-[112px] border-brand-main-600 bg-brand-main-800/50 text-sm text-white light:text-brand-main-50">
                <SelectValue />
              </SelectTrigger>
            )}
            <SelectContent className="border-brand-main-600 bg-brand-main-900">
              <SelectItem value="15m">15m</SelectItem>
              <SelectItem value="6h">6h</SelectItem>
              <SelectItem value="12h">12h</SelectItem>
              <SelectItem value="24h">24h</SelectItem>
              <SelectItem value="3d">3d</SelectItem>
              <SelectItem value="7d">7d</SelectItem>
              <SelectItem value="14d">14d</SelectItem>
              <SelectItem value="30d">30d</SelectItem>
              <SelectItem value="90d">90d</SelectItem>
              <SelectItem
                value="custom"
                onPointerDown={() => {
                  if (timeRange === 'custom') setTimeout(() => setRangeOpen(true), 0)
                }}
              >
                Custom
              </SelectItem>
            </SelectContent>
          </Select>
          <PopoverContent
            className="w-auto rounded p-0"
            align="end"
            side="bottom"
          >
            <DateTimePicker
              dateRange={customRange}
              onDateRangeChange={(range: { start: Date; end: Date }) => {
                onCustomRangeChange(range)
                setRangeOpen(false)
              }}
              onOpenChange={setRangeOpen}
            />
          </PopoverContent>
        </Popover>

        <label className="flex h-8 items-center gap-2 rounded border border-brand-main-600 bg-brand-main-800/50 px-2 text-xs text-white/70 light:text-black/70">
          Compare
          <Switch checked={compare} onCheckedChange={onCompareChange} />
        </label>

        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="outline" className="font-normal h-8 gap-1.5 text-xs">
              <SlidersHorizontal className="size-3.5" />
              Graphs <Badge variant="secondary" className='text-[10px]'>{graphsCount}</Badge>
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent
            align="end"
            className="border-brand-main-600 bg-brand-main-900"
          >
            <DropdownMenuLabel>Visible graphs</DropdownMenuLabel>
            <DropdownMenuSeparator />
            {BOARD_GRAPHS.map((graph) => (
              <DropdownMenuCheckboxItem
                key={graph.key}
                checked={visibleGraphs[graph.key]}
                onCheckedChange={(checked) =>
                  onGraphToggle(graph.key, Boolean(checked))
                }
              >
                {graph.label}
              </DropdownMenuCheckboxItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>

        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="outline" size="icon" className="h-8 w-8">
              <Download className="size-3.5" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent
            align="end"
            className="border-brand-main-600 bg-brand-main-900"
          >
            <DropdownMenuItem onClick={() => download('csv')}>
              Export CSV
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => download('json')}>
              Export JSON
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>

        <Button
          variant="outline"
          className="h-8 w-8"
          onClick={() =>
            queryClient.invalidateQueries({
              predicate: (query) =>
                Array.isArray(query.queryKey) &&
                typeof query.queryKey[0] === 'string' &&
                query.queryKey[0].startsWith('metrics-'),
            })
          }
          title={`Last updated ${lastUpdated}`}
        >
          <RefreshCw className="size-3.5" />
        </Button>
        <span className="hidden text-[10px] text-white/35 light:text-black/35 2xl:block">
          Updated {lastUpdated}
        </span>
      </div>
    </TooltipProvider>
  )
}

function toCsv(payload: unknown): string {
  const rows: string[] = [['section', 'key', 'value'].join(',')]
  const flat = flatten(payload)
  for (const [key, value] of Object.entries(flat)) {
    rows.push(['metrics', csvEscape(key), csvEscape(String(value))].join(','))
  }
  return rows.join('\n')
}

function flatten(value: unknown, prefix = ''): Record<string, unknown> {
  if (value === null || value === undefined) return { [prefix]: '' }
  if (typeof value !== 'object') return { [prefix]: value }
  if (Array.isArray(value)) {
    return value.reduce<Record<string, unknown>>((acc, item, index) => {
      Object.assign(acc, flatten(item, `${prefix}[${index}]`))
      return acc
    }, {})
  }
  return Object.entries(value as Record<string, unknown>).reduce<
    Record<string, unknown>
  >((acc, [key, item]) => {
    Object.assign(acc, flatten(item, prefix ? `${prefix}.${key}` : key))
    return acc
  }, {})
}

function csvEscape(value: string): string {
  if (!/[",\n]/.test(value)) return value
  return `"${value.replace(/"/g, '""')}"`
}
