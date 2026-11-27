import { createFileRoute } from '@tanstack/react-router'
import { MagicLinkVerification } from '@/components/auth'

export const Route = createFileRoute('/verify-magic-link')({
    component: MagicLinkVerification,
})
