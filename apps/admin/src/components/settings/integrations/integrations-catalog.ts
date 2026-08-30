import type { IntegrationStatus } from './integration-card'

export type IntegrationCatalogItem = {
  id: string
  type: 'github' | 'placeholder'
  name: string
  icon: string
  category: string
  status: IntegrationStatus
  description: string
  capabilities: string[]
  keywords: string[]
}

export const integrationsCatalog: IntegrationCatalogItem[] = [
  {
    id: 'github',
    type: 'github',
    name: 'GitHub',
    icon: 'mdi:github',
    category: 'Source Control',
    status: 'beta',
    description: 'Import repositories and branches into sandbox environments.',
    capabilities: [
      'Org-level app installation',
      'Repo & branch discovery',
      'Sandbox source import',
    ],
    keywords: ['git', 'repo', 'repository', 'source code', 'sandbox'],
  },
  {
    id: 'gitlab',
    type: 'placeholder',
    name: 'GitLab',
    icon: 'mdi:gitlab',
    category: 'Source Control',
    status: 'coming_soon',
    description: 'Self-managed and cloud GitLab repository access.',
    capabilities: ['Group access model', 'Repo import', 'Branch pinning'],
    keywords: ['git', 'repo', 'self-hosted', 'source code'],
  },
  {
    id: 'bitbucket',
    type: 'placeholder',
    name: 'Bitbucket',
    icon: 'mdi:bitbucket',
    category: 'Source Control',
    status: 'coming_soon',
    description: 'Atlassian Bitbucket integration for repository import.',
    capabilities: ['Instance-scoped installs', 'Repo import'],
    keywords: ['atlassian', 'repo', 'source code'],
  },
  {
    id: 'linear',
    type: 'placeholder',
    name: 'Linear',
    icon: 'simple-icons:linear',
    category: 'Issue Tracking',
    status: 'coming_soon',
    description: 'Create and update issues from agent execution flows.',
    capabilities: ['Issue sync', 'Status automation'],
    keywords: ['issues', 'tickets', 'planning'],
  },
  {
    id: 'jira',
    type: 'placeholder',
    name: 'Jira',
    icon: 'mdi:jira',
    category: 'Issue Tracking',
    status: 'coming_soon',
    description: 'Track agent-generated work in Jira projects.',
    capabilities: ['Ticket creation', 'State transitions'],
    keywords: ['atlassian', 'issues', 'workflow'],
  },
]
