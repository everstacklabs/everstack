import { describe, expect, it } from 'vitest'
import { canResolveOpenAIApproval } from './openai-pending-approvals'

describe('OpenAI approval controls', () => {
  it('requires a fresh successful capability and approvals result', () => {
    const ready = {
      allowed: true,
      capabilitiesFetching: false,
      capabilitiesError: false,
      approvalsFetching: false,
      approvalsError: false,
    }
    expect(canResolveOpenAIApproval(ready)).toBe(true)
    expect(canResolveOpenAIApproval({ ...ready, approvalsError: true })).toBe(
      false,
    )
    expect(
      canResolveOpenAIApproval({ ...ready, approvalsFetching: true }),
    ).toBe(false)
    expect(canResolveOpenAIApproval({ ...ready, allowed: false })).toBe(false)
  })
})
