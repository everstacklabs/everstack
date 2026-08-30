import { type RequestLog, type FunctionExecution } from '@everstack/proto/everstack/logs/v1/logs_pb'
import { Iconify, ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import { copyToClipboard } from '@everstack/utils/functions/clipboard'
import { useState } from 'react'
import { cn } from '@everstack/utils/functions/cn'
import { capitalize } from '@everstack/utils/functions/capitalize'
import { Check, ExternalLink } from 'lucide-react'
import { NavigationButtons } from '../common/navigation-buttons'
import { useNavigate } from '@tanstack/react-router'
import { safeBigIntToNumber } from '@/utils/trace-formatters'
import { ProviderDisplay } from '../providers/provider-icon'
import dayjs from 'dayjs'
import { ChatType, getChatTypeLabel } from '@/lib/chat-type'
import { JsonViewer } from '@/ui/json-viewer'

const { Sheet, SheetHeader, SheetBody, SheetContent, SheetTitle, Badge, Button, Tooltip, TooltipProvider, Collapsible, CollapsibleTrigger, CollapsibleContent } = ui

function tryParseJson(str: string): Record<string, unknown> | null {
	try {
		const parsed = JSON.parse(str)
		if (typeof parsed === 'object' && parsed !== null) {
			return parsed as Record<string, unknown>
		}
		return null
	} catch {
		return null
	}
}

function formatCost(cost: number): string {
	return `$${cost.toFixed(4)}`
}

function formatTokens(tokens: number | bigint): string {
	const num = typeof tokens === 'bigint' ? safeBigIntToNumber(tokens) : tokens
	return num.toLocaleString()
}

function formatLatency(ms: number | bigint): string {
	const num = typeof ms === 'bigint' ? safeBigIntToNumber(ms) : ms
	if (num < 1000) return `${num}ms`
	return `${(num / 1000).toFixed(2)}s`
}

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

interface LogDetailSheetProps {
	log: RequestLog | null
	open: boolean
	onClose: () => void
	onPrevious: () => void
	onNext: () => void
	canGoPrevious: boolean
	canGoNext: boolean
}

export function LogDetailSheet({ log, open, onClose, onPrevious, onNext, canGoPrevious, canGoNext }: LogDetailSheetProps) {
	const navigateToTraces = useNavigate()
	const [isCopied, setIsCopied] = useState(false)

	const handleCopy = async (text: string) => {
		await copyToClipboard(text)
		toast.success(`Log ID copied to clipboard`)
		setIsCopied(true)
		setTimeout(() => setIsCopied(false), 2000)
	}

	const handleViewTrace = () => {
		if (log?.correlationId) {
			const logStart = log.firstTimestamp
				? new Date(log.firstTimestamp)
				: log.timestamp
					? new Date((typeof log.timestamp.seconds === 'bigint' ? safeBigIntToNumber(log.timestamp.seconds) : Number(log.timestamp.seconds)) * 1000)
					: new Date()

			const logEnd = log.timestamp
				? new Date((typeof log.timestamp.seconds === 'bigint' ? safeBigIntToNumber(log.timestamp.seconds) : Number(log.timestamp.seconds)) * 1000)
				: new Date()

			const bufferMs = 5 * 60 * 1000
			const from = new Date(logStart.getTime() - bufferMs).toISOString()
			const to = new Date(logEnd.getTime() + bufferMs).toISOString()

			navigateToTraces({
				to: '/observability/traces',
				search: {
					live: 'false',
					range: 'custom',
					from,
					to,
					correlationId: log.correlationId
				}
			})
		}
	}

	const hasStreamingMetrics = log?.stream && log.streamingMetrics && (safeBigIntToNumber(log.streamingMetrics.ttftMs) > 0 || log.streamingMetrics.chunkCount > 0)
	const showRequestedModel = log?.requestedModel && log.servedModel && log.requestedModel !== log.servedModel

	return (
		<Sheet open={open} onOpenChange={onClose}>
			<SheetContent side='right' className='min-w-1/2 max-h-screen pb-2 overflow-y-auto scrollbar-macos'>
				<SheetHeader className='w-full'>
					<div className='flex items-center justify-between w-full'>
						<SheetTitle className='w-full'>
							<TooltipProvider>
								{log && (
									<div className='flex items-center justify-between w-full'>
										<div className='flex items-center justify-start py-2.5 text-sm text-white light:text-brand-main-50 font-semibold'>
											<div className='flex items-center gap-2 mr-2'>
												<NavigationButtons
													canGoPrevious={canGoPrevious}
													canGoNext={canGoNext}
													onPrevious={onPrevious}
													onNext={onNext}
													previousLabel='Previous Log'
													nextLabel='Next Log'
													iconClassName='h-4 w-4'
												/>
											</div>
											{log.correlationId && (
												<Tooltip content={
													<div className='flex items-center gap-2 px-2 py-1'>
														<span className='text-white/90 light:text-black/90 text-xs'>Copy Log ID</span>
													</div>
												}>
													<div className='flex items-center gap-2'>
														<Badge variant='secondary' className='text-xs rounded py-1.5 cursor-pointer' onClick={() => handleCopy(log.correlationId)}>
															<span className='text-white/90 light:text-black/90 text-xs'>{log.correlationId}</span>
														</Badge>
														{isCopied && (
															<Check className='size-3 text-green-500' />
														)}
													</div>
												</Tooltip>
											)}
										</div>
										<Tooltip content={
											<div className='flex items-center gap-2 px-2 py-1'>
												<span className='text-white/90 light:text-black/90 text-xs'>View Full Trace</span>
											</div>
										}>
											{log.correlationId && (
												<Button
													variant='secondary'
													onClick={handleViewTrace}
													className='mr-1 p-2'
												>
													<ExternalLink className='size-3' />
												</Button>
											)}
										</Tooltip>
									</div>
								)}
							</TooltipProvider>
						</SheetTitle>
					</div>
				</SheetHeader>
				<SheetBody>
					{log && (
						<div className='space-y-4 mt-4'>
							{/* Request Details */}
							<div className='space-y-2 border-b border-dotted border-white/10 light:border-black/10 pb-4'>
								<h3 className='text-sm font-semibold text-white/80 light:text-black/80'>Request Details</h3>
								<div className='grid grid-cols-3 gap-3 text-sm'>
									<div className='space-y-1'>
										<div className='text-white/50 light:text-black/50 text-xs'>Provider</div>
										<div className='inline-flex items-center gap-2 bg-brand-main-800/50 border border-brand-main-600/30 rounded-md px-2.5 py-1 w-fit'>
											<ProviderDisplay
												providerName={log.provider || 'N/A'}
												isActive={false}
												useImage={true}
											/>
											<span className='text-xs text-white/90 light:text-black/90 font-medium'>
												{capitalize(log.provider || 'N/A')}
											</span>
										</div>
									</div>
									<div className='space-y-1'>
										<div className='text-white/50 light:text-black/50 text-xs'>Model</div>
										<div className='text-white/90 light:text-black/90 text-xs'>
											{log.servedModel || log.model || 'UNKNOWN'}
										</div>
										{showRequestedModel && (
											<div className='text-white/50 light:text-black/50 text-[10px]'>
												Requested: {log.requestedModel}
											</div>
										)}
									</div>
									<div className='space-y-1'>
										<div className='text-white/50 light:text-black/50 text-xs'>Type</div>
										<Badge variant='chatType'>
											{(() => {
												if (log.stream && log.commandType === 'ChatCompletion') {
													return getChatTypeLabel(ChatType.CHAT_STREAMING).label
												}
												return getChatTypeLabel(log.commandType as ChatType).label
											})()}
										</Badge>
									</div>
									<div className='space-y-1'>
										<div className='text-white/50 light:text-black/50 text-xs'>Status</div>
										<Badge variant={log.status === 'success' ? 'success' : log.status === 'error' ? 'error' : 'warning'}>
											{log.status.toUpperCase()}
										</Badge>
									</div>
									<div className='space-y-1'>
										<div className='text-white/50 light:text-black/50 text-xs'>Cost</div>
										<div className='text-white/90 light:text-black/90 font-mono text-sm font-bold'>
											{formatCost(log.cost)}
										</div>
									</div>
									<div className='space-y-1'>
										<div className='text-white/50 light:text-black/50 text-xs'>Severity</div>
										<div className={cn(log.severity === 'error' ? 'text-red-400 light:text-red-600' : log.severity === 'warn' ? 'text-yellow-400 light:text-yellow-700' : 'text-white/90 light:text-black/90')}>
											{log.severity || 'N/A'}
										</div>
									</div>
								</div>
							</div>

							{/* Timings */}
							<div className='space-y-2 border-b border-dotted border-white/10 light:border-black/10 pb-4'>
								<h3 className='text-sm font-semibold text-white/80 light:text-black/80'>Timings</h3>
								<div className='grid grid-cols-3 gap-3 text-sm'>
									<div className='space-y-1'>
										<div className='text-white/50 light:text-black/50 text-xs'>Start</div>
										<div className='text-white/90 light:text-black/90 font-mono text-xs'>
											{dayjs(log.firstTimestamp).format('YYYY-MM-DD HH:mm:ss') || 'N/A'}
										</div>
									</div>
									<div className='space-y-1'>
										<div className='text-white/50 light:text-black/50 text-xs'>End</div>
										<div className='text-white/90 light:text-black/90 font-mono text-xs'>
											{log.timestamp ?
												dayjs(new Date((typeof log.timestamp.seconds === 'bigint' ? safeBigIntToNumber(log.timestamp.seconds) : Number(log.timestamp.seconds)) * 1000).toISOString()).format('YYYY-MM-DD HH:mm:ss')
												: 'N/A'}
										</div>
									</div>
									<div className='space-y-1'>
										<div className='text-white/50 light:text-black/50 text-xs'>Latency</div>
										<div className='text-white/90 light:text-black/90 font-mono'>{formatLatency(log.latencyMs)}</div>
									</div>
								</div>
							</div>

							{/* Streaming Performance */}
							{hasStreamingMetrics && log.streamingMetrics && (
								<div className='space-y-2 border-b border-dotted border-white/10 light:border-black/10 pb-4'>
									<h3 className='text-sm font-semibold text-white/80 light:text-black/80'>Streaming Performance</h3>
									<div className='grid grid-cols-3 gap-3 text-sm'>
										<div className='space-y-1'>
											<div className='text-white/50 light:text-black/50 text-xs'>Time to First Token</div>
											<div className='text-white/90 light:text-black/90 font-semibold'>
												{formatTtft(log.streamingMetrics.ttftMs)}
											</div>
										</div>
										<div className='space-y-1'>
											<div className='text-white/50 light:text-black/50 text-xs'>Tokens/Second</div>
											<div className='text-white/90 light:text-black/90 font-semibold'>
												{formatTokensPerSec(log.streamingMetrics.tokensPerSecond)}
											</div>
										</div>
										<div className='space-y-1'>
											<div className='text-white/50 light:text-black/50 text-xs'>Chunk Count</div>
											<div className='text-white/90 light:text-black/90 font-mono'>
												{log.streamingMetrics.chunkCount}
											</div>
										</div>
										<div className='space-y-1'>
											<div className='text-white/50 light:text-black/50 text-xs'>Avg Chunk Latency</div>
											<div className='text-white/90 light:text-black/90 font-mono'>
												{log.streamingMetrics.avgChunkLatencyMs > 0
													? `${log.streamingMetrics.avgChunkLatencyMs.toFixed(1)}ms`
													: '-'}
											</div>
										</div>
										<div className='space-y-1'>
											<div className='text-white/50 light:text-black/50 text-xs'>Max Chunk Latency</div>
											<div className={cn(
												'font-mono',
												safeBigIntToNumber(log.streamingMetrics.maxChunkLatencyMs) > 500
													? 'text-yellow-400 light:text-yellow-700'
													: 'text-white/90 light:text-black/90'
											)}>
												{safeBigIntToNumber(log.streamingMetrics.maxChunkLatencyMs) > 0
													? `${safeBigIntToNumber(log.streamingMetrics.maxChunkLatencyMs)}ms`
													: '-'}
											</div>
										</div>
										<div className='space-y-1'>
											<div className='text-white/50 light:text-black/50 text-xs'>Stream Duration</div>
											<div className='text-white/90 light:text-black/90 font-mono'>
												{safeBigIntToNumber(log.streamingMetrics.streamDurationMs) > 0
													? formatLatency(log.streamingMetrics.streamDurationMs)
													: '-'}
											</div>
										</div>
									</div>
									{log.status === 'error' && log.streamingMetrics.partialResponseOnError && (
										<div className='mt-3'>
											<div className='text-white/50 light:text-black/50 text-xs mb-1'>Partial Response Before Error</div>
											<div className='text-yellow-400/80 light:text-yellow-700/80 text-sm bg-yellow-500/10 p-2 rounded border border-yellow-500/20 whitespace-pre-wrap max-h-[100px] overflow-y-auto scrollbar-macos'>
												{log.streamingMetrics.partialResponseOnError}
											</div>
										</div>
									)}
								</div>
							)}

							{/* Token Usage */}
							{log.totalTokens > 0 && (
								<div className='space-y-2 border-b border-dotted border-white/10 light:border-black/10 pb-4'>
									<h3 className='text-sm font-semibold text-white/80 light:text-black/80'>Token Usage</h3>
									<div className='grid grid-cols-3 gap-3 text-sm'>
										<div>
											<div className='text-white/50 light:text-black/50 text-xs'>Input</div>
											<div className='text-white/90 light:text-black/90 font-mono'>{formatTokens(log.promptTokens)}</div>
										</div>
										<div>
											<div className='text-white/50 light:text-black/50 text-xs'>Output</div>
											<div className='text-white/90 light:text-black/90 font-mono'>{formatTokens(log.completionTokens)}</div>
										</div>
										<div className='space-y-1'>
											<div className='text-white/50 light:text-black/50 text-xs'>Total Tokens</div>
											<div className='text-white/90 light:text-black/90 font-mono'>
												{log.totalTokens > 0 ? formatTokens(log.totalTokens) : 'N/A'}
											</div>
										</div>
									</div>
								</div>
							)}

							{/* Request Journey */}
							{log.attemptedModels && log.attemptedModels.length > 0 && (
								<div className='space-y-2 border-b border-dotted border-white/10 light:border-black/10 pb-4'>
									<h3 className='text-sm font-semibold text-white/80 light:text-black/80'>Request Journey</h3>
									<div className='space-y-2'>
										<div className='space-y-1.5'>
											<div className='flex items-center gap-2 text-xs'>
												<span className='text-white/50 light:text-black/50 min-w-[80px]'>Models Tried:</span>
												<span className='text-white/90 light:text-black/90'>
													{new Set(log.attemptedModels).size} unique model{new Set(log.attemptedModels).size !== 1 ? 's' : ''}
												</span>
											</div>
											<div className='flex items-center gap-2 text-xs'>
												<span className='text-white/50 light:text-black/50 min-w-[80px]'>Total Attempts:</span>
												<span className='text-white/90 light:text-black/90'>{log.attemptCount || 0} (includes retries)</span>
											</div>
										</div>
										<div className='space-y-2'>
											<div className='text-xs text-white/50 light:text-black/50 mb-1.5'>Model Attempts (chronological):</div>
											{log.attemptedModels.map((model, idx) => (
												<div key={idx} className='flex items-center gap-2 text-xs bg-white/5 light:bg-black/5 p-2 rounded border border-white/10 light:border-black/10'>
													<Badge variant='default' className='text-[10px] px-1.5 py-0 min-w-[35px] text-center'>
														#{idx + 1}
													</Badge>
													<span className='text-white/90 light:text-black/90 font-mono flex-1'>{model}</span>
													{idx === log.attemptedModels.length - 1 && (
														<Badge
															variant={log.status === 'success' ? 'success' : log.status === 'error' ? 'error' : 'warning'}
															className='text-[10px] px-1.5 py-0'
														>
															{log.status === 'success' ? 'SERVED' : log.status === 'error' ? 'FAILED' : 'PENDING'}
														</Badge>
													)}
													{idx < log.attemptedModels.length - 1 && (
														<Badge variant='warning' className='text-[10px] px-1.5 py-0'>
															PENDING
														</Badge>
													)}
												</div>
											))}
										</div>
									</div>
								</div>
							)}

							{/* Function Executions */}
							{log.functionExecutions && log.functionExecutions.length > 0 && (
								<div className='space-y-2 border-b border-dotted border-white/10 light:border-black/10 pb-4'>
									<h3 className='text-sm font-semibold text-white/80 light:text-black/80'>Function Executions</h3>
									<div className='space-y-2'>
										{log.functionExecutions.map((exec: FunctionExecution, idx: number) => (
											<Collapsible key={idx}>
												<div className='bg-white/5 light:bg-black/5 rounded border border-white/10 light:border-black/10'>
													<CollapsibleTrigger className='w-full'>
														<div className='flex items-center justify-between p-3 cursor-pointer hover:bg-white/5 light:hover:bg-black/5'>
															<div className='flex items-center gap-2'>
																<Badge
																	variant={exec.success ? 'success' : 'error'}
																	className='text-[10px] px-1.5 py-0'
																>
																	{exec.executionMode?.toUpperCase() || 'COLD'}
																</Badge>
																<span className='text-sm text-white/90 light:text-black/90 font-mono'>
																	{exec.functionName || exec.functionId}
																</span>
															</div>
															<div className='flex items-center gap-3 text-xs text-white/50 light:text-black/50'>
																<span>{exec.runtime}</span>
																<span>{typeof exec.durationMs === 'bigint' ? safeBigIntToNumber(exec.durationMs) : exec.durationMs}ms</span>
																<Iconify.Icon icon="tabler:chevron-down" className='size-4' />
															</div>
														</div>
													</CollapsibleTrigger>
													<CollapsibleContent>
														<div className='px-3 pb-3 space-y-3 border-t border-white/10 light:border-black/10'>
															<div className='grid grid-cols-4 gap-2 pt-3 text-xs'>
																<div>
																	<div className='text-white/50 light:text-black/50'>Backend</div>
																	<div className='text-white/90 light:text-black/90'>{exec.backend || 'docker'}</div>
																</div>
																<div>
																	<div className='text-white/50 light:text-black/50'>Runtime</div>
																	<div className='text-white/90 light:text-black/90'>{exec.runtime}</div>
																</div>
																<div>
																	<div className='text-white/50 light:text-black/50'>Duration</div>
																	<div className='text-white/90 light:text-black/90 font-mono'>{typeof exec.durationMs === 'bigint' ? safeBigIntToNumber(exec.durationMs) : exec.durationMs}ms</div>
																</div>
																<div>
																	<div className='text-white/50 light:text-black/50'>Status</div>
																	<Badge variant={exec.success ? 'success' : 'error'} className='text-[10px]'>
																		{exec.success ? 'SUCCESS' : 'FAILED'}
																	</Badge>
																</div>
															</div>

															{exec.error && (
																<div>
																	<div className='text-xs text-red-400 light:text-red-600 mb-1'>Error ({exec.errorType || 'runtime'})</div>
																	<pre className='text-xs text-red-300 light:text-red-600 bg-red-500/10 p-2 rounded border border-red-500/20 overflow-x-auto max-h-24 scrollbar-macos'>
																		{exec.error}
																	</pre>
																</div>
															)}

															{exec.stdout && (
																<div>
																	<div className='text-xs text-white/50 light:text-black/50 mb-1'>Output (stdout)</div>
																	{(() => {
																		const parsedJson = tryParseJson(exec.stdout)
																		if (parsedJson) {
																			return (
																				<div className='bg-white/5 light:bg-black/5 p-2 rounded overflow-x-auto max-h-48 overflow-y-auto scrollbar-macos'>
																					<JsonViewer data={parsedJson} collapsed={false} showControls={true} />
																				</div>
																			)
																		}
																		return (
																			<pre className='text-xs text-white/80 light:text-black/80 bg-white/5 light:bg-black/5 p-2 rounded border border-white/10 light:border-black/10 overflow-x-auto max-h-32 scrollbar-macos whitespace-pre-wrap'>
																				{exec.stdout}
																			</pre>
																		)
																	})()}
																</div>
															)}

															{exec.stderr && (
																<div>
																	<div className='text-xs text-yellow-400 light:text-yellow-700 mb-1'>Stderr</div>
																	{(() => {
																		const parsedJson = tryParseJson(exec.stderr)
																		if (parsedJson) {
																			return (
																				<div className='bg-yellow-500/10 p-2 rounded overflow-x-auto max-h-48 overflow-y-auto scrollbar-macos'>
																					<JsonViewer data={parsedJson} collapsed={false} showControls={true} />
																				</div>
																			)
																		}
																		return (
																			<pre className='text-xs text-yellow-300 light:text-yellow-700 bg-yellow-500/10 p-2 rounded border border-yellow-500/20 overflow-x-auto max-h-32 scrollbar-macos whitespace-pre-wrap'>
																				{exec.stderr}
																			</pre>
																		)
																	})()}
																</div>
															)}
														</div>
													</CollapsibleContent>
												</div>
											</Collapsible>
										))}
									</div>
								</div>
							)}

							{/* Trace Information */}
							{(log.traceId || log.correlationId) && (
								<div className='space-y-2 border-b border-dotted border-white/10 light:border-black/10 pb-4'>
									<div className='flex items-center justify-between'>
										<h3 className='text-sm font-semibold text-white/80 light:text-black/80'>Trace Information</h3>
									</div>
									<div className='space-y-2 text-sm'>
										{log.traceId && (
											<div>
												<div className='text-white/50 light:text-black/50 text-xs'>Trace ID</div>
												<div className='text-white/90 light:text-black/90 font-mono text-xs break-all'>{log.traceId}</div>
											</div>
										)}
										{log.spanId && (
											<div>
												<div className='text-white/50 light:text-black/50 text-xs'>Span ID</div>
												<div className='text-white/90 light:text-black/90 font-mono text-xs break-all'>{log.spanId}</div>
											</div>
										)}
										{log.correlationId && (
											<div>
												<div className='text-white/50 light:text-black/50 text-xs'>Correlation ID</div>
												<div className='text-white/90 light:text-black/90 font-mono text-xs break-all'>{log.correlationId}</div>
											</div>
										)}
										{log.tenantId && (
											<div>
												<div className='text-white/50 light:text-black/50 text-xs'>Tenant ID</div>
												<div className='text-white/90 light:text-black/90 font-mono text-xs break-all'>{log.tenantId}</div>
											</div>
										)}
									</div>
								</div>
							)}

							{/* Raw Payload */}
							{log.payload && (
								<div className='space-y-2 border-b border-dotted border-white/10 light:border-black/10 -mt-4 py-2'>
									<Collapsible>
										<CollapsibleTrigger className='w-full'>
											<div className='flex items-center justify-between cursor-pointer'>
												<h3 className='text-sm font-semibold text-white/80 light:text-black/80'>Raw Payload</h3>
												<Iconify.Icon icon="tabler:chevron-down" className='size-4 text-white/50 light:text-black/50' />
											</div>
										</CollapsibleTrigger>
										<CollapsibleContent>
											<div className='mt-2 bg-white/5 light:bg-black/5 p-2 rounded border border-white/10 light:border-black/10 overflow-x-auto max-h-64 overflow-y-auto scrollbar-macos'>
												{(() => {
													const parsedJson = tryParseJson(log.payload)
													if (parsedJson) {
														return <JsonViewer data={parsedJson} collapsed={true} showControls={true} />
													}
													return (
														<pre className='text-xs text-white/80 light:text-black/80 whitespace-pre-wrap'>
															{log.payload}
														</pre>
													)
												})()}
											</div>
										</CollapsibleContent>
									</Collapsible>
								</div>
							)}

							{/* Request Input */}
							{log.requestText && (
								<div className='space-y-2'>
									<h3 className='text-sm font-semibold text-white/80 light:text-black/80'>
										{log.commandType === 'Embeddings' || log.commandType === 'ProcessEmbedding'
											? 'Input Text'
											: 'User Message'}
									</h3>
									<div className='text-sm text-white/90 light:text-black/90 bg-white/5 light:bg-black/5 p-3 rounded border border-white/10 light:border-black/10 whitespace-pre-wrap'>
										{log.requestText}
									</div>
								</div>
							)}

							{/* Response / Error */}
							{log.responseText && (
								<div className='space-y-2'>
									<h3 className='text-sm font-semibold text-white/80 light:text-black/80'>
										{log.status === 'error'
											? 'Error'
											: (log.commandType === 'Embeddings' || log.commandType === 'ProcessEmbedding'
												? 'Embedding Output'
												: 'Chat Response')}
									</h3>
									<div className={`text-sm prose prose-invert max-w-none w-full whitespace-pre-wrap p-3 max-h-[200px] overflow-y-auto scrollbar-macos rounded border ${log.status === 'error'
										? 'text-red-400 light:text-red-600 bg-red-500/10 border-red-500/20'
										: 'text-white/90 light:text-black/90 bg-white/5 light:bg-black/5 border-white/10 light:border-black/10'
										} light:border-black/10`}>
										{log.responseText}
									</div>
								</div>
							)}
						</div>
					)}
				</SheetBody>
			</SheetContent>
		</Sheet>
	)
}
