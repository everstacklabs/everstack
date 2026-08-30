import { useState, useCallback, useRef } from 'react'
import { ObjectPurpose } from '@/server/storage'
import { useSession } from '@/hooks/auth'
import { getApiBaseUrl } from '@/lib/api-url'
import { ui } from '@everstack/ui'
import { Icon } from '@iconify/react'

const { Progress } = ui

const PURPOSE_MAP: Record<number, string> = {
    [ObjectPurpose.DATASET]: 'dataset',
    [ObjectPurpose.ARTIFACT]: 'artifact',
    [ObjectPurpose.UPLOAD]: 'upload',
    [ObjectPurpose.EVAL_RESULT]: 'eval_result',
}

export interface FileUploadProps {
    purpose: ObjectPurpose
    referenceId?: string
    referenceType?: string
    onComplete?: (objectId: string, fileName: string) => void
    onError?: (error: Error) => void
    accept?: string
    maxSizeMb?: number
    className?: string
}

export function FileUpload({
    purpose,
    referenceId,
    referenceType,
    onComplete,
    onError,
    accept,
    maxSizeMb = 50,
    className = '',
}: FileUploadProps) {
    const { data: session } = useSession()
    const orgId = session?.user?.organizations?.[0]?.id ?? ''

    const [isDragging, setIsDragging] = useState(false)
    const [isUploading, setIsUploading] = useState(false)
    const [progress, setProgress] = useState(0)
    const [error, setError] = useState<string | null>(null)
    const [uploadedFile, setUploadedFile] = useState<string | null>(null)
    const fileInputRef = useRef<HTMLInputElement>(null)

    const uploadFile = useCallback(async (file: File) => {
        if (!orgId) {
            setError('No organization context')
            return
        }

        if (file.size > maxSizeMb * 1024 * 1024) {
            setError(`File size exceeds ${maxSizeMb}MB limit`)
            return
        }

        setIsUploading(true)
        setProgress(0)
        setError(null)
        setUploadedFile(null)

        try {
            const form = new FormData()
            form.append('file', file)
            form.append('tenant_id', orgId)
            form.append('purpose', PURPOSE_MAP[purpose] ?? 'upload')
            if (referenceType) form.append('reference_type', referenceType)
            if (referenceId) form.append('reference_id', referenceId)

            const baseUrl = getApiBaseUrl()

            const data = await new Promise<{ objectId: string }>((resolve, reject) => {
                const xhr = new XMLHttpRequest()
                xhr.open('POST', `${baseUrl}/api/v1/storage/upload`, true)

                xhr.upload.onprogress = (event) => {
                    if (event.lengthComputable) {
                        const percent = Math.round((event.loaded / event.total) * 90)
                        setProgress(percent)
                    }
                }

                xhr.onload = () => {
                    if (xhr.status >= 200 && xhr.status < 300) {
                        try {
                            resolve(JSON.parse(xhr.responseText))
                        } catch {
                            reject(new Error('Invalid response from server'))
                        }
                    } else {
                        reject(new Error(`Upload failed: ${xhr.responseText || xhr.statusText}`))
                    }
                }

                xhr.onerror = () => reject(new Error('Upload failed'))
                xhr.send(form)
            })

            setProgress(100)
            setUploadedFile(file.name)
            onComplete?.(data.objectId, file.name)
        } catch (err) {
            const error = err instanceof Error ? err : new Error('Upload failed')
            setError(error.message)
            onError?.(error)
        } finally {
            setIsUploading(false)
        }
    }, [orgId, purpose, referenceId, referenceType, maxSizeMb, onComplete, onError])

    const handleDragOver = useCallback((e: React.DragEvent) => {
        e.preventDefault()
        e.stopPropagation()
        setIsDragging(true)
    }, [])

    const handleDragLeave = useCallback((e: React.DragEvent) => {
        e.preventDefault()
        e.stopPropagation()
        setIsDragging(false)
    }, [])

    const handleDrop = useCallback((e: React.DragEvent) => {
        e.preventDefault()
        e.stopPropagation()
        setIsDragging(false)

        const files = Array.from(e.dataTransfer.files)
        if (files.length > 0) {
            uploadFile(files[0])
        }
    }, [uploadFile])

    const handleFileSelect = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
        const files = e.target.files
        if (files && files.length > 0) {
            uploadFile(files[0])
        }
        // Reset input
        if (fileInputRef.current) {
            fileInputRef.current.value = ''
        }
    }, [uploadFile])

    return (
        <div className={className}>
            <div
                className={`
                    relative border-2 border-dashed rounded-lg p-6 text-center transition-colors cursor-pointer
                    ${isDragging
                        ? 'border-emerald-500 bg-emerald-500/10'
                        : 'border-zinc-700 light:border-zinc-300 hover:border-zinc-500 bg-zinc-900/50 light:bg-zinc-100/50'
                    }
                    ${isUploading ? 'pointer-events-none opacity-60' : ''}
                `}
                onDragOver={handleDragOver}
                onDragLeave={handleDragLeave}
                onDrop={handleDrop}
                onClick={() => fileInputRef.current?.click()}
            >
                <input
                    ref={fileInputRef}
                    type="file"
                    accept={accept}
                    onChange={handleFileSelect}
                    className="hidden"
                />

                {isUploading ? (
                    <div className="space-y-3">
                        <Icon icon="lucide:upload-cloud" className="h-8 w-8 mx-auto text-emerald-500 animate-pulse" />
                        <p className="text-sm text-zinc-400 light:text-zinc-600">Uploading...</p>
                        <Progress value={progress} className="h-2 max-w-[200px] mx-auto" />
                        <p className="text-xs text-zinc-500">{progress}%</p>
                    </div>
                ) : uploadedFile ? (
                    <div className="space-y-2">
                        <Icon icon="lucide:check-circle" className="h-8 w-8 mx-auto text-emerald-500" />
                        <p className="text-sm text-white light:text-brand-main-50">{uploadedFile}</p>
                        <p className="text-xs text-zinc-500">Upload complete. Drop another file to replace.</p>
                    </div>
                ) : (
                    <div className="space-y-2">
                        <Icon icon="lucide:upload-cloud" className="h-8 w-8 mx-auto text-zinc-500" />
                        <p className="text-sm text-zinc-400 light:text-zinc-600">
                            Drag and drop a file here, or click to browse
                        </p>
                        <p className="text-xs text-zinc-600">
                            Max {maxSizeMb}MB{accept ? ` (${accept})` : ''}
                        </p>
                    </div>
                )}
            </div>

            {error && (
                <div className="mt-2 rounded border border-red-500/20 bg-red-500/10 p-2 text-sm text-red-400 light:text-red-600">
                    {error}
                </div>
            )}
        </div>
    )
}
