import { Route } from '@/routes/observability/traces'
import { ui } from '@everstack/ui'
import { Settings, EyeOff } from 'lucide-react'

const { Button, Switch, Label, Popover, PopoverContent, PopoverTrigger } = ui

// TraceListSettings is the gear popover for traces-table display preferences.
// Today it holds the operational-traces toggle; more switches can join it.
export function TraceListSettings() {
    const search = Route.useSearch()
    const navigate = Route.useNavigate()

    const showOperational = search.showOperational === 'true'

    const setShowOperational = (value: boolean) => {
        navigate({
            search: (prev) => ({
                ...prev,
                showOperational: value ? 'true' : undefined,
            }),
            replace: true,
        })
    }

    return (
        <Popover>
            <PopoverTrigger asChild>
                <Button variant="outline" size="sm" className="h-7 gap-1.5 text-xs">
                    <Settings className="size-3.5" />
                    Settings
                </Button>
            </PopoverTrigger>
            <PopoverContent
                side="bottom"
                align="end"
                className="w-72 p-3 bg-brand-main-900 border-brand-main-500"
            >
                <div className="flex items-start justify-between gap-3">
                    <div className="flex flex-col gap-0.5">
                        <Label className="flex items-center gap-1.5 text-xs text-brand-main-50 light:text-black">
                            <EyeOff className="size-3.5 text-brand-main-50 light:text-black" />
                            Show operational traces
                        </Label>
                        <span className="text-[11px] text-brand-main-50 leading-snug light:text-black">
                            Health checks and external interaction wrappers (no model, tokens,
                            or cost) are hidden by default.
                        </span>
                    </div>
                    <Switch checked={showOperational} onCheckedChange={setShowOperational} />
                </div>
            </PopoverContent>
        </Popover>
    )
}
