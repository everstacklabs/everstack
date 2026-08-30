import { useCallback, useState } from 'react'
import { Button } from '@everstack/ui/components'
import { Download } from 'lucide-react'
import { ui } from '@everstack/ui'

const {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} = ui

interface DatasetFileExportProps {
  items: any[]
  datasetName?: string
}

export function DatasetFileExport({
  items,
  datasetName,
}: DatasetFileExportProps) {
  const [exporting, setExporting] = useState(false)

  const download = useCallback(
    (format: 'csv' | 'json') => {
      if (!items || items.length === 0) return
      setExporting(true)

      try {
        const name = datasetName ?? 'dataset'
        let content: string
        let mime: string
        let ext: string

        if (format === 'json') {
          const data = items.map((item) => ({
            input: item.input,
            expectedOutput: item.expectedOutput ?? undefined,
          }))
          content = JSON.stringify(data, null, 2)
          mime = 'application/json'
          ext = 'json'
        } else {
          const rows = items.map((item) => {
            const input = JSON.stringify(item.input ?? '')
            const expected = item.expectedOutput
              ? JSON.stringify(item.expectedOutput)
              : ''
            return `${input},${expected}`
          })
          content = `input,expected_output\n${rows.join('\n')}`
          mime = 'text/csv'
          ext = 'csv'
        }

        const blob = new Blob([content], { type: mime })
        const url = URL.createObjectURL(blob)
        const a = document.createElement('a')
        a.href = url
        a.download = `${name}.${ext}`
        document.body.appendChild(a)
        a.click()
        document.body.removeChild(a)
        URL.revokeObjectURL(url)
      } finally {
        setExporting(false)
      }
    },
    [items, datasetName],
  )

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" disabled={!items?.length || exporting}>
          <Download className="w-3.5 h-3.5 mr-1.5" />
          Export
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent>
        <DropdownMenuItem onClick={() => download('csv')}>
          Export as CSV
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => download('json')}>
          Export as JSON
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
