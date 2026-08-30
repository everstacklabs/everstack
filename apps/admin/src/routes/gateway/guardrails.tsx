import { createFileRoute } from '@tanstack/react-router'
import { ComingSoonRoute } from '@/components/common/coming-soon-route'

export const Route = createFileRoute('/gateway/guardrails')({
    component: GatewayGuardrailsPage,
})

function GatewayGuardrailsPage() {
    return (
        <ComingSoonRoute
            title="Guardrails"
            description="Define and manage runtime safety policies, validators, and policy checks for gateway traffic."
        />
    )
}
