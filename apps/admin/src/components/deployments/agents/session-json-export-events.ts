export interface AgentSessionJsonDownloadDetail {
  sessionId?: string
}

export const AGENT_SESSION_JSON_DOWNLOAD_EVENT =
  'everstack:agent-session:download-json'

export function requestAgentSessionJsonDownload(sessionId?: string) {
  if (typeof window === 'undefined') return

  window.dispatchEvent(
    new CustomEvent<AgentSessionJsonDownloadDetail>(
      AGENT_SESSION_JSON_DOWNLOAD_EVENT,
      { detail: { sessionId } },
    ),
  )
}

export function getAgentSessionJsonDownloadDetail(event: Event) {
  return (event as CustomEvent<AgentSessionJsonDownloadDetail>).detail ?? {}
}
