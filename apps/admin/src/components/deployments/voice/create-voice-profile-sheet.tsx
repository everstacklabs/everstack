import { useState, useCallback } from 'react'
import { useCreateVoiceProfile } from '@/hooks/deployments/use-voice'
import { useStorageConfigs } from '@/hooks/settings/use-storage'
import { AudioRecorder } from './audio-recorder'
import {
    Button,
    Input,
    Label,
    Textarea,
    Sheet,
    SheetContent,
    SheetDescription,
    SheetFooter,
    SheetHeader,
    SheetTitle,
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@everstack/ui/components'

interface CreateVoiceProfileSheetProps {
    open: boolean
    onOpenChange: (open: boolean) => void
}

export function CreateVoiceProfileSheet({ open, onOpenChange }: CreateVoiceProfileSheetProps) {
    const createProfile = useCreateVoiceProfile()
    const { data: storageConfigs } = useStorageConfigs()
    const hasStorage = storageConfigs && storageConfigs.length > 0
    const [name, setName] = useState('')
    const [description, setDescription] = useState('')
    const [referenceText, setReferenceText] = useState('')
    const [provider, setProvider] = useState('qwen')
    const [model, setModel] = useState('qwen3-tts-vc-2026-01-22')
    const [audioBlob, setAudioBlob] = useState<Blob | null>(null)
    const [audioFile, setAudioFile] = useState<File | null>(null)

    const handleAudioRecorded = useCallback((blob: Blob) => {
        setAudioBlob(blob)
        setAudioFile(null)
    }, [])

    const handleFileChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
        const file = e.target.files?.[0]
        if (file) {
            setAudioFile(file)
            setAudioBlob(null)
        }
    }, [])

    const handleSubmit = async () => {
        let referenceAudio: Uint8Array | undefined

        if (audioBlob) {
            const buffer = await audioBlob.arrayBuffer()
            referenceAudio = new Uint8Array(buffer)
        } else if (audioFile) {
            const buffer = await audioFile.arrayBuffer()
            referenceAudio = new Uint8Array(buffer)
        }

        await createProfile.mutateAsync({
            name,
            description,
            referenceAudio,
            referenceText,
            provider,
            model,
        })

        // Reset form
        setName('')
        setDescription('')
        setReferenceText('')
        setAudioBlob(null)
        setAudioFile(null)
        onOpenChange(false)
    }

    return (
        <Sheet open={open} onOpenChange={onOpenChange}>
            <SheetContent className="w-[480px] sm:max-w-[480px]">
                <SheetHeader>
                    <SheetTitle>Create Voice Profile</SheetTitle>
                    <SheetDescription>
                        Create a cloned voice profile from reference audio for text-to-speech.
                    </SheetDescription>
                </SheetHeader>

                <div className="space-y-4 py-4">
                    {!hasStorage && (
                        <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-300 light:text-amber-700">
                            Object storage is not configured. Reference audio will not be persisted.
                        </div>
                    )}
                    <div className="space-y-2">
                        <Label htmlFor="name">Name</Label>
                        <Input
                            id="name"
                            placeholder="My Voice Profile"
                            value={name}
                            onChange={(e) => setName(e.target.value)}
                        />
                    </div>

                    <div className="space-y-2">
                        <Label htmlFor="description">Description</Label>
                        <Input
                            id="description"
                            placeholder="Optional description"
                            value={description}
                            onChange={(e) => setDescription(e.target.value)}
                        />
                    </div>

                    <div className="space-y-2">
                        <Label>Reference Audio</Label>
                        <div className="space-y-3">
                            <AudioRecorder onRecorded={handleAudioRecorded} />
                            <div className="relative">
                                <div className="absolute inset-0 flex items-center">
                                    <span className="w-full border-t" />
                                </div>
                                <div className="relative flex justify-center text-xs uppercase">
                                    <span className="bg-background px-2 text-muted-foreground">or upload</span>
                                </div>
                            </div>
                            <Input
                                type="file"
                                accept="audio/*"
                                onChange={handleFileChange}
                            />
                        </div>
                        {(audioBlob || audioFile) && (
                            <p className="text-xs text-muted-foreground">
                                Audio ready: {audioBlob ? 'Recorded' : audioFile?.name}
                            </p>
                        )}
                    </div>

                    <div className="space-y-2">
                        <Label htmlFor="referenceText">Reference Text (optional)</Label>
                        <Textarea
                            id="referenceText"
                            placeholder="Transcript of the reference audio..."
                            value={referenceText}
                            onChange={(e) => setReferenceText(e.target.value)}
                            rows={3}
                        />
                        <p className="text-xs text-muted-foreground">
                            Providing a transcript improves voice cloning quality.
                        </p>
                    </div>

                    <div className="grid grid-cols-2 gap-4">
                        <div className="space-y-2">
                            <Label>Provider</Label>
                            <Select value={provider} onValueChange={setProvider}>
                                <SelectTrigger>
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="qwen">Qwen</SelectItem>
                                    <SelectItem value="openai">OpenAI</SelectItem>
                                </SelectContent>
                            </Select>
                        </div>
                        <div className="space-y-2">
                            <Label>Model</Label>
                            <Select value={model} onValueChange={setModel}>
                                <SelectTrigger>
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="qwen3-tts-vc-2026-01-22">Qwen3 TTS VC</SelectItem>
                                    <SelectItem value="qwen3-tts-flash">Qwen3 TTS Flash</SelectItem>
                                    <SelectItem value="qwen3-tts-vd">Qwen3 TTS VD</SelectItem>
                                </SelectContent>
                            </Select>
                        </div>
                    </div>
                </div>

                <SheetFooter className="flex justify-end gap-2">
                    <Button variant="outline" onClick={() => onOpenChange(false)}>
                        Cancel
                    </Button>
                    <Button
                        onClick={handleSubmit}
                        disabled={!name || createProfile.isPending}
                    >
                        {createProfile.isPending ? 'Creating...' : 'Create Profile'}
                    </Button>
                </SheetFooter>
            </SheetContent>
        </Sheet>
    )
}
