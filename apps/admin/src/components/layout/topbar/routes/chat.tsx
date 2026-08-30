import { useSearch } from '@tanstack/react-router'
import { Button } from '@everstack/ui/components'
import { Download } from '@everstack/ui/icons'
import { type ActionGroup } from '@/components/layout/topbar/types'
import { requestAgentSessionJsonDownload } from '@/components/deployments/agents/session-json-export-events'

function ChatDownloadSessionJsonButton() {
  const search = useSearch({
    strict: false,
    structuralSharing: false,
  }) as Record<string, unknown>
  const sessionId =
    typeof search.session === 'string' ? search.session : undefined

  if (!sessionId) return null

  return (
    <Button
      variant="outline"
      title="Download session JSON"
      onClick={() => requestAgentSessionJsonDownload(sessionId)}
      className="gap-2 border-brand-main-600 text-white hover:bg-brand-main-800 hover:text-white light:text-brand-main-50 light:hover:text-brand-main-50"
    >
      <Download size={16} />
      Download JSON
    </Button>
  )
}

export const ChatActions: ActionGroup[] = [
  {
    title: 'Chat',
  },
  {
    actions: [
      {
        type: 'custom',
        key: 'download-session-json',
        label: 'Download JSON',
        component: ChatDownloadSessionJsonButton,
      },
    ],
  },
]
