import { useState, useRef, useCallback } from 'react'
import { useSpeechToText } from '@/hooks/deployments/use-audio'
import { useVoiceProfiles } from '@/hooks/deployments/use-voice'
import {
    Button,
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
    Tooltip,
    TooltipProvider,
} from '@everstack/ui/components'
import { Icon } from '@iconify/react'

interface SessionAudioControlsProps {
    onTranscription: (text: string) => void
    ttsEnabled: boolean
    onToggleTTS: () => void
    selectedVoiceProfileId: string | null
    onSelectVoiceProfile: (id: string | null) => void
}

export function SessionAudioControls({
    onTranscription,
    ttsEnabled,
    onToggleTTS,
    selectedVoiceProfileId,
    onSelectVoiceProfile,
}: SessionAudioControlsProps) {
    const [recording, setRecording] = useState(false)
    const mediaRecorderRef = useRef<MediaRecorder | null>(null)
    const chunksRef = useRef<Blob[]>([])

    const stt = useSpeechToText()
    const { data: voiceProfiles } = useVoiceProfiles()

    const startRecording = useCallback(async () => {
        try {
            const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
            const mediaRecorder = new MediaRecorder(stream, { mimeType: 'audio/webm' })
            mediaRecorderRef.current = mediaRecorder
            chunksRef.current = []

            mediaRecorder.ondataavailable = (e) => {
                if (e.data.size > 0) chunksRef.current.push(e.data)
            }

            mediaRecorder.onstop = async () => {
                const blob = new Blob(chunksRef.current, { type: 'audio/webm' })
                stream.getTracks().forEach((track) => track.stop())

                try {
                    const text = await stt.mutateAsync({ audio: blob })
                    onTranscription(text)
                } catch (err) {
                    console.error('STT failed:', err)
                }
            }

            mediaRecorder.start(100)
            setRecording(true)
        } catch (err) {
            console.error('Failed to start recording:', err)
        }
    }, [stt, onTranscription])

    const stopRecording = useCallback(() => {
        if (mediaRecorderRef.current && mediaRecorderRef.current.state !== 'inactive') {
            mediaRecorderRef.current.stop()
        }
        setRecording(false)
    }, [])

    return (
        <TooltipProvider>
        <div className="flex items-center gap-1.5">
            {/* Mic button for STT */}
            <Tooltip content={recording ? 'Stop recording' : stt.isPending ? 'Transcribing...' : 'Voice input'}>
                <Button
                    variant={recording ? 'destructive' : 'ghost'}
                    size="icon"
                    className="h-8 w-8"
                    onClick={recording ? stopRecording : startRecording}
                    disabled={stt.isPending}
                >
                    {stt.isPending ? (
                        <Icon icon="lucide:loader-2" className="h-4 w-4 animate-spin" />
                    ) : recording ? (
                        <Icon icon="lucide:square" className="h-3 w-3" />
                    ) : (
                        <Icon icon="lucide:mic" className="h-4 w-4" />
                    )}
                </Button>
            </Tooltip>

            {/* Speaker toggle for TTS */}
            <Tooltip content={ttsEnabled ? 'Disable TTS' : 'Enable TTS'}>
                <Button
                    variant={ttsEnabled ? 'secondary' : 'ghost'}
                    size="icon"
                    className="h-8 w-8"
                    onClick={onToggleTTS}
                >
                    <Icon
                        icon={ttsEnabled ? 'lucide:volume-2' : 'lucide:volume-x'}
                        className="h-4 w-4"
                    />
                </Button>
            </Tooltip>

            {/* Voice profile selector (shown when TTS is enabled) */}
            {ttsEnabled && voiceProfiles && voiceProfiles.length > 0 && (
                <Select
                    value={selectedVoiceProfileId ?? 'default'}
                    onValueChange={(v) => onSelectVoiceProfile(v === 'default' ? null : v)}
                >
                    <SelectTrigger className="h-8 w-[140px] text-xs">
                        <SelectValue placeholder="Voice" />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="default">Default Voice</SelectItem>
                        {voiceProfiles.map((p) => (
                            <SelectItem key={p.id} value={p.id}>
                                {p.name}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
            )}
        </div>
        </TooltipProvider>
    )
}

/**
 * Play TTS audio for a given text.
 * Call this after an assistant message completes when TTS is enabled.
 */
export function playTTSAudio(audioBlob: Blob) {
    const url = URL.createObjectURL(audioBlob)
    const audio = new Audio(url)
    audio.onended = () => URL.revokeObjectURL(url)
    audio.play().catch(console.error)
}
