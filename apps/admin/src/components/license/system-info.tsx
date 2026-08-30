import { useGatewayLicenseStatus } from '@/hooks/license/use-license-observer'
import { ui } from '@everstack/ui'
import { Copy, Check } from 'lucide-react'
import { useState } from 'react'
import { safeBigIntToNumber } from '@/utils/trace-formatters'

const { Card, CardHeader, CardTitle, CardDescription, CardContent, Badge, Button } = ui

/**
 * SystemInfo component
 * Displays gateway system information including instance_id for support/debugging
 */
export function SystemInfo() {
    const { data: status, isLoading } = useGatewayLicenseStatus()
    const [copied, setCopied] = useState(false)

    const handleCopy = (text: string) => {
        navigator.clipboard.writeText(text)
        setCopied(true)
        setTimeout(() => setCopied(false), 2000)
    }

    if (isLoading) {
        return (
            <Card>
                <CardHeader>
                    <CardTitle>System Information</CardTitle>
                    <CardDescription>Loading...</CardDescription>
                </CardHeader>
            </Card>
        )
    }

    if (!status?.activated) {
        return (
            <Card>
                <CardHeader>
                    <CardTitle>System Information</CardTitle>
                    <CardDescription>Gateway not activated</CardDescription>
                </CardHeader>
            </Card>
        )
    }

    return (
        <Card>
            <CardHeader>
                <CardTitle>System Information</CardTitle>
                <CardDescription>Gateway instance details for support and debugging</CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
                {/* Instance ID */}
                <div className="space-y-1">
                    <label className="text-sm font-medium">Instance ID</label>
                    <div className="flex items-center gap-2">
                        <code className="flex-1 px-3 py-2 bg-muted rounded text-sm font-mono">
                            {status.instanceId}
                        </code>
                        <Button
                            variant="outline"
                            size="sm"
                            onClick={() => handleCopy(status.instanceId)}
                        >
                            {copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
                        </Button>
                    </div>
                    <p className="text-xs text-muted-foreground">
                        Use this ID when contacting support
                    </p>
                </div>

                {/* Status */}
                <div className="space-y-1">
                    <label className="text-sm font-medium">Status</label>
                    <div>
                        <Badge variant={status.activated ? 'default' : 'secondary'}>
                            {status.status || 'Unknown'}
                        </Badge>
                    </div>
                </div>

                {/* Activation Date */}
                {status.activatedAt && (
                    <div className="space-y-1">
                        <label className="text-sm font-medium">Activated</label>
                        <p className="text-sm text-muted-foreground">
                            {new Date((typeof status.activatedAt.seconds === 'bigint' ? safeBigIntToNumber(status.activatedAt.seconds) : Number(status.activatedAt.seconds)) * 1000).toLocaleString()}
                        </p>
                    </div>
                )}

                {/* License Info */}
                {status.licenseState && (
                    <>
                        <div className="space-y-1">
                            <label className="text-sm font-medium">Plan</label>
                            <p className="text-sm">
                                {status.licenseState.planTier?.toString() || 'N/A'}
                            </p>
                        </div>

                        {status.licenseState.expiresAt && (
                            <div className="space-y-1">
                                <label className="text-sm font-medium">License Expires</label>
                                <p className="text-sm text-muted-foreground">
                                    {new Date((typeof status.licenseState.expiresAt.seconds === 'bigint' ? safeBigIntToNumber(status.licenseState.expiresAt.seconds) : Number(status.licenseState.expiresAt.seconds)) * 1000).toLocaleString()}
                                </p>
                            </div>
                        )}
                    </>
                )}

                <div className="pt-4 border-t text-xs text-muted-foreground">
                    <p>
                        <strong>Note:</strong> The instance ID is automatically included in all requests for telemetry and debugging purposes.
                    </p>
                </div>
            </CardContent>
        </Card>
    )
}

