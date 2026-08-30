import { useState } from 'react'
import { ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import { useCreateCollection } from '@/hooks/deployments/use-memory'

const {
    Sheet,
    SheetContent,
    SheetHeader,
    SheetTitle,
    SheetBody,
    Button,
    Input,
    Label,
    Textarea,
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} = ui

interface Props {
    open: boolean
    onOpenChange: (open: boolean) => void
}

export function CreateCollectionDialog({ open, onOpenChange }: Props) {
    const [name, setName] = useState('')
    const [description, setDescription] = useState('')
    const [distanceMetric, setDistanceMetric] = useState('cosine')
    const [embeddingModel, setEmbeddingModel] = useState('')
    const [embeddingDimension, setEmbeddingDimension] = useState<number>(0)

    const createMutation = useCreateCollection()

    const handleSubmit = () => {
        if (!name.trim()) {
            toast.error('Collection name is required')
            return
        }

        createMutation.mutate(
            {
                name: name.trim(),
                description: description.trim() || undefined,
                distanceMetric,
                embeddingModel: embeddingModel.trim() || undefined,
                embeddingDimension: embeddingDimension > 0 ? embeddingDimension : undefined,
            },
            {
                onSuccess: () => {
                    toast.success(`Collection "${name}" created`)
                    onOpenChange(false)
                    setName('')
                    setDescription('')
                    setDistanceMetric('cosine')
                    setEmbeddingModel('')
                    setEmbeddingDimension(0)
                },
                onError: (err) => toast.error(`Failed to create collection: ${err.message}`),
            }
        )
    }

    return (
        <Sheet open={open} onOpenChange={onOpenChange}>
            <SheetContent
                side="right"
                className="flex h-[100vh] w-full flex-col overflow-hidden sm:max-w-[620px]"
            >
                <SheetHeader>
                    <SheetTitle>Create Memory Collection</SheetTitle>
                </SheetHeader>

                <SheetBody className="flex-1 overflow-y-auto py-4 scrollbar-macos">
                    <div className="space-y-4">
                        <div className="space-y-2">
                            <Label htmlFor="collection-name">Name *</Label>
                            <Input
                                id="collection-name"
                                value={name}
                                onChange={(e) => setName(e.target.value)}
                                className="bg-brand-main-800 border-brand-main-600"
                                placeholder="e.g. knowledge-base"
                            />
                        </div>

                        <div className="space-y-2">
                            <Label htmlFor="collection-description">Description</Label>
                            <Textarea
                                id="collection-description"
                                value={description}
                                onChange={(e) => setDescription(e.target.value)}
                                className="bg-brand-main-800 border-brand-main-600 min-h-[80px]"
                                placeholder="Optional description..."
                            />
                        </div>

                        <div className="space-y-2">
                            <Label htmlFor="distance-metric">Distance Metric</Label>
                            <Select value={distanceMetric} onValueChange={setDistanceMetric}>
                                <SelectTrigger className="bg-brand-main-800 border-brand-main-600 text-white light:text-brand-main-50">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent className="bg-brand-main-800 border-brand-main-600 text-white light:text-brand-main-50">
                                    <SelectItem value="cosine">Cosine</SelectItem>
                                    <SelectItem value="euclidean">Euclidean</SelectItem>
                                    <SelectItem value="dot_product">Dot Product</SelectItem>
                                </SelectContent>
                            </Select>
                        </div>

                        <div className="space-y-2">
                            <Label htmlFor="embedding-model">Embedding Model</Label>
                            <Input
                                id="embedding-model"
                                value={embeddingModel}
                                onChange={(e) => setEmbeddingModel(e.target.value)}
                                className="bg-brand-main-800 border-brand-main-600"
                                placeholder="Leave blank for system default"
                            />
                            <p className="text-xs text-white/40 light:text-black/40">
                                The model used to generate vector embeddings for this collection.
                            </p>
                        </div>

                        <div className="space-y-2">
                            <Label htmlFor="embedding-dimension">Embedding Dimension</Label>
                            <Input
                                id="embedding-dimension"
                                type="number"
                                value={embeddingDimension || ''}
                                onChange={(e) => setEmbeddingDimension(Number(e.target.value))}
                                className="bg-brand-main-800 border-brand-main-600"
                                placeholder="0 = system default"
                            />
                            <p className="text-xs text-white/40 light:text-black/40">
                                Vector dimension size. Leave at 0 to use the model's default.
                            </p>
                        </div>
                    </div>
                </SheetBody>

                {/* Footer */}
                <div className="flex gap-3 px-6 py-4 border-t border-brand-main-700/60 shrink-0 w-full">
                    <Button
                        type="button"
                        variant="outline"
                        className="w-1/2"
                        onClick={() => onOpenChange(false)}
                        disabled={createMutation.isPending}
                    >
                        Cancel
                    </Button>
                    <Button
                        type="button"
                        className="w-1/2"
                        onClick={handleSubmit}
                        disabled={createMutation.isPending}
                    >
                        {createMutation.isPending ? 'Creating...' : 'Create'}
                    </Button>
                </div>
            </SheetContent>
        </Sheet>
    )
}
