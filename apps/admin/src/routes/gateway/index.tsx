import { createFileRoute, redirect } from '@tanstack/react-router'

export const Route = createFileRoute('/gateway/')({
    beforeLoad: async () => {
        throw redirect({
            to: '/gateway/config'
        })
    }
})
