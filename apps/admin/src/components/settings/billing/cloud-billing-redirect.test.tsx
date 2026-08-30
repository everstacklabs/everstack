// @vitest-environment jsdom

import { render } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { CloudBillingRedirect } from './cloud-billing-redirect'

describe('CloudBillingRedirect', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('immediately sends managed instances to the Cloud billing overview', () => {
    const replace = vi.fn()
    vi.stubGlobal('location', { replace })
    const billingUrl =
      'https://app.everstack.ai/everstack/settings/billing?tab=overview'

    render(<CloudBillingRedirect billingUrl={billingUrl} />)

    expect(replace).toHaveBeenCalledOnce()
    expect(replace).toHaveBeenCalledWith(billingUrl)
  })
})
