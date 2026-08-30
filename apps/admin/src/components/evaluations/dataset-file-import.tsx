import { useState, useCallback } from 'react'
import { ui } from '@everstack/ui'
import { Button, toast } from '@everstack/ui/components'
import { Upload, FileText, AlertCircle } from 'lucide-react'
import { useCreateDatasetItemBatch } from '@/hooks/evaluations/use-datasets'

const {
    Sheet,
    SheetContent,
    SheetHeader,
    SheetTitle,
    SheetDescription,
    SheetBody,
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
    Label,
} = ui

interface ParsedItem {
    input: Record<string, unknown>
    expectedOutput?: Record<string, unknown>
}

function parseCSV(text: string): ParsedItem[] {
    const lines = text.trim().split('\n')
    if (lines.length < 2) return []

    const headers = lines[0].split(',').map((h) => h.trim().toLowerCase())
    const inputIdx = headers.findIndex((h) => h === 'input')
    const expectedIdx = headers.findIndex(
        (h) => h === 'expected_output' || h === 'expectedoutput' || h === 'expected',
    )

    if (inputIdx === -1) return []

    const items: ParsedItem[] = []
    for (let i = 1; i < lines.length; i++) {
        const cols = lines[i].split(',').map((c) => c.trim())
        const inputVal = cols[inputIdx]
        if (!inputVal) continue

        let input: Record<string, unknown>
        try {
            input = JSON.parse(inputVal)
        } catch {
            input = { text: inputVal }
        }

        let expectedOutput: Record<string, unknown> | undefined
        if (expectedIdx !== -1 && cols[expectedIdx]) {
            try {
                expectedOutput = JSON.parse(cols[expectedIdx])
            } catch {
                expectedOutput = { text: cols[expectedIdx] }
            }
        }

        items.push({ input, expectedOutput })
    }
    return items
}

function parseJSON(text: string): ParsedItem[] {
    const data = JSON.parse(text)
    const arr = Array.isArray(data) ? data : data.items ?? data.data ?? []
    return arr
        .filter((item: any) => item.input)
        .map((item: any) => {
            const input =
                typeof item.input === 'string' ? { text: item.input } : item.input
            const raw = item.expectedOutput ?? item.expected_output ?? item.expected
            const expectedOutput = raw
                ? typeof raw === 'string'
                    ? { text: raw }
                    : raw
                : undefined
            return { input, expectedOutput }
        })
}

interface DatasetFileImportProps {
    datasetId: string
    open: boolean
    onOpenChange: (open: boolean) => void
}

