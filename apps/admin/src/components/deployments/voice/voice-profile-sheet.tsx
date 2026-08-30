import { useState, useCallback, useEffect } from 'react'
import { useCreateVoiceProfile, useUpdateVoiceProfile } from '@/hooks/deployments/use-voice'
import { useStorageConfigs } from '@/hooks/settings/use-storage'
import { AudioRecorder } from './audio-recorder'
import { ui } from '@everstack/ui'
import { Iconify } from '@everstack/ui/icons'
import { toast } from '@everstack/ui/components'
import type { VoiceCloneProfile } from '@/server/voice'

const {
    Sheet, SheetContent, SheetHeader, SheetTitle, SheetBody,
    Button, Input, Label, Textarea,
    Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} = ui

interface VoiceProfileSheetProps {
    open: boolean
    onOpenChange: (open: boolean) => void
    profile: VoiceCloneProfile | null
}

export function VoiceProfileSheet({ open, onOpenChange, profile }: VoiceProfileSheetProps) {
    const isEdit = profile !== null
    const createProfile = useCreateVoiceProfile()
    const updateProfile = useUpdateVoiceProfile()
    const { data: storageConfigs } = useStorageConfigs()
    const hasStorage = storageConfigs && storageConfigs.length > 0

    const [name, setName] = useState('')
    const [description, setDescription] = useState('')
    const [referenceText, setReferenceText] = useState('')
    const [provider, setProvider] = useState('qwen')
    const [model, setModel] = useState('qwen3-tts-vc-2026-01-22')
    const [audioBlob, setAudioBlob] = useState<Blob | null>(null)
    const [audioFile, setAudioFile] = useState<File | null>(null)

    useEffect(() => {
        if (open && profile) {
            setName(profile.name)
            setDescription(profile.description ?? '')
            setReferenceText(profile.referenceText ?? '')
            setProvider(profile.provider)
            setModel(profile.model)
            setAudioBlob(null)
            setAudioFile(null)
        } else if (open && !profile) {
            setName('')
            setDescription('')
            setReferenceText('')
            setProvider('qwen')
            setModel('qwen3-tts-vc-2026-01-22')
            setAudioBlob(null)
            setAudioFile(null)
        }
    }, [open, profile])

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

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault()
        try {
            if (isEdit) {
                await updateProfile.mutateAsync({
                    id: profile.id,
                    name,
                    description,
                    referenceText,
                })
                toast.success('Voice profile updated')
            } else {
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
                toast.success('Voice profile created')
            }
            onOpenChange(false)
        } catch {
            toast.error(isEdit ? 'Failed to update voice profile' : 'Failed to create voice profile')
        }
    }

    const isPending = isEdit ? updateProfile.isPending : createProfile.isPending

    return (
        <Sheet open={open} onOpenChange={onOpenChange}>
            <SheetContent className="bg-brand-main-900 border-l-brand-main-500 w-full sm:max-w-[500px]">
                <SheetHeader className="flex items-center space-x-2.5">
                    <SheetTitle className="text-white light:text-brand-main-50 text-base font-semibold flex items-center gap-2">
                        <Iconify.Icon icon="lucide:audio-waveform" className="h-5 w-5" />
                        <span>{isEdit ? `Edit ${name || 'Profile'}` : 'Create Voice Profile'}</span>
                    </SheetTitle>
                </SheetHeader>

                <SheetBody className="py-4">
                    <form onSubmit={handleSubmit} className="space-y-4">
                        {!isEdit && !hasStorage && (
                            <div className="rounded-md border border-amber-500/20 bg-amber-500/5 p-3">
                                <div className="flex items-start gap-2">
                                    <Iconify.Icon icon="lucide:hard-drive" className="h-4 w-4 text-amber-400 light:text-amber-700 mt-0.5 shrink-0" />
                                    <p className="text-xs text-white/50 light:text-black/50">
                                        Object storage is not configured. Reference audio will not be persisted.
                                    </p>
                                </div>
                            </div>
                        )}

                        <div className="space-y-2">
                            <Label className="text-white light:text-brand-main-50 font-medium">
                                Name <span className="text-red-400 light:text-red-600">*</span>
                            </Label>
                            <Input
                                placeholder="e.g., Customer Support Voice"
                                value={name}
                                onChange={(e) => setName(e.target.value)}
                                className="bg-brand-main-700 border-brand-main-500 text-white light:text-brand-main-50 h-8 text-sm"
                                required
                            />
                        </div>

                        <div className="space-y-2">
                            <Label className="text-white light:text-brand-main-50 font-medium">Description</Label>
                            <Input
                                placeholder="Optional description"
                                value={description}
                                onChange={(e) => setDescription(e.target.value)}
                                className="bg-brand-main-700 border-brand-main-500 text-white light:text-brand-main-50 h-8 text-sm"
                            />
                        </div>

                        {!isEdit && (
                            <div className="space-y-2">
                                <Label className="text-white light:text-brand-main-50 font-medium">Reference Audio</Label>
                                <div className="space-y-3">
                                    <AudioRecorder onRecorded={handleAudioRecorded} />
                                    <div className="relative">
                                        <div className="absolute inset-0 flex items-center">
                                            <span className="w-full border-t border-brand-main-600" />
                                        </div>
                                        <div className="relative flex justify-center text-xs uppercase">
                                            <span className="bg-brand-main-900 px-2 text-white/40 light:text-black/40">or upload</span>
                                        </div>
                                    </div>
                                    <Input
                                        type="file"
                                        accept="audio/*"
                                        onChange={handleFileChange}
                                        className="bg-brand-main-700 border-brand-main-500 text-white light:text-brand-main-50 h-8 text-sm"
                                    />
                                </div>
                                {audioFile && (
                                    <p className="text-xs text-white/40 light:text-black/40">
                                        File selected: {audioFile.name}
                                    </p>
                                )}
                            </div>
                        )}

                        <div className="space-y-2">
                            <Label className="text-white light:text-brand-main-50 font-medium">
                                Reference Text {!isEdit && <span className="text-white/40 light:text-black/40 font-normal">(optional)</span>}
                            </Label>
                            <Textarea
                                placeholder="Transcript of the reference audio..."
                                value={referenceText}
                                onChange={(e) => setReferenceText(e.target.value)}
                                rows={3}
                                className="bg-brand-main-700 border-brand-main-500 text-white light:text-brand-main-50 text-sm"
                            />
                            {!isEdit && (
                                <p className="text-xs text-white/40 light:text-black/40">
                                    Providing a transcript improves voice cloning quality.
                                </p>
                            )}
                        </div>

                        {!isEdit && (
                            <div className="grid grid-cols-2 gap-4">
                                <div className="space-y-2">
                                    <Label className="text-white light:text-brand-main-50 font-medium">Provider</Label>
                                    <Select value={provider} onValueChange={setProvider}>
                                        <SelectTrigger className="w-full bg-brand-main-900/60 border-brand-main-600 text-zinc-200 light:text-zinc-800">
                                            <SelectValue />
                                        </SelectTrigger>
                                        <SelectContent className="bg-brand-main-900 border-brand-main-600 text-zinc-200 light:text-zinc-800">
                                            <SelectItem value="qwen">Qwen</SelectItem>
                                            <SelectItem value="openai">OpenAI</SelectItem>
                                        </SelectContent>
                                    </Select>
                                </div>
                                <div className="space-y-2">
                                    <Label className="text-white light:text-brand-main-50 font-medium">Model</Label>
                                    <Select value={model} onValueChange={setModel}>
                                        <SelectTrigger className="w-full bg-brand-main-900/60 border-brand-main-600 text-zinc-200 light:text-zinc-800">
                                            <SelectValue />
                                        </SelectTrigger>
                                        <SelectContent className="bg-brand-main-900 border-brand-main-600 text-zinc-200 light:text-zinc-800">
                                            <SelectItem value="qwen3-tts-vc-2026-01-22">Qwen3 TTS VC</SelectItem>
                                            <SelectItem value="qwen3-tts-flash">Qwen3 TTS Flash</SelectItem>
                                            <SelectItem value="qwen3-tts-vd">Qwen3 TTS VD</SelectItem>
                                        </SelectContent>
                                    </Select>
                                </div>
                            </div>
                        )}

                        <Button
                            type="submit"
                            className="w-full"
                            disabled={!name || isPending}
                        >
                            {isPending
                                ? (isEdit ? 'Saving...' : 'Creating...')
                                : (isEdit ? 'Save Changes' : 'Create Profile')}
                        </Button>
                    </form>
                </SheetBody>
            </SheetContent>
        </Sheet>
    )
}
