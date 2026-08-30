import { useState, useRef, useCallback } from 'react'
import { Icon } from '@iconify/react'
import { useExecutionStore, type ExecutionEvent } from '@/stores/execution-store'
import { useDownloadObject } from '@/hooks/settings/use-storage'
import { NODE_REGISTRY } from '../node-registry'
import type { StudioNodeType } from '../types'

interface NodeExecutionDetailsProps {
    staticEvents?: ExecutionEvent[]
}

const VALUE_STYLE_MAP: Record<string, { color: string; bg: string }> = {
    pass: { color: 'text-emerald-400 light:text-emerald-600', bg: 'bg-emerald-500/20' },
    block: { color: 'text-red-400 light:text-red-600', bg: 'bg-red-500/20' },
    hit: { color: 'text-emerald-400 light:text-emerald-600', bg: 'bg-emerald-500/20' },
    miss: { color: 'text-amber-400 light:text-amber-700', bg: 'bg-amber-500/20' },
    true: { color: 'text-emerald-400 light:text-emerald-600', bg: 'bg-emerald-500/20' },
    false: { color: 'text-red-400 light:text-red-600', bg: 'bg-red-500/20' },
}

function formatNumber(value: string): string {
    const num = parseInt(value, 10)
    if (isNaN(num)) return value
    return num.toLocaleString()
}

function isTokenField(key: string): boolean {
    return key.endsWith('_tokens') || key === 'total_tokens' || key === 'prompt_tokens' || key === 'completion_tokens'
}

function isPreviewField(key: string): boolean {
    return key.endsWith('_preview')
}

function isBooleanField(value: string): boolean {
    return value === 'true' || value === 'false'
}

function renderValue(key: string, value: string) {
    // Styled pills for pass/block, hit/miss, true/false
    const style = VALUE_STYLE_MAP[value]
    if (style) {
        return (
            <span className={`inline-flex items-center rounded-full px-1.5 py-0.5 text-[10px] font-medium ${style.bg} ${style.color}`}>
                {value}
            </span>
        )
    }

    // Boolean badges
    if (isBooleanField(value)) {
        const isTrue = value === 'true'
        return (
            <span className={`inline-flex items-center rounded-full px-1.5 py-0.5 text-[10px] font-medium ${isTrue ? 'bg-emerald-500/20 text-emerald-400 light:text-emerald-600' : 'bg-red-500/20 text-red-400 light:text-red-600'}`}>
                {value}
            </span>
        )
    }

    // Token counts with comma formatting
    if (isTokenField(key)) {
        return <span className="text-brand-main-200 font-mono text-[10px]">{formatNumber(value)}</span>
    }

    // Preview fields in monospace
    if (isPreviewField(key)) {
        return (
            <span className="text-brand-main-300 font-mono text-[10px] break-all line-clamp-3">
                {value}
            </span>
        )
    }

    return <span className="text-brand-main-200 text-[11px]">{value}</span>
}

function formatFieldLabel(key: string): string {
    return key
        .replace(/_/g, ' ')
        .replace(/\b\w/g, (c) => c.toUpperCase())
}

