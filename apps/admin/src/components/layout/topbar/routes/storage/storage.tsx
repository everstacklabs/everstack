import { useState } from 'react'
import { type ActionGroup } from '@/components/layout/topbar/types'
import { useConfigureStorage } from '@/hooks/settings/use-storage'
import { StorageProvider } from '@/server/storage'
import { Button, toast } from '@everstack/ui/components'
import { ui } from '@everstack/ui'

const {
    Input,
    Label,
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
    Sheet,
    SheetContent,
    SheetHeader,
    SheetTitle,
    SheetBody,
} = ui

function AddStorageConfigButton() {
    const configureMutation = useConfigureStorage()
    const [open, setOpen] = useState(false)
    const [provider, setProvider] = useState<StorageProvider>(StorageProvider.S3)
    const [bucket, setBucket] = useState('')
    const [region, setRegion] = useState('')
    const [endpoint, setEndpoint] = useState('')
    const [accessKeyId, setAccessKeyId] = useState('')
    const [secretAccessKey, setSecretAccessKey] = useState('')
    const [pathPrefix, setPathPrefix] = useState('')

    const resetForm = () => {
        setProvider(StorageProvider.S3)
        setBucket('')
        setRegion('')
        setEndpoint('')
        setAccessKeyId('')
        setSecretAccessKey('')
        setPathPrefix('')
    }

    const handleCreate = async (e: React.FormEvent) => {
        e.preventDefault()
        try {
            await configureMutation.mutateAsync({
                provider,
                bucket,
                region: region || undefined,
                endpoint: endpoint || undefined,
                accessKeyId: accessKeyId || undefined,
                secretAccessKey: secretAccessKey || undefined,
                pathPrefix: pathPrefix || undefined,
            })
            setOpen(false)
            resetForm()
            toast.success('Storage configuration saved')
        } catch {
            toast.error('Failed to save storage configuration')
        }
    }

    return (
        <>
            <Button variant="default" onClick={() => setOpen(true)}>
                <div className="flex items-center gap-2">
                    Add Storage Config
                </div>
            </Button>

            <Sheet open={open} onOpenChange={setOpen}>
                <SheetContent side="right" className="min-w-[480px]">
                    <SheetHeader>
                        <SheetTitle>Configure Storage</SheetTitle>
                    </SheetHeader>

                    {configureMutation.error && (
                        <div className="rounded-lg border border-red-500/20 bg-red-500/10 p-3 text-sm text-red-400 light:text-red-600 mx-4">
                            {(configureMutation.error as Error).message}
                        </div>
                    )}

                    <SheetBody>
                        <form onSubmit={handleCreate} className="space-y-4 py-4">
                            <div className="space-y-2">
                                <Label className="text-brand-main-200">Provider</Label>
                                <Select value={String(provider)} onValueChange={(v) => setProvider(Number(v) as StorageProvider)}>
                                    <SelectTrigger className="border-brand-main-700 bg-brand-main-900/60 text-white light:text-brand-main-50">
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent className="bg-brand-main-900/60 border-brand-main-700">
                                        <SelectItem value={String(StorageProvider.S3)}>Amazon S3</SelectItem>
                                        <SelectItem value={String(StorageProvider.GCS)}>Google Cloud Storage</SelectItem>
                                        <SelectItem value={String(StorageProvider.R2)}>Cloudflare R2</SelectItem>
                                        <SelectItem value={String(StorageProvider.MINIO)}>MinIO</SelectItem>
                                    </SelectContent>
                                </Select>
                            </div>
                            <div className="space-y-2">
                                <Label className="text-brand-main-200">Bucket</Label>
                                <Input
                                    placeholder="my-bucket"
                                    value={bucket}
                                    onChange={(e) => setBucket(e.target.value)}
                                    required
                                    className="border-brand-main-700 bg-brand-main-900/60 text-white light:text-brand-main-50"
                                />
                            </div>
                            <div className="grid grid-cols-2 gap-4">
                                <div className="space-y-2">
                                    <Label className="text-brand-main-200">Region</Label>
                                    <Input
                                        placeholder="us-east-1"
                                        value={region}
                                        onChange={(e) => setRegion(e.target.value)}
                                        className="border-brand-main-700 bg-brand-main-900/60 text-white light:text-brand-main-50"
                                    />
                                </div>
                                <div className="space-y-2">
                                    <Label className="text-brand-main-200">Endpoint</Label>
                                    <Input
                                        placeholder="https://s3.amazonaws.com"
                                        value={endpoint}
                                        onChange={(e) => setEndpoint(e.target.value)}
                                        className="border-brand-main-700 bg-brand-main-900/60 text-white light:text-brand-main-50"
                                    />
                                </div>
                            </div>
                            <div className="grid grid-cols-2 gap-4">
                                <div className="space-y-2">
                                    <Label className="text-brand-main-200">Access Key ID</Label>
                                    <Input
                                        placeholder="AKIA..."
                                        value={accessKeyId}
                                        onChange={(e) => setAccessKeyId(e.target.value)}
                                        className="border-brand-main-700 bg-brand-main-900/60 text-white light:text-brand-main-50"
                                    />
                                </div>
                                <div className="space-y-2">
                                    <Label className="text-brand-main-200">Secret Access Key</Label>
                                    <Input
                                        type="password"
                                        placeholder="Secret..."
                                        value={secretAccessKey}
                                        onChange={(e) => setSecretAccessKey(e.target.value)}
                                        className="border-brand-main-700 bg-brand-main-900/60 text-white light:text-brand-main-50"
                                    />
                                </div>
                            </div>
                            <div className="space-y-2">
                                <Label className="text-brand-main-200">Path Prefix (optional)</Label>
                                <Input
                                    placeholder="data/"
                                    value={pathPrefix}
                                    onChange={(e) => setPathPrefix(e.target.value)}
                                    className="border-brand-main-700 bg-brand-main-900/60 text-white light:text-brand-main-50"
                                />
                            </div>
                            <div className="flex justify-end gap-3 mt-6 border-t border-brand-main-700/60 pt-4">
                                <Button type="button" variant="outline" onClick={() => setOpen(false)}>
                                    Cancel
                                </Button>
                                <Button type="submit" disabled={configureMutation.isPending}>
                                    {configureMutation.isPending ? 'Saving...' : 'Save Configuration'}
                                </Button>
                            </div>
                        </form>
                    </SheetBody>
                </SheetContent>
            </Sheet>
        </>
    )
}

export const StorageActions: ActionGroup[] = [
    {
        title: 'Storage',
        actions: [
            {
                type: 'custom',
                key: 'add-storage-config',
                requiredPermission: 'resource:create',
                label: 'Add Storage Config',
                component: AddStorageConfigButton,
            },
        ],
    },
]
