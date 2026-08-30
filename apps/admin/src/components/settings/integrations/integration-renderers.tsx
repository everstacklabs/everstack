import type { ComponentType } from 'react'
import type { IntegrationCatalogItem } from './integrations-catalog'
import { GitHubIntegration } from './github-integration'

type IntegrationRendererProps = {
    integration: IntegrationCatalogItem
}

type IntegrationRenderer = ComponentType<IntegrationRendererProps>

export const integrationRenderers: Record<string, IntegrationRenderer> = {
    github: ({ integration }) => (
        <GitHubIntegration
            name={integration.name}
            icon={integration.icon}
            category={integration.category}
            status={integration.status}
            description={integration.description}
            capabilities={integration.capabilities}
        />
    ),
}
