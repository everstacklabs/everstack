// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useChangelog } from '@/hooks/vault/use-catalog'
import { CatalogUpdateBanner } from './catalog-update-banner'

vi.mock('@/hooks/vault/use-catalog', () => ({
  useChangelog: vi.fn(),
}))

vi.mock('./changelog-dialog', () => ({
  ChangelogModal: () => null,
}))

const changelogHook = vi.mocked(useChangelog)

describe('CatalogUpdateBanner', () => {
  afterEach(cleanup)

  beforeEach(() => {
    const values = new Map<string, string>()
    Object.defineProperty(window, 'localStorage', {
      configurable: true,
      value: {
        getItem: (key: string) => values.get(key) ?? null,
        setItem: (key: string, value: string) => values.set(key, value),
        removeItem: (key: string) => values.delete(key),
        clear: () => values.clear(),
      },
    })
    changelogHook.mockReturnValue({
      data: {
        $typeName: 'everstack.catalog.v1.Changelog',
        entries: [
          {
            $typeName: 'everstack.catalog.v1.ChangelogEntry',
            version: '2.4.0',
            date: '2026-07-27',
            description: 'Latest models',
            newModels: [
              'Anthropic · Claude Opus 5',
              'OpenAI · GPT-5.6',
            ],
            newProviders: [],
            updatedModels: [],
            deprecatedModels: [],
            pricingChanges: [],
          },
        ],
      },
    } as unknown as ReturnType<typeof useChangelog>)
  })

  it('shows the latest durable catalog release', () => {
    render(<CatalogUpdateBanner />)

    expect(screen.getByText('2 new models available')).toBeDefined()
    expect(screen.getByText('Across 2 providers')).toBeDefined()
  })

  it('dismisses only the current catalog version', () => {
    const { rerender } = render(<CatalogUpdateBanner />)

    fireEvent.click(
      screen.getByRole('button', {
        name: 'Dismiss model catalog 2.4.0 notification',
      }),
    )
    expect(screen.queryByText('2 new models available')).toBeNull()
    expect(
      window.localStorage.getItem('everstack.catalog.dismissed-version'),
    ).toBe('2.4.0')

    changelogHook.mockReturnValue({
      data: {
        $typeName: 'everstack.catalog.v1.Changelog',
        entries: [
          {
            $typeName: 'everstack.catalog.v1.ChangelogEntry',
            version: '2.5.0',
            date: '2026-08-01',
            description: 'Next models',
            newModels: ['xAI · Grok Next'],
            newProviders: [],
            updatedModels: [],
            deprecatedModels: [],
            pricingChanges: [],
          },
        ],
      },
    } as unknown as ReturnType<typeof useChangelog>)
    rerender(<CatalogUpdateBanner />)

    expect(screen.getByText('1 new model available')).toBeDefined()
  })
})
