import { useState } from 'react'
import {
    Dialog,
    DialogContent,
    DialogHeader,
    DialogTitle,
    DialogFooter,
    Button,
    Input,
    Label,
    Textarea,
    toast,
} from '@everstack/ui/components'
import { useAddDocuments } from '@/hooks/deployments/use-memory'

interface Props {
    collectionName: string
    open: boolean
    onOpenChange: (open: boolean) => void
}

export function AddDocumentsDialog({ collectionName, open, onOpenChange }: Props) {
    const [content, setContent] = useState('')
    const [source, setSource] = useState('')
    const [chunkSize, setChunkSize] = useState(512)

    const addMutation = useAddDocuments()

    const handleSubmit = () => {
        if (!content.trim()) {
            toast.error('Content is required')
            return
        }

        addMutation.mutate(
            {
                collectionName,
                content: content.trim(),
                source: source.trim() || undefined,
                chunkSize,
            },
            {
                onSuccess: (data) => {
                    toast.success(
                        `Added ${data.chunksCreated} chunk${data.chunksCreated !== 1 ? 's' : ''} to "${collectionName}"`
                    )
                    onOpenChange(false)
                    setContent('')
                    setSource('')
                    setChunkSize(512)
                },
                onError: (err) => toast.error(`Failed to add documents: ${err.message}`),
            }
        )
    }

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="bg-brand-main-800 border-brand-main-600 text-white light:text-brand-main-50 max-h-[80vh] p-4 flex flex-col">
                <DialogHeader>
                    <DialogTitle>Add Documents to "{collectionName}"</DialogTitle>
                </DialogHeader>

                <div className="space-y-4 py-2 overflow-y-auto flex-1">
                    <div className="space-y-1.5">
                        <Label className="text-sm text-brand-main-200">Content *</Label>
                        <Textarea
                            value={content}
                            onChange={(e) => setContent(e.target.value)}
                            className="bg-brand-main-700/50 focus-visible:ring-0 border-brand-main-500 focus-visible:ring-offset-0 focus:border-brand-secondary-500 text-white light:text-brand-main-50"
                            placeholder="Paste text content to store..."
                            rows={8}
                        />
                    </div>

                    <div className="space-y-1.5">
                        <Label className="text-sm text-brand-main-200">Source Label</Label>
                        <Input
                            value={source}
                            onChange={(e) => setSource(e.target.value)}
                            className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                            placeholder="e.g. docs/api-reference.md"
                        />
                    </div>

                    <div className="space-y-1.5">
                        <Label className="text-sm text-brand-main-200">Chunk Size (characters)</Label>
                        <Input
                            type="number"
                            value={chunkSize}
                            onChange={(e) => setChunkSize(Number(e.target.value))}
                            className="bg-brand-main-700/50 border-brand-main-500 text-white light:text-brand-main-50"
                            min={100}
                            max={4096}
                        />
                    </div>
                </div>

                <DialogFooter>
                    <Button variant="ghost" onClick={() => onOpenChange(false)}>
                        Cancel
                    </Button>
                    <Button
                        variant="default"
                        onClick={handleSubmit}
                        disabled={addMutation.isPending}
                    >
                        {addMutation.isPending ? 'Adding...' : 'Add Documents'}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    )
}
