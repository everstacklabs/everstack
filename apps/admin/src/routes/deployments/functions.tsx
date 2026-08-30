import { createFileRoute } from '@tanstack/react-router'
import { useState, useCallback } from 'react'
import { useFunctions } from '@/hooks/deployments/use-functions'
import {
  FunctionsTable,
  CreateFunctionDialog,
  EditFunctionDialog,
} from '@/components/deployments/functions'
import { Iconify } from '@everstack/ui/icons'
import { X } from '@everstack/ui'
import { Button, Loader } from '@everstack/ui/components'
import type { Function } from '@/server/functions'

export const Route = createFileRoute('/deployments/functions')({
  component: FunctionsPage,
})

function FunctionsPage() {
  const { data: functions = [], isLoading, error } = useFunctions()
  const [createDialogOpen, setCreateDialogOpen] = useState(false)
  const [editDialogOpen, setEditDialogOpen] = useState(false)
  const [editingFunction, setEditingFunction] = useState<Function | null>(null)
  const [bannerDismissed, setBannerDismissed] = useState(() =>
    localStorage.getItem('functions-tip-dismissed') === '1',
  )

  const dismissBanner = useCallback(() => {
    setBannerDismissed(true)
    localStorage.setItem('functions-tip-dismissed', '1')
  }, [])

  const handleEdit = (fn: Function) => {
    setEditingFunction(fn)
    setEditDialogOpen(true)
  }

  const handleEditDialogClose = (open: boolean) => {
    setEditDialogOpen(open)
    if (!open) {
      setEditingFunction(null)
    }
  }

  return (
    <div className="flex flex-col h-full w-full">
      <div className="min-h-0 h-full justify-center items-center overflow-hidden flex flex-col">
        {isLoading ? (
          <div className="flex-1 flex items-center justify-center text-white/70 light:text-black/70">
            <Loader loaderText="Loading functions..." />
          </div>
        ) : error ? (
          <div className="flex-1 flex items-center justify-center text-red-400 light:text-red-600">
            Error loading functions: {error.message}
          </div>
        ) : functions.length === 0 ? (
          <div className="flex-1 flex flex-col h-full items-center justify-center">
            <div className="relative mb-6">
              <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
              <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                <Iconify.Icon
                  icon="heroicons:code-bracket"
                  className="size-8 text-brand-secondary-400"
                />
              </div>
            </div>
            <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">
              No functions yet
            </h3>
            <p className="text-sm text-white/50 light:text-black/50 max-w-md text-center leading-relaxed mb-4">
              Create reusable tools that let agents call your APIs, services,
              and custom logic for lookups, actions, and lightweight processing.
            </p>
            <Button variant="default" onClick={() => setCreateDialogOpen(true)}>
              <div className="flex items-center gap-2">
                <Iconify.Icon icon="heroicons:plus" className="size-4" />
                Create Function
              </div>
            </Button>
          </div>
        ) : (
          <div className="flex h-full w-full flex-col">
            {!bannerDismissed && (
              <div className="mx-3 my-3 rounded border border-brand-main-700 bg-brand-main-800/60 px-4 py-4">
                <div className="flex items-start gap-3.5">
                  <div className="mt-0.5 rounded border border-brand-secondary-500/25 bg-brand-secondary-500/10 p-2.5">
                    <Iconify.Icon
                      icon="heroicons:light-bulb"
                      className="size-4 text-brand-secondary-300"
                    />
                  </div>
                  <div className="flex-1 space-y-1.5 pr-2">
                    <p className="text-sm font-medium leading-none text-white light:text-brand-main-50">
                      Recommended first function patterns
                    </p>
                    <p className="text-sm leading-relaxed text-white/55 light:text-black/55">
                      Start with a few stable tools like customer lookup, order
                      history, ticket creation, or lightweight data transforms.
                      Functions work best when each tool exposes one clear action.
                    </p>
                  </div>
                  <button
                    type="button"
                    onClick={dismissBanner}
                    className="shrink-0 rounded p-1 text-white/40 light:text-black/40 hover:text-white light:hover:text-brand-main-50 hover:bg-brand-main-600 transition-colors"
                  >
                    <X size={14} />
                  </button>
                </div>
              </div>
            )}

            <div className="min-h-0 flex-1">
              <FunctionsTable functions={functions} onEdit={handleEdit} />
            </div>
          </div>
        )}
      </div>

      <CreateFunctionDialog
        open={createDialogOpen}
        onOpenChange={setCreateDialogOpen}
      />

      <EditFunctionDialog
        open={editDialogOpen}
        onOpenChange={handleEditDialogClose}
        functionData={editingFunction}
      />
    </div>
  )
}
