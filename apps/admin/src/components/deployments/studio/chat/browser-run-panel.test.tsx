// @vitest-environment jsdom

import { render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { ExecutionEvent } from '@/stores/execution-store'
import { getPresignedDownloadURL } from '@/server/storage'
import { BrowserRunPanel } from './browser-run-panel'

vi.mock('@/server/storage', () => ({
  getPresignedDownloadURL: vi.fn(),
}))

const getSnapshotURL = vi.mocked(getPresignedDownloadURL)

describe('BrowserRunPanel', () => {
  beforeEach(() => {
    getSnapshotURL.mockReset()
    getSnapshotURL.mockResolvedValue({
      $typeName: 'everstack.storage.v1.GetPresignedDownloadURLResponse',
      downloadUrl: 'https://artifacts.example/browser-step-4.jpg',
      expiresInSeconds: 3600n,
    })
  })

  it('renders an ordered action ledger and resolves retained snapshots by tenant', async () => {
    const events: ExecutionEvent[] = [
      {
        type: 'agent.browser.started',
        nodeId: 'agent-1',
        nodeType: 'agent',
        nodeLabel: 'Research agent',
        timestamp: 1_000,
        data: { sequence: '1' },
      },
      {
        type: 'agent.browser.action',
        nodeId: 'agent-1',
        nodeType: 'agent',
        nodeLabel: 'Research agent',
        timestamp: 1_600,
        data: { sequence: '3', action: 'click', selector: '[2]' },
      },
      {
        type: 'agent.browser.snapshot',
        nodeId: 'agent-1',
        nodeType: 'agent',
        nodeLabel: 'Research agent',
        timestamp: 1_700,
        data: {
          sequence: '4',
          artifact_id: 'artifact-4',
          snapshot_status: 'stored',
          size_bytes: '2048',
          auto: 'true',
        },
      },
      {
        type: 'agent.browser.navigate',
        nodeId: 'agent-1',
        nodeType: 'agent',
        nodeLabel: 'Research agent',
        timestamp: 1_200,
        data: {
          sequence: '2',
          title: 'Everstack',
          url: 'https://everstack.ai',
        },
      },
    ]

    render(<BrowserRunPanel events={events} tenantId="tenant-1" />)

    expect(screen.getByText('Browser allocated')).toBeDefined()
    expect(screen.getByText('Everstack')).toBeDefined()
    expect(screen.getByText('Click')).toBeDefined()
    expect(screen.getByText('Automatic snapshot')).toBeDefined()

    await waitFor(() => {
      expect(getSnapshotURL).toHaveBeenCalledWith({
        tenantId: 'tenant-1',
        objectId: 'artifact-4',
      })
    })
    expect(screen.getByAltText('Browser snapshot 4').getAttribute('src')).toBe(
      'https://artifacts.example/browser-step-4.jpg',
    )
  })

  it('explains how to enable computer use when a run has no browser events', () => {
    render(<BrowserRunPanel events={[]} tenantId="tenant-1" />)

    expect(screen.getByText('No browser run in this execution')).toBeDefined()
    expect(getSnapshotURL).not.toHaveBeenCalled()
  })
})
