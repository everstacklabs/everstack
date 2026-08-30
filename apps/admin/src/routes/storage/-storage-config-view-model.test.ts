import { describe, expect, it } from 'vitest'
import { storageConfigActionPolicy } from './-storage-config-view-model'

describe('storageConfigActionPolicy', () => {
    it('labels system-managed storage and suppresses customer mutations', () => {
        expect(storageConfigActionPolicy(true)).toEqual({
            label: 'Managed',
            canEdit: false,
            canDelete: false,
        })
    })

    it('keeps edit and delete actions for customer-managed storage', () => {
        expect(storageConfigActionPolicy(false)).toEqual({
            label: null,
            canEdit: true,
            canDelete: true,
        })
    })
})
