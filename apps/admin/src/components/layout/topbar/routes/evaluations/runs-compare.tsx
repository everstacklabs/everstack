import { Link, useSearch } from '@tanstack/react-router'
import { type ActionGroup } from '@/components/layout/topbar/types'
import { CompareRunsButton } from '@/components/evaluations/eval-run-select-sheet'

function CompareBreadcrumb() {
    return (
        <div className="flex items-center gap-1.5">
            <Link
                to="/evaluations/runs"
                className="text-sm font-normal text-brand-main-300 hover:text-white/80 light:hover:text-black/80 transition-colors"
            >
                Eval Runs
            </Link>
            <span className="text-brand-main-400 text-sm">/</span>
            <span className="text-sm text-white light:text-brand-main-50 font-normal">Compare</span>
        </div>
    )
}

function EditSelectionButton() {
    const search = useSearch({ strict: false }) as { runs?: string }
    const runIds = (search.runs ?? '')
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean)
    return <CompareRunsButton initialRunIds={runIds} label="Edit selection" />
}

export const EvaluationsRunsCompareActions: ActionGroup[] = [
    {
        title: <CompareBreadcrumb />,
    },
    {
        actions: [
            {
                type: 'custom',
                key: 'edit-compare-selection',
                label: 'Edit selection',
                component: EditSelectionButton,
            },
        ],
    },
]
