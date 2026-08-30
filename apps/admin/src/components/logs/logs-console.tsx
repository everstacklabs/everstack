import { type RequestLog } from '@everstack/proto/everstack/logs/v1/logs_pb'
import type { LogCustomColumnDef } from '@everstack/proto/everstack/logs/v1/logs_service_pb'
import { Iconify, Loader2, ui } from '@everstack/ui'
import { useState, useRef, useEffect, memo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { cn } from '@everstack/utils/functions/cn'
import { listLogColumns } from '@/server/logs'
import { LOG_COLUMNS_QUERY_KEY } from './log-column-manager'
import { Route } from '@/routes/observability/logs'
import { useKeyboardKeys } from '@/hooks/use-keyboard-key'
import { useVirtualizer } from '@tanstack/react-virtual'
import {
	safeBigIntToNumber,
	formatTimeHHMMSS,
	truncateText,
	formatLatencyCompact,
	formatTokenBreakdown,
	formatCostCompact,
	getStatusIcon,
} from '@/utils/trace-formatters'
import { ProviderDisplay } from '../providers/provider-icon'
import { capitalize } from '@everstack/utils/functions/capitalize'
import { LogDetailSheet } from './log-detail-sheet'

const { Badge } = ui

const EMPTY_CUSTOM_COLUMNS: LogCustomColumnDef[] = []

// Format tokens with comma separators (used in tooltip)
function formatTokens(tokens: number | bigint): string {
	const num = typeof tokens === 'bigint' ? safeBigIntToNumber(tokens) : tokens
	return num.toLocaleString()
}

// Format streaming metrics for display
function formatTtft(ttftMs: bigint | number): string {
	const num = typeof ttftMs === 'bigint' ? safeBigIntToNumber(ttftMs) : ttftMs
	if (num < 1000) return `${num}ms`
	return `${(num / 1000).toFixed(1)}s`
}

function formatTokensPerSec(tps: number): string {
	if (tps === 0) return '-'
	if (tps < 1) return `${tps.toFixed(2)} t/s`
	if (tps < 100) return `${tps.toFixed(1)} t/s`
	return `${Math.round(tps)} t/s`
}

interface LogLineProps {
	log: RequestLog
	isSelected: boolean
	customColumnDefs: LogCustomColumnDef[]
	onClick: () => void
	rowRef: (el: HTMLDivElement | null) => void
	onHover: (event: React.MouseEvent<HTMLDivElement>) => void
	onHoverEnd: () => void
}

const LogLine = memo(({ log, isSelected, customColumnDefs, onClick, rowRef, onHover, onHoverEnd }: LogLineProps) => {
	const timestamp = formatTimeHHMMSS(log.timestamp)
	const provider = capitalize(log.provider) || 'N/A'
	const commandType = log.commandType || 'N/A'
	const model = truncateText(log.servedModel || log.model || 'N/A', 28)
	const latency = log.status === 'error' ? 'ERR' : formatLatencyCompact(log.latencyMs)
	const tokenBreakdown = formatTokenBreakdown(log.promptTokens, log.completionTokens)
	const cost = formatCostCompact(log.cost)
	const status = getStatusIcon(log.status)
	const statusColor = log.status === 'success' ? 'text-emerald-400 light:text-emerald-600' : log.status === 'error' ? 'text-rose-400 light:text-rose-600' : 'text-yellow-400 light:text-yellow-700'
	const correlationId = truncateText(log.correlationId, 8)
	const preview = truncateText(log.requestText || log.responseText || '', 35)

	// Streaming metrics
	const hasStreamingMetrics = log.stream && log.streamingMetrics && (safeBigIntToNumber(log.streamingMetrics.ttftMs) > 0 || log.streamingMetrics.chunkCount > 0)
	const ttft = hasStreamingMetrics ? formatTtft(log.streamingMetrics!.ttftMs) : null
	const tokensPerSec = hasStreamingMetrics ? formatTokensPerSec(log.streamingMetrics!.tokensPerSecond) : null

	// Get command type label
	const getCommandTypeLabel = (type: string): string => {
		const labels: Record<string, string> = {
			'ChatCompletion': 'CHAT',
			'ChatStreaming': 'CHAT STREAM',
			'Embeddings': 'EMBED',
			'ProcessEmbedding': 'EMBED',
			'Completion': 'COMP',
		}
		return labels[type] || type.substring(0, 5).toUpperCase()
	}

	return (
		<div
			ref={rowRef}
			className={cn(
				'text-sm px-4 py-2 mt-0.5 cursor-pointer hover:bg-brand-main-800/50 transition-colors border-b border-white/5 light:border-black/5 flex items-center w-full',
				isSelected && 'bg-brand-secondary-500/20'
			)}
			onClick={onClick}
			onMouseEnter={onHover}
			onMouseLeave={onHoverEnd}
		>
			<span className='text-brand-main-100 text-xs shrink-0'>{timestamp}</span>
			<span className={cn(statusColor, 'ml-4 font-semibold text-xs shrink-0')}>{status}</span>
			<Badge variant='secondary' className='ml-4 text-[10px] px-1 py-[2px] shrink-0'>
				{getCommandTypeLabel(commandType)}
			</Badge>
			{log.fallbackOccurred && <Badge variant='warning' className='ml-4 text-[10px] px-1 py-[2px] shrink-0'>FALLBACK</Badge>}
			<span className='ml-4 inline-flex items-center bg-brand-main-800/50 border border-brand-main-500 rounded px-1 py-[2px] gap-2 shrink-0' style={{ minWidth: '65px' }}>
				<span className='size-4 flex items-center justify-center'>
					<ProviderDisplay isActive={log.status === 'success'} providerName={log.provider || 'N/A'} />
				</span>
				<span className='text-brand-main-100 text-xs'>{provider}</span>
			</span>
			<span className='text-brand-main-100 ml-4 text-xs inline-block shrink-0'>{model}</span>
			<span className='text-brand-main-100 ml-4 inline-block text-right text-xs shrink-0'>{latency}</span>
			<span className='text-brand-main-100 ml-4 inline-block text-right text-xs shrink-0'>{tokenBreakdown}</span>
			<span className='text-brand-main-100 ml-4 inline-block text-right text-xs shrink-0'>{cost}</span>
			{/* Streaming performance indicators */}
			{hasStreamingMetrics && (
				<>
					<span className='text-brand-main-100 ml-4 text-xs shrink-0' title='Time to first token'>{ttft} TTFT</span>
					<span className='text-brand-main-100 ml-2 text-xs shrink-0' title='Tokens per second'>{tokensPerSec}</span>
				</>
			)}
			<span className='text-brand-main-100 ml-4 text-xs shrink-0' style={{ minWidth: '65px' }}>{correlationId}</span>
			{preview && <span className='text-brand-main-100 ml-4 italic text-xs shrink-0'>"{preview}"</span>}
			{customColumnDefs.map((def) => {
				const value = log.customColumns?.[def.key]
				if (!value) return null
				return (
					<span key={def.key} className='text-brand-main-100 ml-4 text-xs shrink-0' title={def.label}>
						<span className='text-white/40 light:text-black/40'>{def.label}:</span> {value}
					</span>
				)
			})}
		</div>
	)
})

export const LogsConsole = ({
	pageLogs,
	isLoading,
	selectedLogId: selectedLogIdProp,
	hasInstanceData,
	isLiveMode,
	fetchNextPage,
	isFetchingMore,
}: {
	pageLogs: RequestLog[]
	isLoading?: boolean
	isLiveMode?: boolean
	selectedLogId?: string
	hasInstanceData?: boolean
	fetchNextPage?: () => void
	isFetchingMore?: boolean
}) => {
	const navigate = Route.useNavigate()
	const selectedLogId = selectedLogIdProp || null
	const isExpanded = !!selectedLogId
	const [hoveredLog, setHoveredLog] = useState<RequestLog | null>(null)
	const [tooltipPosition, setTooltipPosition] = useState({ top: 0, left: 0 })

	// Tenant-defined custom columns, surfaced as trailing fields on each line.
	const { data: customColumnDefs = EMPTY_CUSTOM_COLUMNS } = useQuery({
		queryKey: LOG_COLUMNS_QUERY_KEY,
		queryFn: () => listLogColumns(),
		staleTime: 60_000,
	})

	// Refs for virtualization and scrolling
	const parentRef = useRef<HTMLDivElement>(null)
	const rowRefs = useRef<Map<string, HTMLDivElement | null>>(new Map())

	// Find the current log from pageLogs
	const currentLog = selectedLogId
		? pageLogs.find(log => log.correlationId === selectedLogId) ?? null
		: null

	// Navigation helpers
	const currentIndex = selectedLogId
		? pageLogs.findIndex(log => log.correlationId === selectedLogId)
		: -1

	const canGoPrevious = currentIndex > 0
	const canGoNext = currentIndex >= 0 && currentIndex < pageLogs.length - 1

	const handlePrevious = () => {
		if (canGoPrevious) {
			navigate({
				search: (prev) => ({
					...prev,
					log: pageLogs[currentIndex - 1].correlationId
				})
			})
		}
	}

	const handleNext = () => {
		if (canGoNext) {
			navigate({
				search: (prev) => ({
					...prev,
					log: pageLogs[currentIndex + 1].correlationId
				})
			})
		}
	}

	const handleSheetClose = () => {
		navigate({
			search: (prev) => ({
				...prev,
				log: undefined
			})
		})
	}

	const handleRowClick = (log: RequestLog) => {
		navigate({
			search: (prev) => ({
				...prev,
				log: log.correlationId
			})
		})
	}

	// Keyboard navigation
	useKeyboardKeys(
		['ArrowUp', 'ArrowDown'],
		(key) => {
			if (key === 'ArrowUp') {
				handlePrevious()
			} else if (key === 'ArrowDown') {
				handleNext()
			}
		},
		{
			enabled: isExpanded,
			preventDefault: true
		}
	)

	// Scroll selected log into view when it changes
	useEffect(() => {
		if (selectedLogId && rowRefs.current.has(selectedLogId)) {
			const rowElement = rowRefs.current.get(selectedLogId)
			if (rowElement) {
				rowElement.scrollIntoView({
					behavior: 'smooth',
					block: 'nearest',
				})
			}
		}
	}, [selectedLogId])

	// Virtualization setup
	const rowVirtualizer = useVirtualizer({
		count: pageLogs.length,
		getScrollElement: () => parentRef.current,
		estimateSize: () => 41, // Single line height with increased padding
		overscan: 10,
	})

	// Infinite scroll detection
	const virtualItems = rowVirtualizer.getVirtualItems()
	const lastItem = virtualItems[virtualItems.length - 1]

	useEffect(() => {
		if (!lastItem) return
		if (lastItem.index >= pageLogs.length - 1 && fetchNextPage && !isFetchingMore) {
			fetchNextPage()
		}
	}, [lastItem, pageLogs.length, fetchNextPage, isFetchingMore])

	// Update tooltip position when hovering
	const handleLogHover = (log: RequestLog | null, event?: React.MouseEvent<HTMLDivElement>) => {
		if (log && event) {
			const rect = event.currentTarget.getBoundingClientRect()
			const parentRect = parentRef.current?.getBoundingClientRect()
			if (parentRect) {
				let top = rect.top

				const estimatedTooltipHeight = 270
				const viewportHeight = window.innerHeight
				const padding = 16

				if (top + estimatedTooltipHeight > viewportHeight - padding) {
					top = Math.max(padding, viewportHeight - estimatedTooltipHeight - padding)
				}

				setTooltipPosition({
					top,
					left: parentRect.left - 320
				})
			}
		}
		setHoveredLog(log)
	}

	return (
		<div className='flex-1 flex flex-col overflow-hidden'>
			{/* Empty state or log lines */}
			{pageLogs.length === 0 ? (
				<div className='flex-1 flex items-center justify-center'>
					{hasInstanceData === false && pageLogs.length === 0 && !isLoading ? (
						<div className='flex flex-col items-center justify-center'>
							<div className='relative mb-6'>
								<div className='absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl' />
								<div className='relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4'>
									<Iconify.Icon icon='tabler:logs' className='size-8 text-brand-secondary-400' />
								</div>
							</div>
							<h3 className='text-base font-medium text-white light:text-brand-main-50 mb-2'>Welcome to Everstack Logs</h3>
							<p className='text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed'>
								Start sending requests through your gateway to see logs appear here in real-time.
							</p>
						</div>
					) : pageLogs.length === 0 ? (
						isLiveMode ? (
							!isLoading ? (
								<div className='flex items-center justify-center h-full space-x-2'>
									<Loader2 className='w-4 h-4 animate-spin text-brand-main-100' />
									<span className='text-brand-main-100 text-sm'>Listening for logs...</span>
								</div>
							) : (
								<div className='flex items-center justify-center h-full space-x-2'>
									<Loader2 className='w-4 h-4 animate-spin text-brand-main-300' />
									<span className='text-brand-main-100 text-sm'>Loading logs...</span>
								</div>
							)
						) : (
							isLoading ? (
								<div className='flex items-center justify-center h-full space-x-2'>
									<Loader2 className='w-4 h-4 animate-spin text-brand-main-300' />
									<span className='text-brand-main-100 text-sm'>Loading logs...</span>
								</div>
							) : (
								<div className='flex items-center justify-center h-full space-x-2'>
									<span className='text-brand-main-300'>No logs found for this time range.</span>
								</div>
							)
						)
					) : null}
				</div>
			) : (
				<div className='relative flex flex-1 overflow-hidden'>
					{/* Tooltip panel - positioned fixed to escape overflow-hidden */}
					{hoveredLog && (
						<div
							className='fixed w-[280px] transition-all duration-100 z-50'
							style={{ top: `${tooltipPosition.top}px`, left: `${tooltipPosition.left + 35}px` }}
						>
							<div className='bg-brand-main-600 border border-white/5 light:border-black/5 rounded shadow-lg p-1 text-xs space-y-1.5'>
								<div className='flex gap-3 border-b border-white/10 light:border-black/10 pb-1'>
									<span className='text-brand-main-100 w-20 shrink-0'>Timestamp:</span>
									<span className='text-brand-main-100 wrap-break-words font-mono'>{formatTimeHHMMSS(hoveredLog.timestamp)}</span>
								</div>
								<div className='flex items-center gap-3'>
									<span className='text-brand-main-100 w-20 shrink-0'>Provider:</span>
									<div className='size-3.5 flex items-center justify-center -mr-2'>
										<ProviderDisplay isActive={hoveredLog.status === 'success'} providerName={hoveredLog.provider || 'N/A'} />
									</div>
									<span className='text-brand-main-100 wrap-break-words'>{hoveredLog.provider?.toUpperCase() || 'N/A'}</span>
								</div>
								<div className='flex gap-3'>
									<span className='text-brand-main-100 w-20 shrink-0'>Model:</span>
									<span className='text-brand-main-100 wrap-break-words'>{hoveredLog.servedModel || hoveredLog.model || 'N/A'}</span>
								</div>
								<div className='flex gap-3'>
									<span className='text-brand-main-100 w-20 shrink-0'>Type:</span>
									<span className='text-brand-main-100 wrap-break-words'>{hoveredLog.commandType || 'N/A'}</span>
								</div>
								<div className='flex gap-3'>
									<span className='text-brand-main-100 w-20 shrink-0'>Status:</span>
									<span className={cn(
										hoveredLog.status === 'success' ? 'text-emerald-400 light:text-emerald-600' :
											hoveredLog.status === 'error' ? 'text-rose-400 light:text-rose-600' :
												'text-yellow-400 light:text-yellow-700'
									)}>
										{hoveredLog.status.toUpperCase()}
									</span>
								</div>
								<div className='flex gap-3'>
									<span className='text-brand-main-100 w-20 shrink-0'>Latency:</span>
									<span className='text-brand-main-100 wrap-break-words font-mono'>{formatLatencyCompact(hoveredLog.latencyMs)}</span>
								</div>
								<div className='flex gap-3'>
									<span className='text-brand-main-100 w-20 shrink-0'>Tokens:</span>
									<span className='text-brand-main-100 wrap-break-words font-mono'>
										{formatTokens(hoveredLog.promptTokens)} in / {formatTokens(hoveredLog.completionTokens)} out
									</span>
								</div>
								<div className='flex gap-3'>
									<span className='text-brand-main-100 w-20 shrink-0'>Cost:</span>
									<span className='text-brand-main-100 wrap-break-words font-mono'>{formatCostCompact(hoveredLog.cost)}</span>
								</div>
								{hoveredLog.fallbackOccurred && (
									<div className='flex gap-3'>
										<span className='text-brand-main-100 w-20 shrink-0'>Fallback:</span>
										<span className='text-yellow-400 light:text-yellow-700'>YES</span>
									</div>
								)}
								<div className='flex gap-3 border-t border-white/10 light:border-black/10 pt-1.5'>
									<span className='text-brand-main-100 w-20 shrink-0'>ID:</span>
									<span className='text-brand-main-100 text-[10px] break-all font-mono'>{hoveredLog.correlationId}</span>
								</div>
								{(hoveredLog.requestText || hoveredLog.responseText) && (
									<div className='flex gap-3 pt-1'>
										<span className='text-brand-main-100 w-20 shrink-0'>Preview:</span>
										<span className='text-brand-main-100 italic wrap-break-words line-clamp-2'>"{truncateText(hoveredLog.requestText || hoveredLog.responseText || '', 35)}"</span>
									</div>
								)}
								<div className='text-brand-main-100 text-[10px] pt-2 border-t border-white/10 light:border-black/10 text-center'>
									Click to view full details
								</div>
							</div>
						</div>
					)}

					{/* Logs scrollable area - takes remaining space */}
					<div ref={parentRef} className='flex-1 overflow-auto scrollbar-macos'>
						<div style={{ minWidth: '1200px' }}>
							<div
								style={{
									height: `${rowVirtualizer.getTotalSize()}px`,
									position: 'relative',
								}}
							>
								{rowVirtualizer.getVirtualItems().map((virtualRow) => {
									const log = pageLogs[virtualRow.index]
									return (
										<div
											key={log.correlationId}
											style={{
												position: 'absolute',
												top: 0,
												left: 0,
												right: 0,
												height: `${virtualRow.size}px`,
												transform: `translateY(${virtualRow.start}px)`,
											}}
										>
											<LogLine
												log={log}
												isSelected={log.correlationId === selectedLogId}
												customColumnDefs={customColumnDefs}
												onClick={() => handleRowClick(log)}
												onHover={(e) => handleLogHover(log, e)}
												onHoverEnd={() => handleLogHover(null)}
												rowRef={(el) => {
													if (el) {
														rowRefs.current.set(log.correlationId, el)
													} else {
														rowRefs.current.delete(log.correlationId)
													}
												}}
											/>
										</div>
									)
								})}
							</div>
							{isFetchingMore && (
								<div className='flex items-center justify-center py-4 space-x-2'>
									<Loader2 className='w-4 h-4 animate-spin text-brand-main-100' />
									<span className='text-brand-main-100 text-sm'>Loading more...</span>
								</div>
							)}
						</div>
					</div>
				</div>
			)}

			<LogDetailSheet
				log={currentLog}
				open={isExpanded}
				onClose={handleSheetClose}
				onPrevious={handlePrevious}
				onNext={handleNext}
				canGoPrevious={canGoPrevious}
				canGoNext={canGoNext}
			/>
		</div>
	)
}
