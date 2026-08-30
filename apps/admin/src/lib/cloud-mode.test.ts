import { afterEach, describe, expect, it, vi } from 'vitest'
import { getCloudBillingUrl } from './cloud-mode'

describe('getCloudBillingUrl', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('opens the owning organization billing overview from a managed instance', () => {
    vi.stubGlobal('window', {
      location: { hostname: 'instance-abc123.dev.eu-gra-1.everstack.ai' },
      __env: {
        CLOUD_URL: 'https://app.everstack.ai/',
        ORGANIZATION_SLUG: 'everstack',
      },
    })

    expect(getCloudBillingUrl()).toBe(
      'https://app.everstack.ai/everstack/settings/billing?tab=overview',
    )
  })
})
