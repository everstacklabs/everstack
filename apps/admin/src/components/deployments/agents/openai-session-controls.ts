export function openAIInputDisabled({
  environmentTerminal,
  ambiguousIntent,
  capabilitiesReady,
  pending,
}: {
  environmentTerminal: boolean
  ambiguousIntent: boolean
  capabilitiesReady: boolean
  pending: boolean
}) {
  return environmentTerminal || ambiguousIntent || !capabilitiesReady || pending
}

export function openAITurnActive(status: string) {
  return [
    'starting',
    'running',
    'cancelling',
    'queued',
    'in_progress',
    'requires_action',
  ].includes(status)
}
