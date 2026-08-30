import { type RouteActions } from '../types'
import { VaultApiKeysActions, VaultLlmProvidersActions } from './vault'
import {
  SettingsEventsActions,
  SettingsBillingActions,
  IntegrationsActions,
  SettingsSSHKeysActions,
  SettingsMembersActions,
  SettingsGeneralActions,
} from './settings'
import { StorageActions } from './storage'
import {
  ObservabilityLogsActions,
  ObservabilityTracesActions,
  ObservabilityIssuesActions,
  ObservabilityIssueDetailActions,
  ObservabilityMetricsActions,
  ObservabilityOutcomesActions,
  ObservabilitySessionsActions,
  ObservabilityUsersActions,
  ObservabilityAlertsActions,
  ObservabilitySavedQueriesActions,
} from './observability'
import {
  EvaluationsHomeActions,
  EvaluationsDatasetsActions,
  EvaluationsDatasetsDetailActions,
  EvaluationsRunsActions,
  EvaluationsRunsNewActions,
  EvaluationsRunsDetailActions,
  EvaluationsRunsCompareActions,
  EvaluationsScoreConfigsActions,
  EvaluationsScoreConfigsNewActions,
  EvaluationsOnlineEvalsActions,
  EvaluationsAnnotationQueuesActions,
  EvaluationsAnnotationQueuesNewActions,
  EvaluationsAnnotationQueuesDetailActions,
  EvaluationsPlaygroundActions,
  EvaluationsPlaygroundsActions,
  EvaluationsPromptsLibraryActions,
  EvaluationsPromptsLibraryDetailActions,
  EvaluationsPromptsCompareActions,
} from './evaluations'
import { LicenseStatusAndUsageActions } from './license'
import { AccountProfileActions } from './account'
import {
  GatewayConfigActions,
  GatewayMcpGatewayActions,
  GatewayA2aActions,
} from './gateway'
import {
  DeploymentsFunctionsActions,
  DeploymentsStudioActions,
  DeploymentsStudioNewActions,
  DeploymentsSandboxesActions,
  DeploymentsSandboxesNewActions,
  DeploymentsSandboxesDetailActions,
  DeploymentsAgentsActions,
  DeploymentsAgentsDetailActions,
  DeploymentsAgentsSessionDetailActions,
  DeploymentsMemoryActions,
  DeploymentsMemoryDetailActions,
  DeploymentsChannelsActions,
  DeploymentsVoiceActions,
  DeploymentsTroopersActions,
  DeploymentsTroopersDetailActions,
} from './deployments'
import { ChatActions } from './chat'
import { SitesActions, SitesDetailTopbarActions } from './sites'
export const routeActions: RouteActions = {
  '/chat': {
    '': ChatActions,
  },
  '/vault': {
    '/api-keys': VaultApiKeysActions,
    '/llm-providers': VaultLlmProvidersActions,
  },
  '/observability': {
    '/logs': ObservabilityLogsActions,
    '/traces': ObservabilityTracesActions,
    '/issues': ObservabilityIssuesActions,
    '/issues/*': ObservabilityIssueDetailActions,
    '/metrics': ObservabilityMetricsActions,
    '/outcomes': ObservabilityOutcomesActions,
    '/alerts': ObservabilityAlertsActions,
    '/sessions': ObservabilitySessionsActions,
    '/users': ObservabilityUsersActions,
    '/saved-queries': ObservabilitySavedQueriesActions,
  },
  '/settings': {
    '/general': SettingsGeneralActions,
    '/events': SettingsEventsActions,
    '/billing': SettingsBillingActions,
    '/integrations': IntegrationsActions,
    '/ssh-keys': SettingsSSHKeysActions,
    '/team': SettingsMembersActions,
    '/members': SettingsMembersActions,
  },
  '/license': {
    '/status-and-usage': LicenseStatusAndUsageActions,
  },
  '/account': {
    '/profile': AccountProfileActions,
  },
  '/gateway': {
    '/config': GatewayConfigActions,
    '/mcp': GatewayMcpGatewayActions,
    '/a2a': GatewayA2aActions,
  },
  '/evaluations': {
    '': EvaluationsHomeActions,
    '/datasets': EvaluationsDatasetsActions,
    '/datasets/*': EvaluationsDatasetsDetailActions,
    '/playground': EvaluationsPlaygroundActions,
    '/playgrounds': EvaluationsPlaygroundsActions,
    '/prompts-library': EvaluationsPromptsLibraryActions,
    '/prompts-library/compare': EvaluationsPromptsCompareActions,
    '/prompts-library/*': EvaluationsPromptsLibraryDetailActions,
    '/runs': EvaluationsRunsActions,
    '/runs/new': EvaluationsRunsNewActions,
    '/runs/compare': EvaluationsRunsCompareActions,
    '/runs/*': EvaluationsRunsDetailActions,
    '/score-configs': EvaluationsScoreConfigsActions,
    '/score-configs/new': EvaluationsScoreConfigsNewActions,
    '/online-evals': EvaluationsOnlineEvalsActions,
    '/annotation-queues': EvaluationsAnnotationQueuesActions,
    '/annotation-queues/new': EvaluationsAnnotationQueuesNewActions,
    '/annotation-queues/*': EvaluationsAnnotationQueuesDetailActions,
  },
  '/storage': {
    '/overview': StorageActions,
  },
  '/sites': {
    '': SitesActions,
    '/*': SitesDetailTopbarActions,
  },
  '/deployments': {
    '/functions': DeploymentsFunctionsActions,
    '/agents': DeploymentsAgentsActions,
    '/agents/*': DeploymentsAgentsDetailActions,
    '/agents/sessions/*': DeploymentsAgentsSessionDetailActions,
    '/studio': DeploymentsStudioActions,
    '/studio/new': DeploymentsStudioNewActions,
    '/sandboxes': DeploymentsSandboxesActions,
    '/sandboxes/new': DeploymentsSandboxesNewActions,
    '/sandboxes/*': DeploymentsSandboxesDetailActions,
    '/memory': DeploymentsMemoryActions,
    '/memory/*': DeploymentsMemoryDetailActions,
    '/channels': DeploymentsChannelsActions,
    '/voice': DeploymentsVoiceActions,
    '/troopers': DeploymentsTroopersActions,
    '/troopers/*': DeploymentsTroopersDetailActions,
  },
}
