export type StorageConfigActionPolicy = {
    label: 'Managed' | null
    canEdit: boolean
    canDelete: boolean
}

export function storageConfigActionPolicy(systemManaged: boolean): StorageConfigActionPolicy {
    if (systemManaged) {
        return { label: 'Managed', canEdit: false, canDelete: false }
    }

    return { label: null, canEdit: true, canDelete: true }
}