export function DatasetFileImport({
    datasetId,
    open,
    onOpenChange,
}: DatasetFileImportProps) {
    const [parsed, setParsed] = useState<ParsedItem[]>([])
    const [fileName, setFileName] = useState<string | null>(null)
    const [parseError, setParseError] = useState<string | null>(null)
    const batchMutation = useCreateDatasetItemBatch()

    const handleFileChange = useCallback(
        (e: React.ChangeEvent<HTMLInputElement>) => {
            const file = e.target.files?.[0]
            if (!file) return
            setFileName(file.name)
            setParseError(null)

            const reader = new FileReader()
            reader.onload = () => {
                try {
                    const text = reader.result as string
                    const isJson =
                        file.name.endsWith('.json') || file.type === 'application/json'
                    const items = isJson ? parseJSON(text) : parseCSV(text)
                    if (items.length === 0) {
                        setParseError(
                            isJson
                                ? 'No valid items found. Expected an array of objects with an "input" field.'
                                : 'No valid rows found. CSV must have an "input" column header.',
                        )
                        setParsed([])
                        return
                    }
                    setParsed(items)
                } catch (err) {
                    setParseError(
                        `Failed to parse file: ${err instanceof Error ? err.message : String(err)}`,
                    )
                    setParsed([])
                }
            }
            reader.readAsText(file)
        },
        [],
    )

    const handleImport = async () => {
        if (parsed.length === 0) return
        try {
            await batchMutation.mutateAsync({
                datasetId,
                items: parsed.map((p) => ({
                    input: p.input as any,
                    expectedOutput: p.expectedOutput as any,
                })),
            })
            toast.success(`Imported ${parsed.length} items`)
            setParsed([])
            setFileName(null)
            onOpenChange(false)
        } catch {
            toast.error('Failed to import items')
        }
    }

    const handleClose = () => {
        setParsed([])
        setFileName(null)
        setParseError(null)
        onOpenChange(false)
    }

    return (
        <Sheet open={open} onOpenChange={(v) => !v && handleClose()}>
            <SheetContent side="right" className="min-w-[500px]">
                <SheetHeader>
                    <SheetTitle>Import Dataset Items</SheetTitle>
                    <SheetDescription className="text-white/60 text-xs light:text-black/60">
                        Upload a CSV or JSON file to import items into this dataset.
                    </SheetDescription>
                </SheetHeader>
                <SheetBody>
                    <div className="space-y-4 mt-4">
                        <div className="space-y-2">
                            <Label className="text-xs text-white/60 light:text-black/60">File</Label>
                            <label className="flex flex-col items-center justify-center gap-2 rounded-lg border-2 border-dashed border-brand-main-600 bg-brand-main-900/30 px-4 py-6 cursor-pointer hover:border-brand-secondary-500/50 transition-colors">
                                <Upload className="w-6 h-6 text-white/40 light:text-black/40" />
                                <span className="text-sm text-white/60 light:text-black/60">
                                    {fileName ?? 'Choose .csv or .json file'}
                                </span>
                                <input
                                    type="file"
                                    accept=".csv,.json"
                                    onChange={handleFileChange}
                                    className="hidden"
                                />
                            </label>
                        </div>

                        {parseError && (
                            <div className="flex items-start gap-2 rounded-lg border border-red-500/20 bg-red-500/10 p-3 text-sm text-red-400">
                                <AlertCircle className="w-4 h-4 mt-0.5 shrink-0" />
                                {parseError}
                            </div>
                        )}

                        {parsed.length > 0 && (
                            <>
                                <div className="flex items-center gap-2 text-sm text-white/60 light:text-black/60">
                                    <FileText className="w-4 h-4" />
                                    {parsed.length} item{parsed.length !== 1 ? 's' : ''} parsed
                                </div>

                                <div className="max-h-[300px] overflow-auto rounded-lg border border-brand-main-600">
                                    <Table>
                                        <TableHeader>
                                            <TableRow className="border-brand-main-700">
                                                <TableHead className="text-white/60 text-xs light:text-black/60">#</TableHead>
                                                <TableHead className="text-white/60 text-xs light:text-black/60">Input</TableHead>
                                                <TableHead className="text-white/60 text-xs light:text-black/60">Expected Output</TableHead>
                                            </TableRow>
                                        </TableHeader>
                                        <TableBody>
                                            {parsed.slice(0, 50).map((item, i) => (
                                                <TableRow key={i} className="border-brand-main-700">
                                                    <TableCell className="text-white/40 text-xs light:text-black/40">{i + 1}</TableCell>
                                                    <TableCell className="text-white font-mono text-xs max-w-[180px] truncate light:text-brand-main-50">
                                                        {JSON.stringify(item.input)}
                                                    </TableCell>
                                                    <TableCell className="text-white/60 font-mono text-xs max-w-[180px] truncate light:text-black/60">
                                                        {item.expectedOutput ? JSON.stringify(item.expectedOutput) : '-'}
                                                    </TableCell>
                                                </TableRow>
                                            ))}
                                        </TableBody>
                                    </Table>
                                    {parsed.length > 50 && (
                                        <div className="text-center py-2 text-xs text-white/40 light:text-black/40">
                                            ... and {parsed.length - 50} more items
                                        </div>
                                    )}
                                </div>

                                <div className="flex justify-end gap-3 border-t border-brand-main-700/60 pt-4">
                                    <Button variant="outline" onClick={handleClose}>
                                        Cancel
                                    </Button>
                                    <Button
                                        variant="default"
                                        onClick={handleImport}
                                        disabled={batchMutation.isPending}
                                    >
                                        {batchMutation.isPending
                                            ? 'Importing...'
                                            : `Import ${parsed.length} Items`}
                                    </Button>
                                </div>
                            </>
                        )}
                    </div>
                </SheetBody>
            </SheetContent>
        </Sheet>
    )
}
