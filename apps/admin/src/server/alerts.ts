import { getApiBaseUrl } from '@/lib/api-url'
import { createServerTransport } from '@/server'
import { create, createClientFor } from '@everstack/client'
import type {
    AlertRule,
    AlertEvent,
    NotificationTarget,
    AlertsSummary,
    CreateAlertRuleResponse,
    GetAlertRuleResponse,
    UpdateAlertRuleResponse,
    DeleteAlertRuleResponse,
    ListAlertRulesResponse,
    CreateNotificationTargetResponse,
    GetNotificationTargetResponse,
    UpdateNotificationTargetResponse,
    DeleteNotificationTargetResponse,
    ListNotificationTargetsResponse,
    TestNotificationTargetResponse,
    ListAlertEventsResponse,
    AcknowledgeAlertResponse,
    ResolveAlertResponse,
    SeedBuiltinRulesResponse,
    GetAlertsSummaryResponse,
} from '@everstack/proto/everstack/alerts/v1/alerts_pb'
import {
    AlertCategory,
    AlertSeverity,
    ComparisonOperator,
    AlertEventStatus,
    NotificationTargetType,
    CreateAlertRuleRequestSchema,
    GetAlertRuleRequestSchema,
    UpdateAlertRuleRequestSchema,
    DeleteAlertRuleRequestSchema,
    ListAlertRulesRequestSchema,
    CreateNotificationTargetRequestSchema,
    GetNotificationTargetRequestSchema,
    UpdateNotificationTargetRequestSchema,
    DeleteNotificationTargetRequestSchema,
    ListNotificationTargetsRequestSchema,
    TestNotificationTargetRequestSchema,
    ListAlertEventsRequestSchema,
    AcknowledgeAlertRequestSchema,
    ResolveAlertRequestSchema,
    SeedBuiltinRulesRequestSchema,
    GetAlertsSummaryRequestSchema,
} from '@everstack/proto/everstack/alerts/v1/alerts_pb'
import { AlertsService } from '@everstack/proto/everstack/alerts/v1/alerts_service_pb'

const env = (
    (typeof import.meta !== 'undefined'
        ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
        : undefined) ?? {}
) as Record<string, string | undefined>

const baseUrl = getApiBaseUrl()
const connectBase = (env.VITE_CONNECT_BASE_PATH as string | undefined) ?? ''

const transport = createServerTransport(undefined, {
    baseUrl: `${baseUrl}${connectBase}`,
    interceptors: [],
})
const alertsClient = createClientFor(AlertsService)(transport)

// ─── Types ──────────────────────────────────────────────────────────

export type CreateAlertRuleParams = {
    tenantId: string
    name: string
    description: string
    category: AlertCategory
    severity: AlertSeverity
    metric: string
    operator: ComparisonOperator
    threshold: number
    durationSeconds: number
    filters?: Record<string, string>
    enabled: boolean
    targetIds?: string[]
}

export type UpdateAlertRuleParams = {
    id: string
    tenantId: string
    name?: string
    description?: string
    category?: AlertCategory
    severity?: AlertSeverity
    metric?: string
    operator?: ComparisonOperator
    threshold?: number
    durationSeconds?: number
    filters?: Record<string, string>
    enabled?: boolean
    targetIds?: string[]
}

export type CreateNotificationTargetParams = {
    tenantId: string
    name: string
    targetType: NotificationTargetType
    channelConfigId?: string
    platformChannelRef?: string
    webhookUrl?: string
    webhookHeaders?: Record<string, string>
    emailAddresses?: string[]
    minSeverity: AlertSeverity
    enabled: boolean
}

export type UpdateNotificationTargetParams = {
    id: string
    tenantId: string
    name?: string
    targetType?: NotificationTargetType
    channelConfigId?: string
    platformChannelRef?: string
    webhookUrl?: string
    webhookHeaders?: Record<string, string>
    emailAddresses?: string[]
    minSeverity?: AlertSeverity
    enabled?: boolean
}

