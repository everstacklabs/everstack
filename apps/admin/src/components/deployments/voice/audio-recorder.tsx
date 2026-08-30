import { useState, useRef, useCallback, useEffect } from 'react'
import { Button } from '@everstack/ui/components'
import { Icon } from '@iconify/react'

const MAX_DURATION_SECONDS = 30
const SAMPLE_RATE = 48000

/** Encode raw PCM Float32 samples into a WAV Blob (16-bit mono). */
function encodeWav(samples: Float32Array, sampleRate: number): Blob {
    const numSamples = samples.length
    const buffer = new ArrayBuffer(44 + numSamples * 2)
    const view = new DataView(buffer)

    const writeStr = (offset: number, str: string) => {
        for (let i = 0; i < str.length; i++) view.setUint8(offset + i, str.charCodeAt(i))
    }

    writeStr(0, 'RIFF')
    view.setUint32(4, 36 + numSamples * 2, true)
    writeStr(8, 'WAVE')
    writeStr(12, 'fmt ')
    view.setUint32(16, 16, true)           // chunk size
    view.setUint16(20, 1, true)            // PCM
    view.setUint16(22, 1, true)            // mono
    view.setUint32(24, sampleRate, true)
    view.setUint32(28, sampleRate * 2, true) // byte rate
    view.setUint16(32, 2, true)            // block align
    view.setUint16(34, 16, true)           // bits per sample
    writeStr(36, 'data')
    view.setUint32(40, numSamples * 2, true)

    let offset = 44
    for (let i = 0; i < numSamples; i++) {
        const s = Math.max(-1, Math.min(1, samples[i]))
        view.setInt16(offset, s < 0 ? s * 0x8000 : s * 0x7fff, true)
        offset += 2
    }

    return new Blob([buffer], { type: 'audio/wav' })
}

interface AudioRecorderProps {
    onRecorded: (blob: Blob) => void
}

