import { useNavigate, useSearch } from '@tanstack/react-router'
import { ui } from '@everstack/ui'
import { Search } from '@everstack/ui/icons'
import { type ActionGroup } from '@/components/layout/topbar/types'

const { InputWithIcon } = ui

// Filter box for the Queries page, rendered as a topbar action. Drives the
// route's `filter` search param so the page body stays content-only.
function QueriesFilter() {
    const navigate = useNavigate() as (opts: unknown) => void
    const search = useSearch({ strict: false }) as { filter?: string }
    return (
        <InputWithIcon
            icon={<Search className="size-4 text-white/50 light:text-black/50" />}
            placeholder="Filter queries…"
            value={search.filter ?? ''}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) =>
                navigate({
                    to: '.',
                    search: (prev: Record<string, unknown>) => ({ ...prev, filter: e.target.value || undefined }),
                    replace: true,
                })
            }
            className="h-8 w-64"
        />
    )
}

export const ObservabilitySavedQueriesActions: ActionGroup[] = [
    {
        title: 'Queries',
        actions: [{ type: 'custom', key: 'queries-filter', label: 'Filter', component: QueriesFilter }],
    },
]
