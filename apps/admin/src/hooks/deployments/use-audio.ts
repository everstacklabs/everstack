import { useMutation } from '@tanstack/react-query'
import { getApiBaseUrl } from '@/lib/api-url'

const baseUrl = getApiBaseUrl()

/**
 * Hook for text-to-speech: sends text to the gateway Speech endpoint
 * and returns an audio Blob.
 */
export function useTextToSpeech() {
    return useMutation({
        mutationFn: async ({
            text,
            model = 'qwen3-tts-flash',
            voice = 'Cherry',
            voiceCloneProfileId,
            responseFormat = 'mp3',
        }: {
            text: string
            model?: string
            voice?: string
            voiceCloneProfileId?: string
            responseFormat?: string
        }): Promise<Blob> => {
            const payload: Record<string, unknown> = {
                model,
                input: text,
                voice,
                response_format: responseFormat,
            }
            if (voiceCloneProfileId) {
                payload.voice_clone_profile_id = voiceCloneProfileId
            }

            const res = await fetch(`${baseUrl}/v1/audio/speech`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(payload),
            })

            if (!res.ok) {
                const errText = await res.text()
                throw new Error(`TTS failed: ${errText}`)
            }

            return res.blob()
        },
    })
}

/**
 * Hook for speech-to-text: sends audio to the gateway Transcription endpoint
 * and returns the transcribed text.
 */
export function useSpeechToText() {
    return useMutation({
        mutationFn: async ({
            audio,
            model = 'whisper-1',
            language,
            filename = 'recording.webm',
        }: {
            audio: Blob
            model?: string
            language?: string
            filename?: string
        }): Promise<string> => {
            const formData = new FormData()
            formData.append('file', audio, filename)
            formData.append('model', model)
            if (language) {
                formData.append('language', language)
            }
            formData.append('response_format', 'json')

            const res = await fetch(`${baseUrl}/v1/audio/transcriptions`, {
                method: 'POST',
                body: formData,
            })

            if (!res.ok) {
                const errText = await res.text()
                throw new Error(`STT failed: ${errText}`)
            }

            const data = await res.json()
            return data.text ?? ''
        },
    })
}
