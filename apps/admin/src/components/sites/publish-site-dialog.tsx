import { useState, type ReactNode } from 'react'
import { ui } from '@everstack/ui'
import { Button, toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { copyToClipboard } from '@everstack/utils/functions/clipboard'

const { Dialog, DialogContent, DialogDescription, DialogTitle } = ui

type PublishSiteDialogProps = {
  variant?: 'default' | 'outline'
  size?: 'default' | 'sm'
  className?: string
}

export function PublishSiteDialog({
  variant = 'default',
  size = 'default',
  className,
}: PublishSiteDialogProps) {
  const [open, setOpen] = useState(false)
  const command = 'evs sites publish ./dist'

  const copyCommand = async () => {
    try {
      await copyToClipboard(command)
      toast.success('Publish command copied')
    } catch {
      toast.error('Could not copy the command')
    }
  }

  return (
    <>
      <Button
        variant={variant}
        size={size}
        className={className}
        onClick={() => setOpen(true)}
      >
        <Iconify.Icon icon="lucide:upload-cloud" className="size-4" />
        Publish site
      </Button>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="w-[min(560px,calc(100vw-2rem))]">
          <DialogTitle>Publish a static site</DialogTitle>
          <DialogDescription className="text-brand-main-100">
            Publish a prebuilt directory from an authenticated Everstack CLI.
            Every publish creates an immutable version and atomically promotes
            it to the production URL.
          </DialogDescription>

          <div className="mt-5 space-y-4">
            <PublishStep number="1" title="Build your site">
              Generate a static output directory such as dist, build, or out.
            </PublishStep>
            <PublishStep number="2" title="Publish the directory">
              <div className="mt-2 flex items-center gap-2 rounded border border-brand-main-600 bg-brand-main-950/70 p-1.5 pl-3">
                <code className="min-w-0 flex-1 truncate font-mono text-xs text-brand-secondary-200 light:text-brand-secondary-700">
                  {command}
                </code>
                <Button
                  variant="ghost"
                  size="sm"
                  className="h-7 shrink-0 px-2"
                  onClick={copyCommand}
                >
                  <Iconify.Icon icon="lucide:copy" className="size-3.5" />
                  Copy
                </Button>
              </div>
              <p className="mt-1.5 font-mono text-[10px] text-white/35 light:text-black/45">
                Add --slug my-site, --spa, or --noindex when needed.
              </p>
            </PublishStep>
            <PublishStep number="3" title="Open the production URL">
              The CLI prints the live evs.run domain. This page picks up the
              deployment automatically.
            </PublishStep>
          </div>

          <div className="mt-5 flex justify-end">
            <Button variant="outline" onClick={() => setOpen(false)}>
              Done
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </>
  )
}

function PublishStep({
  number,
  title,
  children,
}: {
  number: string
  title: string
  children: ReactNode
}) {
  return (
    <div className="grid grid-cols-[24px_minmax(0,1fr)] gap-3">
      <span className="flex size-6 items-center justify-center rounded border border-brand-main-600 bg-brand-main-800 text-[10px] font-semibold text-brand-secondary-200">
        {number}
      </span>
      <div>
        <p className="text-sm font-medium text-white light:text-brand-main-50">
          {title}
        </p>
        <div className="mt-0.5 text-xs leading-relaxed text-white/45 light:text-black/50">
          {children}
        </div>
      </div>
    </div>
  )
}
