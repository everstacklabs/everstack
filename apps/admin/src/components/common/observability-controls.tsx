import { useMemo, useState } from 'react'
import { TIME_RANGE_LABELS, shouldBeLiveMode } from '@/lib/time-ranges'
import type { TimeRangePreset } from '@/stores/logs-store'
import {
  DateTimePicker,
  InputWithIcon,
  Popover,
  PopoverContent,
  PopoverTrigger,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Tooltip,
  TooltipProvider,
} from '@everstack/ui/components'
import { Play, RefreshCcw, Search } from 'lucide-react'
import { cn } from '@/lib/utils'
import dayjs from 'dayjs'
import type { EvsQueryField } from '@/utils/evs-query'
import { EsqlFilterBar } from './esql-filter-bar'

export type AdvancedSearchField = EvsQueryField

export interface ObservabilityControlsProps {
  // Core props
  search: Record<string, any>
  navigate: (options: any) => void

  // Feature toggles
  showLiveToggle?: boolean
  showSearch?: boolean
  showRefresh?: boolean

  // Live toggle props
  isLiveMode?: boolean
  onLiveToggle?: () => void

  // Search props
  searchPlaceholder?: string
  searchValue?: string
  onSearchChange?: (value: string) => void
  advancedSearchFields?: AdvancedSearchField[]

  // Time range props
  timeRange?: TimeRangePreset
  customDateRange?: { start: Date; end: Date } | null
  onDateRangeChange?: (range: { start: Date; end: Date }) => void

  // Refresh props
  isLoading?: boolean
  onRefresh?: () => void

  // Styling
  className?: string
}