function AudioPlayback({ base64Audio, contentType }: { base64Audio: string; contentType?: string }) {
    const [playing, setPlaying] = useState(false)
    const [currentTime, setCurrentTime] = useState(0)
    const [duration, setDuration] = useState(0)
    const audioRef = useRef<HTMLAudioElement | null>(null)

    const ensureAudio = useCallback(() => {
        if (audioRef.current) return audioRef.current
        const mime = contentType || 'audio/wav'
        const blob = new Blob(
            [Uint8Array.from(atob(base64Audio), c => c.charCodeAt(0))],
            { type: mime }
        )
        const url = URL.createObjectURL(blob)
        const el = new Audio(url)
        audioRef.current = el
        el.onloadedmetadata = () => {
            if (Number.isFinite(el.duration)) setDuration(el.duration)
        }
        el.ontimeupdate = () => setCurrentTime(el.currentTime)
        el.onended = () => { setPlaying(false); setCurrentTime(0) }
        return el
    }, [base64Audio, contentType])

    const toggle = useCallback(() => {
        const el = ensureAudio()
        if (playing) {
            el.pause()
            setPlaying(false)
        } else {
            el.play().then(() => setPlaying(true))
        }
    }, [ensureAudio, playing])

    const handleSeek = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
        const time = parseFloat(e.target.value)
        setCurrentTime(time)
        if (audioRef.current) audioRef.current.currentTime = time
    }, [])

    const handleDownload = useCallback(() => {
        const mime = contentType || 'audio/wav'
        const ext = mime.includes('wav') ? 'wav' : mime.includes('mpeg') ? 'mp3' : 'audio'
        const blob = new Blob(
            [Uint8Array.from(atob(base64Audio), c => c.charCodeAt(0))],
            { type: mime }
        )
        const url = URL.createObjectURL(blob)
        const a = document.createElement('a')
        a.href = url
        a.download = `audio-output.${ext}`
        a.click()
        URL.revokeObjectURL(url)
    }, [base64Audio, contentType])

    const fmt = (s: number) => {
        const m = Math.floor(s / 60)
        const sec = Math.floor(s % 60)
        return `${m}:${sec.toString().padStart(2, '0')}`
    }

    return (
        <div className="flex items-center gap-2 w-full">
            <button
                type="button"
                onClick={toggle}
                className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-brand-main-700 hover:bg-brand-main-600 transition-colors"
            >
                <Icon icon={playing ? 'lucide:pause' : 'lucide:play'} className="h-3 w-3 text-white light:text-brand-main-50" />
            </button>
            <div className="flex-1 min-w-0 space-y-0.5">
                <input
                    type="range"
                    min={0}
                    max={duration || 1}
                    step={0.1}
                    value={currentTime}
                    onChange={handleSeek}
                    className="w-full h-1 rounded-full appearance-none cursor-pointer bg-brand-main-700 accent-emerald-500 [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:h-2.5 [&::-webkit-slider-thumb]:w-2.5 [&::-webkit-slider-thumb]:rounded-full [&::-webkit-slider-thumb]:bg-emerald-500"
                />
                <span className="text-[9px] text-white/40 light:text-black/40">
                    {fmt(currentTime)}{duration > 0 ? ` / ${fmt(duration)}` : ''}
                </span>
            </div>
            <button
                type="button"
                onClick={handleDownload}
                className="flex h-6 w-6 shrink-0 items-center justify-center rounded hover:bg-brand-main-700 transition-colors"
                title="Download"
            >
                <Icon icon="lucide:download" className="h-3 w-3 text-white/50 light:text-black/50 hover:text-white light:hover:text-brand-main-50" />
            </button>
        </div>
    )
}

function AudioDownloadButton({ objectId }: { objectId: string }) {
    const downloadObject = useDownloadObject()
    const [downloading, setDownloading] = useState(false)

    const handleDownload = useCallback(async () => {
        setDownloading(true)
        try {
            await downloadObject(objectId)
        } catch {
            // ignore
        }
        setDownloading(false)
    }, [objectId, downloadObject])

    return (
        <button
            onClick={handleDownload}
            disabled={downloading}
            className="flex items-center gap-1.5 rounded-md bg-brand-main-700 px-2.5 py-1.5 text-[11px] text-brand-main-200 hover:bg-brand-main-600 transition-colors disabled:opacity-50"
        >
            {downloading ? (
                <Icon icon="lucide:loader-2" className="h-3.5 w-3.5 animate-spin" />
            ) : (
                <Icon icon="lucide:download" className="h-3.5 w-3.5" />
            )}
            Download
        </button>
    )
}

