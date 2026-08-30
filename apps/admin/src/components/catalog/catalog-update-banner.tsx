import { useMemo, useState } from 'react'
import { ui } from '@everstack/ui'
import { Iconify } from '@everstack/ui/icons'
import { useChangelog } from '@/hooks/vault/use-catalog'
import { ChangelogModal } from './changelog-dialog'

const { Button } = ui
const dismissedVersionKey = 'everstack.catalog.dismissed-version'

function readDismissedVersion() {
  if (typeof window === 'undefined') return null
  try {
    return window.localStorage.getItem(dismissedVersionKey)
  } catch {
    return null
  }
}

export function CatalogUpdateBanner() {
  const { data: changelog } = useChangelog()
  const [showChangelog, setShowChangelog] = useState(false)
  const [dismissedVersion, setDismissedVersion] = useState<string | null>(
    readDismissedVersion,
  )
  const latest = changelog?.entries?.[0]

  const providerCount = useMemo(() => {
    const providers = new Set(
      (latest?.newModels ?? [])
        .map((model) => model.split(' · ')[0]?.trim())
        .filter(Boolean),
    )
    return providers.size
  }, [latest?.newModels])

  if (!latest || latest.version === dismissedVersion) {
    return null
  }

  const newModelCount = latest.newModels?.length ?? 0
  const newProviderCount = latest.newProviders?.length ?? 0
  const changeCount =
    newModelCount +
    newProviderCount +
    (latest.updatedModels?.length ?? 0) +
    (latest.deprecatedModels?.length ?? 0) +
    (latest.pricingChanges?.length ?? 0)

  const dismiss = () => {
    try {
      window.localStorage.setItem(dismissedVersionKey, latest.version)
    } catch {
      // The in-memory dismissal still works when storage is unavailable.
    }
    setDismissedVersion(latest.version)
  }

  return (
    <>
      <div className="flex items-center justify-between gap-3 border border-brand-main-600 bg-brand-main-800/50 px-3 py-2">
        <div className="flex min-w-0 items-center gap-2.5">
          <span className="flex size-6 shrink-0 items-center justify-center rounded-sm bg-brand-secondary-500/10 text-brand-secondary-300">
            <Iconify.Icon icon="material-symbols:update" className="size-4" />
          </span>
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <span className="text-xs font-medium text-white light:text-brand-main-50">
                {newModelCount > 0
                  ? `${newModelCount} new model${newModelCount === 1 ? '' : 's'} available`
                  : `Model catalog ${latest.version} is available`}
              </span>
              <span className="text-xs text-white/65 light:text-black/65">
                v{latest.version}
              </span>
            </div>
            <p className="mt-0.5 truncate text-xs text-white/70 light:text-black/70">
              {providerCount > 0
                ? `Across ${providerCount} provider${providerCount === 1 ? '' : 's'}`
                : `${changeCount} catalog change${changeCount === 1 ? '' : 's'}`}
              {newProviderCount > 0
                ? ` · ${newProviderCount} new provider${newProviderCount === 1 ? '' : 's'}`
                : ''}
            </p>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          <Button
            size="sm"
            variant="ghost"
            onClick={() => setShowChangelog(true)}
            className="h-7 rounded-xs px-2 text-xs text-white/75 light:text-black/75 hover:bg-brand-main-700 hover:text-white light:hover:text-brand-main-50"
          >
            <Iconify.Icon icon="material-symbols:history" className="size-4" />
            What&apos;s new
          </Button>
          <button
            type="button"
            onClick={dismiss}
            aria-label={`Dismiss model catalog ${latest.version} notification`}
            className="flex size-7 items-center justify-center rounded-xs text-white/65 transition-colors hover:bg-brand-main-700 hover:text-white light:text-black/65 light:hover:text-brand-main-50"
          >
            <Iconify.Icon icon="heroicons:x-mark" className="size-4" />
          </button>
        </div>
      </div>

      <ChangelogModal open={showChangelog} onOpenChange={setShowChangelog} />
    </>
  )
}