export function ObservabilityControls({
  search,
  navigate,
  showLiveToggle = true,
  showSearch = true,
  showRefresh = true,
  isLiveMode = false,
  onLiveToggle,
  searchPlaceholder = 'Search...',
  searchValue = '',
  onSearchChange,
  advancedSearchFields,
  timeRange = '15m',
  customDateRange = null,
  onDateRangeChange,
  isLoading = false,
  onRefresh,
  className,
}: ObservabilityControlsProps) {
  const [open, setOpen] = useState(false)

  // Computed values
  const customDateRangeMemo = useMemo(() => {
    if (search.from && search.to) {
      return {
        start: new Date(search.from),
        end: new Date(search.to),
      }
    }
    return null
  }, [search.from, search.to])

  const handleDateRangeChange = (range: { start: Date; end: Date }) => {
    // Determine if this custom range should be in live or paused mode
    const shouldBeLive = shouldBeLiveMode('custom', range)

    onDateRangeChange?.(range) ||
      navigate({
        search: {
          ...search,
          range: 'custom',
          from: range.start.toISOString(),
          to: range.end.toISOString(),
          live: shouldBeLive ? 'true' : 'false',
        },
      })
    setOpen(false)
  }

  const formatCustomRange = () => {
    const rangeToUse = customDateRange || customDateRangeMemo
    if (!rangeToUse?.start || !rangeToUse?.end) return 'Select dates'

    const startDate = dayjs(rangeToUse.start)
    const endDate = dayjs(rangeToUse.end)

    // Always show timestamps for custom ranges
    const startFormatted = startDate.format('MMM DD HH:mm')
    const endFormatted = endDate.format('MMM DD HH:mm')

    return `${startFormatted} - ${endFormatted}`
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <div
        className={cn(
          'flex items-center gap-2 bg-brand-main-950 px-2 py-1 h-12 w-full',
          className,
        )}
      >
        {/* Live/Pause Toggle */}
        {showLiveToggle && (
          <button
            type="button"
            onClick={
              onLiveToggle ||
              (() => {
                if (isLiveMode) {
                  // If live, clicking pauses at current time range (keep current range)
                  navigate({
                    search: {
                      ...search,
                      live: 'false',
                    },
                  })
                } else {
                  // If paused, clicking goes to live mode with 15m preset
                  navigate({
                    search: {
                      ...search,
                      live: 'true',
                      range: '15m',
                      from: undefined,
                      to: undefined,
                    },
                  })
                }
              })
            }
            className="inline-flex h-8 items-center justify-center gap-2 whitespace-nowrap rounded border border-brand-main-500 bg-brand-main-950/10 px-3 py-[6px] text-sm font-medium text-brand-main-50 shadow-xs transition-all hover:border-brand-main-600 hover:bg-brand-main-700 hover:text-white light:hover:text-brand-main-50 [&_svg]:pointer-events-none [&_svg]:shrink-0"
          >
            {isLiveMode ? (
              <>
                <div className="relative flex items-center justify-center">
                  <div className="relative inline-flex mr-1">
                    <div className="w-2 h-2 bg-brand-secondary-500 rounded-full"></div>
                    <div className="w-2 h-2 bg-brand-secondary-500 rounded-full absolute top-0 left-0 animate-ping"></div>
                    <div className="w-2 h-2 bg-brand-secondary-500 rounded-full absolute top-0 left-0 animate-pulse"></div>
                  </div>
                </div>
                <span>Live</span>
              </>
            ) : (
              <>
                <Play className="size-4" />
                <span>Paused</span>
              </>
            )}
          </button>
        )}

        {/* Search Input */}
        {showSearch &&
          (advancedSearchFields?.length ? (
            <EsqlFilterBar
              search={search}
              navigate={navigate}
              placeholder={searchPlaceholder}
              className="w-4/5 flex-1"
            />
          ) : (
            <InputWithIcon
              icon={
                <Search className="size-4 text-white/50 light:text-black/50" />
              }
              placeholder={searchPlaceholder}
              value={searchValue || search.query || ''}
              onChange={(e) => {
                onSearchChange?.(e.target.value) ||
                  navigate({
                    search: {
                      ...search,
                      query: e.target.value || undefined,
                    },
                  })
              }}
              className="w-4/5 flex-1"
            />
          ))}
        {/* Time Range Selector */}
        <div className="relative flex items-center gap-2">
          <Select
            value={timeRange}
            onValueChange={(value) => {
              if (value === 'custom') {
                setOpen(true)
              } else {
                setOpen(false)
              }

              // Preset ranges should always be in live mode
              const shouldBeLive = value !== 'custom'

              navigate({
                search: {
                  ...search,
                  range: value as TimeRangePreset,
                  from: undefined,
                  to: undefined,
                  live: shouldBeLive ? 'true' : search.live,
                },
              })
            }}
          >
            {timeRange === 'custom' ? (
              <PopoverTrigger asChild>
                <SelectTrigger className="w-[280px] h-8 text-xs border-brand-main-600 space-x-3">
                  <div className="flex items-center gap-2">
                    <div className="bg-brand-main-600 -ml-1 rounded px-2 py-0.5 text-xs text-white light:text-brand-main-50">
                      custom
                    </div>
                    <SelectValue>{formatCustomRange()}</SelectValue>
                  </div>
                </SelectTrigger>
              </PopoverTrigger>
            ) : (
              <SelectTrigger className="w-[280px] h-8 text-xs border-brand-main-600 space-x-3">
                <div className="flex items-center gap-2">
                  <div className="bg-brand-main-600 rounded px-2 -ml-1 py-0.5 text-xs text-white light:text-brand-main-50">
                    {Object.keys(TIME_RANGE_LABELS).find(
                      (key) => key === timeRange,
                    )}
                  </div>
                  <SelectValue>
                    {TIME_RANGE_LABELS[timeRange as TimeRangePreset]}
                  </SelectValue>
                </div>
              </SelectTrigger>
            )}
            <SelectContent className="data-[side=bottom]:translate-x-1">
              {Object.keys(TIME_RANGE_LABELS).map((option) => (
                <SelectItem
                  key={option}
                  value={option as TimeRangePreset}
                  onPointerDown={() => {
                    // Handle re-clicking same option - use pointerDown to catch before select logic
                    if (option === 'custom' && timeRange === 'custom') {
                      setTimeout(() => setOpen(true), 0)
                    }
                  }}
                >
                  {TIME_RANGE_LABELS[option as TimeRangePreset]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        {/* Refresh Button */}
        {showRefresh && !isLiveMode && (
          <TooltipProvider>
            <Tooltip content="Refresh">
              <button
                type="button"
                onClick={onRefresh}
                className={cn(
                  'inline-flex h-8 items-center justify-center rounded border border-brand-main-500 bg-brand-main-950/10 px-2 py-1.5 text-brand-main-50 shadow-xs transition-all hover:border-brand-main-600 hover:bg-brand-main-700 hover:text-white light:hover:text-brand-main-50 cursor-pointer',
                )}
              >
                <RefreshCcw
                  className={cn('size-4', isLoading && 'animate-spin')}
                />
              </button>
            </Tooltip>
          </TooltipProvider>
        )}
      </div>

      {/* Date Time Picker Popover Content */}
      <PopoverContent
        className="data-[side=bottom]:translate-x-1 rounded w-auto p-0"
        align="end"
        side="bottom"
      >
        <DateTimePicker
          dateRange={customDateRange || customDateRangeMemo}
          onDateRangeChange={handleDateRangeChange}
          onOpenChange={setOpen}
        />
      </PopoverContent>
    </Popover>
  )
}

