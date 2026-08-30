import { useState, useRef, useCallback, useMemo } from 'react'
import { useVoiceProfiles, useDeleteVoiceProfile } from '@/hooks/deployments/use-voice'
import { useTextToSpeech } from '@/hooks/deployments/use-audio'
import { useStorageConfigs } from '@/hooks/settings/use-storage'
import { getPresignedDownloadURL } from '@/server/storage'
import { useSession } from '@/hooks/auth'
import { FeatureKey } from '@/config/features'
import { FeatureGatedError } from '@/components/ee/feature-gated-error'
import { VoiceProfileSheet } from './voice-profile-sheet'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { Trash2, Pencil, ui } from '@everstack/ui'
import { Loader, toast } from '@everstack/ui/components'
import { formatTimestamp } from '@everstack/utils/functions/index'
import { useSearch } from '@tanstack/react-router'
import { Icon } from '@iconify/react'
import { Link } from '@tanstack/react-router'
import type { VoiceCloneProfile } from '@/server/voice'

const { Dialog, DialogContent, DialogTitle, DialogDescription, Button } = ui

const TEST_PHRASE = 'Hello, this is a test of my cloned voice profile.'

export function VoiceProfilesList() {
    const { data: profiles, isLoading, error } = useVoiceProfiles()
    const { data: storageConfigs, isLoading: storageLoading } = useStorageConfigs()
    const { data: session } = useSession()
    const orgId = session?.user?.organizations?.[0]?.id ?? ''
    const hasStorage = !storageLoading && storageConfigs && storageConfigs.length > 0
    const deleteProfile = useDeleteVoiceProfile()
    const tts = useTextToSpeech()

    const [sheetOpen, setSheetOpen] = useState(false)
    const [editProfile, setEditProfile] = useState<VoiceCloneProfile | null>(null)
    const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null)
    const [deleteConfirmName, setDeleteConfirmName] = useState<string>('')
    const [playingId, setPlayingId] = useState<string | null>(null)
    const [playingRefId, setPlayingRefId] = useState<string | null>(null)
    const audioRef = useRef<HTMLAudioElement | null>(null)
    const refAudioRef = useRef<HTMLAudioElement | null>(null)

    const search = useSearch({ strict: false })
    const sourceProfiles = profiles ?? []

    const filteredProfiles = useMemo(() => {
        let filtered = [...sourceProfiles]
        const searchTerm = (search as Record<string, unknown>)?.search as string | undefined
        if (searchTerm) {
            const term = searchTerm.toLowerCase()
            filtered = filtered.filter(p =>
                p.name.toLowerCase().includes(term) ||
                (p.description ?? '').toLowerCase().includes(term)
            )
        }
        return filtered
    }, [sourceProfiles, search])

    const handleTest = useCallback(async (profileId: string, profileModel?: string) => {
        if (audioRef.current) {
            audioRef.current.pause()
            audioRef.current = null
        }
        setPlayingId(profileId)
        try {
            const blob = await tts.mutateAsync({
                text: TEST_PHRASE,
                model: profileModel || 'qwen3-tts-vc-2026-01-22',
                voiceCloneProfileId: profileId,
            })
            const url = URL.createObjectURL(blob)
            const audio = new Audio(url)
            audioRef.current = audio
            audio.onended = () => {
                URL.revokeObjectURL(url)
                setPlayingId(null)
                audioRef.current = null
            }
            await audio.play()
        } catch {
            toast.error('Test TTS failed')
            setPlayingId(null)
        }
    }, [tts])

    const handleStopPlaying = useCallback(() => {
        if (audioRef.current) {
            audioRef.current.pause()
            audioRef.current = null
        }
        setPlayingId(null)
    }, [])

    const handlePlayReference = useCallback(async (profileId: string, objectId: string) => {
        if (refAudioRef.current) {
            refAudioRef.current.pause()
            refAudioRef.current = null
        }
        if (playingRefId === profileId) {
            setPlayingRefId(null)
            return
        }
        setPlayingRefId(profileId)
        try {
            const resp = await getPresignedDownloadURL({ tenantId: orgId, objectId })
            if (resp.downloadUrl) {
                const audio = new Audio(resp.downloadUrl)
                refAudioRef.current = audio
                audio.onended = () => {
                    setPlayingRefId(null)
                    refAudioRef.current = null
                }
                await audio.play()
            }
        } catch {
            toast.error('Reference audio playback failed')
            setPlayingRefId(null)
        }
    }, [orgId, playingRefId])

    const handleDelete = async (id: string) => {
        try {
            await deleteProfile.mutateAsync(id)
            setDeleteConfirmId(null)
            setDeleteConfirmName('')
            toast.success('Voice profile deleted')
        } catch {
            toast.error('Failed to delete voice profile')
        }
    }

    const handleEdit = useCallback((profile: VoiceCloneProfile) => {
        setEditProfile(profile)
        setSheetOpen(true)
    }, [])

    if (isLoading) {
        return (
            <div className="flex-1 flex items-center justify-center text-white/70 light:text-black/70">
                <Loader loaderText="Loading voice profiles..." />
            </div>
        )
    }

    if (error) {
        return (
            <FeatureGatedError
                error={error}
                featureKey={FeatureKey.VOICE}
                featureName="Voice"
                description="Voice cloning, text-to-speech, and speech-to-text capabilities for your agents and workflows."
            />
        )
    }

    const columns: ColumnConfig<VoiceCloneProfile>[] = [
        {
            id: 'name',
            header: 'Name',
            width: 240,
            minWidth: 140,
            render: (profile: VoiceCloneProfile) => (
                <div className="flex min-w-0 flex-col gap-0.5">
                    <span className="truncate font-medium text-brand-secondary-100 text-xs">
                        {profile.name}
                    </span>
                    {profile.description && (
                        <span className="truncate text-[10px] text-white/45 light:text-black/45">
                            {profile.description}
                        </span>
                    )}
                </div>
            ),
        },
        {
            id: 'provider',
            header: 'Provider',
            width: 100,
            minWidth: 80,
            render: (profile: VoiceCloneProfile) => (
                <span className="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium bg-emerald-500/15 text-emerald-300 light:text-emerald-600">
                    {profile.provider}
                </span>
            ),
        },
        {
            id: 'model',
            header: 'Model',
            width: 200,
            minWidth: 120,
            render: (profile: VoiceCloneProfile) => (
                <span className="text-xs text-brand-main-100 font-mono">{profile.model}</span>
            ),
        },
        {
            id: 'referenceText',
            header: 'Reference Text',
            width: 200,
            minWidth: 100,
            render: (profile: VoiceCloneProfile) => (
                <span className="truncate text-xs text-white/45 light:text-black/45">
                    {profile.referenceText || '\u2014'}
                </span>
            ),
        },
        {
            id: 'createdAt',
            header: 'Created',
            width: 160,
            minWidth: 140,
            render: (profile: VoiceCloneProfile) => (
                <span className="truncate text-xs text-brand-main-100">{formatTimestamp(profile.createdAt)}</span>
            ),
        },
        {
            id: 'audio',
            header: '',
            width: 70,
            minWidth: 70,
            maxWidth: 70,
            resizable: false,
            render: (profile: VoiceCloneProfile) => (
                <div className="flex items-center gap-1 justify-center" data-row-actions>
                    {profile.referenceAudioObjectId && (
                        <button
                            type="button"
                            className="p-1 rounded hover:bg-brand-main-500/30 transition-colors"
                            onClick={() => handlePlayReference(profile.id, profile.referenceAudioObjectId)}
                            title={playingRefId === profile.id ? 'Stop reference' : 'Play reference audio'}
                        >
                            {playingRefId === profile.id ? (
                                <Icon icon="lucide:square" className="h-3.5 w-3.5 text-amber-300 light:text-amber-700" />
                            ) : (
                                <Icon icon="lucide:headphones" className="h-3.5 w-3.5 text-brand-main-200" />
                            )}
                        </button>
                    )}
                    <button
                        type="button"
                        className="p-1 rounded hover:bg-brand-main-500/30 transition-colors"
                        onClick={() =>
                            playingId === profile.id
                                ? handleStopPlaying()
                                : handleTest(profile.id, profile.model)
                        }
                        disabled={tts.isPending && playingId !== profile.id}
                        title={playingId === profile.id ? 'Stop' : 'Test voice'}
                    >
                        {tts.isPending && playingId === profile.id ? (
                            <Icon icon="lucide:loader-2" className="h-3.5 w-3.5 animate-spin text-brand-main-200" />
                        ) : playingId === profile.id ? (
                            <Icon icon="lucide:square" className="h-3 w-3 text-emerald-300 light:text-emerald-600" />
                        ) : (
                            <Icon icon="lucide:play" className="h-3.5 w-3.5 text-brand-main-200" />
                        )}
                    </button>
                </div>
            ),
        },
        {
            id: 'actions',
            header: '',
            width: 70,
            minWidth: 70,
            maxWidth: 70,
            resizable: false,
            render: (profile: VoiceCloneProfile) => (
                <div className="flex items-center gap-2 justify-center" data-row-actions>
                    <button
                        type="button"
                        className="p-1 rounded hover:bg-blue-500/20 hover:text-blue-400 light:hover:text-blue-600 transition-colors"
                        onClick={() => handleEdit(profile)}
                        title="Edit profile"
                    >
                        <Pencil size={14} />
                    </button>
                    <button
                        type="button"
                        className="p-1 rounded hover:bg-red-500/20 hover:text-red-400 light:hover:text-red-600 transition-colors"
                        onClick={() => {
                            setDeleteConfirmId(profile.id)
                            setDeleteConfirmName(profile.name)
                        }}
                        title="Delete profile"
                    >
                        <Trash2 size={14} />
                    </button>
                </div>
            ),
        },
    ]

    return (
        <div className="flex-1 min-h-0 w-full h-full overflow-hidden flex flex-col">
            {/* Storage warning */}
            {!storageLoading && !hasStorage && (
                <div className="shrink-0 flex items-center gap-3 px-4 py-2.5 border-b border-amber-500/20 bg-amber-500/5">
                    <Icon icon="lucide:hard-drive" className="h-4 w-4 text-amber-500 shrink-0" />
                    <p className="text-xs text-amber-300/80 light:text-amber-700/80">
                        Object storage is required for voice profiles.{' '}
                        <Link to="/storage/overview" className="underline hover:text-amber-200 light:hover:text-amber-700">
                            Configure storage
                        </Link>
                    </p>
                </div>
            )}

            <ResponsiveTable
                columns={columns}
                data={filteredProfiles}
                enableResizing={true}
                minTableWidth="100%"
                emptyMessage={
                    sourceProfiles.length === 0
                        ? (
                            <div className="flex flex-col items-center justify-center py-12">
                                <div className="relative mb-6">
                                    <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                                    <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                                        <Icon icon="lucide:mic" className="size-8 text-brand-secondary-400" />
                                    </div>
                                </div>
                                <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No voice profiles yet</h3>
                                <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                                    {hasStorage
                                        ? 'Create your first voice profile to get started.'
                                        : 'Configure object storage to start creating voice profiles.'}
                                </p>
                            </div>
                        )
                        : 'No voice profiles match your search.'
                }
                rowKey={(profile) => profile.id}
            />

            <VoiceProfileSheet
                open={sheetOpen}
                onOpenChange={(open) => {
                    setSheetOpen(open)
                    if (!open) setEditProfile(null)
                }}
                profile={editProfile}
            />

            <Dialog open={deleteConfirmId !== null} onOpenChange={(open) => !open && setDeleteConfirmId(null)}>
                <DialogContent className="w-[500px]">
                    <DialogTitle>Delete Voice Profile</DialogTitle>
                    <DialogDescription className="text-brand-main-100">
                        Are you sure you want to delete <strong className="text-brand-main-100">{deleteConfirmName}</strong>? This action cannot be undone.
                    </DialogDescription>
                    <div className="flex justify-end gap-3 mt-4">
                        <Button variant="outline" onClick={() => setDeleteConfirmId(null)} disabled={deleteProfile.isPending}>
                            Cancel
                        </Button>
                        <Button
                            variant="destructive"
                            className="bg-destructive/60 text-brand-main-100 hover:bg-destructive/90"
                            onClick={() => deleteConfirmId && handleDelete(deleteConfirmId)}
                            disabled={deleteProfile.isPending}
                        >
                            {deleteProfile.isPending ? 'Deleting...' : 'Delete'}
                        </Button>
                    </div>
                </DialogContent>
            </Dialog>
        </div>
    )
}
