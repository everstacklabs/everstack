import { Link } from '@tanstack/react-router'
import { type ActionGroup } from '@/components/layout/topbar/types'

function CompareBreadcrumb() {
    return (
        <div className="flex items-center gap-1.5">
            <Link
                to="/evaluations/prompts-library"
                className="text-sm font-normal text-brand-main-300 hover:text-white/80 light:hover:text-black/80 transition-colors"
            >
                Prompts
            </Link>
            <span className="text-brand-main-400 text-sm">/</span>
            <span className="text-sm text-white light:text-brand-main-50 font-normal">Compare</span>
        </div>
    )
}

export const EvaluationsPromptsCompareActions: ActionGroup[] = [
    {
        title: <CompareBreadcrumb />,
    },
]