export function NodeExecutionDetails({ staticEvents }: NodeExecutionDetailsProps) {
    const liveEvents = useExecutionStore((s) => s.events)
    const events = staticEvents ?? liveEvents

    const completedNodes = events.filter(
        (e) => e.type === 'node.completed' || e.type === 'node.error'
    )

    if (completedNodes.length === 0) {
        return (
            <div className="flex flex-col items-center justify-center py-12">
                <div className="relative mb-5">
                    <div className="absolute inset-0 bg-brand-secondary-500/15 rounded-full blur-lg" />
                    <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-3">
                        <Icon icon="lucide:layers" className="size-6 text-brand-secondary-400" />
                    </div>
                </div>
                <h3 className="text-sm font-medium text-white light:text-brand-main-50 mb-1">No node execution data yet</h3>
                <p className="text-xs text-white/40 light:text-black/40">Execute a workflow to see per-node details</p>
            </div>
        )
    }

    return (
        <div className="flex flex-col gap-2 p-3">
            {completedNodes.map((event, idx) => {
                const meta = NODE_REGISTRY[event.nodeType as StudioNodeType]
                const isError = event.type === 'node.error'
                const data = event.data ?? {}
                const audioBase64 = data['audio'] ?? data['tts_audio'] ?? data['voice_clone_audio']
                const audioContentType = data['content_type'] || 'audio/mpeg'
                const audioObjectId = data['audio_object_id']
                const dataEntries = Object.entries(data).filter(
                    ([k]) => k !== 'execution_id' && k !== 'correlation_id' && k !== 'audio' && k !== 'audio_object_id'
                )

                return (
                    <div
                        key={`${event.nodeId}-${idx}`}
                        className="rounded-lg border border-brand-main-700 bg-brand-main-800/50 overflow-hidden"
                    >
                        {/* Card header */}
                        <div className="flex items-center justify-between px-3 py-2 border-b border-brand-main-700/50">
                            <div className="flex items-center gap-2">
                                <div
                                    className="flex h-5 w-5 items-center justify-center rounded"
                                    style={{
                                        backgroundColor: meta ? `${meta.color}20` : '#374151',
                                        color: meta?.color ?? '#9CA3AF',
                                    }}
                                >
                                    <Icon
                                        icon={meta?.icon ?? 'lucide:box'}
                                        className="h-3 w-3"
                                    />
                                </div>
                                <span className="text-xs font-medium text-white light:text-brand-main-50">
                                    {event.nodeLabel || event.nodeType}
                                </span>
                            </div>
                            <div className="flex items-center gap-2">
                                {event.durationMs != null && (
                                    <span className="rounded bg-brand-main-700 px-1.5 py-0.5 text-[10px] text-brand-main-300 font-mono">
                                        {event.durationMs}ms
                                    </span>
                                )}
                                {isError ? (
                                    <Icon icon="lucide:x-circle" className="h-3.5 w-3.5 text-red-400 light:text-red-600" />
                                ) : (
                                    <Icon icon="lucide:check-circle" className="h-3.5 w-3.5 text-emerald-400 light:text-emerald-600" />
                                )}
                            </div>
                        </div>

                        {/* Error message */}
                        {isError && event.error && (
                            <div className="px-3 py-1.5 bg-red-500/10 border-b border-brand-main-700/50">
                                <p className="text-[10px] text-red-400 light:text-red-600 break-all">{event.error}</p>
                            </div>
                        )}

                        {/* Audio playback for TTS/VoiceClone nodes */}
                        {!isError && (audioBase64 || audioObjectId) && (
                            <div className="flex items-center gap-2 px-3 py-2 border-b border-brand-main-700/30">
                                {audioBase64 && (
                                    <AudioPlayback base64Audio={audioBase64} contentType={audioContentType} />
                                )}
                                {audioObjectId && (
                                    <AudioDownloadButton objectId={audioObjectId} />
                                )}
                            </div>
                        )}

                        {/* Data fields */}
                        {dataEntries.length > 0 && (
                            <div className="divide-y divide-brand-main-700/30">
                                {dataEntries.map(([key, value]) => (
                                    <div
                                        key={key}
                                        className="flex items-start justify-between gap-3 px-3 py-1.5"
                                    >
                                        <span className="text-[10px] text-brand-main-400 whitespace-nowrap min-w-0 shrink-0">
                                            {formatFieldLabel(key)}
                                        </span>
                                        <div className="text-right min-w-0">
                                            {renderValue(key, value)}
                                        </div>
                                    </div>
                                ))}
                            </div>
                        )}
                    </div>
                )
            })}
        </div>
    )
}