// ─── API Functions ──────────────────────────────────────────────────

export async function createAlertRule(params: CreateAlertRuleParams): Promise<CreateAlertRuleResponse> {
    return alertsClient.createAlertRule(create(CreateAlertRuleRequestSchema, params))
}

export async function getAlertRule(params: { id: string; tenantId: string }): Promise<GetAlertRuleResponse> {
    return alertsClient.getAlertRule(create(GetAlertRuleRequestSchema, params))
}

export async function updateAlertRule(params: UpdateAlertRuleParams): Promise<UpdateAlertRuleResponse> {
    return alertsClient.updateAlertRule(create(UpdateAlertRuleRequestSchema, params))
}

export async function deleteAlertRule(params: { id: string; tenantId: string }): Promise<DeleteAlertRuleResponse> {
    return alertsClient.deleteAlertRule(create(DeleteAlertRuleRequestSchema, params))
}

export async function listAlertRules(params: {
    tenantId: string
    category?: AlertCategory
    enabled?: boolean
    limit?: number
    offset?: number
}): Promise<ListAlertRulesResponse> {
    return alertsClient.listAlertRules(create(ListAlertRulesRequestSchema, params))
}

export async function createNotificationTarget(params: CreateNotificationTargetParams): Promise<CreateNotificationTargetResponse> {
    return alertsClient.createNotificationTarget(create(CreateNotificationTargetRequestSchema, params))
}

export async function getNotificationTarget(params: { id: string; tenantId: string }): Promise<GetNotificationTargetResponse> {
    return alertsClient.getNotificationTarget(create(GetNotificationTargetRequestSchema, params))
}

export async function updateNotificationTarget(params: UpdateNotificationTargetParams): Promise<UpdateNotificationTargetResponse> {
    return alertsClient.updateNotificationTarget(create(UpdateNotificationTargetRequestSchema, params))
}

export async function deleteNotificationTarget(params: { id: string; tenantId: string }): Promise<DeleteNotificationTargetResponse> {
    return alertsClient.deleteNotificationTarget(create(DeleteNotificationTargetRequestSchema, params))
}

export async function listNotificationTargets(params: {
    tenantId: string
    targetType?: NotificationTargetType
    limit?: number
    offset?: number
}): Promise<ListNotificationTargetsResponse> {
    return alertsClient.listNotificationTargets(create(ListNotificationTargetsRequestSchema, params))
}

export async function testNotificationTarget(params: { id: string; tenantId: string }): Promise<TestNotificationTargetResponse> {
    return alertsClient.testNotificationTarget(create(TestNotificationTargetRequestSchema, params))
}

export async function listAlertEvents(params: {
    tenantId: string
    alertRuleId?: string
    status?: AlertEventStatus
    limit?: number
    offset?: number
}): Promise<ListAlertEventsResponse> {
    return alertsClient.listAlertEvents(create(ListAlertEventsRequestSchema, params))
}

export async function acknowledgeAlert(params: { id: string; tenantId: string; acknowledgedBy: string }): Promise<AcknowledgeAlertResponse> {
    return alertsClient.acknowledgeAlert(create(AcknowledgeAlertRequestSchema, params))
}

export async function resolveAlert(params: { id: string; tenantId: string }): Promise<ResolveAlertResponse> {
    return alertsClient.resolveAlert(create(ResolveAlertRequestSchema, params))
}

export async function seedBuiltinRules(params: { tenantId: string }): Promise<SeedBuiltinRulesResponse> {
    return alertsClient.seedBuiltinRules(create(SeedBuiltinRulesRequestSchema, params))
}

export async function getAlertsSummary(params: { tenantId: string }): Promise<GetAlertsSummaryResponse> {
    return alertsClient.getAlertsSummary(create(GetAlertsSummaryRequestSchema, params))
}

export {
    AlertCategory,
    AlertSeverity,
    ComparisonOperator,
    AlertEventStatus,
    NotificationTargetType,
}
export type { AlertRule, AlertEvent, NotificationTarget, AlertsSummary }
