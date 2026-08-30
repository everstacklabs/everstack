import { describe, expect, it } from 'vitest'
import {
  SANDBOX_MACHINE_PROFILES,
  sandboxMachineProfilesForTier,
} from './sandbox-pricing'

describe('managed sandbox machine profiles', () => {
  it('uses the five fixed priced sizes with a 20 GiB root disk', () => {
    expect(
      SANDBOX_MACHINE_PROFILES.map(({ id, cpu, memoryMb, diskMb }) => ({
        id,
        cpu,
        memoryMb,
        diskMb,
      })),
    ).toEqual([
      { id: 'nano', cpu: 0.5, memoryMb: 512, diskMb: 20480 },
      { id: 'small', cpu: 1, memoryMb: 1024, diskMb: 20480 },
      { id: 'medium', cpu: 2, memoryMb: 2048, diskMb: 20480 },
      { id: 'large', cpu: 4, memoryMb: 4096, diskMb: 20480 },
      { id: 'xlarge', cpu: 8, memoryMb: 8192, diskMb: 20480 },
    ])
  })

  it('limits sizes by plan without treating Free as free compute', () => {
    expect(
      sandboxMachineProfilesForTier('free').map((size) => size.id),
    ).toEqual(['nano'])
    expect(
      sandboxMachineProfilesForTier('basic').map((size) => size.id),
    ).toEqual(['nano', 'small'])
    expect(sandboxMachineProfilesForTier('pro').map((size) => size.id)).toEqual(
      ['nano', 'small', 'medium', 'large'],
    )
    expect(
      sandboxMachineProfilesForTier('enterprise').map((size) => size.id),
    ).toEqual(['nano', 'small', 'medium', 'large', 'xlarge'])
  })
})
