import { useState } from 'react'
import { ui, Copy, CheckCircle2, useCopyToClipboard } from '@everstack/ui'
import { ApiKeyType } from '@everstack/proto/everstack/api_key/v1/api_key_pb'
import { useApiKeys, useCreateApiKey } from '@/hooks/vault/use-api-keys'
import { toast } from '@everstack/ui/components'

const {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
  Button,
  Input,
  Label,
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} = ui

interface CreateApiKeyDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function CreateApiKeyDialog({
  open,
  onOpenChange,
}: CreateApiKeyDialogProps) {
  const [name, setName] = useState('')
  const [userId, setUserId] = useState('')
  const [keyType, setKeyType] = useState<ApiKeyType>(ApiKeyType.USER)
  const [createdKey, setCreatedKey] = useState<string | null>(null)
  const [copyToClipboard, clipboardState] = useCopyToClipboard()
  const createApiKeyMutation = useCreateApiKey()
  const listApiKeysQuery = useApiKeys()

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()

    if (!name.trim()) {
      return
    }

    try {
      const response = await createApiKeyMutation.mutateAsync({
        name: name.trim(),
        userId: userId.trim() || undefined,
        type: keyType,
      })

      // Show the generated key
      if (response.apiKey) {
        setCreatedKey(response.apiKey.hash)
      }
    } catch (error) {
      console.error('Failed to create API key:', error)
    }
  }

  const handleCopy = () => {
    if (!createdKey) {
      toast.error('No API key to copy')
      return
    }
    copyToClipboard(createdKey)

    // Show toast based on clipboard state (will update after promise resolves)
    setTimeout(() => {
      if (clipboardState === true) {
        toast.success('API key copied to clipboard!')
      } else if (clipboardState instanceof Error) {
        toast.error('Failed to copy API key')
      }
    }, 100)
  }

  const handleClose = () => {
    // Reset form
    setName('')
    setUserId('')
    setKeyType(ApiKeyType.USER)
    setCreatedKey(null)
    onOpenChange(false)
    listApiKeysQuery.refetch()
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="w-[600px] border-brand-main-600 bg-brand-main-900 text-white light:text-brand-main-50 gap-0 p-0 overflow-hidden">
        {!createdKey ? (
          <div className="bg-gradient-to-b from-brand-main-900 to-brand-main-950">
            <DialogHeader className="border-b border-brand-main-700/60 px-6 py-5">
              <DialogTitle className="text-white light:text-brand-main-50">
                Create New API Key
              </DialogTitle>
              <DialogDescription className="mt-3 rounded border border-brand-secondary-500/20 bg-brand-secondary-500/8 px-3 py-2 text-xs text-brand-secondary-200">
                Create a new API key for programmatic access to your gateway.
              </DialogDescription>
            </DialogHeader>

            <form onSubmit={handleSubmit} className="space-y-5 px-6 py-5">
              <div className="space-y-1.5">
                <Label htmlFor="name" className="text-brand-main-200">
                  Name *
                </Label>
                <Input
                  id="name"
                  placeholder="My API Key"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  required
                  className="border-brand-main-600 bg-brand-main-950/70 text-white light:text-brand-main-50 placeholder:text-white/25 light:placeholder:text-black/25"
                />
                <p className="text-xs text-white/40 light:text-black/40">
                  A descriptive name for this API key
                </p>
              </div>

              <div className="space-y-1.5">
                <Label htmlFor="userId" className="text-brand-main-200">
                  User ID (Optional)
                </Label>
                <Input
                  id="userId"
                  placeholder="user_123"
                  value={userId}
                  onChange={(e) => setUserId(e.target.value)}
                  className="border-brand-main-600 bg-brand-main-950/70 text-white light:text-brand-main-50 placeholder:text-white/25 light:placeholder:text-black/25"
                />
                <p className="text-xs text-white/40 light:text-black/40">
                  Associate this key with a specific user
                </p>
              </div>

              <div className="space-y-1.5">
                <Label htmlFor="type" className="text-brand-main-200">
                  Type
                </Label>
                <Select
                  value={
                    keyType != null
                      ? keyType.toString()
                      : ApiKeyType.USER.toString()
                  }
                  onValueChange={(value) =>
                    setKeyType(parseInt(value) as ApiKeyType)
                  }
                >
                  <SelectTrigger className="border-brand-main-600 bg-brand-main-950/70 text-white light:text-brand-main-50">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent className="bg-brand-main-900 border-brand-main-600 text-white light:text-brand-main-50">
                    <SelectItem value={ApiKeyType.USER.toString()}>
                      User Account
                    </SelectItem>
                    <SelectItem value={ApiKeyType.ORG.toString()}>
                      Service Account
                    </SelectItem>
                  </SelectContent>
                </Select>
                <p className="text-xs text-white/40 light:text-black/40">
                  User keys are for individual users, service keys are for
                  applications
                </p>
              </div>

              <DialogFooter className="border-t border-brand-main-700/60 pt-4">
                <Button
                  type="button"
                  variant="ghost"
                  onClick={handleClose}
                  disabled={createApiKeyMutation.isPending}
                  className="text-white/60 light:text-black/60 hover:text-white light:hover:text-brand-main-50 hover:bg-brand-main-700/40"
                >
                  Cancel
                </Button>
                <Button
                  type="submit"
                  disabled={createApiKeyMutation.isPending || !name.trim()}
                >
                  {createApiKeyMutation.isPending
                    ? 'Creating...'
                    : 'Create API Key'}
                </Button>
              </DialogFooter>
            </form>
          </div>
        ) : (
          <div className="bg-gradient-to-b from-brand-main-900 to-brand-main-950">
            <DialogHeader className="border-b border-brand-main-700/60 px-6 py-5">
              <DialogTitle className="flex items-center gap-2 text-white light:text-brand-main-50">
                <span className="flex h-8 w-8 items-center justify-center rounded border border-brand-secondary-500/25 bg-brand-secondary-500/10">
                  <CheckCircle2
                    className="text-brand-secondary-300"
                    size={16}
                  />
                </span>
                API Key Created
              </DialogTitle>
              <DialogDescription className="mt-3 rounded border border-amber-500/20 bg-amber-500/8 px-3 py-2 text-xs text-amber-200 light:text-amber-700">
                Save this key now — you won't be able to see it again.
              </DialogDescription>
            </DialogHeader>

            <div className="space-y-5 px-6 py-5">
              <div className="rounded border border-brand-main-700/70 bg-brand-main-950/70 p-4">
                <Label className="mb-2 block text-[11px] uppercase tracking-wider text-white/45 light:text-black/45">
                  Your API Key
                </Label>
                <div className="flex items-center gap-2">
                  <code className="flex-1 break-all rounded border border-brand-main-700/60 bg-brand-main-900 px-3 py-2 font-mono text-sm text-white light:text-brand-main-50">
                    {createdKey}
                  </code>
                  <Button
                    type="button"
                    variant="outline"
                    onClick={handleCopy}
                    className="flex-shrink-0 gap-2 border-brand-main-600 bg-brand-main-900 text-white/70 light:text-black/70 hover:text-white light:hover:text-brand-main-50 hover:bg-brand-main-800"
                  >
                    {clipboardState ? (
                      clipboardState instanceof Error ? (
                        <>
                          <Copy size={14} />
                          Failed
                        </>
                      ) : (
                        <>
                          <CheckCircle2
                            size={14}
                            className="text-brand-secondary-400"
                          />
                          Copied
                        </>
                      )
                    ) : (
                      <>
                        <Copy size={14} />
                        Copy
                      </>
                    )}
                  </Button>
                </div>
              </div>

              <DialogFooter className="border-t border-brand-main-700/60 pt-4">
                <Button onClick={handleClose}>Done</Button>
              </DialogFooter>
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
