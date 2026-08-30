import { useState } from 'react'
import { ui } from '@everstack/ui'
import { Iconify } from '@everstack/ui/icons'
import { cn } from '@everstack/utils/functions/cn'
import type { ProviderAPIKey } from '@everstack/proto/everstack/providers/providers_pb'
import { truncateString } from '@everstack/utils/functions/common'

const { Input, Label, Button } = ui

interface APIKeyRowProps {
    apiKey: ProviderAPIKey
    onUpdateWeight: (keyId: string, weight: number) => void
    onToggle: (keyId: string, isActive: boolean) => void
    onDelete: (keyId: string) => void
}

export function APIKeyRow({ apiKey, onUpdateWeight, onToggle, onDelete }: APIKeyRowProps) {
    const [weight, setWeight] = useState(apiKey.weight)
    const [isEditing, setIsEditing] = useState(false)

    const isConfigKey = apiKey.source === 'config'

    const handleWeightSave = () => {
        if (weight !== apiKey.weight && weight > 0) {
            onUpdateWeight(apiKey.id, weight)
        }
        setIsEditing(false)
    }

    const handleWeightChange = (value: string) => {
        const num = parseInt(value)
        if (!isNaN(num) && num > 0) {
            setWeight(num)
        }
    }

    return (
        <div className={cn(
            "flex items-center gap-3 p-3 border rounded-md transition-all",
            apiKey.isActive
                ? "bg-brand-main-700 border-brand-main-500"
                : "bg-brand-main-800/50 border-brand-main-600/50 opacity-60"
        )}>
            {/* Key Info */}
            <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2">
                    <div className="text-sm font-medium text-white light:text-brand-main-50 truncate">
                        {apiKey.keyName}
                    </div>

                    {/* Add "from Config" badge */}
                    {isConfigKey && (
                        <span className="text-xs px-2 py-0.5 rounded bg-blue-600/20 text-blue-400 light:text-blue-600 border border-blue-600/30">
                            Config
                        </span>
                    )}

                    {!apiKey.isActive && (
                        <span className="text-xs px-1.5 py-0.5 rounded bg-yellow-600/20 text-yellow-400 light:text-yellow-700 border border-yellow-600/30">
                            Inactive
                        </span>
                    )}
                </div>
                <div className="text-xs text-white/50 light:text-black/50 font-mono">
                    {truncateString(apiKey.keyMasked)}
                </div>
            </div>

            {/* Weight Control - disable for config keys */}
            <div className="flex items-center gap-2">
                <Label className="text-xs text-white/60 light:text-black/60 whitespace-nowrap">Weight:</Label>
                {isEditing && !isConfigKey ? (
                    <div className="flex items-center gap-1">
                        <Input
                            type="number"
                            min="1"
                            value={weight}
                            onChange={(e) => handleWeightChange(e.target.value)}
                            className="w-16 h-7 text-sm bg-brand-main-600 border-brand-main-400 text-white light:text-brand-main-50"
                            autoFocus
                        />
                        <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            onClick={handleWeightSave}
                            className="h-7 w-7 p-0"
                        >
                            <Iconify.Icon icon="heroicons:check" className="size-4 text-green-400 light:text-green-600" />
                        </Button>
                    </div>
                ) : (
                    <button
                        type="button"
                        onClick={() => !isConfigKey && setIsEditing(true)}
                        disabled={isConfigKey}
                        className={cn(
                            "text-sm text-white light:text-brand-main-50 px-2 py-0.5 rounded transition-colors",
                            isConfigKey
                                ? "bg-brand-main-500 border border-brand-main-400 cursor-not-allowed opacity-50"
                                : "bg-brand-main-500 border border-brand-main-400 hover:bg-brand-main-500"
                        )}
                        title={isConfigKey ? "Config keys cannot be modified from UI" : "Click to edit"}
                    >
                        {weight}
                    </button>
                )}
            </div>

            {/* Actions - disable for config keys */}
            <div className="flex items-center gap-1">
                <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => onToggle(apiKey.id, !apiKey.isActive)}
                    className="h-7 w-7 p-0"
                    title={isConfigKey ? "Config keys cannot be toggled from UI" : (apiKey.isActive ? "Deactivate" : "Activate")}
                    disabled={isConfigKey}
                >
                    <Iconify.Icon
                        icon={apiKey.isActive ? "heroicons:eye" : "heroicons:eye-slash"}
                        className={cn("size-4", apiKey.isActive ? "text-blue-400 light:text-blue-600" : "text-gray-400 light:text-gray-600", isConfigKey && "opacity-50")}
                    />
                </Button>
                <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => onDelete(apiKey.id)}
                    className="h-7 w-7 p-0"
                    title={isConfigKey ? "Config keys cannot be deleted from UI" : "Delete"}
                    disabled={isConfigKey}
                >
                    <Iconify.Icon icon="heroicons:trash" className={cn("size-4 text-red-400 light:text-red-600", isConfigKey && "opacity-50")} />
                </Button>
            </div>
        </div>
    )
}