export function AudioRecorder({ onRecorded }: AudioRecorderProps) {
    const [recording, setRecording] = useState(false)
    const [duration, setDuration] = useState(0)
    const [recordedBlob, setRecordedBlob] = useState<Blob | null>(null)
    const [recordedUrl, setRecordedUrl] = useState<string | null>(null)
    const [recordedDuration, setRecordedDuration] = useState(0)
    const [playing, setPlaying] = useState(false)
    const [playbackTime, setPlaybackTime] = useState(0)

    const pcmChunksRef = useRef<Float32Array[]>([])
    const streamRef = useRef<MediaStream | null>(null)
    const audioCtxRef = useRef<AudioContext | null>(null)
    const processorRef = useRef<ScriptProcessorNode | null>(null)
    const timerRef = useRef<ReturnType<typeof setInterval> | null>(null)
    const canvasRef = useRef<HTMLCanvasElement>(null)
    const analyserRef = useRef<AnalyserNode | null>(null)
    const animFrameRef = useRef<number>(0)
    const playbackAudioRef = useRef<HTMLAudioElement | null>(null)

    useEffect(() => {
        return () => {
            if (recordedUrl) URL.revokeObjectURL(recordedUrl)
            if (playbackAudioRef.current) {
                playbackAudioRef.current.pause()
                playbackAudioRef.current = null
            }
        }
    }, [recordedUrl])

    // Auto-stop at max duration
    useEffect(() => {
        if (recording && duration >= MAX_DURATION_SECONDS) {
            stopRecording()
        }
    }, [recording, duration])

    const drawWaveform = useCallback(() => {
        const analyser = analyserRef.current
        const canvas = canvasRef.current
        if (!analyser || !canvas) return

        const ctx = canvas.getContext('2d')
        if (!ctx) return

        const bufferLength = analyser.frequencyBinCount
        const dataArray = new Uint8Array(bufferLength)

        const draw = () => {
            animFrameRef.current = requestAnimationFrame(draw)
            analyser.getByteTimeDomainData(dataArray)

            ctx.fillStyle = 'hsl(var(--muted))'
            ctx.fillRect(0, 0, canvas.width, canvas.height)

            ctx.lineWidth = 2
            ctx.strokeStyle = 'hsl(var(--primary))'
            ctx.beginPath()

            const sliceWidth = canvas.width / bufferLength
            let x = 0

            for (let i = 0; i < bufferLength; i++) {
                const v = dataArray[i] / 128.0
                const y = (v * canvas.height) / 2

                if (i === 0) {
                    ctx.moveTo(x, y)
                } else {
                    ctx.lineTo(x, y)
                }
                x += sliceWidth
            }

            ctx.lineTo(canvas.width, canvas.height / 2)
            ctx.stroke()
        }

        draw()
    }, [])

    const startRecording = useCallback(async () => {
        if (recordedUrl) URL.revokeObjectURL(recordedUrl)
        if (playbackAudioRef.current) {
            playbackAudioRef.current.pause()
            playbackAudioRef.current = null
        }
        setRecordedBlob(null)
        setRecordedUrl(null)
        setRecordedDuration(0)
        setPlaying(false)
        setPlaybackTime(0)

        try {
            const stream = await navigator.mediaDevices.getUserMedia({
                audio: { sampleRate: SAMPLE_RATE, channelCount: 1, echoCancellation: true },
            })
            streamRef.current = stream

            const audioCtx = new AudioContext({ sampleRate: SAMPLE_RATE })
            audioCtxRef.current = audioCtx
            const source = audioCtx.createMediaStreamSource(stream)

            // Analyser for waveform visualization
            const analyser = audioCtx.createAnalyser()
            analyser.fftSize = 2048
            source.connect(analyser)
            analyserRef.current = analyser

            // ScriptProcessor to capture raw PCM samples
            pcmChunksRef.current = []
            const processor = audioCtx.createScriptProcessor(4096, 1, 1)
            processorRef.current = processor
            processor.onaudioprocess = (e) => {
                const input = e.inputBuffer.getChannelData(0)
                pcmChunksRef.current.push(new Float32Array(input))
            }
            source.connect(processor)
            processor.connect(audioCtx.destination) // required for processor to fire

            setRecording(true)
            setDuration(0)

            timerRef.current = setInterval(() => {
                setDuration((d) => d + 1)
            }, 1000)

            drawWaveform()
        } catch (err) {
            console.error('Failed to start recording:', err)
        }
    }, [onRecorded, drawWaveform, recordedUrl])

    const stopRecording = useCallback(() => {
        // Stop processor and audio context
        if (processorRef.current) {
            processorRef.current.disconnect()
            processorRef.current = null
        }
        cancelAnimationFrame(animFrameRef.current)
        if (streamRef.current) {
            streamRef.current.getTracks().forEach((t) => t.stop())
            streamRef.current = null
        }

        const audioCtx = audioCtxRef.current
        const sampleRate = audioCtx?.sampleRate ?? SAMPLE_RATE
        if (audioCtx) {
            audioCtx.close()
            audioCtxRef.current = null
        }

        if (timerRef.current) {
            clearInterval(timerRef.current)
            timerRef.current = null
        }

        // Merge PCM chunks and encode to WAV
        const chunks = pcmChunksRef.current
        const totalLength = chunks.reduce((acc, c) => acc + c.length, 0)
        const merged = new Float32Array(totalLength)
        let offset = 0
        for (const chunk of chunks) {
            merged.set(chunk, offset)
            offset += chunk.length
        }

        const wavBlob = encodeWav(merged, sampleRate)
        const url = URL.createObjectURL(wavBlob)
        setRecordedBlob(wavBlob)
        setRecordedUrl(url)
        setRecordedDuration(duration)
        setRecording(false)
        onRecorded(wavBlob)
    }, [duration, onRecorded])

    const togglePlayback = useCallback(() => {
        if (!recordedUrl) return

        if (playing && playbackAudioRef.current) {
            playbackAudioRef.current.pause()
            setPlaying(false)
            return
        }

        if (playbackAudioRef.current) {
            playbackAudioRef.current.play()
            setPlaying(true)
            return
        }

        const audio = new Audio(recordedUrl)
        playbackAudioRef.current = audio

        audio.ontimeupdate = () => {
            setPlaybackTime(audio.currentTime)
        }
        audio.onended = () => {
            setPlaying(false)
            setPlaybackTime(0)
            playbackAudioRef.current = null
        }

        audio.play()
        setPlaying(true)
    }, [recordedUrl, playing])

    const handleSeek = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
        const time = parseFloat(e.target.value)
        setPlaybackTime(time)
        if (playbackAudioRef.current) {
            playbackAudioRef.current.currentTime = time
        }
    }, [])

    const clearRecording = useCallback(() => {
        if (playbackAudioRef.current) {
            playbackAudioRef.current.pause()
            playbackAudioRef.current = null
        }
        if (recordedUrl) URL.revokeObjectURL(recordedUrl)
        setRecordedBlob(null)
        setRecordedUrl(null)
        setRecordedDuration(0)
        setPlaying(false)
        setPlaybackTime(0)
    }, [recordedUrl])

    useEffect(() => {
        return () => {
            if (timerRef.current) clearInterval(timerRef.current)
            cancelAnimationFrame(animFrameRef.current)
        }
    }, [])

    const formatTime = (s: number) => {
        const min = Math.floor(s / 60)
        const sec = Math.floor(s % 60)
        return `${min}:${sec.toString().padStart(2, '0')}`
    }

    // Playback state
    if (recordedBlob && recordedUrl && !recording) {
        return (
            <div className="rounded-md border border-brand-main-600 bg-brand-main-800/30 p-3 space-y-2">
                <div className="flex items-center gap-3">
                    <button
                        type="button"
                        onClick={togglePlayback}
                        className="shrink-0 flex items-center justify-center h-8 w-8 rounded-full bg-brand-main-700 hover:bg-brand-main-600 transition-colors"
                    >
                        <Icon
                            icon={playing ? 'lucide:pause' : 'lucide:play'}
                            className="h-3.5 w-3.5 text-white light:text-brand-main-50"
                        />
                    </button>
                    <div className="flex-1 min-w-0 space-y-1">
                        <input
                            type="range"
                            min={0}
                            max={recordedDuration}
                            step={0.1}
                            value={playbackTime}
                            onChange={handleSeek}
                            className="w-full h-1.5 rounded-full appearance-none cursor-pointer bg-brand-main-700 accent-emerald-500 [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:h-3 [&::-webkit-slider-thumb]:w-3 [&::-webkit-slider-thumb]:rounded-full [&::-webkit-slider-thumb]:bg-emerald-500"
                        />
                        <div className="flex items-center justify-between">
                            <span className="text-[10px] text-white/40 light:text-black/40">
                                {formatTime(playbackTime)} / {formatTime(recordedDuration)}
                            </span>
                            <span className="text-[10px] text-emerald-400 light:text-emerald-600 flex items-center gap-1">
                                <Icon icon="lucide:check" className="h-3 w-3" />
                                Recorded
                            </span>
                        </div>
                    </div>
                    <div className="flex items-center gap-1 shrink-0">
                        <button
                            type="button"
                            onClick={startRecording}
                            className="p-1.5 rounded hover:bg-brand-main-600 transition-colors"
                            title="Re-record"
                        >
                            <Icon icon="lucide:rotate-ccw" className="h-3.5 w-3.5 text-white/50 light:text-black/50 hover:text-white light:hover:text-brand-main-50" />
                        </button>
                        <button
                            type="button"
                            onClick={clearRecording}
                            className="p-1.5 rounded hover:bg-red-500/20 transition-colors"
                            title="Remove recording"
                        >
                            <Icon icon="lucide:x" className="h-3.5 w-3.5 text-white/50 light:text-black/50 hover:text-red-400 light:hover:text-red-600" />
                        </button>
                    </div>
                </div>
            </div>
        )
    }

    // Recording state
    const remaining = MAX_DURATION_SECONDS - duration

    return (
        <div className="space-y-2">
            <div className="flex items-center gap-3">
                {recording ? (
                    <>
                        <Button type="button" variant="destructive" onClick={stopRecording}>
                            <Icon icon="lucide:square" className="mr-2 h-3 w-3" />
                            Stop ({formatTime(duration)})
                        </Button>
                        <span className="text-[10px] text-white/40 light:text-black/40">
                            {remaining}s remaining
                        </span>
                    </>
                ) : (
                    <Button type="button" variant="outline" onClick={startRecording}>
                        <Icon icon="lucide:mic" className="h-4 w-4" />
                        Record Audio
                    </Button>
                )}
            </div>
            {recording && (
                <>
                    <div className="h-1 w-full rounded-full bg-brand-main-700 overflow-hidden">
                        <div
                            className="h-full rounded-full bg-red-500 transition-all duration-1000"
                            style={{ width: `${(duration / MAX_DURATION_SECONDS) * 100}%` }}
                        />
                    </div>
                    <canvas
                        ref={canvasRef}
                        width={400}
                        height={60}
                        className="w-full rounded-md border"
                    />
                </>
            )}
        </div>
    )
}
