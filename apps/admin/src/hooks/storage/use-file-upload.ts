import { useState, useCallback } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ObjectPurpose, getPresignedDownloadURL } from '@/server/storage'
import { useSession } from '@/hooks/auth'
import { getApiBaseUrl } from '@/lib/api-url'

interface UploadResult {
  objectId: string
  url: string
}

interface UseFileUploadReturn {
  upload: (
    file: File,
    purpose: ObjectPurpose,
    referenceType?: string,
    referenceId?: string,
  ) => Promise<UploadResult>
  isUploading: boolean
  error: Error | null
}

export function getReadableUploadErrorMessage(error: unknown) {
  const message = error instanceof Error ? error.message : String(error)
  const normalized = message.toLowerCase()

  if (
    normalized.includes('no storage config found') ||
    normalized.includes('storage not configured') ||
    normalized.includes('configure storage first')
  ) {
    return 'Storage is not configured yet. Configure storage first in the Storage settings.'
  }

  if (message.startsWith('Upload failed: ')) {
    return message.slice('Upload failed: '.length).trim()
  }

  return message
}

const PURPOSE_MAP: Record<number, string> = {
  [ObjectPurpose.DATASET]: 'dataset',
  [ObjectPurpose.ARTIFACT]: 'artifact',
  [ObjectPurpose.UPLOAD]: 'upload',
  [ObjectPurpose.EVAL_RESULT]: 'eval_result',
}

export function useFileUpload(): UseFileUploadReturn {
  const { data: session } = useSession()
  const orgId = session?.user?.organizations?.[0]?.id ?? ''
  const queryClient = useQueryClient()
  const [isUploading, setIsUploading] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  const upload = useCallback(
    async (
      file: File,
      purpose: ObjectPurpose,
      referenceType?: string,
      referenceId?: string,
    ): Promise<UploadResult> => {
      setIsUploading(true)
      setError(null)
      try {
        if (!orgId) {
          throw new Error(
            'No organization ID available. Please ensure you are logged in.',
          )
        }

        const form = new FormData()
        form.append('file', file)
        form.append('tenant_id', orgId)
        form.append('purpose', PURPOSE_MAP[purpose] ?? 'upload')
        if (referenceType) form.append('reference_type', referenceType)
        if (referenceId) form.append('reference_id', referenceId)

        const baseUrl = getApiBaseUrl()
        const res = await fetch(`${baseUrl}/api/v1/storage/upload`, {
          method: 'POST',
          body: form,
        })

        if (!res.ok) {
          const text = await res.text().catch(() => res.statusText)
          throw new Error(`Upload failed: ${text}`)
        }

        const data = await res.json()

        queryClient.invalidateQueries({ queryKey: ['storage-objects'] })
        queryClient.invalidateQueries({ queryKey: ['storage-usage'] })

        // Fetch presigned download URL for the uploaded object
        let downloadUrl = ''
        try {
          const dlRes = await getPresignedDownloadURL({
            tenantId: orgId,
            objectId: data.objectId,
          })
          downloadUrl = dlRes.downloadUrl
        } catch {
          // Non-fatal: URL will be empty, file still uploaded successfully
        }

        return { objectId: data.objectId, url: downloadUrl }
      } catch (err) {
        const e = err instanceof Error ? err : new Error(String(err))
        setError(e)
        throw e
      } finally {
        setIsUploading(false)
      }
    },
    [orgId, queryClient],
  )

  return { upload, isUploading, error }
}
